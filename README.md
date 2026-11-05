# DCC Lite

A light, local web tool for Enigma2 receivers: devices, FTP files, Telnet terminal, live screen with
remote keys, signal meter for aiming a dish, channel list with logos, programme guide with search, timers and recordings, bouquet editor, backup/restore, power actions.

Run `DCCLite`; it opens in your browser. Data is kept in the user config folder `DCCLite`
(or in `data/` beside the program with `--portable`).

## Download

| System | Package |
| --- | --- |
| Windows 10 / 11 | `windows-amd64`, `windows-386` |
| Windows 7 / 8 / 8.1 (SP1 + KB3125574) | `windows-amd64-win7`, `windows-386-win7` |
| Linux | `linux-amd64`, `linux-386`, `linux-arm64`, `linux-arm` |
| macOS | `darwin-arm64` (Apple silicon), `darwin-amd64` (Intel) |

## Release

Every push to `main` builds and publishes release `v<VERSION>` with all packages, unless it already
exists. To release a new version, raise the number in `VERSION` and push.

## Code

```
src/        all the code (src/httpapi/server.go lists every route; src/web is the browser UI)
test/       integration.sh, dccimport.test.js (Go unit tests sit next to their code)
scripts/    build.sh, build.ps1, setup-go-win7.sh
```

Build: `scripts/build.sh [os/arch]`. Tests: `go test ./...`, `bash test/integration.sh`.
