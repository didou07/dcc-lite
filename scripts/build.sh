#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

VERSION="${VERSION:-$(tr -d '[:space:]' < VERSION)}"
TARGETS=("$@")
if [[ ${#TARGETS[@]} -eq 0 ]]; then
  TARGETS=(windows/amd64 windows/386 linux/amd64 linux/386 linux/arm64 linux/arm darwin/amd64 darwin/arm64)
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
    extra+=(GOTOOLCHAIN=local)
  fi
  [[ "$arch" == arm ]] && extra+=(GOARM="${GOARM:-7}")
  ext=""; [[ "$os" == windows ]] && ext=".exe"
  name="DCC-Lite-${VERSION}-${os}-${arch}${suffix}"
  echo "==> ${os}/${arch}${suffix}"
  stage="$(mktemp -d)"
  env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" ${extra[@]+"${extra[@]}"} \
    "$gobin" build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.version=${VERSION}" -o "${stage}/DCCLite${ext}" ./src/dcclite
  zip -qj -9 "dist/${name}.zip" "${stage}/DCCLite${ext}"
  rm -rf "$stage"
done
(cd dist && { sha256sum *.zip 2>/dev/null || shasum -a 256 *.zip; } > SHA256SUMS.txt)
echo "Done: $(pwd)/dist"
