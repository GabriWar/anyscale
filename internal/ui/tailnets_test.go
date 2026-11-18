package ui

import (
	"testing"

	"github.com/gabriwar/anyscale/internal/mix"
)

func TestTailnetState(t *testing.T) {
	for _, test := range []struct {
		t    mix.Tailnet
		exit bool
		want string
	}{
		{mix.Tailnet{RuntimeState: "running", BackendState: "Running", PeerCount: 25}, false, "Connected · 25 peers"},
		{mix.Tailnet{RuntimeState: "running", BackendState: "Running", PeerCount: 3}, true, "Connected · 3 peers · exit node"},
		{mix.Tailnet{RuntimeState: "starting", BackendState: "Stopped"}, false, "Disconnected"},
		{mix.Tailnet{RuntimeState: "starting", BackendState: "Starting"}, false, "Starting…"},
		{mix.Tailnet{RuntimeState: "needs-login", BackendState: "NeedsLogin"}, false, "Needs login"},
		{mix.Tailnet{RuntimeState: "disabled"}, false, "Disabled"},
		{mix.Tailnet{RuntimeState: "down"}, false, "Daemon stopped"},
		{mix.Tailnet{RuntimeState: "error", LastError: "boom"}, false, "Error"},
	} {
		if got := tailnetState(test.t, test.exit); got != test.want {
			t.Errorf("tailnetState(%v/%v, %v) = %q, want %q", test.t.RuntimeState, test.t.BackendState, test.exit, got, test.want)
		}
	}
}

func TestDefaultTailnetPrefersConnected(t *testing.T) {
	st := mix.Status{Profiles: []mix.Tailnet{
		{ID: "a", Name: "alpha", BackendState: "Stopped"},
		{ID: "b", Name: "beta", BackendState: "Running"},
	}}
	if got := defaultTailnet(st); got != "b" {
		t.Errorf("defaultTailnet = %q, want the connected one, b", got)
	}
	st.Profiles[1].BackendState = "Stopped"
	if got := defaultTailnet(st); got != "a" {
		t.Errorf("defaultTailnet with none connected = %q, want the first, a", got)
	}
	if got := defaultTailnet(mix.Status{}); got != "" {
		t.Errorf("defaultTailnet with no tailnets = %q, want empty", got)
	}
}

func TestValidTailnetName(t *testing.T) {
	for name, want := range map[string]bool{
		"work":       true,
		" home ":     true,
		"":           false,
		"   ":        false,
		"two words":  false,
		"../escape":  false,
		"multi\nrow": false,
	} {
		if got := validTailnetName(name); got != want {
			t.Errorf("validTailnetName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestEscapeMarkup(t *testing.T) {
	if got := escapeMarkup("R&D <lab>"); got != "R&amp;D &lt;lab&gt;" {
		t.Errorf("escapeMarkup = %q", got)
	}
}

func TestUsesTailscaleControl(t *testing.T) {
	for url, want := range map[string]bool{
		"":                                       true,
		"https://controlplane.tailscale.com":     true,
		"https://login.tailscale.com":            true,
		"https://headscale.example.com": false,
	} {
		if got := usesTailscaleControl(url); got != want {
			t.Errorf("usesTailscaleControl(%q) = %v, want %v", url, got, want)
		}
	}
}
