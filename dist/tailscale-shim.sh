#!/bin/sh
# Installed as tailscale and tailscalectl in anyscale's bin dir, ahead of
# the distro's tailscale in PATH. With tailscaled running, tailscale is the
# real one; otherwise (anyscaled runs the tailnets) both point to anyscale.
real=/usr/bin/tailscale
if [ "$(basename "$0")" = tailscale ] && [ -x "$real" ] && systemctl is-active --quiet tailscaled 2>/dev/null; then
	exec "$real" "$@"
fi
cat >&2 <<'MSG'
This machine runs its tailnets with anyscale, not tailscaled.
  anyscale --help                     the app and every command
  anyscalectl status                  each tailnet and its peers
  anyscalectl ts -p <profile> <cmd>   a tailscale command on one tailnet
MSG
exit 1
