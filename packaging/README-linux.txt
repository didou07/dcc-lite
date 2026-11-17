DCC Lite - Linux (amd64, arm64, 386, arm)

Run:
  unzip DCC-Lite-*-linux-*.zip
  chmod +x DCCLite
  ./DCCLite

Needed:
  A Chromium-family browser for the window (Chrome, Chromium, Edge, Brave...).
    Debian/Ubuntu:  sudo apt install chromium    (or chromium-browser)
    Fedora:         sudo dnf install chromium
    Arch:           sudo pacman -S chromium
  Without one, the interface opens in your default browser at
  http://127.0.0.1:8787 and the program runs until you press Ctrl+C.
  Another browser: DCC_BROWSER=/path/to/browser ./DCCLite
