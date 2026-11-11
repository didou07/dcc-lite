# DCC Lite

local web tool for Enigma2 receivers: devices, FTP files, Telnet terminal, live screen 

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
scripts/    build.sh, build.ps1, setup-go-win7.sh
```

Build: `scripts/build.sh [os/arch]`.
