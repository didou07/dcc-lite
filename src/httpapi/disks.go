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
	"unicode"

	"github.com/didou07/dcc-lite/src/config"
)

var (
	diskDeviceRe  = regexp.MustCompile(`^/dev/(sd[a-z]{1,2}[0-9]{1,2}|hd[a-z][0-9]{1,2}|mmcblk[0-9]{1,2}p[0-9]{1,2}|nvme[0-9]{1,2}n[0-9]{1,2}p[0-9]{1,2})$`)
	diskMountRe   = regexp.MustCompile(`^/(media|mnt)/[A-Za-z0-9._-]{1,48}(/[A-Za-z0-9._-]{1,48}){0,2}$`)
	diskHostRe    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	diskNFSPathRe = regexp.MustCompile(`^/[A-Za-z0-9._/ -]{1,150}$`)
	diskCIFSRe    = regexp.MustCompile(`^[A-Za-z0-9._$ -]{1,64}(/[A-Za-z0-9._ -]{1,64}){0,3}$`)
	diskUserRe    = regexp.MustCompile(`^[A-Za-z0-9._@\\ -]{0,64}$`)
	diskLabelRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{0,16}$`)
	diskOptionsRe = regexp.MustCompile(`^[A-Za-z0-9=,._-]{0,120}$`)
	diskVersionRe = regexp.MustCompile(`^(auto|1\.0|2\.0|2\.1|3\.0)$`)
	diskNFSVerRe  = regexp.MustCompile(`^(auto|3|4)$`)
	diskNameRe    = regexp.MustCompile(`^(sd[a-z]+|hd[a-z]+|mmcblk[0-9]+|nvme[0-9]+n[0-9]+)(p?[0-9]+)?$`)
	diskWholeRe   = regexp.MustCompile(`^(sd[a-z]+|hd[a-z]+|mmcblk[0-9]+|nvme[0-9]+n[0-9]+)$`)
	diskOfRe      = regexp.MustCompile(`^(sd[a-z]+|hd[a-z]+|mmcblk[0-9]+|nvme[0-9]+n[0-9]+)(p?[0-9]+)?$`)
	blkidFieldRe  = regexp.MustCompile(`([A-Za-z_]+)="([^"]*)"`)

	diskLocalFS   = map[string]bool{"auto": true, "ext4": true, "ext3": true, "ext2": true, "vfat": true, "ntfs": true, "ntfs-3g": true, "exfat": true}
	diskFormatFS  = map[string]string{"ext4": "mkfs.ext4", "ext3": "mkfs.ext3", "vfat": "mkfs.vfat", "exfat": "mkfs.exfat", "ntfs": "mkfs.ntfs"}
	diskFormatSeq = []string{"ext4", "ext3", "vfat", "exfat", "ntfs"}
)

const (
	disksInfoCommand = `echo "@@mounts"; cat /proc/mounts; echo "@@swaps"; cat /proc/swaps 2>/dev/null; ` +
		`echo "@@df"; df -kP 2>/dev/null || df -k 2>/dev/null; echo "@@parts"; cat /proc/partitions; ` +
		`echo "@@blkid"; blkid 2>/dev/null; echo "@@sys"; ` +
		`for B in /sys/block/*; do N=${B##*/}; case "$N" in sd*|hd*|mmcblk*|nvme*) echo "dev=$N|$(cat $B/removable 2>/dev/null)|$(cat $B/device/model 2>/dev/null || cat $B/device/name 2>/dev/null)";; esac; done; ` +
		`echo "@@mem"; grep -E '^(MemTotal|MemFree|MemAvailable|Buffers|Cached|SwapTotal|SwapFree):' /proc/meminfo; ` +
		`echo "@@fstab"; cat /etc/fstab 2>/dev/null; echo "@@fs"; cat /proc/filesystems; echo "@@tools"; ` +
		`for T in mkfs.ext4 mkfs.ext3 mkfs.vfat mkfs.exfat mkfs.ntfs mount.cifs mount.nfs; do command -v $T >/dev/null 2>&1 && echo "tool=$T"; done; echo "@@fmt"; ` + diskFormatStatusCommand + `; echo "@@end"`

	diskFormatFiles = `/tmp/dcc-format`
)

type diskPart struct {
	Name     string `json:"name"`
	Device   string `json:"device"`
	Label    string `json:"label,omitempty"`
	UUID     string `json:"uuid,omitempty"`
	FS       string `json:"fs,omitempty"`
	SizeKB   int64  `json:"size_kb"`
	UsedKB   int64  `json:"used_kb"`
	AvailKB  int64  `json:"avail_kb"`
	Mount    string `json:"mount,omitempty"`
	Options  string `json:"options,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
	Swap     bool   `json:"swap,omitempty"`
	System   bool   `json:"system,omitempty"`
}

type diskDevice struct {
	Name      string     `json:"name"`
	Device    string     `json:"device"`
	Model     string     `json:"model,omitempty"`
	SizeKB    int64      `json:"size_kb"`
	Removable bool       `json:"removable"`
	System    bool       `json:"system,omitempty"`
	Parts     []diskPart `json:"parts"`
}

type diskShare struct {
	Source  string `json:"source"`
	Mount   string `json:"mount"`
	FS      string `json:"fs"`
	Options string `json:"options,omitempty"`
	SizeKB  int64  `json:"size_kb"`
	UsedKB  int64  `json:"used_kb"`
	AvailKB int64  `json:"avail_kb"`
	Boot    bool   `json:"boot"`
}

type fstabEntry struct {
	Source  string `json:"source"`
	Mount   string `json:"mount"`
	FS      string `json:"fs"`
	Options string `json:"options,omitempty"`
	Network bool   `json:"network"`
	Mounted bool   `json:"mounted"`
}

type diskMemory struct {
	TotalKB     int64 `json:"total_kb"`
	FreeKB      int64 `json:"free_kb"`
	AvailableKB int64 `json:"available_kb"`
	BuffersKB   int64 `json:"buffers_kb"`
	CachedKB    int64 `json:"cached_kb"`
	SwapTotalKB int64 `json:"swap_total_kb"`
	SwapFreeKB  int64 `json:"swap_free_kb"`
}

type disksInfo struct {
	Disks   []diskDevice    `json:"disks"`
	Shares  []diskShare     `json:"shares"`
	Fstab   []fstabEntry    `json:"fstab"`
	Memory  diskMemory      `json:"memory"`
	Formats []string        `json:"formats"`
	CIFS    bool            `json:"cifs"`
	NFS     bool            `json:"nfs"`
	Format  diskFormatState `json:"format"`
}

func diskSections(out string) map[string][]string {
	sections := map[string][]string{}
	current := ""
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if strings.HasPrefix(line, "@@") {
			current = strings.TrimSpace(strings.TrimPrefix(line, "@@"))
			continue
		}
		if current != "" && strings.TrimSpace(line) != "" {
			sections[current] = append(sections[current], line)
		}
	}
	return sections
}

func fstabUnescape(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

func fstabEscape(s string) string {
	return strings.ReplaceAll(s, " ", `\040`)
}

func diskIsNetworkFS(fs string) bool {
	switch fs {
	case "nfs", "nfs4", "cifs", "smb3", "smbfs":
		return true
	}
	return false
}

func diskOf(name string) string {
	if m := diskOfRe.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return name
}

type mountRow struct {
	source, mount, fs, options string
}

func parseDiskMounts(lines []string) []mountRow {
	rows := make([]mountRow, 0, len(lines))
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		rows = append(rows, mountRow{fstabUnescape(f[0]), fstabUnescape(f[1]), f[2], f[3]})
	}
	return rows
}

func parseDiskDF(lines []string) map[string][3]int64 {
	out := map[string][3]int64{}
	for i, line := range lines {
		if i == 0 && strings.HasPrefix(line, "Filesystem") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		size, e1 := strconv.ParseInt(f[1], 10, 64)
		used, e2 := strconv.ParseInt(f[2], 10, 64)
		avail, e3 := strconv.ParseInt(f[3], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		out[fstabUnescape(strings.Join(f[5:], " "))] = [3]int64{size, used, avail}
	}
	return out
}

func parseDiskBlkid(lines []string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, line := range lines {
		dev, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := map[string]string{}
		for _, m := range blkidFieldRe.FindAllStringSubmatch(rest, -1) {
			fields[strings.ToUpper(m[1])] = m[2]
		}
		out[strings.TrimSpace(dev)] = fields
	}
	return out
}

func parseDiskMemory(lines []string) diskMemory {
	var mem diskMemory
	for _, line := range lines {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			mem.TotalKB = n
		case "MemFree":
			mem.FreeKB = n
		case "MemAvailable":
			mem.AvailableKB = n
		case "Buffers":
			mem.BuffersKB = n
		case "Cached":
			mem.CachedKB = n
		case "SwapTotal":
			mem.SwapTotalKB = n
		case "SwapFree":
			mem.SwapFreeKB = n
		}
	}
	if mem.AvailableKB == 0 {
		mem.AvailableKB = mem.FreeKB + mem.BuffersKB + mem.CachedKB
	}
	return mem
}

func parseDisksInfo(out string) disksInfo {
	sections := diskSections(out)
	info := disksInfo{Disks: []diskDevice{}, Shares: []diskShare{}, Fstab: []fstabEntry{}, Formats: []string{}}

	mounts := parseDiskMounts(sections["mounts"])
	df := parseDiskDF(sections["df"])
	blk := parseDiskBlkid(sections["blkid"])
	swaps := map[string]bool{}
	for i, line := range sections["swaps"] {
		if i == 0 && strings.HasPrefix(line, "Filename") {
			continue
		}
		if f := strings.Fields(line); len(f) > 0 {
			swaps[f[0]] = true
		}
	}
	byDevice := map[string]mountRow{}
	mountedAt := map[string]bool{}
	for _, row := range mounts {
		mountedAt[row.mount] = true
		if strings.HasPrefix(row.source, "/dev/") {
			if _, seen := byDevice[row.source]; !seen {
				byDevice[row.source] = row
			}
		}
	}

	physical := map[string]*diskDevice{}
	var order []string
	for _, line := range sections["sys"] {
		rest, ok := strings.CutPrefix(line, "dev=")
		if !ok {
			continue
		}
		p := strings.SplitN(rest, "|", 3)
		if len(p) < 3 || !diskWholeRe.MatchString(p[0]) {
			continue
		}
		physical[p[0]] = &diskDevice{Name: p[0], Device: "/dev/" + p[0], Model: strings.TrimSpace(p[2]), Removable: strings.TrimSpace(p[1]) == "1", Parts: []diskPart{}}
		order = append(order, p[0])
	}

	for i, line := range sections["parts"] {
		if i == 0 && strings.Contains(line, "major") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 || !diskNameRe.MatchString(f[3]) {
			continue
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			continue
		}
		name := f[3]
		owner := diskOf(name)
		disk := physical[owner]
		if disk == nil {
			disk = &diskDevice{Name: owner, Device: "/dev/" + owner, Parts: []diskPart{}}
			physical[owner] = disk
			order = append(order, owner)
		}
		if name == owner {
			disk.SizeKB = size
			continue
		}
		if size <= 1 {
			continue
		}
		dev := "/dev/" + name
		part := diskPart{Name: name, Device: dev, SizeKB: size, Swap: swaps[dev]}
		if tags := blk[dev]; tags != nil {
			part.Label, part.UUID, part.FS = tags["LABEL"], tags["UUID"], tags["TYPE"]
		}
		if row, ok := byDevice[dev]; ok {
			part.Mount, part.Options = row.mount, row.options
			if part.FS == "" {
				part.FS = row.fs
			}
			part.ReadOnly = strings.HasPrefix(row.options, "ro,") || row.options == "ro"
			if space, ok := df[row.mount]; ok {
				part.SizeKB, part.UsedKB, part.AvailKB = space[0], space[1], space[2]
			}
			if row.mount == "/" || row.mount == "/boot" {
				disk.System = true
			}
		}
		disk.Parts = append(disk.Parts, part)
	}

	sort.Strings(order)
	for _, name := range order {
		disk := physical[name]
		if disk.System {
			for i := range disk.Parts {
				disk.Parts[i].System = true
			}
		}
		info.Disks = append(info.Disks, *disk)
	}

	boot := map[string]bool{}
	for _, line := range sections["fstab"] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		entry := fstabEntry{Source: fstabUnescape(f[0]), Mount: fstabUnescape(f[1]), FS: f[2], Options: f[3], Network: diskIsNetworkFS(f[2])}
		entry.Mounted = mountedAt[entry.Mount]
		if entry.Network {
			boot[entry.Mount] = true
		}
		info.Fstab = append(info.Fstab, entry)
	}

	for _, row := range mounts {
		if !diskIsNetworkFS(row.fs) {
			continue
		}
		share := diskShare{Source: row.source, Mount: row.mount, FS: row.fs, Options: row.options, Boot: boot[row.mount]}
		if space, ok := df[row.mount]; ok {
			share.SizeKB, share.UsedKB, share.AvailKB = space[0], space[1], space[2]
		}
		info.Shares = append(info.Shares, share)
	}

	info.Memory = parseDiskMemory(sections["mem"])

	tools := map[string]bool{}
	for _, line := range sections["tools"] {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "tool="); ok {
			tools[name] = true
		}
	}
	for _, fs := range diskFormatSeq {
		if tools[diskFormatFS[fs]] {
			info.Formats = append(info.Formats, fs)
		}
	}
	kernel := map[string]bool{}
	for _, line := range sections["fs"] {
		if f := strings.Fields(line); len(f) > 0 {
			kernel[f[len(f)-1]] = true
		}
	}
	info.CIFS = tools["mount.cifs"] || kernel["cifs"]
	info.NFS = tools["mount.nfs"] || kernel["nfs"]
	info.Format = parseDiskFormatStatus(strings.Join(sections["fmt"], "\n") + "\n@@log\n" + strings.Join(sections["log"], "\n"))
	return info
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func fstabPattern(mount string) string {
	return strings.ReplaceAll(mount, ".", `\.`)
}

func diskCredName(mount string) string {
	return "/etc/.dcc-cred-" + strings.NewReplacer("/", "_", ".", "_").Replace(strings.Trim(mount, "/"))
}

type diskRequest struct {
	Action   string `json:"action"`
	Device   string `json:"device"`
	Mount    string `json:"mount"`
	FS       string `json:"fs"`
	Label    string `json:"label"`
	Confirm  string `json:"confirm"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Share    string `json:"share"`
	Username string `json:"username"`
	Password string `json:"password"`
	Domain   string `json:"domain"`
	Version  string `json:"version"`
	Options  string `json:"options"`
	Persist  bool   `json:"persist"`
	Force    bool   `json:"force"`
}

type diskError struct{ msg string }

func (e *diskError) Error() string { return e.msg }

func badDisk(msg string) error { return &diskError{msg} }

func diskMountPoint(mount string) error {
	if !diskMountRe.MatchString(mount) || strings.Contains(mount, "..") {
		return badDisk("Choose a folder under /media or /mnt, for example /media/hdd.")
	}
	return nil
}

func diskMountCommand(device, mount, fs string) (string, error) {
	if !diskDeviceRe.MatchString(device) {
		return "", badDisk("Unsupported device.")
	}
	if err := diskMountPoint(mount); err != nil {
		return "", err
	}
	if fs == "" {
		fs = "auto"
	}
	if !diskLocalFS[fs] {
		return "", badDisk("Unsupported file system.")
	}
	typeFlag := ""
	if fs != "auto" {
		typeFlag = "-t " + fs + " "
	}
	return `if grep -q '^` + device + ` ' /proc/mounts; then echo ALREADY; elif grep -q ' ` + fstabPattern(mount) + ` ' /proc/mounts; then echo BUSY; else ` +
		`mkdir -p ` + mount + ` && mount ` + typeFlag + device + ` ` + mount + ` 2>&1 | tail -n 4; ` +
		`grep -q ' ` + fstabPattern(mount) + ` ' /proc/mounts && echo MOUNTED || echo FAILED; fi`, nil
}

func diskUnmountCommand(mount string, force bool) (string, error) {
	if err := diskMountPoint(mount); err != nil {
		return "", err
	}
	flag := ""
	if force {
		flag = "-l "
	}
	return `if grep -q ' ` + fstabPattern(mount) + ` ' /proc/mounts; then sync; umount ` + flag + mount + ` 2>&1 | tail -n 4; ` +
		`grep -q ' ` + fstabPattern(mount) + ` ' /proc/mounts && echo STILL || echo UNMOUNTED; else echo NOTMOUNTED; fi`, nil
}

func diskFstabMountCommand(mount string) (string, error) {
	if err := diskMountPoint(mount); err != nil {
		return "", err
	}
	pat := fstabPattern(mount)
	return `if grep -q ' ` + pat + ` ' /proc/mounts; then echo ALREADY; elif ! grep -E -q '^[^#]*[[:space:]]` + pat + `[[:space:]]' /etc/fstab; then echo NOENTRY; else ` +
		`mount ` + mount + ` 2>&1 | tail -n 4; grep -q ' ` + pat + ` ' /proc/mounts && echo MOUNTED || echo FAILED; fi`, nil
}

func diskFstabRemoveCommand(mount string) (string, error) {
	if err := diskMountPoint(mount); err != nil {
		return "", err
	}
	pat := fstabPattern(mount)
	return `if grep -E -q '^[^#]*[[:space:]]` + pat + `[[:space:]]+(nfs4?|cifs)[[:space:]]' /etc/fstab 2>/dev/null; then cp /etc/fstab /etc/fstab.dcc-bak 2>/dev/null; ` +
		`sed -i -E '\@^[^#]*[[:space:]]` + pat + `[[:space:]]+(nfs4?|cifs)[[:space:]]@d' /etc/fstab && rm -f ` + diskCredName(mount) + ` && echo REMOVED || echo FSTABFAILED; else echo NOENTRY; fi`, nil
}

func diskFormatStartCommand(device, fs, label string) (string, error) {
	if !diskDeviceRe.MatchString(device) {
		return "", badDisk("Unsupported device.")
	}
	tool, ok := diskFormatFS[fs]
	if !ok {
		return "", badDisk("Unsupported file system.")
	}
	if !diskLabelRe.MatchString(label) {
		return "", badDisk("The name may use letters, digits, - and _ (16 characters at most).")
	}
	if fs == "vfat" {
		label = strings.ToUpper(label)
		if len(label) > 11 {
			label = label[:11]
		}
	}
	var mkfs string
	switch fs {
	case "ext4", "ext3":
		mkfs = tool + ` -F -q`
		if label != "" {
			mkfs += ` -L ` + label
		}
	case "vfat":
		mkfs = tool
		if label != "" {
			mkfs += ` -n ` + label
		}
	case "exfat":
		mkfs = tool + ` ` + device
		if label != "" {
			mkfs = `{ ` + tool + ` -L ` + label + ` ` + device + ` || ` + tool + ` -n ` + label + ` ` + device + `; }`
		}
		return diskFormatWrap(device, mkfs), nil
	case "ntfs":
		mkfs = tool + ` -f -Q`
		if label != "" {
			mkfs += ` -L ` + label
		}
	}
	return diskFormatWrap(device, mkfs+` `+device), nil
}

func diskFormatWrap(device, mkfs string) string {
	f := diskFormatFiles
	return `if grep -q '^` + device + ` ' /proc/mounts || grep -q '^` + device + ` ' /proc/swaps; then echo ISMOUNTED; ` +
		`elif [ ! -b ` + device + ` ]; then echo NODEVICE; ` +
		`elif [ ! -f ` + f + `.done ] && [ -f ` + f + `.log ] && ps 2>/dev/null | grep -q '[m]kfs'; then echo RUNNING; else ` +
		`rm -f ` + f + `.done ` + f + `.log; echo ` + device + ` > ` + f + `.dev; ` +
		`( trap '' HUP; ` + mkfs + ` >` + f + `.log 2>&1; echo $? >` + f + `.done; sync ) </dev/null >/dev/null 2>&1 & echo STARTED; fi`
}

const diskFormatStatusCommand = `f=` + diskFormatFiles + `; if [ -f $f.done ]; then echo "state=done"; echo "dev=$(cat $f.dev 2>/dev/null)"; echo "code=$(cat $f.done)"; echo "@@log"; tail -n 6 $f.log 2>/dev/null; ` +
	`elif [ -f $f.log ]; then if ps 2>/dev/null | grep -q '[m]kfs'; then echo "state=running"; echo "dev=$(cat $f.dev 2>/dev/null)"; else echo "state=lost"; echo "dev=$(cat $f.dev 2>/dev/null)"; fi; else echo "state=none"; fi`

type diskFormatState struct {
	State  string `json:"state"`
	Device string `json:"device,omitempty"`
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
}

func parseDiskFormatStatus(out string) diskFormatState {
	out = strings.ReplaceAll(out, "\r", "")
	head, log, _ := strings.Cut(out, "@@log")
	state := diskFormatState{State: "none"}
	code := ""
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "state="):
			state.State = strings.TrimPrefix(line, "state=")
		case strings.HasPrefix(line, "dev="):
			state.Device = strings.TrimPrefix(line, "dev=")
		case strings.HasPrefix(line, "code="):
			code = strings.TrimPrefix(line, "code=")
		}
	}
	if !diskDeviceRe.MatchString(state.Device) {
		state.Device = ""
	}
	if state.State == "done" {
		state.OK = code == "0"
		state.Output = cleanText(log, 500)
	}
	switch state.State {
	case "done", "running", "lost", "none":
	default:
		state.State = "none"
	}
	return state
}

func diskShareCommand(req diskRequest) (string, error) {
	if err := diskMountPoint(req.Mount); err != nil {
		return "", err
	}
	if !diskHostRe.MatchString(req.Host) {
		return "", badDisk("Enter the server name or IP address.")
	}
	if req.Options != "" && !diskOptionsRe.MatchString(req.Options) {
		return "", badDisk("The extra options contain characters that are not allowed.")
	}
	mount, pat := req.Mount, fstabPattern(req.Mount)
	var source, fs, opts, prepare, cleanup, saveLine string
	switch req.Kind {
	case "nfs":
		if !diskNFSPathRe.MatchString(req.Share) || strings.Contains(req.Share, "..") {
			return "", badDisk("Enter the exported folder, for example /export/movies.")
		}
		version := req.Version
		if version == "" {
			version = "auto"
		}
		if !diskNFSVerRe.MatchString(version) {
			return "", badDisk("Unsupported NFS version.")
		}
		source, fs = req.Host+":"+req.Share, "nfs"
		opts = "nolock,rsize=8192,wsize=8192,soft,timeo=14"
		if version != "auto" {
			opts += ",vers=" + version
		}
		if req.Options != "" {
			opts += "," + req.Options
		}
	case "cifs":
		if !diskCIFSRe.MatchString(req.Share) || strings.Contains(req.Share, "..") {
			return "", badDisk("Enter the share name, for example Movies.")
		}
		if !diskUserRe.MatchString(req.Username) || !diskUserRe.MatchString(req.Domain) {
			return "", badDisk("The user name contains characters that are not allowed.")
		}
		if len(req.Password) > 128 || strings.IndexFunc(req.Password, unicode.IsControl) >= 0 {
			return "", badDisk("The password is too long or contains control characters.")
		}
		version := req.Version
		if version == "" {
			version = "auto"
		}
		if !diskVersionRe.MatchString(version) {
			return "", badDisk("Unsupported SMB version.")
		}
		source, fs = "//"+req.Host+"/"+req.Share, "cifs"
		opts = "rw,file_mode=0777,dir_mode=0777"
		if version != "auto" {
			opts += ",vers=" + version
		}
		if req.Options != "" {
			opts += "," + req.Options
		}
		if req.Username == "" {
			opts = "guest," + opts
		} else {
			cred := diskCredName(mount)
			if !req.Persist {
				cred = "/tmp/.dcc-cred.$$"
				cleanup = `rm -f ` + cred + `; `
			}
			lines := `'username=%s\npassword=%s\n'`
			args := shellQuote(req.Username) + ` ` + shellQuote(req.Password)
			if req.Domain != "" {
				lines = `'username=%s\npassword=%s\ndomain=%s\n'`
				args += ` ` + shellQuote(req.Domain)
			}
			prepare = `( umask 077; printf ` + lines + ` ` + args + ` > ` + cred + ` ); `
			opts = "credentials=" + cred + "," + opts
		}
	default:
		return "", badDisk("Choose NFS or CIFS.")
	}

	if req.Persist {
		saveOpts := opts
		saveLine = fstabEscape(source) + " " + mount + " " + fs + " " + saveOpts + " 0 0"
	}
	mountLine := `mount -t ` + fs + ` ` + shellQuote(source) + ` ` + mount + ` -o ` + opts + ` 2>&1 | tail -n 4; `
	body := `mkdir -p ` + mount + ` && { ` + prepare + mountLine + cleanup + `}; ` +
		`if grep -q ' ` + pat + ` ' /proc/mounts; then echo MOUNTED; `
	if saveLine != "" {
		body += `cp /etc/fstab /etc/fstab.dcc-bak 2>/dev/null; sed -i -E '\@^[^#]*[[:space:]]` + pat + `[[:space:]]+(nfs4?|cifs)[[:space:]]@d' /etc/fstab; ` +
			`printf '%s\n' ` + shellQuote(saveLine) + ` >> /etc/fstab && echo SAVED || echo FSTABFAILED; `
	}
	body += `else echo FAILED; fi`

	guard := ``
	if req.Persist {
		guard = `elif grep -E -q '^[^#]*[[:space:]]` + pat + `[[:space:]]' /etc/fstab 2>/dev/null && ! grep -E -q '^[^#]*[[:space:]]` + pat + `[[:space:]]+(nfs4?|cifs)[[:space:]]' /etc/fstab; then echo FSTABCONFLICT; `
	}
	return `if grep -q ' ` + pat + ` ' /proc/mounts; then echo BUSY; ` + guard + `else ` + body + ` fi`, nil
}

func disksFetch(ctx context.Context, profile config.Profile) (disksInfo, error) {
	out, _, err := (shellRunner{profile}).run(ctx, 25*time.Second, disksInfoCommand, 256<<10)
	if err != nil {
		return disksInfo{}, err
	}
	return parseDisksInfo(out), nil
}

func (s *Server) disksInfo(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	info, err := disksFetch(ctx, profile)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func diskStatusOf(out string) string {
	for _, word := range []string{"STARTED", "MOUNTED", "UNMOUNTED", "NOTMOUNTED", "STILL", "ALREADY", "BUSY", "NOENTRY", "REMOVED", "SAVED", "FSTABFAILED", "FSTABCONFLICT", "RUNNING", "NODEVICE", "ISMOUNTED", "FAILED"} {
		for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
			if strings.TrimSpace(line) == word {
				if word == "MOUNTED" && strings.Contains(out, "SAVED") {
					return "SAVED"
				}
				if word == "MOUNTED" && strings.Contains(out, "FSTABFAILED") {
					return "FSTABFAILED"
				}
				return word
			}
		}
	}
	return ""
}

func diskOutputText(out string) string {
	var keep []string
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		switch line {
		case "", "STARTED", "MOUNTED", "UNMOUNTED", "NOTMOUNTED", "STILL", "ALREADY", "BUSY", "NOENTRY", "REMOVED", "SAVED", "FSTABFAILED", "FSTABCONFLICT", "RUNNING", "NODEVICE", "ISMOUNTED", "FAILED":
			continue
		}
		keep = append(keep, line)
	}
	return cleanText(strings.Join(keep, " "), 500)
}

var diskOKStatus = map[string]bool{"STARTED": true, "MOUNTED": true, "UNMOUNTED": true, "ALREADY": true, "REMOVED": true, "SAVED": true, "NOTMOUNTED": true}

func (s *Server) disksAction(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "disks action")
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var req diskRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()

	var cmd string
	var err error
	timeout := 60 * time.Second
	switch req.Action {
	case "mount":
		cmd, err = diskMountCommand(req.Device, req.Mount, req.FS)
	case "umount":
		cmd, err = diskUnmountCommand(req.Mount, req.Force)
	case "mount-fstab":
		cmd, err = diskFstabMountCommand(req.Mount)
	case "fstab-remove":
		cmd, err = diskFstabRemoveCommand(req.Mount)
	case "share-mount":
		cmd, err = diskShareCommand(req)
		timeout = 80 * time.Second
	case "format":
		cmd, err = s.diskFormatChecked(ctx, w, profile, req)
		if cmd == "" && err == nil {
			return
		}
	default:
		err = badDisk("Invalid request.")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, _, runErr := (shellRunner{profile}).run(ctx, timeout, cmd, 32<<10)
	if runErr != nil {
		telnetFailure(w, profile, runErr)
		return
	}
	status := diskStatusOf(out)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     diskOKStatus[status],
		"status": strings.ToLower(status),
		"output": diskOutputText(out),
	})
}

func (s *Server) diskFormatChecked(ctx context.Context, w http.ResponseWriter, profile config.Profile, req diskRequest) (string, error) {
	if !diskDeviceRe.MatchString(req.Device) {
		return "", badDisk("Unsupported device.")
	}
	if req.Confirm != path.Base(req.Device) {
		return "", badDisk("Type the device name to confirm.")
	}
	info, err := disksFetch(ctx, profile)
	if err != nil {
		telnetFailure(w, profile, err)
		return "", nil
	}
	var found *diskPart
	for i := range info.Disks {
		for j := range info.Disks[i].Parts {
			if info.Disks[i].Parts[j].Device == req.Device {
				found = &info.Disks[i].Parts[j]
			}
		}
	}
	switch {
	case found == nil:
		return "", badDisk("This partition was not found on the receiver.")
	case found.Mount != "" || found.Swap:
		return "", badDisk("Unmount the partition before formatting it.")
	case found.System:
		return "", badDisk("This partition is on the disk the receiver runs from. It is not formatted from here.")
	}
	available := false
	for _, fs := range info.Formats {
		if fs == req.FS {
			available = true
		}
	}
	if !available {
		return "", badDisk("This receiver has no tool to format as " + req.FS + ".")
	}
	return diskFormatStartCommand(req.Device, req.FS, req.Label)
}
