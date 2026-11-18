package tsutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"sync"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/cmd/tailscale/cli"
	"tailscale.com/ipn"
	"tailscale.com/net/netcheck"
	"tailscale.com/net/netmon"
	"tailscale.com/tailcfg"
	"tailscale.com/types/logger"
	"tailscale.com/util/eventbus"
)

// Client talks to one tailscaled-compatible LocalAPI socket. With
// anyscale, each tailnet has its own socket and so its own Client.
type Client struct {
	lc local.Client

	// socket is passed to the tailscale CLI. Empty means its default.
	socket string
}

// NewClient returns a Client for the LocalAPI socket at path. An empty
// path uses tailscaled's default socket.
func NewClient(path string) *Client {
	c := &Client{socket: path}
	if path != "" {
		c.lc.Socket = path
		c.lc.UseSocketOnly = true
	}
	return c
}

// Socket returns the LocalAPI socket path that c uses, or an empty
// string for tailscaled's default socket.
func (c *Client) Socket() string {
	return c.socket
}

// cliMu serializes tailscale CLI runs, as the CLI keeps its socket in
// a package variable.
var cliMu sync.Mutex

// runCLI runs the tailscale CLI with args against c's socket.
func (c *Client) runCLI(ctx context.Context, args ...string) error {
	if c.socket != "" {
		args = append([]string{"--socket=" + c.socket}, args...)
	}
	cliMu.Lock()
	defer cliMu.Unlock()
	return cli.RunWithContext(ctx, args)
}

var (
	// defaultClient is used by Pollers without a Client.
	defaultClient Client

	bus            = eventbus.New()
	monitor        = initMonitor()
	netcheckClient = netcheck.Client{
		NetMon: monitor,
		Logf:   logger.Discard,
	}
)

func initMonitor() *netmon.Monitor {
	monitor, err := netmon.New(bus, logger.Discard)
	if err != nil {
		slog.Error("init netmon monitor", "err", err)
	}
	return monitor
}

// Prefs returns the options of the local node.
func (c *Client) Prefs(ctx context.Context) (*ipn.Prefs, error) {
	return c.lc.GetPrefs(ctx)
}

// Start connects the local peer to the Tailscale network.
func (c *Client) Start(ctx context.Context) error {
	return c.runCLI(ctx, "up")
}

// Stop disconnects the local peer from the Tailscale network.
func (c *Client) Stop(ctx context.Context) error {
	return c.runCLI(ctx, "down")
}

// ExitNode uses the specified peer as an exit node, or unsets
// an existing exit node if peer is an empty string. Unsetting it this
// way also makes Tailscale forget the previous exit node, so use
// [DisableExitNode] to turn exit node use off instead.
func (c *Client) ExitNode(ctx context.Context, peer tailcfg.StableNodeID) error {
	if peer == "" {
		var prefs ipn.Prefs
		prefs.ClearExitNode()
		_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
			Prefs:           prefs,
			ExitNodeIDSet:   true,
			ExitNodeIPSet:   true,
			AutoExitNodeSet: true,
		})
		if err != nil {
			return fmt.Errorf("edit prefs: %w", err)
		}
		return nil
	}

	prefs := ipn.Prefs{
		ExitNodeID: peer,
	}
	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:           prefs,
		ExitNodeIDSet:   true,
		ExitNodeIPSet:   true,
		AutoExitNodeSet: true,
	})
	if err != nil {
		return fmt.Errorf("edit prefs: %w", err)
	}

	return nil
}

// ExitNodeSelection is how an exit node is chosen when exit node use
// is turned on.
type ExitNodeSelection int

const (
	// SelectPreviousExitNode reuses the most recently used exit node.
	SelectPreviousExitNode ExitNodeSelection = iota

	// SelectRecommendedExitNode lets the daemon pick and keep
	// tracking the recommended exit node.
	SelectRecommendedExitNode
)

// ParseExitNodeSelection parses a setting value as written by
// [ExitNodeSelection.String]. Unknown values select the previous exit
// node.
func ParseExitNodeSelection(str string) ExitNodeSelection {
	if str == "recommended" {
		return SelectRecommendedExitNode
	}
	return SelectPreviousExitNode
}

func (sel ExitNodeSelection) String() string {
	if sel == SelectRecommendedExitNode {
		return "recommended"
	}
	return "previous"
}

// SetUseExitNode turns exit node use on or off. When turning it on,
// sel determines which exit node is used.
func (c *Client) SetUseExitNode(ctx context.Context, use bool, sel ExitNodeSelection) error {
	if use && sel == SelectRecommendedExitNode {
		err := c.useAutoExitNode(ctx)
		if err == nil {
			return nil
		}
		slog.Error("use automatic exit node", "err", err)
		return errors.Join(err, c.useSuggestedExitNode(ctx))
	}

	if !use {
		return c.DisableExitNode(ctx)
	}

	useErr := c.lc.SetUseExitNode(ctx, true)
	if useErr != nil {
		suggestErr := c.useSuggestedExitNode(ctx)
		if suggestErr == nil {
			return nil
		}
		return errors.Join(useErr, suggestErr)
	}

	return c.keepPreviousExitNode(ctx)
}

// ExitNodeChoice returns the exit node to use when exit node use is
// turned on with sel. It doesn't change anything, so that something
// else, such as anyscale, can apply the choice. Without a previous exit
// node, it falls back to the recommended one.
func (c *Client) ExitNodeChoice(ctx context.Context, sel ExitNodeSelection) (tailcfg.StableNodeID, error) {
	if sel == SelectPreviousExitNode {
		prefs, err := c.Prefs(ctx)
		if err != nil {
			return "", fmt.Errorf("get prefs: %w", err)
		}
		if prior := prefs.InternalExitNodePrior; prior != "" {
			return prior, nil
		}
	}
	return c.suggestExitNode(ctx)
}

// DisableExitNode turns exit node use off. Tailscale remembers the
// exit node that was in use, so [SelectPreviousExitNode] can get it
// back.
func (c *Client) DisableExitNode(ctx context.Context) error {
	if err := c.pinAutoExitNode(ctx); err != nil {
		slog.Error("pin automatic exit node before disabling", "err", err)
	}
	return c.lc.SetUseExitNode(ctx, false)
}

// pinAutoExitNode replaces automatic exit node selection with the node
// that it currently resolves to. The daemon remembers automatic mode,
// rather than the node, as the prior exit node when exit node use is
// turned off, so without this, turning it back on with
// [SelectPreviousExitNode] can't get the node that was in use back.
func (c *Client) pinAutoExitNode(ctx context.Context) error {
	prefs, err := c.Prefs(ctx)
	if err != nil {
		return fmt.Errorf("get prefs: %w", err)
	}
	id := prefs.ExitNodeID
	if !prefs.AutoExitNode.IsSet() || id == "" {
		return nil
	}
	if _, ok := ipn.ParseAutoExitNodeString(id); ok {
		// Not resolved yet, so there's no node to remember.
		return nil
	}

	_, err = c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		ExitNodeID:      id,
		AutoExitNodeSet: true,
		ExitNodeIDSet:   true,
		ExitNodeIPSet:   true,
	})
	if err != nil {
		return fmt.Errorf("edit prefs: %w", err)
	}
	return nil
}

func (c *Client) useAutoExitNode(ctx context.Context) error {
	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		AutoExitNode:    ipn.AnyExitNode,
		AutoExitNodeSet: true,
	})
	if err != nil {
		return fmt.Errorf("edit prefs: %w", err)
	}
	return nil
}

// errNoRecommendedExitNode is returned when Tailscale has no exit node
// to recommend. Tailscale reports that as a suggestion with no ID, not
// as an error.
var errNoRecommendedExitNode = errors.New("no exit node to recommend")

func (c *Client) suggestExitNode(ctx context.Context) (tailcfg.StableNodeID, error) {
	suggested, err := c.lc.SuggestExitNode(ctx)
	if err != nil {
		return "", fmt.Errorf("suggest exit node: %w", err)
	}
	if suggested.ID == "" {
		return "", errNoRecommendedExitNode
	}
	slog.Info("got suggested exit node", "id", suggested.ID, "name", suggested.Name, "location", suggested.Location)
	return suggested.ID, nil
}

func (c *Client) useSuggestedExitNode(ctx context.Context) error {
	id, err := c.suggestExitNode(ctx)
	if err != nil {
		return err
	}
	return c.ExitNode(ctx, id)
}

// keepPreviousExitNode pins the exit node that the daemon just
// restored. Turning exit node use off while automatic selection was on
// makes the daemon remember that, so turning it back on would resume
// automatic selection instead of using the previous node.
func (c *Client) keepPreviousExitNode(ctx context.Context) error {
	prefs, err := c.Prefs(ctx)
	if err != nil {
		return fmt.Errorf("get prefs: %w", err)
	}
	if !prefs.AutoExitNode.IsSet() {
		return nil
	}

	id := prefs.ExitNodeID
	// The daemon uses this placeholder until it has picked a node.
	if id == "" || id == "auto:any" {
		id, err = c.suggestExitNode(ctx)
		if err != nil {
			// Turn it back off rather than leave automatic selection on
			// with no node. Tailscale remembers automatic mode again.
			return errors.Join(err, c.lc.SetUseExitNode(ctx, false))
		}
	}

	_, err = c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		ExitNodeID:      id,
		AutoExitNodeSet: true,
		ExitNodeIDSet:   true,
		ExitNodeIPSet:   true,
	})
	if err != nil {
		return fmt.Errorf("edit prefs: %w", err)
	}
	return nil
}

// AdvertiseExitNode enables and disables exit node advertisement for
// the current node.
func (c *Client) AdvertiseExitNode(ctx context.Context, enable bool) error {
	prefs, err := c.Prefs(ctx)
	if err != nil {
		return fmt.Errorf("get prefs: %w", err)
	}
	prefs.SetAdvertiseExitNode(enable)

	_, err = c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:              *prefs,
		AdvertiseRoutesSet: true,
	})
	if err != nil {
		return fmt.Errorf("edit prefs: %w", err)
	}

	return nil
}

func (c *Client) AdvertiseRoutes(ctx context.Context, routes []netip.Prefix) error {
	prefs, err := c.Prefs(ctx)
	if err != nil {
		return fmt.Errorf("get prefs: %w", err)
	}
	exit := prefs.AdvertisesExitNode()
	prefs.AdvertiseRoutes = routes
	prefs.SetAdvertiseExitNode(exit)

	_, err = c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:              *prefs,
		AdvertiseRoutesSet: true,
	})
	if err != nil {
		return fmt.Errorf("edit prefs: %w", err)
	}

	return nil
}

// AllowLANAccess enables and disables the ability for the current
// node to get access to the regular LAN that it is connected to while
// an exit node is in use.
func (c *Client) AllowLANAccess(ctx context.Context, allow bool) error {
	prefs := ipn.Prefs{
		ExitNodeAllowLANAccess: allow,
	}

	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:                     prefs,
		ExitNodeAllowLANAccessSet: true,
	})
	if err != nil {
		return fmt.Errorf("edit prefs: %w", err)
	}

	return nil
}

// AcceptRoutes sets whether or not all shared subnet routes from
// other nodes should be used by the local node.
func (c *Client) AcceptRoutes(ctx context.Context, accept bool) error {
	prefs := ipn.Prefs{
		RouteAll: accept,
	}

	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:       prefs,
		RouteAllSet: true,
	})
	if err != nil {
		return fmt.Errorf("edit prefs: %w", err)
	}

	return nil
}

// AcceptDNS sets whether or not the Tailscale DNS config should be
// used.
func (c *Client) AcceptDNS(ctx context.Context, accept bool) error {
	prefs := ipn.Prefs{
		CorpDNS: accept,
	}

	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:      prefs,
		CorpDNSSet: true,
	})
	if err != nil {
		return fmt.Errorf("edit prefs: %w", err)
	}

	return nil
}

// SetControlURL changes the URL of the control plane server used by
// the daemon. If controlURL is empty, the default Tailscale server is
// used.
func (c *Client) SetControlURL(ctx context.Context, controlURL string) error {
	prefs, err := c.Prefs(ctx)
	if err != nil {
		return fmt.Errorf("get prefs: %w", err)
	}
	prefs.ControlURL = controlURL

	err = c.lc.Start(ctx, ipn.Options{
		UpdatePrefs: prefs,
	})
	if err != nil {
		return fmt.Errorf("start local client: %w", err)
	}

	return nil
}

func (c *Client) NetCheck(ctx context.Context, full bool) (*netcheck.Report, *tailcfg.DERPMap, error) {
	err := netcheckClient.Standalone(ctx, "")
	if err != nil {
		return nil, nil, fmt.Errorf("standalone: %w", err)
	}

	dm, err := c.lc.CurrentDERPMap(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("current DERP map: %w", err)
	}

	if full {
		netcheckClient.MakeNextReportFull()
	}
	r, err := netcheckClient.GetReport(ctx, dm, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("get netcheck report: %w", err)
	}

	return r, dm, nil
}

func (c *Client) PushFile(ctx context.Context, target tailcfg.StableNodeID, size int64, name string, r io.Reader) error {
	return c.lc.PushFile(ctx, target, size, name, r)
}

func (c *Client) GetWaitingFile(ctx context.Context, name string) (io.ReadCloser, int64, error) {
	return c.lc.GetWaitingFile(ctx, name)
}

func (c *Client) DeleteWaitingFile(ctx context.Context, name string) error {
	return c.lc.DeleteWaitingFile(ctx, name)
}

// WaitingFiles polls for any pending incoming files. It returns
// quickly if there are no files currently pending.
func (c *Client) WaitingFiles(ctx context.Context) ([]apitype.WaitingFile, error) {
	return c.lc.AwaitWaitingFiles(ctx, 0)
}

func (c *Client) FileTargets(ctx context.Context) ([]apitype.FileTarget, error) {
	return c.lc.FileTargets(ctx)
}

func (c *Client) GetProfileStatus(ctx context.Context) (ipn.LoginProfile, []ipn.LoginProfile, error) {
	return c.lc.ProfileStatus(ctx)
}

func (c *Client) SwitchProfile(ctx context.Context, id ipn.ProfileID) error {
	return c.lc.SwitchProfile(ctx, id)
}

func (c *Client) StartLogin(ctx context.Context) error {
	return c.lc.StartLoginInteractive(ctx)
}
