DCC Lite - Linux (amd64, arm64, 386, arm)

Run:
  unzip DCC-Lite-*-linux-*.zip
  chmod +x DCCLite
  ./DCCLite

The program opens its own GTK window. If the window cannot open (no graphical session, missing libraries),
the interface opens in your default browser at http://127.0.0.1:8787 and the program runs until you press
Ctrl+C.

Needed for the window (all CPUs; on WSL use WSLg):
  Debian/Ubuntu:  sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0
  Fedora:         sudo dnf install gtk3 webkit2gtk4.1
  Arch:           sudo pacman -S gtk3 webkit2gtk-4.1
