# anyscale

anyscale connects one Mac or Linux machine to multiple Tailscale tailnets at the
same time. Each tailnet gets its own profile, and you choose which routes, exit
node, and DNS each one provides.

This is the daemon part of [Anyscale](https://github.com/GabriWar/anyscale),
based on [tailmix](https://github.com/maisem/tailmix) by Maisem Ali.

anyscale is an independent project and is not affiliated with, sponsored by, or
endorsed by Tailscale Inc. Tailscale is a registered trademark of Tailscale
Inc.

## Install

Disconnect the regular Tailscale client first; it competes with anyscale for
routes and DNS.

1. Build and install Anyscale from the repository root, then start the daemon:

   ```sh
   make && sudo make install
   sudo systemctl enable --now anyscaled
   ```

2. Log in to your first tailnet and open the link it prints:

   ```sh
   sudo anyscalectl ts login
   ```

   anyscale creates a profile named `default` for it.

3. Check it:

   ```sh
   anyscalectl status
   ```

   You are done when `default` is listed as running.

For Linux prerequisites and troubleshooting, see the
[Linux guide](docs/linux-install.md).

## Add another tailnet

```sh
sudo anyscalectl profiles add work
sudo anyscalectl ts -p work login
```

`anyscalectl ts` uses the only profile when there is one; with several, choose one
with `-p`. Rename the first profile with
`sudo anyscalectl profiles rename default home`.

## Routes and exit nodes

For each destination, anyscale uses the first match:

1. **Peers** are reached through the tailnet they belong to.
2. **Subnet routes** you accepted; the most specific one wins.
3. **The exit node**, if you selected one.
4. Otherwise, your normal network.

No subnet routes are accepted until you ask:

```sh
anyscalectl routes list --available                         # what each tailnet advertises
sudo anyscalectl routes set -p work --accept-all=true       # accept all of work's routes
sudo anyscalectl routes bind -p lab 10.20.0.0/16            # pin one prefix to lab
```

A pinned prefix overrides accept-all. If two tailnets advertise the same prefix
under accept-all, neither is used until you pin one.

Select one exit node from any tailnet:

```sh
anyscalectl exit-node list                                  # or: --filter Canada
sudo anyscalectl exit-node set -p home gateway
sudo anyscalectl exit-node clear
```

`gateway` may be a short hostname, full DNS name, stable node ID, or Tailscale
IP.

## DNS

Each tailnet's MagicDNS names work automatically. The exit node's tailnet
answers names no other route covers.

Use short names such as `ssh mybox` by adding a tailnet's MagicDNS suffix as a
search domain. `anyscalectl dns search list` shows each profile's suffix:

```sh
sudo anyscalectl dns search add example.ts.net
```

Add one suffix per tailnet; earlier entries win when a short name exists in
more than one.

Send another DNS suffix to a tailnet, optionally as a search domain too:

```sh
sudo anyscalectl dns routes bind -p work corp.example.com
sudo anyscalectl dns search add corp.example.com
sudo anyscalectl dns routes set -p work --accept-all=true   # or accept all of work's DNS routes
```

## Overlapping addresses

Two tailnets can use the same `100.x` address. anyscale gives every node and
Tailscale Service a stable local address from `100.127.0.0/16` (and
`fd6d:6e65:7400::/56` for IPv6), and rewrites packets as they enter and leave
each tailnet. MagicDNS names resolve to these local addresses, so connect by
name. Inside each tailnet, your machine still uses its own Tailscale address,
so ACLs work as usual.

## Everyday commands

```sh
anyscalectl status                     # what is running and selected
anyscalectl ts -p work ping printer    # any Tailscale CLI command, per profile
sudo anyscalectl ts -p work set --ssh  # enable Tailscale SSH for one profile
sudo anyscale down                  # pause every profile; survives reboots
sudo anyscale up                    # resume them
anyscalectl help                       # every command
```

Read-only commands work without `sudo`. Policy changes apply immediately
without restarting other profiles.

## Updates

Installs from `install.sh` update themselves: they check stable GitHub releases
daily, verify release checksums, and restart the daemon.

```sh
anyscalectl update status
sudo anyscalectl update apply
sudo anyscalectl update disable           # or: enable
```

Homebrew installs from source are updated by Homebrew instead.

## Shell completion

Homebrew installs completions automatically. Otherwise, load them in your shell:

```sh
source <(anyscalectl completion bash)     # Bash
source <(anyscalectl completion zsh)      # Zsh
anyscalectl completion fish | source      # fish
```

For PowerShell, run
`anyscalectl completion powershell | Out-String | Invoke-Expression`.

## Documentation

- [Linux guide](docs/linux-install.md)
- [Command reference: profiles, routes, exit nodes, DNS](docs/profile-management.md)
- [Raw WireGuard profiles](docs/wireguard.md)
- [Architecture](docs/architecture.md)
- [Development](DEVELOPMENT.md)

## License

BSD 3-Clause; see [LICENSE](LICENSE). The forks under [`tsnet/`](tsnet/README.md)
and [`netns/`](netns/README.md) keep Tailscale's copyright and licenses. The Go
dependency license report is in [licenses/](licenses/README.md).
