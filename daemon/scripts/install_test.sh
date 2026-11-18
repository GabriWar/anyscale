#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
stage=$(mktemp -d "${TMPDIR:-/tmp}/anyscale-install-test.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
mkdir -p "$stage/archive/service" "$stage/root"
printf '#!/bin/sh\n' > "$stage/archive/anyscale"
printf '#!/bin/sh\n' > "$stage/archive/anyscaled"
cp "$repo_root/systemd/anyscaled.service.in" "$stage/archive/service/"
cp "$repo_root/launchd/io.github.gabriwar.anyscaled.plist.in" "$stage/archive/service/"
tar -czf "$stage/anyscale_linux_amd64.tar.gz" -C "$stage/archive" \
	anyscale anyscaled service/anyscaled.service.in \
	service/io.github.gabriwar.anyscaled.plist.in

DESTDIR="$stage/root" "$repo_root/scripts/install.sh" \
	--archive "$stage/anyscale_linux_amd64.tar.gz" --version v1.2.3

test "$(readlink "$stage/root/usr/local/lib/anyscale/current")" = versions/v1.2.3
test "$(readlink "$stage/root/usr/local/bin/anyscalectl")" = /usr/local/lib/anyscale/current/anyscale
test -x "$stage/root/usr/local/lib/anyscale/versions/v1.2.3/anyscaled"
grep -q 'ExecStart=/usr/local/lib/anyscale/current/anyscaled' \
	"$stage/root/etc/systemd/system/anyscaled.service"

DESTDIR="$stage/root" "$repo_root/scripts/install.sh" \
	--archive "$stage/anyscale_linux_amd64.tar.gz" --version v1.2.4
test "$(readlink "$stage/root/usr/local/lib/anyscale/current")" = versions/v1.2.4
test -x "$stage/root/usr/local/lib/anyscale/versions/v1.2.3/anyscaled"

printf 'different\n' > "$stage/root/usr/local/lib/anyscale/versions/v1.2.4/anyscale"
if DESTDIR="$stage/root" "$repo_root/scripts/install.sh" \
	--archive "$stage/anyscale_linux_amd64.tar.gz" --version v1.2.4 \
	>"$stage/reinstall.out" 2>&1; then
	echo "installer accepted a conflicting existing version" >&2
	exit 1
fi
