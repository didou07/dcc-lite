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

# Every package has a window; it falls back to the default browser if the window cannot open.
case "$(uname -m)" in x86_64|amd64) HOST=amd64 ;; aarch64|arm64) HOST=arm64 ;; *) HOST="" ;; esac
if [[ $HOST == arm64 && $(uname -s) == Linux ]]; then
  STANDARD=(linuxgui/arm64)
else
  STANDARD=(windowsgui/amd64 windowsgui/386 linuxgui/amd64)
  # 386 and arm only when their cross toolchain and libraries are installed.
  [[ $(uname -s) == Linux ]] && command -v arm-linux-gnueabihf-gcc >/dev/null \
    && PKG_CONFIG_LIBDIR=/usr/lib/arm-linux-gnueabihf/pkgconfig:/usr/share/pkgconfig pkg-config --exists gtk+-3.0 webkit2gtk-4.1 \
    && STANDARD+=(linuxgui/arm)
  [[ $(uname -s) == Linux ]] \
    && PKG_CONFIG_LIBDIR=/usr/lib/i386-linux-gnu/pkgconfig:/usr/share/pkgconfig pkg-config --exists gtk+-3.0 webkit2gtk-4.1 \
    && STANDARD+=(linuxgui/386)
fi
case "${1:-}" in
  ""|all) TARGETS=("${STANDARD[@]}"); [[ $(uname -s) == Darwin ]] && TARGETS+=(darwingui/arm64 darwingui/amd64) ;;
  mac)    TARGETS=(darwingui/arm64 darwingui/amd64) ;;
  windows7gui/*|windows7/*) echo "the Windows 7 builds were merged into windowsgui (Windows 7 to 11): use windowsgui/${1#*/}" >&2; exit 1 ;;
  windows/*|linux/*|darwin/*) echo "browser-only packages are no longer built: use windowsgui/${1#*/} or linuxgui/${1#*/}; on a Mac use ./build.sh mac" >&2; exit 1 ;;
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
    os=linux variant=gui tags="native" cgo=1
    [[ $(uname -s) == Linux ]] || { echo "linuxgui must be built on Linux (cgo, GTK and WebKitGTK)" >&2; exit 1; }
    command -v pkg-config >/dev/null || { echo "pkg-config is required for linuxgui/$arch" >&2; exit 1; }
    case $arch in
      amd64|arm64)
        command -v gcc >/dev/null || { echo "gcc is required for linuxgui/$arch" >&2; exit 1; }
        [[ $arch == "$HOST" ]] || { echo "linuxgui/$arch must be built on a Linux $arch machine (cgo, no cross build)" >&2; exit 1; } ;;
      386)
        command -v gcc >/dev/null || { echo "gcc (with -m32 support, gcc-multilib) is required for linuxgui/386" >&2; exit 1; }
        [[ $HOST == amd64 ]] || { echo "linuxgui/386 must be built on a Linux amd64 machine" >&2; exit 1; }
        extra+=(CC="gcc -m32" PKG_CONFIG_LIBDIR=/usr/lib/i386-linux-gnu/pkgconfig:/usr/share/pkgconfig) ;;
      arm)
        command -v arm-linux-gnueabihf-gcc >/dev/null || { echo "gcc-arm-linux-gnueabihf is required for linuxgui/arm" >&2; exit 1; }
        [[ $HOST == amd64 ]] || { echo "linuxgui/arm must be built on a Linux amd64 machine" >&2; exit 1; }
        extra+=(CC=arm-linux-gnueabihf-gcc PKG_CONFIG_LIBDIR=/usr/lib/arm-linux-gnueabihf/pkgconfig:/usr/share/pkgconfig) ;;
      *) echo "linuxgui/$arch is not supported" >&2; exit 1 ;;
    esac
    env ${extra[@]+"${extra[@]}"} pkg-config --exists gtk+-3.0 gdk-x11-3.0 webkit2gtk-4.1 || { echo "linuxgui/$arch needs the GTK 3 and WebKitGTK 4.1 development packages for $arch" >&2; exit 1; }
  fi
  if [[ $os == darwingui ]]; then
    [[ $(uname -s) == Darwin ]] || { echo "darwingui must be built on a Mac (cgo and the system WebKit; no cross build)" >&2; exit 1; }
    command -v clang >/dev/null || { echo "clang is required for darwingui (xcode-select --install)" >&2; exit 1; }
    os=darwin variant=gui tags="native" cgo=1
  fi
  [[ $arch == arm ]] && extra+=(GOARM="${GOARM:-7}")
  [[ $os == windows ]] && ext=.exe

  name="DCC-Lite-$VERSION-$os-$arch"
  echo "==> $name"
  env CGO_ENABLED="$cgo" GOOS="$os" GOARCH="$arch" ${extra[@]+"${extra[@]}"} \
    "$go" build -trimpath -buildvcs=false ${tags:+-tags "$tags"} -ldflags "-s -w -buildid= $gui -X main.version=$VERSION -X main.variant=$variant" \
    -o "$stage/$exe$ext" ./src/dcclite
  case $os in windows) readme=README-windows.txt ;; darwin) readme=README-macos.txt ;; *) readme=README-linux.txt ;; esac
  rm -f "$stage/README.txt" && cp "packaging/$readme" "$stage/README.txt"
  zip -qj9 "dist/$name.zip" "$stage/$exe$ext" "$stage/README.txt"
  if [[ -n $isgui && $os == windows ]]; then
    # Optional Windows 7/8 bundle with an unpacked WebView2 109 folder.
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
