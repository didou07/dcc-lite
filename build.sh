#!/usr/bin/env bash
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

# One package per system. linux/amd64 is the Chromium app window unless LINUX_AMD64=gtk.
LINUX_AMD64="${LINUX_AMD64:-gui}"
case "$LINUX_AMD64" in gtk|gui) ;; *) echo "LINUX_AMD64 must be gui or gtk" >&2; exit 1 ;; esac
if [[ $LINUX_AMD64 == gtk ]]; then LINUX_AMD64_TARGET=linuxgtk/amd64; else LINUX_AMD64_TARGET=linuxgui/amd64; fi
STANDARD=(windowsgui/amd64 windowsgui/386 "$LINUX_AMD64_TARGET" linuxgui/arm64 linuxgui/386 linuxgui/arm)
case "${1:-}" in
  ""|all) TARGETS=("${STANDARD[@]}"); [[ $(uname -s) == Darwin ]] && TARGETS+=(darwingui/arm64 darwingui/amd64) ;;
  mac)    TARGETS=(darwingui/arm64 darwingui/amd64) ;;
  windows7gui/*|windows7/*) echo "the Windows 7 builds were merged into windowsgui (Windows 7 to 11): use windowsgui/${1#*/}" >&2; exit 1 ;;
  windows/*|linux/*|darwin/*) echo "browser-only packages are no longer built: use windowsgui/${1#*/} or linuxgui/${1#*/} (or linuxgtk/amd64); on a Mac use ./build.sh mac" >&2; exit 1 ;;
  *)   TARGETS=("$@") ;;
esac

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
rm -rf dist && mkdir dist

for target in "${TARGETS[@]}"; do
  os="${target%/*}" arch="${target#*/}" cgo=0 go=go ext="" variant="" exe="DCCLite" tags="" gui="" isgui="" extra=()
  if [[ $os == windowsgui ]]; then
    os=windows variant=gui tags="native" gui="-H windowsgui" go="${GO_WIN7:=$(win7_go)}" extra+=(GOTOOLCHAIN=local)
    isgui=1
  fi
  if [[ $os == linuxgui ]]; then
    os=linux variant=gui tags="native"
  fi
  if [[ $os == darwingui ]]; then
    [[ $(uname -s) == Darwin ]] || { echo "darwingui must be built on a Mac (cgo and the system WebKit; no cross build)" >&2; exit 1; }
    command -v clang >/dev/null || { echo "clang is required for darwingui (xcode-select --install)" >&2; exit 1; }
    os=darwin variant=gui tags="native" cgo=1
  fi
  if [[ $os == linuxgtk ]]; then
    for tool in gcc pkg-config; do command -v "$tool" >/dev/null || { echo "$tool is required for linuxgtk" >&2; exit 1; }; done
    pkg-config --exists gtk+-3.0 webkit2gtk-4.1 || { echo "linuxgtk needs libgtk-3-dev and libwebkit2gtk-4.1-dev" >&2; exit 1; }
    case "$(uname -m)" in x86_64|amd64) host=amd64 ;; aarch64|arm64) host=arm64 ;; *) host="" ;; esac
    [[ $arch == "$host" ]] || { echo "linuxgtk/$arch must be built on a Linux $arch machine (cgo, no cross build)" >&2; exit 1; }
    os=linux variant=gtk tags="native gtk" cgo=1
  fi
  [[ $arch == arm ]] && extra+=(GOARM="${GOARM:-7}")
  [[ $os == windows ]] && ext=.exe

  name="DCC-Lite-$VERSION-$os-$arch"
  echo "==> $name"
  env CGO_ENABLED="$cgo" GOOS="$os" GOARCH="$arch" ${extra[@]+"${extra[@]}"} \
    "$go" build -trimpath -buildvcs=false ${tags:+-tags "$tags"} -ldflags "-s -w -buildid= $gui -X main.version=$VERSION -X main.variant=$variant" \
    -o "$stage/$exe$ext" ./src/dcclite
  # Each package holds the program and a README.txt for its system (how to run and what to install).
  case $os in windows) readme=README-windows.txt ;; darwin) readme=README-macos.txt ;; *) readme=README-linux.txt ;; esac
  rm -f "$stage/README.txt" && cp "packaging/$readme" "$stage/README.txt"
  zip -qj9 "dist/$name.zip" "$stage/$exe$ext" "$stage/README.txt"
  if [[ -n $isgui && $os == windows ]]; then
    # Optional full package for Windows 7/8: the program plus an unpacked WebView2 109 folder.
    case $arch in amd64) wv="${WEBVIEW2_X64_DIR:-}" ;; 386) wv="${WEBVIEW2_X86_DIR:-}" ;; *) wv="" ;; esac
    if [[ -n $wv ]]; then
      [[ -d $wv ]] || { echo "WebView2 folder not found: $wv" >&2; exit 1; }
      mkdir -p "$stage/bundle" && cp "$stage/$exe$ext" "$stage/bundle/" && cp -r "$wv" "$stage/bundle/WebView2"
      (cd "$stage/bundle" && zip -qr9 "$OLDPWD/dist/$name-bundle.zip" .)
      rm -rf "$stage/bundle"
    fi
  fi
  rm -f "$stage/$exe$ext"
done

(cd dist && { sha256sum ./*.zip 2>/dev/null || shasum -a 256 ./*.zip; } | sed 's| \./| |' > SHA256SUMS.txt)
echo "Done: $PWD/dist"
