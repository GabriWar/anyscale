package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"slices"
	"sync"

	"github.com/gabriwar/anyscale/daemon/controlapi"
	"github.com/gabriwar/anyscale/daemon/profilesocket"
	"github.com/tailscale/peercred"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/safesocket"
)

type peerUIDContextKey struct{}

type controlServer struct {
	path      string
	listener  net.Listener
	http      *http.Server
	done      chan error
	closeOnce sync.Once
	closeErr  error
}

func startControlServer(ctx context.Context, socketDir string, backend controlapi.Backend, operatorGroup string) (*controlServer, error) {
	path := profilesocket.ControlPath(socketDir)
	listener, err := safesocket.Listen(path)
	if err != nil {
		return nil, fmt.Errorf("listen on daemon control socket %s: %w", path, err)
	}
	mode := os.FileMode(0600)
	if safesocket.PlatformUsesPeerCreds() {
		mode = 0666
	}
	if err := os.Chmod(path, mode); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("secure daemon control socket %s: %w", path, err)
	}
	handler := controlapi.Handler(backend)
	httpServer := &http.Server{Handler: handler}
	if safesocket.PlatformUsesPeerCreds() {
		isOperator := groupMember(operatorGroup)
		handler = makeAdderOperator(handler, socketDir, isOperator)
		httpServer.Handler = requireMutationAccess(handler, isOperator)
		httpServer.ConnContext = controlConnContext
	}
	server := &controlServer{
		path:     path,
		listener: listener,
		http:     httpServer,
		done:     make(chan error, 1),
	}
	go func() {
		err := server.http.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			err = nil
		}
		server.done <- err
	}()
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	return server, nil
}

func controlConnContext(ctx context.Context, conn net.Conn) context.Context {
	creds, err := peercred.Get(conn)
	if err != nil {
		return ctx
	}
	uid, ok := creds.UserID()
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, peerUIDContextKey{}, uid)
}

func requireRootForMutations(next http.Handler) http.Handler {
	return requireMutationAccess(next, nil)
}

// requireMutationAccess lets root, and users for which isOperator
// returns true, make changes. isOperator may be nil.
func requireMutationAccess(next http.Handler, isOperator func(uid string) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		uid, _ := r.Context().Value(peerUIDContextKey{}).(string)
		if uid != "0" && (uid == "" || isOperator == nil || !isOperator(uid)) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(controlapi.NewError(
				"permission_denied", "anyscale management commands require root or the daemon's operator group"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *controlServer) Errors() <-chan error { return s.done }

func (s *controlServer) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = errors.Join(s.http.Close(), s.listener.Close())
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.closeErr = errors.Join(s.closeErr, err)
		}
	})
	return s.closeErr
}

// groupMember returns a function that reports whether the user with a
// given UID is in the group named group. It returns nil if group is
// empty, so that only root may make changes.
func groupMember(group string) func(uid string) bool {
	if group == "" {
		return nil
	}
	return func(uid string) bool {
		g, err := user.LookupGroup(group)
		if err != nil {
			return false
		}
		u, err := user.LookupId(uid)
		if err != nil {
			return false
		}
		ids, err := u.GroupIds()
		return err == nil && slices.Contains(ids, g.Gid)
	}
}

// makeAdderOperator makes an operator who adds a profile the operator of
// its Tailscale node too, so that they can log in and connect without
// root, as with tailscale set --operator.
func makeAdderOperator(next http.Handler, socketDir string, isOperator func(uid string) bool) http.Handler {
	if isOperator == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := r.Context().Value(peerUIDContextKey{}).(string)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/profiles" || uid == "" || uid == "0" {
			next.ServeHTTP(w, r)
			return
		}

		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		if rec.Code >= 200 && rec.Code < 300 {
			var profile controlapi.Profile
			if json.Unmarshal(rec.Body.Bytes(), &profile) == nil {
				if err := setOperator(r.Context(), socketDir, profile, uid); err != nil {
					fmt.Fprintf(os.Stderr, "set operator of profile %s: %v\n", profile.ID, err)
				}
			}
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
	})
}

func setOperator(ctx context.Context, socketDir string, profile controlapi.Profile, uid string) error {
	u, err := user.LookupId(uid)
	if err != nil {
		return err
	}
	path := profile.LocalAPISocket
	if path == "" {
		if path, err = profilesocket.Path(socketDir, profile.ID); err != nil {
			return err
		}
	}
	lc := local.Client{Socket: path, UseSocketOnly: true}
	_, err = lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:           ipn.Prefs{OperatorUser: u.Username},
		OperatorUserSet: true,
	})
	return err
}
