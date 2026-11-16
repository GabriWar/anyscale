package tsutil

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"tailscale.com/ipn"
)

// serveFakePrefs serves a LocalAPI on a new socket that answers prefs
// requests with a ControlURL of url, and returns the socket's path.
func serveFakePrefs(t *testing.T, url string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/localapi/v0/prefs" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(&ipn.Prefs{ControlURL: url})
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return path
}

// TestClientsUseTheirOwnSockets checks that two Clients reach two
// different LocalAPIs, as two tailnets in tailmix do.
func TestClientsUseTheirOwnSockets(t *testing.T) {
	work := NewClient(serveFakePrefs(t, "https://headscale.example.com"))
	home := NewClient(serveFakePrefs(t, "https://controlplane.tailscale.com"))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	for c, want := range map[*Client]string{
		work: "https://headscale.example.com",
		home: "https://controlplane.tailscale.com",
	} {
		prefs, err := c.Prefs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if prefs.ControlURL != want {
			t.Errorf("client for %v got ControlURL %q, want %q", c.Socket(), prefs.ControlURL, want)
		}
	}
}

// TestExitNodeChoicePrefersPrior checks that turning an exit node back
// on reuses the one that was in use, without asking for a suggestion.
func TestExitNodeChoicePrefersPrior(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prior.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/localapi/v0/prefs":
			json.NewEncoder(w).Encode(&ipn.Prefs{InternalExitNodePrior: "nPrior"})
		case "/localapi/v0/suggest-exit-node":
			json.NewEncoder(w).Encode(map[string]string{"ID": "nSuggested", "Name": "suggested"})
		default:
			http.NotFound(w, r)
		}
	})}
	go srv.Serve(l)
	defer srv.Close()

	c := NewClient(path)
	got, err := c.ExitNodeChoice(t.Context(), SelectPreviousExitNode)
	if err != nil || got != "nPrior" {
		t.Fatalf("previous: got %q, %v; want nPrior", got, err)
	}
	got, err = c.ExitNodeChoice(t.Context(), SelectRecommendedExitNode)
	if err != nil || got != "nSuggested" {
		t.Fatalf("recommended: got %q, %v; want nSuggested", got, err)
	}
}
