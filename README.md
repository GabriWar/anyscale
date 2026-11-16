Anyscale
========

Anyscale connects one Linux machine to several Tailscale and Headscale tailnets at the same time, with a GTK window and a tray icon to manage them.

It is two things merged together:

* a GUI forked from [Trayscale](https://github.com/DeedleFake/trayscale) by DeedleFake, and
* a daemon, `anyscaled`, forked from [tailmix](https://github.com/maisem/tailmix) by Maisem Ali, which runs one Tailscale node per tailnet behind a single TUN device and maps every peer to its own local address so tailnets with overlapping `100.x` addresses don't collide.

Features
--------

* A sidebar that lists every tailnet with its own connect switch.
* Add tailnets on Tailscale or on any Headscale server (enter its URL as the login server); the browser opens to log in.
* Remove a tailnet (keeps its identity so it can be restored) or delete it permanently, from a trash button or by right-clicking it.
* A peer list that shows each peer's IP, OS, live upload and download rates, or when it was last seen.
* One exit node across all tailnets, chosen through the daemon.
* A tray icon with a checkbox per tailnet.
* Everything else from Trayscale, per tailnet: peer details, advertised routes, network checks and so on.

Differences from upstream tailmix
---------------------------------

`daemon/` is tailmix v0.1.20, imported unmodified in its own commit, with these changes on top:

* The daemon is called `anyscaled`.
* `-operator-group` lets members of a group add, remove and change tailnets without root. A tailnet that such a user adds makes them its Tailscale operator, so they can log in and connect without root too. The shipped systemd unit uses `wheel`.
* Automatic self-updates are off by default, as Anyscale pins the tailmix version.

Without the operator group, the GUI falls back to asking for permission with polkit.

Install
-------

You need Go, GTK 4 and libadwaita.

```sh
make
sudo make install
sudo systemctl daemon-reload
sudo systemctl enable --now anyscaled
anyscale
```

`anyscaled` conflicts with `tailscaled`, as both manage routes and DNS, so starting it stops `tailscaled`. To go back, run `sudo systemctl disable --now anyscaled` and start `tailscaled` again.

### Moving an existing tailscaled node over

To keep a node's identity rather than log in again, add a disabled tailnet with the node's hostname, copy tailscaled's state into it, then enable it:

```sh
tailmix profiles add work --hostname myhost --disabled
tailmix profiles show work --json   # note its stateDir
sudo systemctl stop tailscaled
sudo install -m600 /var/lib/tailscale/tailscaled.state <stateDir>/tailscaled.state
tailmix profiles enable work
```

Don't run both daemons with the same state at once.

Known issues
------------

* The daemon doesn't include Taildrop, so sending files doesn't work yet.
* Peers show their normal Tailscale addresses, but this machine reaches them through tailmix's local addresses. Use MagicDNS names.
* The tailmix daemon decides which routes and DNS to use, so the "accept routes" and "accept DNS" switches are hidden.

License
-------

The GUI is MIT-licensed, like Trayscale (see `LICENSE`). The daemon is BSD-3-Clause, like tailmix (see `daemon/LICENSE`).
