package mix

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"github.com/maisem/tailmix/controlapi"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
)

// HelperCommand is the first argument that puts the anyscale binary
// into helper mode.
const HelperCommand = "helper"

// Helper operations, as passed after [HelperCommand].
const (
	opAdd         = "add"
	opRemove      = "remove"
	opPurge       = "purge"
	opEnable      = "enable"
	opDisable     = "disable"
	opSetOperator = "set-operator"
	opExitNode    = "exit-node"
	opClearExit   = "clear-exit-node"
)

// Helper runs privileged daemon operations. It asks the daemon
// directly first, which works for root and for members of anyscaled's
// operator group. If the daemon refuses, it starts the anyscale
// binary's helper mode as root with pkexec, so polkit asks for
// permission.
type Helper struct {
	// API, if not nil, is tried before pkexec.
	API *Client

	// Exe is the absolute path of the anyscale binary.
	Exe string
}

// direct runs f against the daemon if h has a client. It reports
// whether the result is final, which it isn't when the daemon refused
// for lack of permission.
func (h Helper) direct(f func(*Client) error) (error, bool) {
	if h.API == nil {
		return nil, false
	}
	err := f(h.API)
	var apiErr *controlapi.Error
	if errors.As(err, &apiErr) && apiErr.Code == "permission_denied" {
		return nil, false
	}
	return err, true
}

func (h Helper) run(ctx context.Context, args ...string) error {
	if h.Exe == "" {
		return errors.New("anyscale binary path unknown")
	}
	cmd := exec.CommandContext(ctx, "pkexec", append([]string{h.Exe, HelperCommand}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// pkexec exits with 126 when the password dialog is dismissed
		// and with 127 when authorization fails, including when no
		// polkit agent is running. The helper itself never uses either.
		switch exitErr.ExitCode() {
		case 126:
			return ErrNotAuthorized
		case 127:
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return fmt.Errorf("%w: %s", ErrNotAuthorized, msg)
			}
			return ErrNotAuthorized
		}
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return errors.New(msg)
	}
	return err
}

// ErrNotAuthorized is returned when polkit doesn't allow a privileged
// operation, including when the password dialog is dismissed.
var ErrNotAuthorized = errors.New("not authorized")

// Add adds a tailnet named name and makes the calling user its
// operator, so that they can log in and connect without root.
func (h Helper) Add(ctx context.Context, name string) error {
	// anyscaled makes a non-root caller that it lets add a tailnet that
	// tailnet's operator.
	if err, ok := h.direct(func(c *Client) error {
		_, err := c.api.AddProfile(ctx, controlapi.AddProfileRequest{Name: name})
		return err
	}); ok {
		return err
	}
	return h.run(ctx, opAdd, name)
}

// Remove removes the tailnet named name. Unless purge is true, the
// daemon keeps its identity, so adding the same name again restores
// it. With purge, its identity and state are deleted for good.
func (h Helper) Remove(ctx context.Context, name string, purge bool) error {
	if err, ok := h.direct(func(c *Client) error {
		_, err := c.api.RemoveProfile(ctx, name, purge)
		return err
	}); ok {
		return err
	}
	if purge {
		return h.run(ctx, opPurge, name)
	}
	return h.run(ctx, opRemove, name)
}

// SetEnabled starts or stops the node of the tailnet named name.
func (h Helper) SetEnabled(ctx context.Context, name string, enabled bool) error {
	action := opDisable
	if enabled {
		action = opEnable
	}
	if err, ok := h.direct(func(c *Client) error {
		_, err := c.api.ProfileAction(ctx, name, action)
		return err
	}); ok {
		return err
	}
	if enabled {
		return h.run(ctx, opEnable, name)
	}
	return h.run(ctx, opDisable, name)
}

// SetOperator makes the calling user the operator of the tailnet named
// name.
func (h Helper) SetOperator(ctx context.Context, name string) error {
	return h.run(ctx, opSetOperator, name)
}

// SetExitNode routes all traffic through peer of the tailnet named
// name. tailmix allows only one exit node across all tailnets.
func (h Helper) SetExitNode(ctx context.Context, name, peer string) error {
	if err, ok := h.direct(func(c *Client) error {
		_, err := c.api.SetExitNode(ctx, controlapi.SetExitNodeRequest{ProfileName: name, Peer: peer})
		return err
	}); ok {
		return err
	}
	return h.run(ctx, opExitNode, name, peer)
}

// ClearExitNode stops using an exit node.
func (h Helper) ClearExitNode(ctx context.Context) error {
	if err, ok := h.direct(func(c *Client) error {
		_, err := c.api.ClearExitNode(ctx)
		return err
	}); ok {
		return err
	}
	return h.run(ctx, opClearExit)
}

// RunHelper performs the helper operation in args, which are the
// arguments after [HelperCommand]. It talks to the daemon at socketDir
// and must run as root. The operator for new tailnets is the user that
// pkexec says started it, never one from args.
func RunHelper(ctx context.Context, socketDir string, args []string) error {
	if len(args) == 0 {
		return errors.New("missing helper operation")
	}
	c := NewClient(socketDir)
	op, args := args[0], args[1:]

	want := map[string]int{
		opAdd: 1, opRemove: 1, opPurge: 1, opEnable: 1, opDisable: 1, opSetOperator: 1,
		opExitNode: 2, opClearExit: 0,
	}
	n, ok := want[op]
	if !ok {
		return fmt.Errorf("unknown helper operation %q", op)
	}
	if len(args) != n {
		return fmt.Errorf("%v takes %v arguments, got %v", op, n, len(args))
	}

	switch op {
	case opAdd:
		operator, err := callingUser()
		if err != nil {
			return err
		}
		t, err := c.api.AddProfile(ctx, controlapi.AddProfileRequest{Name: args[0]})
		if err != nil {
			return err
		}
		return c.setOperator(ctx, t, operator)

	case opRemove, opPurge:
		_, err := c.api.RemoveProfile(ctx, args[0], op == opPurge)
		return err

	case opEnable, opDisable:
		_, err := c.api.ProfileAction(ctx, args[0], op)
		return err

	case opSetOperator:
		operator, err := callingUser()
		if err != nil {
			return err
		}
		t, err := c.api.Profile(ctx, args[0])
		if err != nil {
			return err
		}
		return c.setOperator(ctx, t, operator)

	case opExitNode:
		_, err := c.api.SetExitNode(ctx, controlapi.SetExitNodeRequest{ProfileName: args[0], Peer: args[1]})
		return err

	case opClearExit:
		_, err := c.api.ClearExitNode(ctx)
		return err
	}
	panic("unreachable")
}

// setOperator lets operator change t's settings through its LocalAPI,
// as with tailscale set --operator.
func (c *Client) setOperator(ctx context.Context, t Tailnet, operator string) error {
	path, err := c.Socket(t)
	if err != nil {
		return err
	}
	lc := local.Client{Socket: path, UseSocketOnly: true}
	_, err = lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:           ipn.Prefs{OperatorUser: operator},
		OperatorUserSet: true,
	})
	if err != nil {
		return fmt.Errorf("make %v the operator of %v: %w", operator, t.Name, err)
	}
	return nil
}

// callingUser returns the name of the user that ran pkexec.
func callingUser() (string, error) {
	uid, ok := os.LookupEnv("PKEXEC_UID")
	if !ok {
		return "", errors.New("PKEXEC_UID not set; the helper must be started by pkexec")
	}
	if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
		return "", fmt.Errorf("invalid PKEXEC_UID %q", uid)
	}
	u, err := user.LookupId(uid)
	if err != nil {
		return "", fmt.Errorf("look up calling user: %w", err)
	}
	return u.Username, nil
}
