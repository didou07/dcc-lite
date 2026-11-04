#!/usr/bin/env bash
# Download the patched Go toolchain that builds programs able to run on Windows 7/8/8.1
# (https://github.com/XTLS/go-win7) and print the path of its `go` binary on stdout.
#   export GO_WIN7="$(scripts/setup-go-win7.sh)"
#   scripts/build.sh windows7/386 windows7/amd64
# GO_WIN7_TAG pins the release (default: patched-1.26.5, the newest line that still supports
# every Windows 7 SP1 install). GO_WIN7_DIR chooses where it is unpacked.
set -euo pipefail

TAG="${GO_WIN7_TAG:-patched-1.26.5}"
DEST="${GO_WIN7_DIR:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/go-win7-${TAG}}"

if [[ ! -x "$DEST/bin/go" ]]; then
  case "$(uname -s)" in Linux) host_os=linux ;; Darwin) host_os=darwin ;; *) echo "unsupported host OS" >&2; exit 1 ;; esac
  case "$(uname -m)" in x86_64|amd64) host_arch=amd64 ;; aarch64|arm64) host_arch=arm64 ;; *) echo "unsupported host CPU" >&2; exit 1 ;; esac
  asset="go-for-win7-${host_os}-${host_arch}.zip"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  echo "Downloading ${asset} (${TAG})" >&2
  curl -fsSL -o "$tmp/$asset" "https://github.com/XTLS/go-win7/releases/download/${TAG}/${asset}"
  rm -rf "$DEST" && mkdir -p "$DEST"
  unzip -q "$tmp/$asset" -d "$DEST"
  chmod +x "$DEST"/bin/* "$DEST"/pkg/tool/*/* 2>/dev/null || true
fi

"$DEST/bin/go" version >&2
echo "$DEST/bin/go"
