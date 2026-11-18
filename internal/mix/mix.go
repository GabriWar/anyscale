// Package mix connects anyscale to the anyscale daemon, which runs one
// Tailscale node per tailnet on the same machine.
//
// Reading the daemon's state works as any user. Changing it, such as
// adding a tailnet or picking an exit node, needs root, so those
// operations run the anyscale binary's helper mode through pkexec. See
// [Helper] and [RunHelper].
package mix

import (
	"cmp"
	"context"
	"slices"

	"github.com/gabriwar/anyscale/daemon/controlapi"
	"github.com/gabriwar/anyscale/daemon/profilesocket"
)

// Tailnet is one anyscale profile, which is one tailnet.
type Tailnet = controlapi.Profile

// Status is a snapshot of every tailnet and the daemon-wide policy.
type Status = controlapi.Status

// Client reads state from the anyscale daemon. Its zero value is not
// usable. Use [NewClient].
type Client struct {
	api       *controlapi.Client
	socketDir string
}

// NewClient returns a client for the daemon whose sockets are in
// socketDir. An empty socketDir uses anyscale's default.
func NewClient(socketDir string) *Client {
	if socketDir == "" {
		socketDir = profilesocket.DefaultDir()
	}
	return &Client{
		api:       controlapi.NewClient(socketDir),
		socketDir: socketDir,
	}
}

// Status returns the daemon's current state with only the tailnets
// that anyscale shows, sorted by name.
func (c *Client) Status(ctx context.Context) (Status, error) {
	st, err := c.api.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	st.Profiles = Tailnets(st.Profiles)
	return st, nil
}

// Tailnets returns the Tailscale profiles in profiles that haven't
// been removed, sorted by name. Raw WireGuard profiles aren't
// tailnets, so they're left out.
func Tailnets(profiles []controlapi.Profile) []Tailnet {
	tailnets := make([]Tailnet, 0, len(profiles))
	for _, p := range profiles {
		if p.Removed || (p.Kind != "" && p.Kind != "tailscale") {
			continue
		}
		tailnets = append(tailnets, p)
	}
	slices.SortFunc(tailnets, func(a, b Tailnet) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return tailnets
}

// Socket returns the path of t's LocalAPI socket, which the tailscale
// CLI and LocalAPI clients can use as if it were tailscaled's.
func (c *Client) Socket(t Tailnet) (string, error) {
	if t.LocalAPISocket != "" {
		return t.LocalAPISocket, nil
	}
	return profilesocket.Path(c.socketDir, t.ID)
}

// Find returns the tailnet in st with the given profile ID.
func Find(st Status, id string) (Tailnet, bool) {
	i := slices.IndexFunc(st.Profiles, func(t Tailnet) bool { return t.ID == id })
	if i < 0 {
		return Tailnet{}, false
	}
	return st.Profiles[i], true
}

// Live reports whether t has a running node, and so a LocalAPI socket.
// Disabled tailnets and tailnets of a stopped daemon don't.
func Live(t Tailnet) bool {
	switch t.RuntimeState {
	case "running", "starting", "needs-login":
		return true
	case "error":
		// Only tailnets with a node report a backend state.
		return t.BackendState != ""
	}
	return false
}

// Connected reports whether t's node is up and logged in.
func Connected(t Tailnet) bool {
	return t.BackendState == "Running"
}
