package tray

import (
	"bytes"
	_ "embed"
	"fmt"
	"image/png"
	"slices"
	"sync"
	"unique"

	"deedles.dev/tray"
	"github.com/gabriwar/anyscale/internal/locale"
	"github.com/gabriwar/anyscale/internal/tsutil"
)

var (
	//go:embed status-icon-active.png
	statusIconActiveData []byte
	statusIconActive     = decode(statusIconActiveData)

	//go:embed status-icon-inactive.png
	statusIconInactiveData []byte
	statusIconInactive     = decode(statusIconInactiveData)

	//go:embed status-icon-exit-node.png
	statusIconExitNodeData []byte
	statusIconExitNode     = decode(statusIconExitNodeData)

	selfHandle       = unique.Make("self")
	connToggleHandle = unique.Make("connToggle")
	exitToggleHandle = unique.Make("exitToggle")
	statusIconHandle = unique.Make("statusIcon")
)

func decode(data []byte) tray.Pixmap {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	return tray.ToPixmap(img)
}

func handler(f func()) tray.MenuItemProp {
	return tray.MenuItemHandler(tray.ClickedHandler(func(data any, timestamp uint32) error {
		f()
		return nil
	}))
}

type Tray struct {
	OnShow          func()
	OnConnToggle    func()
	OnExitToggle    func()
	OnSelfNode      func()
	OnTailnetToggle func(id string)
	OnQuit          func()

	m    sync.Mutex
	item *tray.Item
	prev map[unique.Handle[string]][]any

	showItem       *tray.MenuItem
	connToggleItem *tray.MenuItem
	exitToggleItem *tray.MenuItem
	selfNodeItem   *tray.MenuItem
	quitSep        *tray.MenuItem
	tailnetSep     *tray.MenuItem
	tailnetIDs     []string
	tailnetItems   map[string]*tray.MenuItem
	tailnets       map[string]Tailnet
	quitItem       *tray.MenuItem
}

// Tailnet is one tailnet's entry in the tray menu.
type Tailnet struct {
	ID        string
	Name      string
	Connected bool
}

// Start registers the tray icon. The menu shows a disconnected state
// until the first call to Update.
func (t *Tray) Start(tailnets []Tailnet) error {
	if t.item != nil {
		return nil
	}

	t.m.Lock()
	defer t.m.Unlock()

	item, err := tray.New(
		tray.ItemID("io.github.gabriwar.Anyscale"),
		tray.ItemTitle("Anyscale"),
		tray.ItemIconPixmap(&statusIconInactive),
		tray.ItemHandler(tray.ActivateHandler(func(x, y int) error {
			t.OnShow()
			return nil
		})),
	)
	if err != nil {
		return err
	}
	t.item = item
	t.prev = make(map[unique.Handle[string]][]any)

	menu := item.Menu()

	t.tailnetItems = make(map[string]*tray.MenuItem)
	t.tailnets = make(map[string]Tailnet)

	t.showItem, _ = menu.AddChild(tray.MenuItemLabel(locale.Get("Show")), handler(t.OnShow))
	menu.AddChild(tray.MenuItemType(tray.Separator))
	t.connToggleItem, _ = menu.AddChild(
		tray.MenuItemLabel(connToggleText(false)),
		handler(t.OnConnToggle),
	)
	t.exitToggleItem, _ = menu.AddChild(
		tray.MenuItemLabel(locale.Get("Enable exit node")),
		tray.MenuItemEnabled(false),
		handler(t.OnExitToggle),
	)
	t.selfNodeItem, _ = menu.AddChild(
		tray.MenuItemLabel(locale.Get("This machine: %v", locale.Get("Not connected"))),
		tray.MenuItemEnabled(false),
		handler(t.OnSelfNode),
	)
	t.quitSep, _ = menu.AddChild(tray.MenuItemType(tray.Separator))
	t.quitItem, _ = menu.AddChild(tray.MenuItemLabel(locale.Get("Quit")), handler(t.OnQuit))
	t.syncTailnets(tailnets)

	return nil
}

func (t *Tray) Close() error {
	if t == nil {
		return nil
	}

	t.m.Lock()
	defer t.m.Unlock()

	if t.item == nil {
		return nil
	}

	err := t.item.Close()
	t.item = nil
	t.prev = nil
	t.quitSep = nil
	t.tailnetSep = nil
	t.tailnetIDs = nil
	t.tailnetItems = nil
	t.tailnets = nil
	return err
}

func (t *Tray) Update(s tsutil.Status) {
	if t == nil {
		return
	}

	status, ok := s.(*tsutil.IPNStatus)
	if !ok {
		return
	}

	t.m.Lock()
	defer t.m.Unlock()

	t.update(status)
}

func (t *Tray) dirty(key unique.Handle[string], vals ...any) bool {
	prev := t.prev[key]
	if slices.Equal(vals, prev) {
		return false
	}

	t.prev[key] = vals
	return true
}

func (t *Tray) update(status *tsutil.IPNStatus) {
	if t.item == nil {
		return
	}

	selfTitle, connected := selfTitle(status)
	connToggleLabel := connToggleText(status.Online())
	exitToggleLabel := exitToggleText(status)

	t.updateStatusIcon(status)

	if t.dirty(selfHandle, selfTitle, connected) {
		t.selfNodeItem.SetProps(
			tray.MenuItemLabel(locale.Get("This machine: %v", selfTitle)),
			tray.MenuItemEnabled(connected),
		)
	}

	if t.dirty(connToggleHandle, connToggleLabel) {
		t.connToggleItem.SetProps(tray.MenuItemLabel(connToggleLabel))
	}

	if t.dirty(exitToggleHandle, exitToggleLabel, connected) {
		t.exitToggleItem.SetProps(
			tray.MenuItemLabel(exitToggleLabel),
			tray.MenuItemEnabled(connected),
		)
	}
}

func (t *Tray) updateStatusIcon(status *tsutil.IPNStatus) {
	newIcon := statusIcon(status)
	if !t.dirty(statusIconHandle, newIcon) {
		return
	}

	t.item.SetProps(tray.ItemIconPixmap(newIcon))
}

func statusIcon(status *tsutil.IPNStatus) *tray.Pixmap {
	if !status.Online() {
		return &statusIconInactive
	}
	if status.ExitNodeActive() {
		return &statusIconExitNode
	}
	return &statusIconActive
}

func selfTitle(status *tsutil.IPNStatus) (string, bool) {
	self, ok := status.Self()
	if !ok {
		return locale.Get("Not connected"), false
	}
	addr := status.SelfAddr()
	if !addr.IsValid() {
		return locale.Get("Not connected"), false
	}

	return fmt.Sprintf("%v (%v)", self.DisplayName(true), addr), true
}

func connToggleText(online bool) string {
	if online {
		return locale.Get("Disconnect")
	}

	return locale.Get("Connect")
}

func exitToggleText(status *tsutil.IPNStatus) string {
	if status.ExitNodeActive() {
		// TODO: Show some actual information about the current exit node?
		return locale.Get("Disable exit node")
	}

	return locale.Get("Enable exit node")
}

// SetTailnets shows a checkbox for each tailnet, checked if it's
// connected. Clicking one calls OnTailnetToggle with its ID.
func (t *Tray) SetTailnets(tailnets []Tailnet) {
	if t == nil {
		return
	}

	t.m.Lock()
	defer t.m.Unlock()

	if t.item == nil {
		return
	}
	t.syncTailnets(tailnets)
}

func (t *Tray) syncTailnets(tailnets []Tailnet) {
	ids := make([]string, len(tailnets))
	for i, tn := range tailnets {
		ids[i] = tn.ID
	}
	if !slices.Equal(t.tailnetIDs, ids) {
		t.rebuildTailnets(tailnets, ids)
		return
	}
	for _, tn := range tailnets {
		if t.tailnets[tn.ID] == tn {
			continue
		}
		t.tailnetItems[tn.ID].SetProps(
			tray.MenuItemLabel(escapeMnemonics(tn.Name)),
			tray.MenuItemToggleState(toggleState(tn.Connected)),
		)
		t.tailnets[tn.ID] = tn
	}
}

func (t *Tray) rebuildTailnets(tailnets []Tailnet, ids []string) {
	for _, item := range t.tailnetItems {
		item.Remove()
	}
	t.tailnetItems = make(map[string]*tray.MenuItem, len(tailnets))
	t.tailnets = make(map[string]Tailnet, len(tailnets))
	t.tailnetIDs = ids

	root := t.item.Menu()
	if len(tailnets) == 0 {
		if t.tailnetSep != nil {
			t.tailnetSep.Remove()
			t.tailnetSep = nil
		}
		return
	}
	if t.tailnetSep == nil {
		t.tailnetSep, _ = root.AddChild(tray.MenuItemType(tray.Separator))
		if t.quitSep != nil {
			t.tailnetSep.MoveBefore(t.quitSep)
		}
	}

	for _, tn := range tailnets {
		id := tn.ID
		child, _ := root.AddChild(
			tray.MenuItemLabel(escapeMnemonics(tn.Name)),
			tray.MenuItemToggleType(tray.Checkmark),
			tray.MenuItemToggleState(toggleState(tn.Connected)),
			handler(func() {
				if t.OnTailnetToggle != nil {
					t.OnTailnetToggle(id)
				}
			}),
		)
		if t.quitSep != nil {
			child.MoveBefore(t.quitSep)
		}
		t.tailnetItems[id] = child
		t.tailnets[id] = tn
	}
}

func toggleState(on bool) tray.MenuToggleState {
	if on {
		return tray.On
	}
	return tray.Off
}
