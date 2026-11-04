#!/usr/bin/env bash
# Build DCC Lite into dist/ (one zip per platform + SHA256SUMS.txt).
#   scripts/build.sh                          every platform
#   scripts/build.sh windows/amd64            only the given os/arch targets
#   scripts/build.sh windows7/386             32-bit Windows 7/8/8.1 (see below)
#
# Targets: windows/{amd64,386}  linux/{amd64,386,arm64,arm}  darwin/{amd64,arm64}
#          windows7/{amd64,386}
#
# Windows 7, 8 and 8.1: programs built with Go 1.21 or newer do not start there. The windows7/*
# targets need the patched toolchain from https://github.com/XTLS/go-win7 : set GO_WIN7 to its `go`
# binary (scripts/setup-go-win7.sh downloads it). When GO_WIN7 is set, the default build also makes
# both windows7 packages. They are named DCC-Lite-<version>-windows-<arch>-win7.zip and run on
# Windows 10/11 too.
# 32-bit ARM Linux is built for ARMv7 (set GOARM=6 for a Raspberry Pi 1 / Zero).
# The version comes from the VERSION file (override with VERSION=1.2.3).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

VERSION="${VERSION:-$(tr -d '[:space:]' < VERSION)}"
TARGETS=("$@")
if [[ ${#TARGETS[@]} -eq 0 ]]; then
  TARGETS=(windows/amd64 linux/amd64 linux/386 linux/arm64 linux/arm darwin/amd64 darwin/arm64)
  [[ -z "${GO_WIN7:-}" ]] || TARGETS+=(windows7/amd64 windows7/386)
fi

rm -rf dist && mkdir -p dist
for target in "${TARGETS[@]}"; do
  os="${target%/*}" arch="${target#*/}"
  gobin=go suffix=""
  extra=()
  if [[ "$os" == windows7 ]]; then
    [[ -n "${GO_WIN7:-}" ]] || { echo "windows7 targets need GO_WIN7=/path/to/go-win7/bin/go (see scripts/setup-go-win7.sh)" >&2; exit 1; }
    gobin="$GO_WIN7" os=windows suffix="-win7"
    extra+=(GOTOOLCHAIN=local) # never let Go swap in the official toolchain: its output needs Windows 10
  fi
  [[ "$arch" == arm ]] && extra+=(GOARM="${GOARM:-7}")
  ext=""; [[ "$os" == windows ]] && ext=".exe"
  name="DCC-Lite-${VERSION}-${os}-${arch}${suffix}"
  echo "==> ${os}/${arch}${suffix}"
  mkdir -p "dist/${name}"
  env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" ${extra[@]+"${extra[@]}"} \
    "$gobin" build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "dist/${name}/DCCLite${ext}" ./src/dcclite
  if [[ "$os" == windows ]]; then
    printf '@echo off\r\ncd /d "%%~dp0"\r\nstart "DCC Lite" "%%~dp0DCCLite.exe" --portable\r\n' > "dist/${name}/START-DCCLite.cmd"
  fi
  (cd dist && zip -qr "${name}.zip" "${name}" && rm -rf "${name}")
done
(cd dist && { sha256sum *.zip 2>/dev/null || shasum -a 256 *.zip; } > SHA256SUMS.txt)
echo "Done: $(pwd)/dist"
