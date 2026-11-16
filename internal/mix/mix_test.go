package mix

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/maisem/tailmix/controlapi"
	"github.com/maisem/tailmix/profilesocket"
	"tailscale.com/ipn"
)

// fakeDaemon implements the parts of the tailmix control API that
// anyscale uses. Calling anything else panics through the nil Backend.
type fakeDaemon struct {
	controlapi.Backend

	mu       sync.Mutex
	profiles []controlapi.Profile
	added    []controlapi.AddProfileRequest
	exitNode *controlapi.SetExitNodeRequest
	enabled  map[string]bool
}

func (d *fakeDaemon) Status(context.Context) (controlapi.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return controlapi.Status{State: "running", Profiles: d.profiles}, nil
}

func (d *fakeDaemon) GetProfile(_ context.Context, name string) (controlapi.Profile, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.profiles {
		if p.Name == name {
			return p, nil
		}
	}
	return controlapi.Profile{}, controlapi.NewError("profile_not_found", "no profile %q", name)
}

func (d *fakeDaemon) AddProfile(_ context.Context, req controlapi.AddProfileRequest) (controlapi.Profile, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.added = append(d.added, req)
	for _, p := range d.profiles {
		if p.Name == req.Name {
			return p, nil
		}
	}
	return controlapi.Profile{}, controlapi.NewError("profile_not_found", "fake has no socket for %q", req.Name)
}

func (d *fakeDaemon) SetProfileEnabled(_ context.Context, name string, enabled bool) (controlapi.Profile, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.enabled == nil {
		d.enabled = make(map[string]bool)
	}
	d.enabled[name] = enabled
	return controlapi.Profile{Name: name, Enabled: enabled}, nil
}

func (d *fakeDaemon) SetExitNode(_ context.Context, req controlapi.SetExitNodeRequest) (controlapi.ExitNodes, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exitNode = &req
	return controlapi.ExitNodes{}, nil
}

// serveDaemon serves d as the control API of a daemon whose sockets
// are in the returned directory.
func serveDaemon(t *testing.T, d *fakeDaemon) string {
	t.Helper()
	// Unix socket paths are short, so avoid t.TempDir's long names.
	dir, err := os.MkdirTemp("", "anyscale-mix")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	serve(t, profilesocket.ControlPath(dir), controlapi.Handler(d))
	return dir
}

func serve(t *testing.T, path string, h http.Handler) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
}

// fakeLocalAPI records the operator set through a profile's LocalAPI.
type fakeLocalAPI struct {
	mu       sync.Mutex
	operator string
}

func (f *fakeLocalAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/localapi/v0/prefs" || r.Method != http.MethodPatch {
		http.NotFound(w, r)
		return
	}
	var mp ipn.MaskedPrefs
	if err := json.NewDecoder(r.Body).Decode(&mp); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if mp.OperatorUserSet {
		f.mu.Lock()
		f.operator = mp.OperatorUser
		f.mu.Unlock()
	}
	json.NewEncoder(w).Encode(&mp.Prefs)
}

func TestStatusShowsOnlyTailnets(t *testing.T) {
	d := &fakeDaemon{profiles: []controlapi.Profile{
		{ID: "3", Name: "work", Kind: "tailscale"},
		{ID: "1", Name: "home", Kind: "tailscale"},
		{ID: "2", Name: "old", Kind: "tailscale", Removed: true},
		{ID: "4", Name: "wg0", Kind: "wireguard"},
	}}
	c := NewClient(serveDaemon(t, d))

	st, err := c.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tn := range st.Profiles {
		names = append(names, tn.Name)
	}
	if len(names) != 2 || names[0] != "home" || names[1] != "work" {
		t.Fatalf("got tailnets %v, want [home work]", names)
	}
}

func TestHelperAddMakesCallerOperator(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PKEXEC_UID", me.Uid)

	dir, err := os.MkdirTemp("", "anyscale-lapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	lapi := &fakeLocalAPI{}
	sock := filepath.Join(dir, "work.sock")
	serve(t, sock, lapi)

	d := &fakeDaemon{profiles: []controlapi.Profile{{ID: "w", Name: "work", Kind: "tailscale", LocalAPISocket: sock}}}
	err = RunHelper(t.Context(), serveDaemon(t, d), []string{"add", "work"})
	if err != nil {
		t.Fatal(err)
	}

	if len(d.added) != 1 || d.added[0].Name != "work" {
		t.Fatalf("daemon got add requests %+v, want one for work", d.added)
	}
	if lapi.operator != me.Username {
		t.Fatalf("operator is %q, want %q", lapi.operator, me.Username)
	}
}

func TestHelperAddNeedsPkexec(t *testing.T) {
	os.Unsetenv("PKEXEC_UID")
	d := &fakeDaemon{}
	err := RunHelper(t.Context(), serveDaemon(t, d), []string{"add", "work"})
	if err == nil {
		t.Fatal("add without PKEXEC_UID succeeded")
	}
	if len(d.added) != 0 {
		t.Fatalf("daemon got add requests %+v without a calling user", d.added)
	}
}

func TestHelperExitNodeAndEnable(t *testing.T) {
	d := &fakeDaemon{}
	dir := serveDaemon(t, d)

	if err := RunHelper(t.Context(), dir, []string{"exit-node", "work", "nodeABC"}); err != nil {
		t.Fatal(err)
	}
	if d.exitNode == nil || d.exitNode.ProfileName != "work" || d.exitNode.Peer != "nodeABC" {
		t.Fatalf("daemon got exit node %+v, want work/nodeABC", d.exitNode)
	}

	if err := RunHelper(t.Context(), dir, []string{"disable", "work"}); err != nil {
		t.Fatal(err)
	}
	if enabled, ok := d.enabled["work"]; !ok || enabled {
		t.Fatalf("work enabled is %v (set %v), want disabled", enabled, ok)
	}
}

func TestHelperRejectsBadArgs(t *testing.T) {
	dir := serveDaemon(t, &fakeDaemon{})
	for _, args := range [][]string{
		nil,
		{"rm", "-rf"},
		{"add"},
		{"add", "a", "b"},
		{"exit-node", "work"},
		{"clear-exit-node", "extra"},
	} {
		if err := RunHelper(t.Context(), dir, args); err == nil {
			t.Errorf("RunHelper(%q) succeeded", args)
		}
	}
}

func TestLive(t *testing.T) {
	for _, test := range []struct {
		t    Tailnet
		want bool
	}{
		{Tailnet{RuntimeState: "running", BackendState: "Running"}, true},
		{Tailnet{RuntimeState: "starting", BackendState: "Stopped"}, true},
		{Tailnet{RuntimeState: "needs-login", BackendState: "NeedsLogin"}, true},
		{Tailnet{RuntimeState: "error", BackendState: "Running"}, true},
		{Tailnet{RuntimeState: "error"}, false},
		{Tailnet{RuntimeState: "disabled"}, false},
		{Tailnet{RuntimeState: "down"}, false},
	} {
		if got := Live(test.t); got != test.want {
			t.Errorf("Live(%v/%v) = %v, want %v", test.t.RuntimeState, test.t.BackendState, got, test.want)
		}
	}
}

// TestCallingUserRejectsBadUID checks that only numeric UIDs are looked up.
func TestCallingUserRejectsBadUID(t *testing.T) {
	t.Setenv("PKEXEC_UID", "0; rm -rf /")
	if _, err := callingUser(); err == nil {
		t.Fatal("callingUser accepted a non-numeric PKEXEC_UID")
	}
	t.Setenv("PKEXEC_UID", strconv.Itoa(os.Getuid()))
	if _, err := callingUser(); err != nil {
		t.Fatal(err)
	}
}

// deniedDaemon refuses to clear the exit node, as anyscaled does for a
// user outside its operator group.
type deniedDaemon struct{ fakeDaemon }

func (*deniedDaemon) ClearExitNode(context.Context) (controlapi.ExitNodes, error) {
	return controlapi.ExitNodes{}, controlapi.NewError("permission_denied", "nope")
}

func TestHelperTriesDaemonFirst(t *testing.T) {
	d := &fakeDaemon{}
	h := Helper{API: NewClient(serveDaemon(t, d))}
	if err := h.SetExitNode(t.Context(), "work", "nodeABC"); err != nil {
		t.Fatal(err)
	}
	if d.exitNode == nil || d.exitNode.Peer != "nodeABC" {
		t.Fatalf("daemon got exit node %+v, want nodeABC", d.exitNode)
	}

	// Refused, it falls back to pkexec, which needs the binary's path.
	denied := Helper{API: NewClient(serveDaemonBackend(t, &deniedDaemon{}))}
	err := denied.ClearExitNode(t.Context())
	if err == nil || err.Error() != "anyscale binary path unknown" {
		t.Fatalf("refused clear returned %v, want the pkexec fallback's error", err)
	}
}

// serveDaemonBackend is serveDaemon for any backend.
func serveDaemonBackend(t *testing.T, b controlapi.Backend) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "anyscale-mix")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	serve(t, profilesocket.ControlPath(dir), controlapi.Handler(b))
	return dir
}

// removeDaemon records remove requests.
type removeDaemon struct {
	fakeDaemon
	removed []string
	purged  []bool
}

func (d *removeDaemon) RemoveProfile(_ context.Context, name string, purge bool) (controlapi.Profile, error) {
	d.removed = append(d.removed, name)
	d.purged = append(d.purged, purge)
	return controlapi.Profile{Name: name, Removed: true}, nil
}

func TestHelperRemoveAndPurge(t *testing.T) {
	d := &removeDaemon{}
	h := Helper{API: NewClient(serveDaemonBackend(t, d))}
	if err := h.Remove(t.Context(), "work", false); err != nil {
		t.Fatal(err)
	}
	if err := h.Remove(t.Context(), "home", true); err != nil {
		t.Fatal(err)
	}
	if len(d.removed) != 2 || d.purged[0] || !d.purged[1] {
		t.Fatalf("daemon got removes %v with purge %v, want work kept and home purged", d.removed, d.purged)
	}
}
