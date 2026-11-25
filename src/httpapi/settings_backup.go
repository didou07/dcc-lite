package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/filetransfer"
)

const (
	settingsMaxArchive = 64 << 20
	settingsMaxEntries = 4000
)

const (
	scopeChannels = "channels"
	scopeSettings = "settings"
	scopeSoftcam  = "softcam"
)

var settingsScopeIDs = []string{scopeChannels, scopeSettings, scopeSoftcam}

var settingsChannelExtras = []string{"etc/tuxbox/satellites.xml", "etc/tuxbox/terrestrial.xml", "etc/tuxbox/cables.xml"}

var settingsSoftcamPaths = []string{"etc/tuxbox/config", "var/tuxbox/config", "etc/CCcam.cfg", "var/etc/CCcam.cfg", "var/keys", "usr/keys"}

const channelNamePattern = "lamedb|lamedb5|bouquets.tv|bouquets.radio|blacklist|whitelist|whitelist_streamrelay|userbouquet.*.tv|userbouquet.*.radio|alternatives.*.tv|alternatives.*.radio"

func isChannelName(name string) bool {
	switch name {
	case "lamedb", "lamedb5", "bouquets.tv", "bouquets.radio", "blacklist", "whitelist", "whitelist_streamrelay":
		return true
	}
	for _, prefix := range []string{"userbouquet.", "alternatives."} {
		if strings.HasPrefix(name, prefix) && (strings.HasSuffix(name, ".tv") || strings.HasSuffix(name, ".radio")) {
			return true
		}
	}
	return false
}

var (
	settingsJobRe   = regexp.MustCompile(`^set-[0-9a-f]{12}$`)
	settingsEntryRe = regexp.MustCompile("^[^\\x00-\\x1f\\x7f'\"`$\\\\;|<>*?]{1,128}$")

	settingsSafetyRe = regexp.MustCompile(`^before-restore-[0-9]{8}-[0-9]{6}\.tar\.gz$`)

	errNoSettingsFiles = errors.New("Nothing to back up: none of the selected files exist.")
)

const settingsSafetyKeep = 15

func settingsScopeKnown(id string) bool {
	for _, k := range settingsScopeIDs {
		if k == id {
			return true
		}
	}
	return false
}

func underPath(name, p string) bool { return name == p || strings.HasPrefix(name, p+"/") }

func settingsScopeOf(name string) string {
	name = strings.TrimSuffix(name, "/")
	if underPath(name, "etc/enigma2") {
		if path.Dir(name) == "etc/enigma2" && isChannelName(path.Base(name)) {
			return scopeChannels
		}
		return scopeSettings
	}
	for _, p := range settingsChannelExtras {
		if name == p {
			return scopeChannels
		}
	}
	for _, p := range settingsSoftcamPaths {
		if underPath(name, p) {
			return scopeSoftcam
		}
	}
	return ""
}

func settingsPathAllowed(name string) bool { return settingsScopeOf(name) != "" }

func settingsPackCommand(root string, scopes []string, remote string) (string, error) {
	if !regexp.MustCompile(`^/tmp/dcc-settings-[0-9a-f]{12}\.tar\.gz$`).MatchString(remote) {
		return "", errors.New("Invalid backup request.")
	}
	want := map[string]bool{}
	for _, id := range scopes {
		if !settingsScopeKnown(id) {
			return "", errors.New("Unknown backup type.")
		}
		want[id] = true
	}
	if len(want) == 0 {
		return "", errors.New("Choose what to back up.")
	}
	flag := func(on bool) string {
		if on {
			return "1"
		}
		return "0"
	}
	var b strings.Builder
	b.WriteString("cd " + root + "; set --; CH=" + flag(want[scopeChannels]) + "; ST=" + flag(want[scopeSettings]) + "; ")
	if want[scopeChannels] || want[scopeSettings] {
		b.WriteString("for f in etc/enigma2/*; do [ -e \"$f\" ] || continue; case \"${f##*/}\" in " + channelNamePattern +
			") [ $CH = 1 ] && set -- \"$@\" \"$f\";; *) [ $ST = 1 ] && set -- \"$@\" \"$f\";; esac; done; ")
	}
	add := func(paths []string, test string) {
		b.WriteString("for f in " + strings.Join(paths, " ") + "; do [ " + test + " \"$f\" ] && set -- \"$@\" \"$f\"; done; ")
	}
	if want[scopeChannels] {
		add(settingsChannelExtras, "-f")
	}
	if want[scopeSoftcam] {
		add(settingsSoftcamPaths, "-e")
	}
	b.WriteString("[ $# -gt 0 ] || { echo NOFILES; exit 0; }; tar -czf " + remote + " \"$@\" 2>/dev/null; echo \"rc=$?\"; md5sum " + remote + "; wc -c < " + remote)
	return b.String(), nil
}

func newSettingsJobID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "set-" + hex.EncodeToString(b[:]), nil
}

func parsePackOutput(out string) (md5 string, size int64, err error) {
	if strings.Contains(out, "NOFILES") {
		return "", 0, errNoSettingsFiles
	}
	okRC := false
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "rc=0" {
			okRC = true
			continue
		}
		f := strings.Fields(line)
		if len(f) == 2 && len(f[0]) == 32 && strings.HasPrefix(f[1], "/tmp/dcc-settings-") {
			md5 = strings.ToLower(f[0])
			continue
		}
		if len(f) == 1 {
			var n int64
			if _, e := fmt.Sscanf(f[0], "%d", &n); e == nil && n > 0 {
				size = n
			}
		}
	}
	if !okRC || md5 == "" || size == 0 {
		return "", 0, errors.New("The receiver could not pack the files (no tar, or no free space).")
	}
	return md5, size, nil
}

func (s *Server) fetchSettingsArchive(ctx context.Context, profile config.Profile, scopes []string) ([]byte, error) {
	job, err := newSettingsJobID()
	if err != nil {
		return nil, err
	}
	remote := "/tmp/dcc-settings-" + strings.TrimPrefix(job, "set-") + ".tar.gz"
	cmd, err := settingsPackCommand("/", scopes, remote)
	if err != nil {
		return nil, err
	}
	run := shellRunner{profile}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _, _ = run.run(cctx, 15*time.Second, "rm -f "+remote, 4<<10)
	}()
	out, _, err := run.run(ctx, 2*time.Minute, cmd, 16<<10)
	if err != nil {
		return nil, err
	}
	sum, size, err := parsePackOutput(out)
	if err != nil {
		return nil, err
	}
	if size > settingsMaxArchive {
		return nil, errors.New("The backup is larger than 64 MB.")
	}
	dir, err := os.MkdirTemp("", "dcclite-settings-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ftp, err := filetransfer.OpenFTP(ctx, filetransfer.FTPOptions{Host: profile.Host, Port: profile.EffectiveFTPPort(), Username: telnetUser(profile), Password: profile.Password})
	if err != nil {
		return nil, fmt.Errorf("FTP failed: %s", cleanText(err.Error(), 120))
	}
	defer ftp.Close()
	local := filepath.Join(dir, "settings.tar.gz")
	if err := ftp.Download(ctx, remote, local, nil); err != nil {
		return nil, fmt.Errorf("Download failed: %s", cleanText(err.Error(), 120))
	}
	got, n, err := fileMD5(local)
	if err != nil || n != size || got != sum {
		return nil, errors.New("The file arrived damaged. Try again.")
	}
	return os.ReadFile(local)
}

type settingsBackupRequest struct {
	Scopes []string `json:"scopes"`
}

func (s *Server) settingsBackup(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "settings backup requested")
	if s.denyRemoteBackup(w) {
		return
	}
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var req settingsBackupRequest
	_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req)
	if len(req.Scopes) == 0 {
		req.Scopes = settingsScopeIDs
	}
	if _, busy := backupBusy.LoadOrStore("set-"+profile.ID, true); busy {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Another backup or restore is running.", "code": "busy"})
		return
	}
	defer backupBusy.Delete("set-" + profile.ID)
	longTransfer(w)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	data, err := s.fetchSettingsArchive(ctx, profile, req.Scopes)
	skipped := 0
	if err == nil {
		data, skipped, err = cleanSettingsArchive(data)
	}
	if err == nil {
		if _, rerr := reviewSettingsArchive(data); rerr != nil {
			err = errors.New("The backup was not saved because it could not be restored later: " + rerr.Error())
		}
	}
	if err != nil {
		if errors.Is(err, errNoSettingsFiles) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error(), "code": "no_files"})
			return
		}
		if strings.Contains(err.Error(), "Telnet") || strings.Contains(err.Error(), "dial") || strings.Contains(err.Error(), "refused") {
			telnetFailure(w, profile, err)
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "backup_failed"})
		return
	}
	name := backupArchiveName(s.readImageIdentity(ctx, profile), profile.Host, time.Now())
	h := w.Header()
	h.Set("Content-Type", "application/gzip")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("Content-Length", fmt.Sprint(len(data)))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Settings-Skipped", fmt.Sprint(skipped))
	h.Set("Access-Control-Expose-Headers", "Content-Disposition, X-Settings-Skipped")
	_, _ = w.Write(data)
}
