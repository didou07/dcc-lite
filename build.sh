#!/usr/bin/env bash
# DCC Lite release build (run on Linux).
#
#   ./build.sh                 8 standard targets
#   ./build.sh all             + Windows 7 builds (downloads the patched Go)
#   ./build.sh linux/arm64 ... only the targets you list
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

VERSION="${VERSION:-$(tr -d '[:space:]' < VERSION)}"
[[ $VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || { echo "Invalid VERSION: $VERSION" >&2; exit 1; }
for tool in go zip; do command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }; done

# Patched Go toolchain that still produces Windows 7 binaries.
win7_go() {
  local tag="${GO_WIN7_TAG:-patched-1.26.5}" dir os arch tmp
  dir="${TMPDIR:-/tmp}/go-win7-$tag"
  if [[ ! -x $dir/bin/go ]]; then
    case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) echo "Unsupported host OS" >&2; exit 1 ;; esac
    case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "Unsupported host CPU" >&2; exit 1 ;; esac
    tmp="$(mktemp -d)"
    curl -fsSL -o "$tmp/go.zip" "https://github.com/XTLS/go-win7/releases/download/$tag/go-for-win7-$os-$arch.zip"
    mkdir -p "$dir" && unzip -q "$tmp/go.zip" -d "$dir"
    chmod +x "$dir"/bin/* "$dir"/pkg/tool/*/* 2>/dev/null || true
    rm -rf "$tmp"
  fi
  echo "$dir/bin/go"
}

STANDARD=(windows/amd64 windows/386 linux/amd64 linux/386 linux/arm64 linux/arm darwin/amd64 darwin/arm64)
case "${1:-}" in
  "")  TARGETS=("${STANDARD[@]}") ;;
  all) TARGETS=("${STANDARD[@]}" windows7/amd64 windows7/386) ;;
  *)   TARGETS=("$@") ;;
esac

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
rm -rf dist && mkdir dist

for target in "${TARGETS[@]}"; do
  os="${target%/*}" arch="${target#*/}" go=go suffix="" ext="" extra=()
  if [[ $os == windows7 ]]; then
    os=windows suffix=-win7 go="${GO_WIN7:=$(win7_go)}" extra+=(GOTOOLCHAIN=local)
  fi
  [[ $arch == arm ]] && extra+=(GOARM="${GOARM:-7}")
  [[ $os == windows ]] && ext=.exe

  name="DCC-Lite-$VERSION-$os-$arch$suffix"
  echo "==> $name"
  env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" ${extra[@]+"${extra[@]}"} \
    "$go" build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.version=$VERSION" \
    -o "$stage/DCCLite$ext" ./src/dcclite
  zip -qj9 "dist/$name.zip" "$stage/DCCLite$ext"
  rm -f "$stage/DCCLite$ext"
done

(cd dist && { sha256sum ./*.zip 2>/dev/null || shasum -a 256 ./*.zip; } | sed 's| \./| |' > SHA256SUMS.txt)
echo "Done: $PWD/dist"
