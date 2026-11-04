# DCC Lite

A light, local web tool for Enigma2 receivers: devices, FTP files, Telnet terminal
(multiple tabs), live screen with remote keys, channel list with logos and
"on now" programmes, bouquet editor, power actions.

- Run `DCCLite` ; it opens in your browser.
- Data lives in the user config folder `DCCLite` (or `data/` beside the program with `--portable`).
  Folders from earlier builds (`DCCNext`, `BoxDeck`) are moved automatically on first start.
- Channel logos are looked up on the receiver like OpenWebif does: service reference,
  the same with service type 1, then the simplified channel name (`bbcone.png`).
- In the channel list, type a number and press Enter to zap to it.
- **Watch on the computer:** the play button in the channel list (or on a channel row) saves an
  `.m3u` playlist; open it with VLC or any player. The receiver's own playlist is used when it answers,
  otherwise `http://<receiver>:8001/<channel>` is written.
- **Receiver information:** the list button on a device card shows image, Enigma2, uptime, memory,
  tuners (what each one is doing), tuner signal (SNR/AGC), disks and, when Files is connected to the
  device, the temperature (OpenWebif does not publish it; it is read from `/proc/stb/...`).
- **Files → Tools:** backup the channel list (or every plain file of `/etc/enigma2`) as a `.zip`,
  restore it (only channel list files are written, bouquet index last, then the receiver reloads),
  and install an `.ipk`: it is uploaded to `/tmp` and `opkg install` runs in a Terminal tab.

Build: see Packages below. Tests: see Tests.

## Project layout

Everything that runs is in `src/`; everything that checks it is in `test/`.

```
src/
  dcclite/        program entry point: flags, start-up, HTTPS remote access
  httpapi/        HTTP + WebSocket server (one topic per file, table below)
  web/            browser interface embedded in the binary: index.html, app.css, login.*
    js/           the page's scripts, loaded in the order index.html lists them
    vendor/       xterm (terminal), with its licence
  app/            application object: version, system info, profiles
  config/         saved device profiles
  connection/     checks which services a device answers on
  core/           interfaces shared by file transfer and terminal
  enigma2/        channel list files (bouquets, lamedb)
  filetransfer/   FTP, local files, editing
  netscan/        finds receivers on the local network
  remoteaccess/   login, password and certificate for the HTTPS listener
  terminal/       telnet terminal sessions
test/             integration.sh (runs the real program) and dccimport.test.js
scripts/          build.sh, build.ps1, setup-go-win7.sh
```

Go unit tests (`*_test.go`) stay next to the code they test: Go gives them access to unexported
names only from the same folder.

### Where things are in `src/httpapi`

`server.go` registers every route, so start there.

| File | Topic |
| --- | --- |
| `server.go`, `security.go` | server, routes, JSON helpers, security headers, loopback-only guard |
| `profiles.go` | device profiles, passwords, import/export, connection test |
| `files.go` | local files, FTP sessions, remote files, text editing, transfers |
| `terminal.go`, `websocket.go` | terminal sessions and their WebSocket |
| `network.go` | scan for receivers |
| `openwebif.go` | client for a receiver's web interface (OpenWebif); used by the files below |
| `screen.go`, `rcu.go`, `channels.go`, `picon.go`, `stream.go` | live picture, remote-control keys, channel list, logos, `.m3u` |
| `deviceinfo.go`, `multiboot.go` | receiver information, MultiBoot slots |
| `bouquets.go`, `backup.go`, `ipk.go` | bouquet editor, channel-list backup/restore, `.ipk` install |
| `access.go`, `access_admin.go` | optional password-protected HTTPS listener and its settings |

In this code, *access* means the HTTPS listener you can reach from other computers, and *rcu* means
the receiver's remote-control keys.

## Security

Device profiles (with passwords) are stored in the data folder and ignored by git. The local page only
answers on loopback; remote access is off by default, needs a password, and can only be configured
from the local page. Plain HTTP for remote access must be chosen explicitly.

## Releases

Releases are automatic. GitHub Actions builds Windows (7 to 11, 32/64-bit), Linux (PC, ARM, ARM64)
and macOS (Intel, Apple silicon) and publishes them. To release, change the number in `VERSION`
(for example `1.0.0` to `1.0.1`), commit and push to `main`:

```
echo 1.0.1 > VERSION
git commit -am "Release 1.0.1" && git push
```

The `Release` workflow builds every package and creates the release `v1.0.1` with
the `.zip` files and `SHA256SUMS.txt` attached. If that release already exists nothing happens; a
number with a dash (`1.1.0-rc1`) becomes a pre-release.

## Tests

```
go test ./...                 # unit and API tests
node test/dccimport.test.js   # script import
bash test/integration.sh      # builds and runs the program, checks pages and security rules
```

## Packages

Download the package that matches the computer:

| System | Package |
| --- | --- |
| Windows 10 / 11, 64-bit | `windows-amd64` |
| Windows 10, 32-bit | `windows-386` |
| Windows 7 / 8 / 8.1, 64-bit | `windows-amd64-win7` |
| Windows 7 / 8 / 8.1, 32-bit | `windows-386-win7` |
| Linux PC, 64-bit | `linux-amd64` |
| Linux PC, 32-bit | `linux-386` |
| Linux ARM 64-bit (Raspberry Pi 3/4/5 with a 64-bit system) | `linux-arm64` |
| Linux ARM 32-bit (Raspberry Pi 2 and newer) | `linux-arm` |
| macOS, Apple silicon | `darwin-arm64` |
| macOS, Intel | `darwin-amd64` |

The `-win7` packages also run on Windows 10 and 11. Windows 7 needs Service Pack 1 and the
Convenience Rollup (KB3125574). Windows XP and Vista cannot run any current Go program. On Windows 7
the newest browsers are Chrome 109 and Firefox ESR 115.

Build one yourself: `scripts/build.sh linux/amd64` (or `scripts\build.ps1` on Windows).

The Windows 7 packages are built with the patched Go toolchain from
[XTLS/go-win7](https://github.com/XTLS/go-win7), because programs made with the official Go 1.21+ do
not start on Windows 7:

```
export GO_WIN7="$(scripts/setup-go-win7.sh)"          # downloads it
scripts/build.sh windows7/386 windows7/amd64          # or scripts/build.sh: it then includes them
```

On Windows: `$env:GO_WIN7 = "C:\go-win7\bin\go.exe"; .\scripts\build.ps1 -Win7 -Arch 386`.
