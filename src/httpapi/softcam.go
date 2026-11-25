package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	softcamInitRe  = regexp.MustCompile(`^/etc/init\.d/[A-Za-z0-9._-]{1,48}$`)
	softcamFileRe  = regexp.MustCompile(`^(/(etc/tuxbox/config|var/tuxbox/config|var/keys|usr/keys)/[A-Za-z0-9._+-]{1,64}|/(etc|var/etc)/CCcam\.cfg)$`)
	softcamLogRe   = regexp.MustCompile(`^/(tmp|var/log)(/[A-Za-z0-9._-]{1,48}){0,2}/[A-Za-z0-9._-]{1,64}\.log$`)
	softcamWebRe   = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,100}/(oscam|ncam)(-[A-Za-z0-9._-]+)?\.conf$`)
	softcamProcRe  = regexp.MustCompile(`(?i)^(oscam[\w.-]*|ncam[\w.-]*|cccam[\w.-]*|[\w.-]*camd[\w.-]*|gcam[\w.-]*|doscam[\w.-]*|gbox|sbox|newcs|spcs|mpcs|wicardd|cardserver)$`)
	softcamNameRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,48}$`)
	softcamBinRe   = regexp.MustCompile(`^/(usr/bin|usr/softcams|usr/sbin|var/bin|usr/local/bin|bin|var/emu|usr/emu)/[A-Za-z0-9._-]{1,48}$`)
	softcamDirRe   = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,100}$`)
	softcamConfArg = regexp.MustCompile(`(?:^|\s)(?:-c|--config-dir|--config)[ =]+(/[A-Za-z0-9._/-]{1,100})`)
	softcamRCRe    = regexp.MustCompile(`^S[0-9]{2}(.+)$`)
	softcamActions = map[string]bool{"start": true, "stop": true, "restart": true, "switch": true}
)

var softcamNames = []string{"oscam", "ncam", "cccam", "camd", "gcam", "doscam", "gbox", "sbox", "newcs", "spcs", "mpcs", "wicardd", "cardserver"}

func softcamCase() string {
	patterns := make([]string, 0, len(softcamNames))
	for _, name := range softcamNames {
		var b strings.Builder
		if name == "camd" {
			b.WriteByte('*')
		}
		for _, r := range name {
			b.WriteByte('[')
			b.WriteRune(r - ('a' - 'A'))
			b.WriteRune(r)
			b.WriteByte(']')
		}
		b.WriteByte('*')
		patterns = append(patterns, b.String())
	}
	return strings.Join(patterns, "|")
}

var softcamProcHelpers = `softcam_is_cam() {
P="$1"
[ -r "/proc/$P/cmdline" ] || return 1
X=$(readlink "/proc/$P/exe" 2>/dev/null)
[ -n "$X" ] || X=$(tr '\000' '\n' < "/proc/$P/cmdline" 2>/dev/null | head -n 1)
B=${X##*/}
case "$B" in *" (deleted)") B=${B% (deleted)};; esac
case "$B" in ` + softcamCase() + `) return 0;; esac
return 1
}
`

func softcamScan(withDirs bool) string {
	collectDir := ""
	if withDirs {
		collectDir = `D=$(echo "$C" | grep -o -E '(-c|--config-dir|--config)[ =]+/[A-Za-z0-9._/-]+' | head -n 1 | sed 's/^[^/]*//'); [ -n "$D" ] && DIRS="$DIRS $D"; W=$(readlink "$D0/cwd" 2>/dev/null); case "$W" in ""|/) ;; *) DIRS="$DIRS $W";; esac; `
	}
	scanStart := `DIRS="${DIRS:-}"
`
	if !withDirs {
		scanStart = ""
	}
	return softcamProcHelpers + scanStart + `for D0 in /proc/[0-9]*; do
P=${D0#/proc/}
[ "$P" = "$$" ] && continue
softcam_is_cam "$P" || continue
X=$(readlink "$D0/exe" 2>/dev/null)
[ -n "$X" ] || X=$(tr '\000' '\n' < "$D0/cmdline" 2>/dev/null | head -n 1)
C=$(tr '\000' '\n' < "$D0/cmdline" 2>/dev/null | sed '1d' | tr '\n' ' ' | sed 's/[[:space:]]*$//')
[ -n "$X" ] || continue
echo "proc=$P $X $C"
LP=""
for FD in /proc/$P/fd/*; do L=$(readlink "$FD" 2>/dev/null); case "$L" in socket:*) LP="$LP $(echo "$L" | tr -dc 0-9)";; esac; done
PT=""
for H in $(awk -v ids="$LP" 'BEGIN{n=split(ids,a," ");for(i=1;i<=n;i++)w[a[i]]=1} $4=="0A"&&($10 in w){split($2,x,":");print x[2]}' /proc/net/tcp /proc/net/tcp6 2>/dev/null); do PT="$PT,$((0x$H))"; done
[ -n "$PT" ] && echo "listen=$P ${PT#,}"
` + collectDir + `done`
}

var softcamPS = softcamScan(false)

func softcamCommandLine(script string) string {
	var out strings.Builder
	quote := byte(0)
	awkPrev := byte(0)
	escaped := false
	for i := 0; i < len(script); i++ {
		c := script[i]
		if c == '\n' || c == '\r' {
			for i+1 < len(script) && (script[i+1] == '\n' || script[i+1] == '\r') {
				i++
			}
			for i+1 < len(script) && (script[i+1] == ' ' || script[i+1] == '\t') {
				i++
			}
			if quote == '\'' {
				next := byte(0)
				for j := i + 1; j < len(script); j++ {
					if script[j] != ' ' && script[j] != '\t' && script[j] != '\n' && script[j] != '\r' {
						next = script[j]
						break
					}
				}
				if awkPrev == 0 || awkPrev == '{' || awkPrev == '}' || next == '}' {
					out.WriteByte(' ')
				} else {
					out.WriteString("; ")
					awkPrev = ';'
				}
				continue
			}
			if quote == '"' {
				out.WriteByte(' ')
				continue
			}
			fields := strings.Fields(out.String())
			last := ""
			if len(fields) > 0 {
				last = fields[len(fields)-1]
			}
			if last == "do" || last == "then" || last == "else" || last == "{" {
				out.WriteByte(' ')
			} else {
				out.WriteString("; ")
			}
			escaped = false
			continue
		}
		if escaped {
			out.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' && quote != '\'' {
			out.WriteByte(c)
			escaped = true
			continue
		}
		insideAwk := quote == '\''
		if quote == 0 && (c == '\'' || c == '"') {
			quote = c
			if quote == '\'' {
				awkPrev = 0
			}
		} else if quote == c {
			quote = 0
			awkPrev = 0
		}
		out.WriteByte(c)
		if insideAwk && quote == '\'' && c != ' ' && c != '\t' {
			awkPrev = c
		}
	}
	return strings.TrimSpace(out.String())
}

var softcamInfoCommand = `DIRS=""
for F in /etc/init.d/softcam* /etc/init.d/*cam*; do
  if [ -x "$F" ] && [ -f "$F" ]; then
    echo "init=$F"
    B=""
    for C in $(grep -h -o -E '/(usr/bin|usr/softcams|usr/sbin|var/bin|usr/local/bin|bin|var/emu|usr/emu)/[A-Za-z0-9._-]+' "$F" 2>/dev/null | grep -i -E 'cam|gbox|sbox|newcs'); do
      [ -z "$B" ] && B="$C"
      [ -x "$C" ] && { B="$C"; break; }
    done
    [ -n "$B" ] && echo "bin=$F $B"
    C=$(grep -h -o -E '(-c|--config-dir|--config)[ =]+/[A-Za-z0-9._/-]+' "$F" 2>/dev/null | head -n 1 | sed 's/^[^/]*//')
    [ -n "$C" ] && DIRS="$DIRS $C" && echo "cfg=$F $C"
  fi
done
[ -L /etc/init.d/softcam ] && echo "link=$(readlink /etc/init.d/softcam)"
echo "@@ps"
` + softcamScan(true) + `
echo "@@web"
SEEN_DIRS=""
for D in $DIRS /etc/tuxbox/config/* /var/tuxbox/config/* /etc/tuxbox/config /var/tuxbox/config /etc/oscam /var/etc; do
  D=${D%/}
  case " $SEEN_DIRS " in *" $D "*) continue;; esac
  SEEN_DIRS="$SEEN_DIRS $D"
  for F in "$D/oscam.conf" "$D/ncam.conf"; do
    [ -f "$F" ] || continue
    echo "webf=$F"
    awk '
      /^[[:space:]]*\[/ {
        section=tolower($0)
        gsub(/^[[:space:]]*\[/, "", section)
        gsub(/\][[:space:]]*$/, "", section)
        next
      }
      section == "webif" && tolower($0) ~ /^[[:space:]]*httpport[[:space:]]*=/ {
        v=$0; sub(/^[^=]*=[[:space:]]*/, "", v); sub(/[[:space:]]*[#;].*$/, "", v); gsub(/[[:space:]\r]/, "", v); print "kv=httpport=" v
      }
      section == "global" && tolower($0) ~ /^[[:space:]]*logfile[[:space:]]*=/ {
        v=$0; sub(/^[^=]*=[[:space:]]*/, "", v); sub(/[[:space:]]*#.*$/, "", v); gsub(/^[[:space:]]+|[[:space:]]+$/, "", v); print "kv=logfile=" v
      }
    ' "$F" 2>/dev/null
  done
done
echo "@@boot"
for F in /etc/rc[S2-5].d/S*; do
  [ -e "$F" ] && case "$F" in *cam*|*Cam*|*CAM*) echo "rc=$(basename "$F") $(readlink "$F" 2>/dev/null)";; esac
done
echo "@@files"
for F in /etc/tuxbox/config/* /var/tuxbox/config/* /etc/CCcam.cfg /var/etc/CCcam.cfg /var/keys/* /usr/keys/*; do
  [ -f "$F" ] && echo "file=$F $(wc -c < "$F")"
done
echo "@@logs"
for F in /tmp/*.log /var/log/*.log /tmp/*/*.log /var/log/*/*.log; do
  [ -f "$F" ] || continue
  case "$F" in *oscam*|*OSCam*|*ncam*|*NCam*|*cccam*|*camd*|*gcam*|*softcam*) echo "log=$F $(wc -c < "$F")";; esac
done`

type softcamFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type softcamProc struct {
	PID   int    `json:"pid"`
	Name  string `json:"name"`
	Cmd   string `json:"cmd"`
	Bin   string `json:"bin,omitempty"`
	Conf  string `json:"conf,omitempty"`
	Ports []int  `json:"ports,omitempty"`
}

type softcamWeb struct {
	File string `json:"file"`
	Dir  string `json:"dir"`
	Port int    `json:"port"`
	TLS  bool   `json:"tls,omitempty"`
	Log  string `json:"log,omitempty"`
}

type softcamInfo struct {
	Cams    []string          `json:"cams"`
	Link    string            `json:"link"`
	Running []string          `json:"running"`
	Files   []softcamFile     `json:"files"`
	Logs    []softcamFile     `json:"logs"`
	Bins    map[string]string `json:"bins"`
	Confs   map[string]string `json:"confs"`
	Procs   []softcamProc     `json:"procs"`
	Boot    []string          `json:"boot"`
	Web     []softcamWeb      `json:"web"`
}

func softcamLogOK(name string) bool {
	return softcamLogRe.MatchString(name) && !strings.Contains(name, "..")
}

func addSoftcamProc(info *softcamInfo, seen map[int]bool, pid int, bin, cmd string) {
	if pid < 2 || seen[pid] {
		return
	}
	name := strings.ToLower(path.Base(bin))
	if !softcamProcRe.MatchString(name) {
		return
	}
	seen[pid] = true
	proc := softcamProc{PID: pid, Name: name, Cmd: cleanText(strings.TrimSpace(cmd), 240)}
	if strings.HasPrefix(bin, "/") {
		proc.Bin = cleanText(bin, 160)
	}
	if c := softcamConfArg.FindStringSubmatch(cmd); c != nil && !strings.Contains(c[1], "..") {
		proc.Conf = strings.TrimRight(c[1], "/")
	}
	info.Procs = append(info.Procs, proc)
	found := false
	for _, old := range info.Running {
		if old == name {
			found = true
			break
		}
	}
	if !found {
		info.Running = append(info.Running, name)
	}
}

func parseSoftcamInfo(out string) softcamInfo {
	info := softcamInfo{
		Cams: []string{}, Running: []string{}, Files: []softcamFile{}, Logs: []softcamFile{},
		Bins: map[string]string{}, Confs: map[string]string{}, Procs: []softcamProc{}, Boot: []string{}, Web: []softcamWeb{},
	}
	section := ""
	seen := map[string]bool{}
	seenPIDs := map[int]bool{}
	webIndex := -1
	seenWeb := map[string]bool{}
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		line := strings.TrimSpace(raw)
		switch line {
		case "@@ps", "@@web", "@@boot", "@@files", "@@logs":
			section = line
			webIndex = -1
			continue
		}
		switch section {
		case "@@ps":
			if strings.HasPrefix(line, "proc=") {
				pidAndRest := strings.TrimPrefix(line, "proc=")
				pidText, rest, ok := strings.Cut(pidAndRest, " ")
				if !ok {
					continue
				}
				pid, _ := strconv.Atoi(pidText)
				bin, args, _ := strings.Cut(strings.TrimSpace(rest), " ")
				cmd := strings.TrimSpace(rest)
				if args != "" {
					cmd = strings.TrimSpace(rest)
				}
				addSoftcamProc(&info, seenPIDs, pid, bin, cmd)
				continue
			}
			if strings.HasPrefix(line, "listen=") {
				pidText, list, _ := strings.Cut(strings.TrimPrefix(line, "listen="), " ")
				pid, _ := strconv.Atoi(pidText)
				for i := range info.Procs {
					if info.Procs[i].PID != pid {
						continue
					}
					for _, value := range strings.Split(strings.TrimSpace(list), ",") {
						if port, err := strconv.Atoi(value); err == nil && port > 0 && port <= 65535 {
							info.Procs[i].Ports = append(info.Procs[i].Ports, port)
						}
					}
				}
				continue
			}
			tokens := strings.Fields(line)
			for i, token := range tokens {
				if strings.Contains(token, "grep") {
					break
				}
				if strings.HasPrefix(token, "[") {
					continue
				}
				base := strings.ToLower(path.Base(token))
				if !softcamProcRe.MatchString(base) {
					continue
				}
				pid, _ := strconv.Atoi(tokens[0])
				addSoftcamProc(&info, seenPIDs, pid, token, strings.Join(tokens[i:], " "))
				break
			}
		case "@@web":
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch key {
			case "webf":
				file := path.Clean(value)
				if softcamWebRe.MatchString(file) && !strings.Contains(file, "..") {
					if !seenWeb[file] {
						seenWeb[file] = true
						info.Web = append(info.Web, softcamWeb{File: file, Dir: path.Dir(file)})
					}
					for i := range info.Web {
						if info.Web[i].File == file {
							webIndex = i
							break
						}
					}
				}
			case "kv":
				if webIndex < 0 || webIndex >= len(info.Web) {
					continue
				}
				name, val, _ := strings.Cut(value, "=")
				switch strings.ToLower(name) {
				case "httpport":
					tls := strings.HasPrefix(val, "+")
					port, err := strconv.Atoi(strings.TrimPrefix(val, "+"))
					if err == nil && port > 0 && port <= 65535 {
						info.Web[webIndex].Port = port
						info.Web[webIndex].TLS = tls
					}
				case "logfile":
					for _, candidate := range strings.Split(val, ",") {
						candidate = strings.TrimSpace(candidate)
						if softcamLogOK(candidate) {
							info.Web[webIndex].Log = candidate
							break
						}
					}
				}
			}
		case "@@boot":
			key, value, ok := strings.Cut(line, "=")
			if !ok || key != "rc" {
				continue
			}
			fields := strings.Fields(value)
			name := ""
			if len(fields) == 2 && softcamInitRe.MatchString(fields[1]) {
				name = path.Base(fields[1])
			} else if len(fields) > 0 {
				if match := softcamRCRe.FindStringSubmatch(fields[0]); match != nil {
					name = match[1]
				}
			}
			if name != "" && softcamNameRe.MatchString(name) && !seen["b"+name] {
				seen["b"+name] = true
				info.Boot = append(info.Boot, name)
			}
		case "@@files", "@@logs":
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			fields := strings.Fields(value)
			if len(fields) != 2 {
				continue
			}
			size, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				continue
			}
			if key == "file" && softcamFileRe.MatchString(fields[0]) {
				info.Files = append(info.Files, softcamFile{Path: fields[0], Size: size})
			}
			if key == "log" && softcamLogOK(fields[0]) {
				info.Logs = append(info.Logs, softcamFile{Path: fields[0], Size: size})
			}
		default:
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch key {
			case "init":
				if softcamInitRe.MatchString(value) && !seen[value] {
					seen[value] = true
					info.Cams = append(info.Cams, path.Base(value))
				}
			case "bin":
				fields := strings.Fields(value)
				if len(fields) == 2 && softcamInitRe.MatchString(fields[0]) && softcamBinRe.MatchString(fields[1]) {
					info.Bins[path.Base(fields[0])] = fields[1]
				}
			case "cfg":
				fields := strings.Fields(value)
				if len(fields) == 2 && softcamInitRe.MatchString(fields[0]) && softcamDirRe.MatchString(fields[1]) && !strings.Contains(fields[1], "..") {
					info.Confs[path.Base(fields[0])] = strings.TrimRight(fields[1], "/")
				}
			case "link":
				info.Link = cleanText(value, 100)
			}
		}
	}
	sort.Strings(info.Cams)
	for _, web := range info.Web {
		if web.Log == "" {
			continue
		}
		found := false
		for _, logFile := range info.Logs {
			if logFile.Path == web.Log {
				found = true
				break
			}
		}
		if !found {
			info.Logs = append(info.Logs, softcamFile{Path: web.Log})
		}
	}
	return info
}

func (s *Server) softcamInfo(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	out, _, err := (shellRunner{profile}).run(ctx, 30*time.Second, softcamCommandLine(softcamInfoCommand), 128<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	writeJSON(w, http.StatusOK, parseSoftcamInfo(out))
}

type softcamActionRequest struct {
	Action string `json:"action"`
	Cam    string `json:"cam"`
	PIDs   []int  `json:"pids"`
}

func softcamActionCommand(action, cam string) (string, error) {
	if !softcamActions[action] || (cam != "" && !softcamNameRe.MatchString(cam)) || strings.Contains(cam, "..") {
		return "", errBadSoftcam
	}
	script := "/etc/init.d/softcam"
	if cam != "" {
		script = "/etc/init.d/" + cam
	}
	if !softcamInitRe.MatchString(script) {
		return "", errBadSoftcam
	}
	if action != "switch" {
		return "[ -x " + script + " ] || { echo NOSCRIPT; exit 0; }; " + script + " " + action + " 2>&1 | tail -n 8", nil
	}
	if cam == "" {
		return "", errBadSoftcam
	}
	return "A=/etc/init.d/softcam; N=" + script + "; [ -x \"$N\" ] || { echo NOSCRIPT; exit 0; }; " +
		"[ -x \"$A\" ] && \"$A\" stop 2>&1 | tail -n 3; " +
		"if [ -L \"$A\" ]; then ln -sf \"$N\" \"$A\" && echo LINKED; else echo NOLINK; fi; " +
		"sleep 1; if [ -L \"$A\" ]; then \"$A\" start 2>&1 | tail -n 5; else \"$N\" start 2>&1 | tail -n 5; fi; sleep 2", nil
}

func softcamKillCommand(pids []int) (string, error) {
	if len(pids) == 0 || len(pids) > 32 {
		return "", errBadSoftcam
	}
	seen := map[int]bool{}
	values := make([]string, 0, len(pids))
	for _, pid := range pids {
		if pid < 2 || pid > 4194304 || seen[pid] {
			if seen[pid] {
				continue
			}
			return "", errBadSoftcam
		}
		seen[pid] = true
		values = append(values, strconv.Itoa(pid))
	}
	return softcamProcHelpers + `for P in ` + strings.Join(values, " ") + `; do
  softcam_is_cam "$P" && kill -TERM "$P" 2>/dev/null
done
sleep 1
for P in ` + strings.Join(values, " ") + `; do
  if [ -d "/proc/$P" ] && softcam_is_cam "$P"; then kill -KILL "$P" 2>/dev/null; fi
done
sleep 1
echo "@@ps"
` + softcamPS, nil
}

func softcamKillAllCommand() string {
	return softcamProcHelpers + `PIDS=""
for D in /proc/[0-9]*; do
  P=${D#/proc/}
  softcam_is_cam "$P" && PIDS="$PIDS $P"
done
for P in $PIDS; do softcam_is_cam "$P" && kill -TERM "$P" 2>/dev/null; done
sleep 1
for P in $PIDS; do
  if [ -d "/proc/$P" ] && softcam_is_cam "$P"; then kill -KILL "$P" 2>/dev/null; fi
done
sleep 1
echo "@@ps"
` + softcamPS
}

var errBadSoftcam = &softcamError{"Invalid softcam request."}

type softcamError struct{ msg string }

func (e *softcamError) Error() string { return e.msg }

func (s *Server) softcamAction(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "softcam action")
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var req softcamActionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	var cmd string
	var err error
	switch req.Action {
	case "kill":
		cmd, err = softcamKillCommand(req.PIDs)
	case "killall":
		cmd = softcamKillAllCommand()
	default:
		cmd, err = softcamActionCommand(req.Action, req.Cam)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	runCommand := cmd
	if req.Action == "kill" || req.Action == "killall" {
		runCommand = softcamCommandLine(cmd)
	}
	out, _, err := (shellRunner{profile}).run(ctx, 50*time.Second, runCommand, 64<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	if strings.Contains(out, "NOSCRIPT") {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "No start script for this softcam.", "code": "no_script"})
		return
	}
	head := out
	if req.Action == "kill" || req.Action == "killall" {
		var tail string
		head, tail, _ = strings.Cut(out, "@@ps")
		post := parseSoftcamInfo("@@ps\n" + tail)
		writeJSON(w, http.StatusOK, map[string]any{
			"output":  cleanText(strings.TrimSpace(strings.ReplaceAll(head, "\r", "")), 600),
			"running": post.Running,
			"procs":   post.Procs,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"output":  cleanText(strings.TrimSpace(strings.ReplaceAll(head, "\r", "")), 600),
		"linked":  strings.Contains(head, "LINKED"),
		"no_link": strings.Contains(head, "NOLINK"),
	})
}

func softcamLogCommand(file string, lines int, offset int64) string {
	const maxChunk = 131072
	const initialWindow = 262144
	limit := strconv.Itoa(maxChunk)
	if offset < 0 {
		return `if [ ! -f ` + file + ` ]; then echo "@@missing"; else S=$(wc -c < ` + file + ` 2>/dev/null); S=${S:-0}; START=$((S-` + strconv.Itoa(initialWindow) + `)); [ "$START" -lt 0 ] && START=0; echo "@@size=$S"; tail -c +$((START+1)) ` + file + ` 2>/dev/null | head -c $((S-START)) | tail -n ` + strconv.Itoa(lines) + ` | tail -c ` + limit + `; fi`
	}
	o := strconv.FormatInt(offset, 10)
	return `if [ ! -f ` + file + ` ]; then echo "@@missing"; else S=$(wc -c < ` + file + ` 2>/dev/null); S=${S:-0}; ` +
		`if [ "$S" -lt ` + o + ` ]; then START=$((S-` + limit + `)); [ "$START" -lt 0 ] && START=0; echo "@@size=$S"; echo "@@reset"; ` +
		`tail -c +$((START+1)) ` + file + ` 2>/dev/null | head -c $((S-START)); ` +
		`else N=$((S-` + o + `)); [ "$N" -gt ` + limit + ` ] && N=` + limit + `; NEXT=$((` + o + `+N)); echo "@@size=$NEXT"; ` +
		`if [ "$N" -gt 0 ]; then tail -c +$((` + o + `+1)) ` + file + ` 2>/dev/null | head -c "$N"; fi; fi; fi`
}

func parseSoftcamLog(out string) (text string, size int64, reset, missing bool) {
	out = strings.ReplaceAll(out, "\r", "")
	line, rest, found := strings.Cut(out, "\n")
	if strings.TrimSpace(line) == "@@missing" {
		return "", 0, false, true
	}
	if !found || !strings.HasPrefix(line, "@@size=") {
		return out, 0, false, false
	}
	size, _ = strconv.ParseInt(strings.TrimPrefix(line, "@@size="), 10, 64)
	if strings.HasPrefix(rest, "@@reset\n") {
		reset = true
		rest = strings.TrimPrefix(rest, "@@reset\n")
	}
	return rest, size, reset, false
}

func (s *Server) softcamLog(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	file := r.URL.Query().Get("file")
	if !softcamLogOK(file) {
		writeError(w, http.StatusBadRequest, "Unknown log file.")
		return
	}
	lines := 200
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n >= 10 && n <= 1000 {
		lines = n
	}
	offset := int64(-1)
	if value := r.URL.Query().Get("offset"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "Invalid request.")
			return
		}
		offset = n
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, _, err := (shellRunner{profile}).run(ctx, 20*time.Second, softcamLogCommand(file, lines, offset), 256<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	text, size, reset, missing := parseSoftcamLog(out)
	if missing {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "The log file does not exist.", "code": "no_log"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file, "text": text, "size": size, "reset": reset})
}
