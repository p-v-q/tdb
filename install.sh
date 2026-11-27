#!/bin/sh
# Install the TDB terminal database client from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/p-v-q/tdb/main/install.sh | sh
#
# Environment variables:
#   TDB_VERSION       release to install (default: latest)
#   TDB_INSTALL_DIR   directory for the tdb binary (default: /usr/local/bin
#                     when writable, otherwise ~/.local/bin)
#   TDB_REPO          owner/repo to download from (default: p-v-q/tdb)
set -eu

REPO="${TDB_REPO:-p-v-q/tdb}"
VERSION="${TDB_VERSION:-}"
INSTALL_DIR="${TDB_INSTALL_DIR:-}"

say() { printf '%s\n' "$*"; }
die() { printf 'tdb: %s\n' "$*" >&2; exit 1; }

command -v uname >/dev/null 2>&1 || die "uname is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

if command -v curl >/dev/null 2>&1; then
	download() { curl -fsSL "$1" -o "$2"; }
	fetch() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
	download() { wget -q "$1" -O "$2"; }
	fetch() { wget -qO- "$1"; }
else
	die "curl or wget is required"
fi

os="$(uname -s)"
case "$os" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "unsupported operating system: $os (use the Windows installer or a release archive)" ;;
esac

arch="$(uname -m)"
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported architecture: $arch" ;;
esac

if [ -z "$VERSION" ]; then
	api="https://api.github.com/repos/${REPO}/releases/latest"
	json="$(fetch "$api")" || die "cannot query the latest release; set TDB_VERSION"
	VERSION="$(printf '%s' "$json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
	[ -n "$VERSION" ] || die "cannot determine the latest version; set TDB_VERSION"
fi
VERSION="${VERSION#v}"

asset="tdb_${VERSION}_${os}_${arch}.tar.gz"
base="https://github.com/${REPO}/releases/download/v${VERSION}"
tmp="$(mktemp -d 2>/dev/null || mktemp -dt tdb.XXXXXX)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading ${asset}"
download "${base}/${asset}" "${tmp}/${asset}" || die "download failed: ${base}/${asset}"

# Verify the published checksum when the release provides one.
if download "${base}/SHA256SUMS" "${tmp}/SHA256SUMS" 2>/dev/null; then
	expected="$(awk -v a="$asset" '$2 == a { print $1; exit }' "${tmp}/SHA256SUMS")"
	if [ -n "$expected" ]; then
		if command -v sha256sum >/dev/null 2>&1; then
			actual="$(sha256sum "${tmp}/${asset}" | awk '{ print $1 }')"
		elif command -v shasum >/dev/null 2>&1; then
			actual="$(shasum -a 256 "${tmp}/${asset}" | awk '{ print $1 }')"
		else
			actual=""
		fi
		if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
			die "checksum mismatch for ${asset}"
		fi
	fi
fi

tar -xzf "${tmp}/${asset}" -C "$tmp"
dir="${tmp}/${asset%.tar.gz}"
[ -f "${dir}/tdb" ] || die "archive did not contain the tdb binary"
chmod 0755 "${dir}/tdb"

if [ -z "$INSTALL_DIR" ]; then
	if [ -w /usr/local/bin ]; then
		INSTALL_DIR=/usr/local/bin
	else
		INSTALL_DIR="${HOME}/.local/bin"
	fi
fi
mkdir -p "$INSTALL_DIR" || die "cannot create ${INSTALL_DIR}"
[ -w "$INSTALL_DIR" ] || die "no write permission for ${INSTALL_DIR}"

cp "${dir}/tdb" "${INSTALL_DIR}/tdb"
chmod 0755 "${INSTALL_DIR}/tdb"

say "Installed tdb to ${INSTALL_DIR}/tdb"
case ":${PATH}:" in
	*":${INSTALL_DIR}:"*) ;;
	*) say "Add ${INSTALL_DIR} to PATH to run tdb from anywhere." ;;
esac
"${INSTALL_DIR}/tdb" -version 2>/dev/null || true
