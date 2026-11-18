package ui

import (
	"slices"
	"strings"

	"codeberg.org/puregotk/puregotk/v4/adw"
	"codeberg.org/puregotk/puregotk/v4/gtk"
	"github.com/gabriwar/anyscale/internal/gutil"
	"github.com/gabriwar/anyscale/internal/locale"
	"github.com/gabriwar/anyscale/internal/mix"
)

// tailnetList is the state of the main window's tailnet sidebar.
type tailnetList struct {
	// ids are the profile IDs of the rows, in order.
	ids  []string
	rows map[string]*tailnetRow
	// updating is set while the list is changed to match a status, so
	// that the changes aren't taken as the user's.
	updating bool
}

type tailnetRow struct {
	row *adw.ActionRow
	sw  *gtk.Switch
	// pending is set while a connect or disconnect started from the
	// switch is running, so that status updates don't flip it back.
	pending bool
}

func (win *MainWindow) initTailnets() {
	win.tailnets.rows = make(map[string]*tailnetRow)

	// GtkBuilder can't set a list box's placeholder.
	ph := adw.NewStatusPage()
	ph.AddCssClass("compact")
	ph.SetIconName("network-workgroup-symbolic")
	add := gtk.NewButtonWithMnemonic(locale.Get("_Add Tailnet"))
	add.SetActionName("app.add_tailnet")
	add.AddCssClass("pill")
	add.AddCssClass("suggested-action")
	add.SetHalign(gtk.AlignCenterValue)
	ph.SetChild(&add.Widget)
	add.Unref()
	win.TailnetList.SetPlaceholder(&ph.Widget)
	win.TailnetPlaceholder = ph // Ours until disposeTailnets.

	gutil.Connect(win.TailnetList, win.TailnetList.ConnectRowSelected, func(gtk.ListBox, uintptr) {
		if win.tailnets.updating {
			return
		}
		row := win.TailnetList.GetSelectedRow()
		if row == nil {
			return
		}
		i := int(row.GetIndex())
		row.Unref()
		if i < 0 || i >= len(win.tailnets.ids) {
			return
		}
		win.app.selectTailnet(win.tailnets.ids[i])
		if win.TailnetSplit.GetCollapsed() {
			win.TailnetSplit.SetShowSidebar(false)
		}
	})
}

func (win *MainWindow) disposeTailnets() {
	win.clearTailnetRows()
	if win.TailnetPlaceholder != nil {
		win.TailnetPlaceholder.Unref()
		win.TailnetPlaceholder = nil
	}
}

// clearTailnetRows drops the window's references to the tailnet rows.
func (win *MainWindow) clearTailnetRows() {
	for _, r := range win.tailnets.rows {
		r.sw.Unref()
		r.row.Unref()
	}
	clear(win.tailnets.rows)
	win.tailnets.ids = nil
}

// UpdateTailnets shows the tailnets in st with the one with the
// selected profile ID selected. daemonErr is the error from getting
// st, if any.
func (win *MainWindow) UpdateTailnets(st mix.Status, daemonErr error, selected string) {
	l := &win.tailnets
	l.updating = true
	defer func() { l.updating = false }()

	if daemonErr != nil {
		win.TailnetPlaceholder.SetTitle(locale.Get("Daemon Not Running"))
		win.TailnetPlaceholder.SetDescription(locale.Get("Anyscale needs its daemon, anyscaled. Start it with <tt>sudo systemctl start anyscaled</tt>."))
	} else {
		win.TailnetPlaceholder.SetTitle(locale.Get("No Tailnets"))
		win.TailnetPlaceholder.SetDescription(locale.Get("Add a Tailscale or Headscale tailnet to get started."))
	}

	ids := make([]string, len(st.Profiles))
	for i, t := range st.Profiles {
		ids[i] = t.ID
	}
	if !slices.Equal(ids, l.ids) {
		win.clearTailnetRows()
		win.TailnetList.RemoveAll()
		for _, id := range ids {
			l.rows[id] = win.newTailnetRow(id)
		}
		l.ids = ids
	}

	var exitProfile string
	if sel := st.ExitNodes.Selected; sel != nil {
		exitProfile = sel.ProfileID
	}
	for _, t := range st.Profiles {
		r := l.rows[t.ID]
		r.row.SetTitle(escapeMarkup(t.Name))
		r.row.SetSubtitle(escapeMarkup(tailnetState(t, t.ID == exitProfile)))
		r.row.SetTooltipText(t.LastError)
		if !r.pending {
			setTailnetSwitch(r.sw, mix.Connected(t))
		}
		if t.ID == selected {
			win.TailnetList.SelectRow(&r.row.ListBoxRow)
		}
	}

	t, ok := mix.Find(st, selected)
	if !ok {
		win.TailnetTitle.SetTitle(locale.Get("Anyscale"))
		win.TailnetTitle.SetSubtitle("")
		return
	}
	win.TailnetTitle.SetTitle(t.Name)
	win.TailnetTitle.SetSubtitle(tailnetState(t, t.ID == exitProfile))
}

func (win *MainWindow) newTailnetRow(id string) *tailnetRow {
	r := &tailnetRow{
		row: adw.NewActionRow(),
		sw:  gtk.NewSwitch(),
	}
	r.row.SetTitleLines(1)
	r.row.SetSubtitleLines(1)
	r.sw.SetValign(gtk.AlignCenterValue)
	r.sw.SetTooltipText(locale.Get("Connect"))

	sw := r.sw
	gutil.Connect(sw, sw.ConnectStateSet, func(_ gtk.Switch, s bool) bool {
		if win.tailnets.updating || s == sw.GetState() {
			return false
		}
		r.pending = true
		win.app.setTailnetRunning(id, s, func(ok bool) {
			r.pending = false
			if !ok {
				sw.SetActive(!s)
			}
		})
		return true
	})

	r.row.AddSuffix(&r.sw.Widget)

	remove := func() {
		if t, ok := mix.Find(win.app.mixStatus, id); ok {
			win.app.showRemoveTailnetFor(t)
		}
	}
	trash := gtk.NewButtonFromIconName("user-trash-symbolic")
	trash.SetValign(gtk.AlignCenterValue)
	trash.AddCssClass("flat")
	trash.SetTooltipText(locale.Get("Remove tailnet"))
	gutil.Connect(trash, trash.ConnectClicked, func(gtk.Button) { remove() })
	r.row.AddSuffix(&trash.Widget)
	trash.Unref()

	click := gtk.NewGestureClick()
	click.SetButton(3) // Right button.
	gutil.Connect(click, click.ConnectPressed, func(gtk.GestureClick, int32, float64, float64) { remove() })
	r.row.AddController(&click.EventController) // Takes ownership.

	win.TailnetList.Append(&r.row.Widget)
	return r
}

// setTailnetSwitch shows on on sw. Like updateStatusSwitch, it only calls
// SetActive when that changes something, so as not to stop a running
// toggle animation.
func setTailnetSwitch(sw *gtk.Switch, on bool) {
	sw.SetState(on)
	if sw.GetActive() != on {
		sw.SetActive(on)
	}
}

// tailnetState describes the state of t in a few words. exitNode is
// whether the selected exit node is in t.
func tailnetState(t mix.Tailnet, exitNode bool) string {
	var s string
	switch {
	case t.RuntimeState == "disabled":
		s = locale.Get("Disabled")
	case t.RuntimeState == "down":
		s = locale.Get("Daemon stopped")
	case t.RuntimeState == "needs-login":
		s = locale.Get("Needs login")
	case t.RuntimeState == "error":
		s = locale.Get("Error")
	case mix.Connected(t):
		s = locale.Get("Connected · %v peers", t.PeerCount)
	case t.BackendState == "Stopped":
		s = locale.Get("Disconnected")
	default:
		s = locale.Get("Starting…")
	}
	if exitNode {
		s += " · " + locale.Get("exit node")
	}
	return s
}

// escapeMarkup escapes s for rows, which use Pango markup.
func escapeMarkup(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
