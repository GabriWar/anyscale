#!/bin/sh
set -eu

repo=${ANYSCALE_REPOSITORY:-maisem/anyscale}
version=
archive=
destdir=${DESTDIR:-}
start_service=1

usage() {
	cat <<'EOF'
Usage: install.sh [--version VERSION] [--archive FILE] [--no-start]

Install anyscale and anyscaled from a GitHub release. DESTDIR stages an install
without changing or starting host services.
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--version) version=$2; shift 2 ;;
	--archive) archive=$2; shift 2 ;;
	--no-start) start_service=0; shift ;;
	-h|--help) usage; exit 0 ;;
	*) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
	esac
done

case $(uname -s) in
Linux) os=linux; install_root=/usr/local/lib/anyscale; state_dir=/var/lib/anyscale ;;
Darwin) os=darwin; install_root='/Library/Application Support/anyscale'; state_dir='/Library/Application Support/anyscale/state' ;;
*) echo "anyscale supports Linux and macOS" >&2; exit 1 ;;
esac
case $(uname -m) in
x86_64|amd64) arch=amd64 ;;
arm64|aarch64) arch=arm64 ;;
*) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

if [ -n "$destdir" ]; then start_service=0; fi
if [ -z "$version" ]; then
	if [ -n "$archive" ]; then
		echo "--version is required with --archive" >&2; exit 2
	fi
	version=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$version" ] || { echo "could not determine latest release" >&2; exit 1; }
fi
case "$version" in *[!A-Za-z0-9._+-]*) echo "invalid version: $version" >&2; exit 2 ;; esac

tmp=$(mktemp -d "${TMPDIR:-/tmp}/anyscale-install.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
asset="anyscale_${os}_${arch}.tar.gz"
if [ -n "$archive" ]; then
	cp "$archive" "$tmp/$asset"
else
	base="https://github.com/$repo/releases/download/$version"
	curl -fsSL "$base/$asset" -o "$tmp/$asset"
	curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$tmp" && grep "  $asset\$" checksums.txt | sha256sum -c -)
	else
		expected=$(sed -n "s/[[:space:]][[:space:]]$asset\$//p" "$tmp/checksums.txt")
		actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
		[ -n "$expected" ] && [ "$actual" = "$expected" ] || {
			echo "checksum verification failed for $asset" >&2; exit 1;
		}
	fi
fi
mkdir "$tmp/unpack"
tar -xzf "$tmp/$asset" -C "$tmp/unpack" \
	anyscale anyscaled service/anyscaled.service.in \
	service/io.github.gabriwar.anyscaled.plist.in
for binary in anyscale anyscaled; do
	[ -f "$tmp/unpack/$binary" ] || { echo "$asset does not contain $binary" >&2; exit 1; }
done

root="$destdir$install_root"
version_root="$root/versions/$version"
install -d -m 0755 "$root/versions" "$destdir/usr/local/bin"
if [ ! -d "$version_root" ]; then
	staged_version="$root/versions/.$version.$$"
	install -d -m 0755 "$staged_version"
	install -m 0755 "$tmp/unpack/anyscale" "$staged_version/anyscale"
	install -m 0755 "$tmp/unpack/anyscaled" "$staged_version/anyscaled"
	mv "$staged_version" "$version_root"
else
	for binary in anyscale anyscaled; do
		if ! cmp -s "$tmp/unpack/$binary" "$version_root/$binary"; then
			echo "$version_root already exists with different contents" >&2
			exit 1
		fi
	done
fi
next="$root/.current.$$"
ln -s "versions/$version" "$next"
if [ "$os" = darwin ]; then
	mv -fh "$next" "$root/current"
else
	mv -Tf "$next" "$root/current"
fi
ln -sfn "$install_root/current/anyscale" "$destdir/usr/local/bin/anyscalectl"
ln -sfn "$install_root/current/anyscaled" "$destdir/usr/local/bin/anyscaled"

if [ "$os" = linux ]; then
	unit_dir="$destdir/etc/systemd/system"
	install -d -m 0755 "$unit_dir"
	sed "s|@BINDIR@|$install_root/current|g" "$tmp/unpack/service/anyscaled.service.in" > "$unit_dir/anyscaled.service"
	chmod 0644 "$unit_dir/anyscaled.service"
	if [ "$start_service" -eq 1 ]; then
		systemctl daemon-reload
		systemctl enable --now anyscaled.service
	fi
else
	plist="$destdir/Library/LaunchDaemons/io.github.gabriwar.anyscaled.plist"
	install -d -m 0755 "$(dirname "$plist")"
	install -d -m 0700 "$destdir$state_dir"
	chmod 0700 "$destdir$state_dir"
	sed -e "s|@INSTALL_ROOT@|$install_root|g" -e "s|@STATE_DIR@|$state_dir|g" \
		"$tmp/unpack/service/io.github.gabriwar.anyscaled.plist.in" > "$plist"
	chmod 0644 "$plist"
	if [ "$start_service" -eq 1 ]; then
		launchctl bootout system/io.github.gabriwar.anyscaled 2>/dev/null || true
		launchctl bootstrap system "$plist"
	fi
fi

echo "Installed anyscale $version."
