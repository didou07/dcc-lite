#!/bin/sh
# DCC Lite launcher for Linux: installs the libraries the window needs (GTK 3 and WebKitGTK 4.1)
# when they are missing, then starts DCCLite. Any arguments are passed on to DCCLite.
# DCC_AUTO_INSTALL=1 installs without asking.
cd "$(dirname "$0")" || exit 1
[ -x ./DCCLite ] || chmod +x ./DCCLite 2>/dev/null

missing() { ldd ./DCCLite 2>/dev/null | grep "not found"; }

if [ -n "$(missing)" ]; then
  echo "DCC Lite needs these libraries, which are not installed:"
  missing | sed 's/^[[:space:]]*/  /'
  if command -v apt-get >/dev/null 2>&1; then
    pm="apt-get install -y libgtk-3-0 libwebkit2gtk-4.1-0"; pre="apt-get update"
  elif command -v dnf >/dev/null 2>&1; then
    pm="dnf install -y gtk3 webkit2gtk4.1"
  elif command -v pacman >/dev/null 2>&1; then
    pm="pacman -S --noconfirm gtk3 webkit2gtk-4.1"
  elif command -v zypper >/dev/null 2>&1; then
    pm="zypper --non-interactive install libgtk-3-0 libwebkit2gtk-4_1-0"
  else
    echo "Install GTK 3 and WebKitGTK 4.1 with your package manager, then run this again."; exit 1
  fi
  sudo=""
  if [ "$(id -u)" != 0 ]; then
    if command -v sudo >/dev/null 2>&1; then sudo="sudo"
    else echo "Run as root: ${pre:+$pre && }$pm"; exit 1; fi
  fi
  if [ "${DCC_AUTO_INSTALL:-}" != 1 ]; then
    printf "Install them now with: %s%s ? [Y/n] " "${sudo:+sudo }" "$pm"
    read -r answer
    case "$answer" in n*|N*) echo "Cancelled."; exit 1 ;; esac
  fi
  [ -n "${pre:-}" ] && { $sudo $pre || exit 1; }
  $sudo $pm || { echo "Installing failed. Run it yourself: $pm" >&2; exit 1; }
  if [ -n "$(missing)" ]; then
    echo "Still missing after installing:"; missing | sed 's/^[[:space:]]*/  /'; exit 1
  fi
fi
exec ./DCCLite "$@"
