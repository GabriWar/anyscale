package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"codeberg.org/puregotk/puregotk/v4/adw"
	"codeberg.org/puregotk/puregotk/v4/glib"
	"codeberg.org/puregotk/puregotk/v4/gtk"
	"github.com/gabriwar/anyscale/internal/gutil"
	"github.com/gabriwar/anyscale/internal/locale"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

// peerList is the state of the sidebar that lists the pages of the
// selected tailnet, one row per page with a line of details under its
// name, and a header row before each section.
type peerList struct {
	// layout is what the rows show, in order.
	layout []sidebarPage
	// names has a page name for each row, or "" for a header.
	names []string
	rows  []*gtk.ListBoxRow
	items map[string]*peerRow
	// traffic is the last byte counts seen for each peer, to work out
	// rates from.
	traffic map[string]trafficSample
	// syncing is set while the selection is changed to follow the
	// stack, so that it isn't taken as the user's.
	syncing bool
}

type peerRow struct {
	row  *adw.ActionRow
	icon *gtk.Image
}

type trafficSample struct {
	rx, tx         int64
	at             time.Time
	rxRate, txRate float64
}

func (win *MainWindow) initPeerList() {
	win.peerList.items = make(map[string]*peerRow)
	win.peerList.traffic = make(map[string]trafficSample)

	ph := adw.NewStatusPage()
	ph.AddCssClass("compact")
	ph.SetIconName("edit-find-symbolic")
	ph.SetTitle(locale.Get("No matching peers"))
	win.PeerList.SetPlaceholder(&ph.Widget)
	ph.Unref()

	gutil.Connect(win.PeerList, win.PeerList.ConnectRowSelected, func(gtk.ListBox, uintptr) {
		if win.peerList.syncing {
			return
		}
		if name := win.selectedPeerRow(); name != "" {
			win.PeersStack.SetVisibleChildName(name)
		}
	})

	contentVariant := glib.NewVariantString("content").RefSink()
	gutil.Connect(win.PeerList, win.PeerList.ConnectRowActivated, func(gtk.ListBox, uintptr) {
		win.SplitView.ActivateActionVariant("navigation.push", contentVariant)
	})
	gutil.OnFinalize(win.PeerList, contentVariant.Unref)
}

// selectedPeerRow returns the page name of the selected row, if any.
func (win *MainWindow) selectedPeerRow() string {
	row := win.PeerList.GetSelectedRow()
	if row == nil {
		return ""
	}
	i := int(row.GetIndex())
	row.Unref()
	if i < 0 || i >= len(win.peerList.names) {
		return ""
	}
	return win.peerList.names[i]
}

// selectPeerRow selects the row of the visible page.
func (win *MainWindow) selectPeerRow() {
	l := &win.peerList
	i := slices.Index(l.names, win.PeersStack.GetVisibleChildName())
	if i < 0 {
		return
	}
	l.syncing = true
	win.PeerList.SelectRow(l.rows[i])
	l.syncing = false
}

func (win *MainWindow) disposePeerList() {
	l := &win.peerList
	for _, r := range l.items {
		r.icon.Unref()
		r.row.Unref()
	}
	clear(l.items)
	for _, r := range l.rows {
		r.Unref()
	}
	l.rows, l.names, l.layout = nil, nil, nil
}

// syncPeerList shows a row for each page in layout and updates the
// details of every row.
func (win *MainWindow) syncPeerList(layout []sidebarPage) {
	l := &win.peerList
	if !slices.Equal(layout, l.layout) {
		win.disposePeerList()
		win.PeerList.RemoveAll()
		for _, p := range layout {
			if p.section != "" {
				win.appendPeerHeader(p.section)
			}
			r := &peerRow{row: adw.NewActionRow(), icon: gtk.NewImage()}
			r.row.SetTitleLines(1)
			r.row.SetSubtitleLines(1)
			r.row.AddPrefix(&r.icon.Widget)
			r.row.SetActivatable(true)
			win.PeerList.Append(&r.row.Widget)
			r.row.Ref() // Ours, as well as the list's.
			l.rows = append(l.rows, &r.row.ListBoxRow)
			l.names = append(l.names, p.name)
			l.items[p.name] = r
		}
		l.layout = slices.Clone(layout)
		win.selectPeerRow()
	}

	now := time.Now()
	for name, r := range l.items {
		vp := win.stackPages[name]
		if vp == nil {
			continue
		}
		r.row.SetTitle(escapeMarkup(vp.GetTitle()))
		r.icon.SetFromIconName(vp.GetIconName())
		r.row.SetSubtitle(escapeMarkup(win.peerSubtitle(name, now)))
	}
}

func (win *MainWindow) appendPeerHeader(title string) {
	label := gtk.NewLabel(title)
	label.AddCssClass("heading")
	label.AddCssClass("dim-label")
	label.SetXalign(0)
	label.SetMarginTop(12)
	label.SetMarginStart(6)
	row := gtk.NewListBoxRow()
	row.SetChild(&label.Widget)
	label.Unref()
	row.SetSelectable(false)
	row.SetActivatable(false)
	win.PeerList.Append(&row.Widget)
	l := &win.peerList
	l.rows = append(l.rows, row) // Keeps NewListBoxRow's reference.
	l.names = append(l.names, "")
}

// peerSubtitle describes the page with the given name in one line.
func (win *MainWindow) peerSubtitle(name string, now time.Time) string {
	status := win.ipn
	if status == nil {
		return ""
	}
	switch name {
	case "self":
		self, ok := status.Self()
		if !ok {
			return ""
		}
		return joinInfo(firstIP(self), peerOS(self), locale.Get("this machine"))
	case "mullvad":
		return locale.Get("Mullvad exit nodes")
	case "offline":
		return ""
	}

	peer, ok := status.Peers[tailcfg.StableNodeID(name)]
	if !ok || !peer.Valid() {
		return ""
	}
	if !peerIsOnline(peer) {
		var seen string
		if t, ok := peer.LastSeen().GetOk(); ok && !t.IsZero() {
			seen = locale.Get("last seen %v ago", shortDuration(now.Sub(t)))
		}
		return joinInfo(peerOS(peer), seen)
	}

	var live ipnstate.PeerStatusLite
	if status.Engine != nil {
		live = status.Engine.LivePeers[peer.Key()]
	}
	sample := win.peerList.traffic[name].next(live.RxBytes, live.TxBytes, now)
	win.peerList.traffic[name] = sample
	return joinInfo(firstIP(peer), peerOS(peer), trafficInfo(live, sample, now))
}

// next returns s updated with new byte counts.
func (s trafficSample) next(rx, tx int64, now time.Time) trafficSample {
	n := trafficSample{rx: rx, tx: tx, at: now, rxRate: s.rxRate, txRate: s.txRate}
	if s.at.IsZero() {
		return n
	}
	dt := now.Sub(s.at).Seconds()
	if dt < 0.5 {
		// Too soon after the last sample to say anything new.
		return s
	}
	if rx < s.rx || tx < s.tx {
		// The counters restarted.
		n.rxRate, n.txRate = 0, 0
		return n
	}
	n.rxRate = float64(rx-s.rx) / dt
	n.txRate = float64(tx-s.tx) / dt
	return n
}

// trafficInfo describes how much has gone to and come from a peer.
func trafficInfo(live ipnstate.PeerStatusLite, s trafficSample, now time.Time) string {
	if live.RxBytes == 0 && live.TxBytes == 0 {
		return locale.Get("idle")
	}
	if s.rxRate >= 1 || s.txRate >= 1 {
		return fmt.Sprintf("↓%v/s ↑%v/s", humanBytes(int64(s.rxRate)), humanBytes(int64(s.txRate)))
	}
	return fmt.Sprintf("↓%v ↑%v", humanBytes(live.RxBytes), humanBytes(live.TxBytes))
}

func joinInfo(parts ...string) string {
	parts = slices.DeleteFunc(parts, func(s string) bool { return s == "" })
	return strings.Join(parts, " · ")
}

func firstIP(n tailcfg.NodeView) string {
	for _, a := range n.Addresses().All() {
		if a.Addr().Is4() {
			return a.Addr().String()
		}
	}
	if n.Addresses().Len() > 0 {
		return n.Addresses().At(0).Addr().String()
	}
	return ""
}

func peerOS(n tailcfg.NodeView) string {
	hi := n.Hostinfo()
	if !hi.Valid() {
		return ""
	}
	return hi.OS()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return locale.Get("%vs", int(d.Seconds()))
	case d < time.Hour:
		return locale.Get("%vm", int(d.Minutes()))
	case d < 24*time.Hour:
		return locale.Get("%vh", int(d.Hours()))
	}
	return locale.Get("%vd", int(d.Hours()/24))
}
