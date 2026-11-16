package ui

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"codeberg.org/puregotk/puregotk/v4/adw"
	"codeberg.org/puregotk/puregotk/v4/gtk"
	"github.com/gabriwar/anyscale/internal/gutil"
	"github.com/gabriwar/anyscale/internal/locale"
	"github.com/gabriwar/anyscale/internal/mix"
	"github.com/gabriwar/anyscale/internal/tray"
	"github.com/gabriwar/anyscale/internal/tsutil"
	"tailscale.com/tailcfg"
)

// noTailnetSocket is polled when no tailnet with a running node is
// selected. Nothing can listen there, so the poller reports that
// there's no connection, as it does when tailscaled is down.
const noTailnetSocket = "/dev/null/anyscale-no-tailnet"

// mixInterval is how often the tailmix daemon is asked for the state of
// every tailnet.
const mixInterval = 2 * time.Second

// startPoller replaces the poller with one for the LocalAPI socket at
// path. Its statuses are dropped once it's replaced.
func (a *App) startPoller(path string) {
	ctx, cancel := context.WithCancel(a.ctx)
	c := tsutil.NewClient(path)
	p := &tsutil.Poller{
		Client:   c,
		Interval: a.getInterval(),
		Error: func(err error) {
			slog.Error("set Tailscale status", "err", err)
			a.toast(locale.Get("Failed to start or stop Tailscale"))
		},
	}
	p.New = func(s tsutil.Status) {
		gutil.IdleAdd(func() {
			if a.currentPoller() == p {
				a.update(s)
			}
		})
	}

	a.mu.Lock()
	if a.stopPoller != nil {
		a.stopPoller()
	}
	a.poller, a.ts, a.stopPoller = p, c, cancel
	a.mu.Unlock()

	go p.Run(ctx)
}

// watchMix gets the state of every tailnet from tailmix at regular
// intervals and whenever refreshMix is called.
func (a *App) watchMix(ctx context.Context) {
	tick := time.NewTicker(mixInterval)
	defer tick.Stop()

	var lastErr string
	for {
		getCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		st, err := a.mix.Status(getCtx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if msg := errString(err); msg != lastErr {
			lastErr = msg
			if err != nil {
				slog.Error("get tailmix status", "err", err)
			}
		}
		gutil.IdleAdd(func() { a.updateMix(st, err) })

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-a.mixRefresh:
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// refreshMix gets the state of every tailnet again soon. It's safe to
// call from any thread.
func (a *App) refreshMix() {
	select {
	case a.mixRefresh <- struct{}{}:
	default:
	}
}

// updateMix shows the state of every tailnet. err is the error from
// getting it.
func (a *App) updateMix(st mix.Status, err error) {
	if err != nil {
		st = mix.Status{}
	}
	prev, hadPrev := a.mixStatus, a.mixLoaded
	a.mixStatus, a.mixErr, a.mixLoaded = st, err, true
	if hadPrev {
		a.notifyTailnetChanges(prev, st)
	}

	if _, ok := mix.Find(st, a.tailnet); !ok {
		a.tailnet = defaultTailnet(st)
		a.operatorCheck = false
	}
	a.syncPoller()

	if a.win != nil {
		a.win.UpdateTailnets(st, err, a.tailnet)
	}
	a.tray.SetTailnets(trayTailnets(st))
}

// notifyTailnetChanges notifies about tailnets that connected or
// disconnected between prev and cur.
func (a *App) notifyTailnetChanges(prev, cur mix.Status) {
	for _, t := range cur.Profiles {
		old, ok := mix.Find(prev, t.ID)
		if !ok || mix.Connected(old) == mix.Connected(t) {
			continue
		}
		body := locale.Get("%v is disconnected.", t.Name)
		if mix.Connected(t) {
			body = locale.Get("%v is connected.", t.Name)
		}
		a.notify(locale.Get("Tailnet Status"), body)
	}
}

// defaultTailnet returns the profile ID of the tailnet to show when
// none is selected: the first connected one, or else the first one.
func defaultTailnet(st mix.Status) string {
	for _, t := range st.Profiles {
		if mix.Connected(t) {
			return t.ID
		}
	}
	if len(st.Profiles) > 0 {
		return st.Profiles[0].ID
	}
	return ""
}

func trayTailnets(st mix.Status) []tray.Tailnet {
	tailnets := make([]tray.Tailnet, len(st.Profiles))
	for i, t := range st.Profiles {
		tailnets[i] = tray.Tailnet{ID: t.ID, Name: t.Name, Connected: mix.Connected(t)}
	}
	return tailnets
}

// selectTailnet shows the tailnet with the given profile ID.
func (a *App) selectTailnet(id string) {
	if id == a.tailnet {
		return
	}
	a.tailnet = id
	a.operatorCheck = false
	a.files = nil
	a.syncPoller()
	if a.win != nil {
		a.win.UpdateTailnets(a.mixStatus, a.mixErr, a.tailnet)
	}
}

// syncPoller points the poller at the selected tailnet's LocalAPI if it
// isn't already, and clears the window's peers if it changes.
func (a *App) syncPoller() {
	path := noTailnetSocket
	if t, ok := mix.Find(a.mixStatus, a.tailnet); ok && mix.Live(t) {
		p, err := a.mix.Socket(t)
		if err != nil {
			slog.Error("find tailnet LocalAPI socket", "tailnet", t.Name, "err", err)
		} else {
			path = p
		}
	}
	if _, c := a.current(); c != nil && c.Socket() == path {
		return
	}

	a.startPoller(path)
	if a.win != nil {
		a.win.ResetPeers()
	}
}

// selected returns the selected tailnet.
func (a *App) selected() (mix.Tailnet, bool) {
	return mix.Find(a.mixStatus, a.tailnet)
}

// runHelper runs f, which asks polkit for permission, in the
// background. Once it's done, it gets the state of every tailnet again
// and calls done, if it's not nil, on the main thread.
func (a *App) runHelper(f func(context.Context) error, done func(error)) {
	a.spin()
	go func() {
		defer a.stopSpin()

		// Leave time to type a password.
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
		defer cancel()

		err := f(ctx)
		if err != nil {
			slog.Error("privileged tailmix operation", "err", err)
		}
		gutil.IdleAdd(func() {
			a.refreshMix()
			if done != nil {
				done(err)
			}
		})
	}()
}

// setTailnetRunning connects the tailnet with the given profile ID if
// up is true and disconnects it otherwise. done is called on the main
// thread with whether that worked.
func (a *App) setTailnetRunning(id string, up bool, done func(ok bool)) {
	t, ok := mix.Find(a.mixStatus, id)
	if !ok {
		done(false)
		return
	}

	switch {
	case up && !t.Enabled:
		a.runHelper(func(ctx context.Context) error {
			return a.helper.SetEnabled(ctx, t.Name, true)
		}, func(err error) {
			if err != nil {
				a.toast(locale.Get("Failed to enable %v: %v", t.Name, err))
			}
			done(err == nil)
		})
		return

	case up && t.RuntimeState == "needs-login":
		// Connecting needs a login, which shows in the selected
		// tailnet.
		done(false)
		a.selectTailnet(id)
		a.login(a.ctx)
		return

	case !mix.Live(t):
		a.toast(locale.Get("%v isn't running", t.Name))
		done(false)
		return
	}

	path, err := a.mix.Socket(t)
	if err != nil {
		a.toast(locale.Get("Failed to connect to %v: %v", t.Name, err))
		done(false)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()

		c := tsutil.NewClient(path)
		var err error
		if up {
			err = c.Start(ctx)
		} else {
			err = c.Stop(ctx)
		}
		gutil.IdleAdd(func() {
			if err != nil {
				slog.Error("set tailnet running", "tailnet", t.Name, "up", up, "err", err)
				if up {
					a.toast(locale.Get("Failed to connect %v: %v", t.Name, err))
				} else {
					a.toast(locale.Get("Failed to disconnect %v: %v", t.Name, err))
				}
			}
			a.refreshMix()
			done(err == nil)
		})
	}()
}

// toggleTailnet connects the tailnet with the given profile ID if it's
// disconnected and disconnects it otherwise.
func (a *App) toggleTailnet(id string) {
	t, ok := mix.Find(a.mixStatus, id)
	if !ok {
		return
	}
	a.setTailnetRunning(id, !mix.Connected(t), func(bool) {})
}

func (a *App) showAddTailnet() {
	group := gtk.NewListBox()
	group.AddCssClass("boxed-list")
	group.SetSelectionMode(gtk.SelectionNoneValue)

	name := adw.NewEntryRow()
	name.SetTitle(locale.Get("Name"))
	group.Append(&name.Widget)

	server := adw.NewEntryRow()
	server.SetTitle(locale.Get("Login server (empty for Tailscale)"))
	server.SetInputPurpose(gtk.InputPurposeUrlValue)
	group.Append(&server.Widget)

	dialog := adw.NewAlertDialog(
		locale.Get("Add Tailnet"),
		locale.Get("For Headscale, enter its URL as the login server. Adding a tailnet asks for administrator access, then opens a browser to log in."),
	)
	dialog.SetExtraChild(&group.Widget)
	dialog.AddResponse("cancel", locale.Get("_Cancel"))
	dialog.SetCloseResponse("cancel")
	dialog.AddResponse("add", locale.Get("_Add"))
	dialog.SetResponseAppearance("add", adw.ResponseSuggestedValue)
	dialog.SetDefaultResponse("add")
	dialog.SetResponseEnabled("add", false)

	gutil.ConnectNotify(name, "text", func() {
		dialog.SetResponseEnabled("add", validTailnetName(name.GetText()))
	})
	gutil.Connect(dialog, dialog.ConnectResponse, func(_ adw.AlertDialog, response string) {
		if response != "add" {
			return
		}
		a.addTailnet(strings.TrimSpace(name.GetText()), strings.TrimSpace(server.GetText()))
	})

	name.Unref()
	server.Unref()
	group.Unref() // The dialog holds it now.
	a.present(dialog)
}

// validTailnetName reports whether name can name a new tailnet.
// tailmix has the final say.
func validTailnetName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && !strings.ContainsAny(name, "/ \t\n")
}

// addTailnet adds a tailnet called name, selects it and logs in to it
// at loginServer, or Tailscale's control server if it's empty.
func (a *App) addTailnet(name, loginServer string) {
	a.runHelper(func(ctx context.Context) error {
		return a.helper.Add(ctx, name)
	}, func(err error) {
		if err != nil {
			a.toast(locale.Get("Failed to add %v: %v", name, err))
			return
		}

		go func() {
			ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
			st, err := a.mix.Status(ctx)
			cancel()
			gutil.IdleAdd(func() {
				a.updateMix(st, err)
				for _, t := range a.mixStatus.Profiles {
					if t.Name == name {
						a.selectTailnet(t.ID)
						a.loginAt(loginServer)
						return
					}
				}
				a.toast(locale.Get("Added %v, but it isn't listed yet", name))
			})
		}()
	})
}

// loginAt logs in to the selected tailnet at controlURL, or keeps its
// control server if controlURL is empty.
func (a *App) loginAt(controlURL string) {
	if controlURL == "" {
		a.login(a.ctx)
		return
	}

	c := a.client()
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()

		err := c.SetControlURL(ctx, controlURL)
		gutil.IdleAdd(func() {
			if err != nil {
				slog.Error("set login server", "url", controlURL, "err", err)
				a.toast(locale.Get("Error setting control URL: %v", err))
				return
			}
			a.login(a.ctx)
		})
	}()
}

func (a *App) showRemoveTailnet() {
	if t, ok := a.selected(); ok {
		a.showRemoveTailnetFor(t)
	}
}

// showRemoveTailnetFor asks whether to remove t, keeping its identity,
// or to delete it for good.
func (a *App) showRemoveTailnetFor(t mix.Tailnet) {
	dialog := adw.NewAlertDialog(
		locale.Get("Remove %v?", t.Name),
		locale.Get("Removing keeps this machine's identity in the tailnet, so adding a tailnet with the same name later brings it back. Deleting erases the identity; coming back needs a new login."),
	)
	dialog.AddResponse("cancel", locale.Get("_Cancel"))
	dialog.SetCloseResponse("cancel")
	dialog.AddResponse("purge", locale.Get("_Delete Permanently"))
	dialog.SetResponseAppearance("purge", adw.ResponseDestructiveValue)
	dialog.AddResponse("remove", locale.Get("_Remove"))
	dialog.SetDefaultResponse("cancel")

	gutil.Connect(dialog, dialog.ConnectResponse, func(_ adw.AlertDialog, response string) {
		if response != "remove" && response != "purge" {
			return
		}
		purge := response == "purge"
		a.runHelper(func(ctx context.Context) error {
			return a.helper.Remove(ctx, t.Name, purge)
		}, func(err error) {
			if err != nil {
				a.toast(locale.Get("Failed to remove %v: %v", t.Name, err))
			}
		})
	})
	a.present(dialog)
}

// setExitNode uses peer, from the selected tailnet, as the exit node,
// or stops using one if peer is empty. tailmix allows only one exit
// node across all tailnets, so this replaces any other. done, if it's
// not nil, is called on the main thread with the result.
func (a *App) setExitNode(peer tailcfg.StableNodeID, done func(error)) {
	t, ok := a.selected()
	if peer != "" && !ok {
		if done != nil {
			done(errors.New("no tailnet selected"))
		}
		return
	}

	a.runHelper(func(ctx context.Context) error {
		if peer == "" {
			return a.helper.ClearExitNode(ctx)
		}
		return a.helper.SetExitNode(ctx, t.Name, string(peer))
	}, func(err error) {
		if err != nil {
			a.toast(locale.Get("Failed to toggle exit node"))
		}
		if done != nil {
			done(err)
		}
	})
}

// useExitNode turns exit node use on or off, picking the exit node
// according to the preferences.
func (a *App) useExitNode(use bool) {
	a.useExitNodeWith(use, a.exitNodeSelection(), nil)
}

func (a *App) useExitNodeWith(use bool, sel tsutil.ExitNodeSelection, done func(error)) {
	if !use {
		a.setExitNode("", done)
		return
	}

	c := a.client()
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()

		id, err := c.ExitNodeChoice(ctx, sel)
		gutil.IdleAdd(func() {
			if err != nil {
				slog.Error("choose exit node", "err", err)
				a.toast(locale.Get("Failed to toggle exit node"))
				if done != nil {
					done(err)
				}
				return
			}
			a.setExitNode(id, done)
		})
	}()
}

// exitNodeActive reports whether tailmix is routing through an exit
// node of any tailnet.
func (a *App) exitNodeActive() bool {
	return a.mixStatus.ExitNodes.Selected != nil
}
