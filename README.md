Anyscale
========

Anyscale connects one Linux machine to several Tailscale and Headscale tailnets at the same time.

It has three parts:

* `anyscaled`, the daemon. It runs one Tailscale node per tailnet behind a single TUN device, `anyscale0`, and gives every peer its own local address, so tailnets with overlapping `100.x` addresses don't collide.
* `anyscale`, a GTK app with a tray icon to manage the tailnets.
* `anyscalectl`, a command-line client for the daemon.

Features
--------

* A sidebar that lists every tailnet with its own connect switch.
* Add tailnets on Tailscale or on any Headscale server (enter its URL as the login server); the browser opens to log in.
* Remove a tailnet (keeps its identity so it can be restored) or delete it permanently, from a trash button or by right-clicking it.
* A peer list that shows each peer's IP, OS, live upload and download rates, or when it was last seen.
* Peer details, advertised routes and network checks for each tailnet.
* One exit node across all tailnets.
* A tray icon with a checkbox per tailnet.
* Members of an operator group (`wheel` in the shipped systemd unit) can add, remove and change tailnets without root. Everyone else gets a polkit prompt.

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

If a firewall such as ufw drops incoming traffic, allow the tailnet interface, as tailscaled does for `tailscale0`:

```sh
sudo ufw allow in on anyscale0
```

### Moving an existing tailscaled node over

To keep a node's identity rather than log in again, add a disabled tailnet with the node's hostname, copy tailscaled's state into it, then enable it:

```sh
anyscalectl profiles add work --hostname myhost --disabled
anyscalectl profiles show work --json   # note its stateDir
sudo systemctl stop tailscaled
sudo install -m600 /var/lib/tailscale/tailscaled.state <stateDir>/tailscaled.state
anyscalectl profiles enable work
```

Don't run both daemons with the same state at once.

Known issues
------------

* The daemon doesn't include Taildrop, so sending files doesn't work yet.
* Peers show their normal Tailscale addresses, but this machine reaches them through Anyscale's local addresses. Use MagicDNS names.
* The daemon decides which routes and DNS to use, so the "accept routes" and "accept DNS" switches are hidden.
* Serve can't be reached from this machine itself, only from other devices.

Not affiliated with Tailscale
-----------------------------

Anyscale is an independent project. It is not affiliated with, sponsored by or endorsed by Tailscale Inc. Tailscale is a registered trademark of Tailscale Inc.

Credits
-------

Anyscale's app is based on [Trayscale](https://github.com/DeedleFake/trayscale) by DeedleFake, and its daemon and CLI on [tailmix](https://github.com/maisem/tailmix) by Maisem Ali (`daemon/UPSTREAM` records the version).

License
-------

The app is MIT-licensed (see `LICENSE`) and the daemon BSD-3-Clause (see `daemon/LICENSE`).
