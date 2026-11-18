package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gabriwar/anyscale/daemon/controlapi"
	"tailscale.com/ipn"
	"tailscale.com/safesocket"
)

type controlServerTestBackend struct {
	controlapi.Backend
	addCalls int
}

func (b *controlServerTestBackend) ListProfiles(context.Context, bool) (controlapi.Profiles, error) {
	return controlapi.Profiles{Profiles: []controlapi.Profile{{Name: "work"}}}, nil
}

func (b *controlServerTestBackend) AddProfile(_ context.Context, request controlapi.AddProfileRequest) (controlapi.Profile, error) {
	b.addCalls++
	return controlapi.Profile{Name: request.Name}, nil
}

func TestControlServerAllowsReadsAndAuthorizesMutations(t *testing.T) {
	if !safesocket.PlatformUsesPeerCreds() {
		t.Skip("platform does not support Unix peer credentials")
	}
	socketDir, err := os.MkdirTemp("/tmp", "anyscale-control-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &controlServerTestBackend{}
	server, err := startControlServer(ctx, socketDir, backend, "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	info, err := os.Stat(server.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0666 {
		t.Fatalf("control socket mode = %04o, want 0666", got)
	}

	client := controlapi.NewClient(socketDir)
	profiles, err := client.Profiles(ctx, false)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if len(profiles.Profiles) != 1 || profiles.Profiles[0].Name != "work" {
		t.Fatalf("profiles = %+v", profiles.Profiles)
	}

	_, err = client.AddProfile(ctx, controlapi.AddProfileRequest{Name: "home"})
	if os.Geteuid() == 0 {
		if err != nil {
			t.Fatalf("root mutation: %v", err)
		}
		if backend.addCalls != 1 {
			t.Fatalf("add calls = %d, want 1", backend.addCalls)
		}
		return
	}
	apiErr, ok := err.(*controlapi.Error)
	if !ok || apiErr.Code != "permission_denied" {
		t.Fatalf("non-root mutation error = %v, want permission_denied", err)
	}
	if backend.addCalls != 0 {
		t.Fatalf("add calls = %d, want 0", backend.addCalls)
	}
}

func TestRequireRootForMutations(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		uid        string
		operator   bool
		wantStatus int
		wantCalled bool
	}{
		{
			name:       "read without credentials",
			method:     http.MethodGet,
			wantStatus: http.StatusNoContent,
			wantCalled: true,
		},
		{
			name:       "root mutation",
			method:     http.MethodPatch,
			uid:        "0",
			wantStatus: http.StatusNoContent,
			wantCalled: true,
		},
		{
			name:       "non-root mutation",
			method:     http.MethodPost,
			uid:        "501",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "operator group mutation",
			method:     http.MethodPost,
			uid:        "1000",
			operator:   true,
			wantStatus: http.StatusNoContent,
			wantCalled: true,
		},
		{
			name:       "mutation without credentials",
			method:     http.MethodDelete,
			wantStatus: http.StatusForbidden,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			isOperator := func(uid string) bool { return test.operator && uid == "1000" }
			handler := requireMutationAccess(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}), isOperator)
			request := httptest.NewRequest(test.method, "/v1/test", nil)
			if test.uid != "" {
				request = request.WithContext(context.WithValue(
					request.Context(), peerUIDContextKey{}, test.uid))
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if called != test.wantCalled {
				t.Fatalf("next handler called = %t, want %t", called, test.wantCalled)
			}
			if test.wantStatus == http.StatusForbidden {
				var got controlapi.Error
				if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if got.Code != "permission_denied" {
					t.Fatalf("error = %+v, want permission_denied", got)
				}
			}
		})
	}
}

// TestAdderOperatorAfterSudoAndEnable checks that root naming an
// operator, as anyscalectl does under sudo, makes that user the operator
// once a profile added disabled is enabled.
func TestAdderOperatorAfterSudoAndEnable(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "anyscaled-op")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "work.sock")
	profile := controlapi.Profile{ID: "w", Name: "work", LocalAPISocket: sock}

	handler := makeAdderOperator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(profile)
	}), dir)
	post := func(path, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), peerUIDContextKey{}, "0"))
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	// Added disabled: no LocalAPI yet, so the operator waits.
	post("/v1/profiles", `{"name":"work","disabled":true,"operator":"`+me.Username+`"}`)

	var mu sync.Mutex
	var operator string
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var mp ipn.MaskedPrefs
		json.NewDecoder(r.Body).Decode(&mp)
		mu.Lock()
		operator = mp.OperatorUser
		mu.Unlock()
		json.NewEncoder(w).Encode(&mp.Prefs)
	})}
	go srv.Serve(l)
	defer srv.Close()

	post("/v1/profiles/by-name/work/enable", "")
	mu.Lock()
	defer mu.Unlock()
	if operator != me.Username {
		t.Fatalf("operator after enable = %q, want %q", operator, me.Username)
	}
}
