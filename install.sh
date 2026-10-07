#!/bin/sh
# Polyroot installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/Kazaz-Or/polyroot/master/install.sh | sh
#
# Environment variables:
#   POLYROOT_VERSION      release tag to install, e.g. v0.1.0 (default: latest)
#   POLYROOT_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
#
# The script downloads the release archive for your OS and CPU, verifies its
# SHA-256 against the release's checksums.txt, and copies one file into
# POLYROOT_INSTALL_DIR. It never uses sudo and touches nothing else.
set -eu

REPO="Kazaz-Or/polyroot"
RELEASES="https://github.com/${REPO}/releases"
# For testing against a local build: a directory URL holding the archives and checksums.txt.
DOWNLOAD_BASE="${POLYROOT_DOWNLOAD_BASE:-}"
INSTALL_DIR="${POLYROOT_INSTALL_DIR:-${HOME}/.local/bin}"
VERSION="${POLYROOT_VERSION:-}"

say() { printf '%s\n' "$*"; }
die() { printf 'polyroot install: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "unsupported OS $(uname -s); Polyroot supports macOS and Linux" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported CPU $(uname -m); Polyroot supports amd64 and arm64" ;;
esac

if [ -z "$VERSION" ]; then
  # /releases/latest redirects to /releases/tag/<tag>; no API token or jq needed.
  latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${RELEASES}/latest") ||
    die "no published release found at ${RELEASES} (the repository may be private, or nothing is released yet)"
  VERSION=${latest##*/}
  case "$VERSION" in
    v[0-9]*) ;;
    *) die "no published release found at ${RELEASES}" ;;
  esac
fi
case "$VERSION" in v*) ;; *) VERSION="v${VERSION}" ;; esac

archive="polyroot_${VERSION#v}_${os}_${arch}.tar.gz"
base="${DOWNLOAD_BASE:-${RELEASES}/download/${VERSION}}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading Polyroot ${VERSION} (${os}/${arch})..."
curl -fsSL -o "${tmp}/${archive}" "${base}/${archive}" ||
  die "download failed: ${base}/${archive}"
curl -fsSL -o "${tmp}/checksums.txt" "${base}/checksums.txt" ||
  die "download failed: ${base}/checksums.txt"

expected=$(awk -v f="$archive" '$2 == f { print $1 }' "${tmp}/checksums.txt")
[ -n "$expected" ] || die "${archive} is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "${tmp}/${archive}" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "${tmp}/${archive}" | awk '{ print $1 }')
else
  die "sha256sum or shasum is required to verify the download"
fi
[ "$expected" = "$actual" ] || die "checksum mismatch for ${archive} (expected ${expected}, got ${actual})"

tar -xzf "${tmp}/${archive}" -C "$tmp" polyroot
mkdir -p "$INSTALL_DIR"
# Install via a temp name + rename so a running polyroot is never half-written.
cp "${tmp}/polyroot" "${INSTALL_DIR}/.polyroot.tmp"
chmod 755 "${INSTALL_DIR}/.polyroot.tmp"
mv -f "${INSTALL_DIR}/.polyroot.tmp" "${INSTALL_DIR}/polyroot"

say "Installed $("${INSTALL_DIR}/polyroot" --version) to ${INSTALL_DIR}/polyroot"
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    say ""
    say "${INSTALL_DIR} is not on your PATH. Add it, for example:"
    say "  echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.zshrc   # or ~/.bashrc"
    ;;
esac
say ""
config_home="${POLYROOT_CONFIG_HOME:-${XDG_CONFIG_HOME:-${HOME}/.config}/polyroot}"
if [ -f "${config_home}/config.yaml" ]; then
  say "Your config is already set up (${config_home}/config.yaml). Run: polyroot doctor"
else
  say "Get started:"
  say "  polyroot setup                                   # choose your agent and where your code lives"
  say "  polyroot workspace add <name> <repo> <repo>...   # create a workspace"
  say "  polyroot <name>                                  # open it"
fi
say ""
say "Update later with: polyroot update"
