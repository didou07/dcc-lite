package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/terminal"
)

var upgradeLine = regexp.MustCompile(`^Inst (\S+) \[([^\]]+)\] \((\S+)`)

const dreamOSListCommand = "dpkg-query -W -f='${Status}\\t${Package}\\t${Version}\\n' 2>/dev/null; " +
	"printf '@@AVAIL@@\\n'; apt-cache dumpavail 2>/dev/null | grep -E '^(Package|Version|Section|Description):'; " +
	"printf '@@UP@@\\n'; apt-get -s -qq upgrade 2>/dev/null | grep '^Inst '"

func parseDreamOSPackages(out string) ([]packageEntry, error) {
	installedPart, rest, ok := strings.Cut(out, "@@AVAIL@@\n")
	if !ok {
		return nil, errors.New("no package list")
	}
	availPart, upPart, _ := strings.Cut(rest, "@@UP@@\n")

	installed := map[string]string{}
	for _, line := range strings.Split(installedPart, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) == 3 && f[0] == "install ok installed" && packageNamePattern.MatchString(f[1]) {
			installed[f[1]] = f[2]
		}
	}
	if len(installed) == 0 {
		return nil, errors.New("no installed packages")
	}
	oldVersion, newVersion := map[string]string{}, map[string]string{}
	for _, line := range strings.Split(upPart, "\n") {
		if m := upgradeLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			oldVersion[m[1]], newVersion[m[1]] = m[2], m[3]
		}
	}

	byName := map[string]*packageEntry{}
	var cur *packageEntry
	for _, line := range strings.Split(availPart, "\n") {
		line = strings.TrimRight(line, " \t")
		switch {
		case strings.HasPrefix(line, "Package: "):
			name := strings.TrimPrefix(line, "Package: ")
			cur = nil
			if packageNamePattern.MatchString(name) {
				if _, seen := byName[name]; !seen {
					cur = &packageEntry{Name: name}
					byName[name] = cur
				}
			}
		case cur == nil:
		case strings.HasPrefix(line, "Version: "):
			cur.Version = cleanText(strings.TrimPrefix(line, "Version: "), 60)
		case strings.HasPrefix(line, "Section: "):
			cur.Section = cleanText(strings.TrimPrefix(line, "Section: "), 40)
		case strings.HasPrefix(line, "Description: "):
			cur.Description = cleanText(strings.TrimPrefix(line, "Description: "), 240)
		}
	}
	for name, version := range installed {
		e, ok := byName[name]
		if !ok {
			e = &packageEntry{Name: name}
			byName[name] = e
		}
		e.Installed = true
		e.Version = cleanText(version, 60)
		if nv, up := newVersion[name]; up && nv != version {
			e.Version = cleanText(nv, 60)
			e.UpgradeFrom = cleanText(oldVersion[name], 60)
		}
	}
	out2 := make([]packageEntry, 0, len(byName))
	for _, e := range byName {
		out2 = append(out2, *e)
	}
	sort.Slice(out2, func(i, j int) bool { return out2[i].Name < out2[j].Name })
	return out2, nil
}

func dreamOSProtectedPackage(name string) bool {
	switch name {
	case "dpkg", "apt", "bash", "dash", "coreutils", "util-linux", "login", "openssh-server", "dropbear", "telnetd",
		"inetutils-telnetd", "enigma2-plugin-extensions-webinterface", "dreambox-base-files":
		return true
	}
	return strings.HasPrefix(name, "dreambox-") || strings.HasPrefix(name, "linux-image") || strings.Contains(name, "telnet")
}

func telnetFailure(w http.ResponseWriter, profile config.Profile, err error) {
	switch {
	case errors.Is(err, terminal.ErrCommandTimeout), errors.Is(err, context.DeadlineExceeded):
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": "The receiver is still working on it.", "code": "still_running"})
	case errors.Is(err, terminal.ErrNoShell):
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver's Telnet service did not give a command prompt. Check the user name and password saved for it.", "code": "web_auth"})
	default:
		var netErr net.Error
		msg := "The receiver's Telnet service (port " + strconv.Itoa(profile.EffectiveTelnetPort()) + ") did not answer. On DreamOS, packages are managed through it."
		if errors.As(err, &netErr) || strings.Contains(err.Error(), "refused") {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": msg, "code": "no_telnet"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver's Telnet command failed.", "code": "web_error"})
	}
}

func telnetUser(profile config.Profile) string {
	if profile.Username != "" {
		return profile.Username
	}
	return config.DefaultUsername
}

// dreamOSPackageList answers the package list; the caller caches it.
func dreamOSPackageList(ctx context.Context, w http.ResponseWriter, profile config.Profile) ([]packageEntry, bool) {
	out, _, err := terminal.RunCommand(ctx, profile.Host, profile.EffectiveTelnetPort(), telnetUser(profile), profile.Password, dreamOSListCommand, 24<<20)
	if err != nil {
		telnetFailure(w, profile, err)
		return nil, false
	}
	list, perr := parseDreamOSPackages(out)
	if perr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return its package list (dpkg and apt were not found).", "code": "web_error"})
		return nil, false
	}
	return list, true
}

const dreamOSPackageLog = "/tmp/dcc-pkg.log"

func dreamOSPackageCommand(action, pkg string) string {
	apt := "DEBIAN_FRONTEND=noninteractive nohup apt-get -y -o Dpkg::Options::=--force-confold -o Dpkg::Options::=--force-confdef "
	var run string
	switch action {
	case "update":
		run = "nohup apt-get update"
	case "upgrade":
		run = apt + "upgrade"
	case "install":
		run = apt + "install " + pkg
	case "remove":
		run = apt + "remove " + pkg
	default:
		return ""
	}
	return run + " > " + dreamOSPackageLog + " 2>&1 < /dev/null; rc=$?; tail -n 200 " + dreamOSPackageLog + "; (exit $rc)"
}

func dreamOSPackageAction(ctx context.Context, w http.ResponseWriter, profile config.Profile, action, pkg string) {
	command := dreamOSPackageCommand(action, pkg)
	out, code, err := terminal.RunCommand(ctx, profile.Host, profile.EffectiveTelnetPort(), telnetUser(profile), profile.Password, command, 4<<20)
	packageCachesMu.Lock()
	delete(packageCaches, profile.ID)
	packageCachesMu.Unlock()
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	clean := make([]string, 0, 200)
	for _, line := range strings.Split(out, "\n") {
		if line = cleanText(line, 300); line != "" {
			clean = append(clean, line)
		}
	}
	if len(clean) > 200 {
		clean = clean[len(clean)-200:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": code == 0, "lines": clean})
}
