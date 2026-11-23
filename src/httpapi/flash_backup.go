package httpapi

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/filetransfer"
	"github.com/didou07/dcc-lite/src/terminal"
)

// Flash backup is read-only. The receiver copies one flash partition into /tmp (RAM), the copy is
// downloaded over FTP, its checksum is compared, and the temporary file is deleted. Nothing is ever
// written to the flash itself: restoring a partition is deliberately not offered, because a wrong or
// interrupted write can leave a receiver that no longer starts.
const (
	flashMaxBytes     = 128 << 20
	flashSpareBytes   = 8 << 20 // memory that must stay free besides the copy
	flashDumpTimeout  = 10 * time.Minute
	flashFTPTimeout   = 20 * time.Minute
	flashCleanupLimit = 15 * time.Second
)

type flashPartition struct {
	Index     int    `json:"index"`
	Device    string `json:"device"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	EraseSize int64  `json:"erase_size"`
}

var procMTDLine = regexp.MustCompile(`^mtd(\d{1,2}):\s+([0-9a-fA-F]+)\s+([0-9a-fA-F]+)\s+"([^"]*)"\s*$`)

// parseProcMTD reads the output of `cat /proc/mtd`.
func parseProcMTD(out string) []flashPartition {
	var list []flashPartition
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		m := procMTDLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		index, _ := strconv.Atoi(m[1])
		size, err1 := strconv.ParseInt(m[2], 16, 64)
		erase, err2 := strconv.ParseInt(m[3], 16, 64)
		if err1 != nil || err2 != nil || size <= 0 {
			continue
		}
		list = append(list, flashPartition{Index: index, Device: "mtd" + m[1], Name: cleanText(m[4], 40), Size: size, EraseSize: erase})
	}
	return list
}

// parseFreeKB reads the free space from `df -k /tmp`. The line can wrap when the device name is long, so
// all fields after the header are joined and the figure is taken from the end.
func parseFreeKB(out string) (int64, bool) {
	lines := strings.Split(strings.ReplaceAll(out, "\r", ""), "\n")
	var fields []string
	for i, line := range lines {
		if i == 0 && strings.HasPrefix(strings.TrimSpace(line), "Filesystem") {
			continue
		}
		fields = append(fields, strings.Fields(line)...)
	}
	if len(fields) < 6 {
		return 0, false
	}
	free, err := strconv.ParseInt(fields[len(fields)-3], 10, 64)
	if err != nil || free < 0 {
		return 0, false
	}
	return free, true
}

func parseMD5(out string) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 1 && len(f[0]) == 32 {
			if _, err := hex.DecodeString(f[0]); err == nil {
				return strings.ToLower(f[0])
			}
		}
	}
	return ""
}

func flashTempPath() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "/tmp/dcc-flash-" + hex.EncodeToString(b[:]) + ".bin", nil
}

func validFlashTempPath(p string) bool {
	const prefix, suffix = "/tmp/dcc-flash-", ".bin"
	if len(p) != len(prefix)+16+len(suffix) || !strings.HasPrefix(p, prefix) || !strings.HasSuffix(p, suffix) {
		return false
	}
	_, err := hex.DecodeString(p[len(prefix) : len(p)-len(suffix)])
	return err == nil
}

// flashDumpCommand copies partition index (a number, never text from the request) into remote.
func flashDumpCommand(index int, remote string) (string, error) {
	if index < 0 || index > 31 || !validFlashTempPath(remote) {
		return "", errors.New("invalid flash backup request")
	}
	n := strconv.Itoa(index)
	return "umask 077; src=/dev/mtd/" + n + "; [ -e \"$src\" ] || src=/dev/mtd" + n +
		"; [ -e \"$src\" ] || { echo 'no device node for this partition'; exit 3; }; cat \"$src\" > '" + remote + "'", nil
}

var flashBusy sync.Map

// denyFlashRemote keeps flash images off remote sessions. A flash image holds the receiver's
// configuration, including saved passwords, so it is only offered on this computer.
func (s *Server) denyFlashRemote(w http.ResponseWriter) bool {
	if s.remote == nil {
		return false
	}
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": "Flash backups are only available on the computer that runs DCC Lite, not in a remote session.",
		"code":  "flash_unavailable_remote",
	})
	return true
}

func (s *Server) flashPartitions(w http.ResponseWriter, r *http.Request) {
	if s.denyFlashRemote(w) {
		return
	}
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	list, ok := readFlashPartitions(ctx, w, profile)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"partitions": list, "max_bytes": flashMaxBytes})
}

func readFlashPartitions(ctx context.Context, w http.ResponseWriter, profile config.Profile) ([]flashPartition, bool) {
	out, _, err := terminal.RunCommand(ctx, profile.Host, profile.EffectiveTelnetPort(), telnetUser(profile), profile.Password, "cat /proc/mtd", 64<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return nil, false
	}
	list := parseProcMTD(out)
	if len(list) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "This receiver lists no flash partitions (/proc/mtd is empty). It may boot from a disk or card instead.", "code": "no_flash"})
		return nil, false
	}
	return list, true
}

func (s *Server) flashBackup(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "flash partition backup requested")
	if s.denyFlashRemote(w) {
		return
	}
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	index, err := strconv.Atoi(r.URL.Query().Get("partition"))
	if err != nil || index < 0 || index > 31 {
		writeError(w, http.StatusBadRequest, "choose a partition number")
		return
	}
	if _, busy := flashBusy.LoadOrStore(profile.ID, true); busy {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "A flash backup of this receiver is already running.", "code": "busy"})
		return
	}
	defer flashBusy.Delete(profile.ID)
	longTransfer(w)

	ctx, cancel := context.WithTimeout(r.Context(), flashDumpTimeout+flashFTPTimeout)
	defer cancel()
	list, ok := readFlashPartitions(ctx, w, profile)
	if !ok {
		return
	}
	var part *flashPartition
	for i := range list {
		if list[i].Index == index {
			part = &list[i]
		}
	}
	if part == nil {
		writeError(w, http.StatusNotFound, "the receiver has no partition mtd"+strconv.Itoa(index))
		return
	}
	if part.Size > flashMaxBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": fmt.Sprintf("This partition is %d MB, more than the %d MB a backup can hold.", part.Size>>20, flashMaxBytes>>20), "code": "too_large"})
		return
	}

	user, host := telnetUser(profile), profile.Host
	port, pass := profile.EffectiveTelnetPort(), profile.Password
	run := func(timeout time.Duration, command string, max int) (string, int, error) {
		c, cancelRun := context.WithTimeout(ctx, timeout)
		defer cancelRun()
		return terminal.RunCommand(c, host, port, user, pass, command, max)
	}

	// The copy lives in RAM, so make sure the receiver can hold it and still run.
	out, _, err := run(20*time.Second, "df -k /tmp", 16<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	if free, known := parseFreeKB(out); known && free*1024 < part.Size+flashSpareBytes {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": fmt.Sprintf("The receiver has only %d MB free in /tmp, not enough to copy this %d MB partition safely.", free>>10, part.Size>>20),
			"code":  "not_enough_memory",
		})
		return
	}

	remote, err := flashTempPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not name the temporary file")
		return
	}
	defer func() {
		if validFlashTempPath(remote) {
			c, cancelClean := context.WithTimeout(context.Background(), flashCleanupLimit)
			defer cancelClean()
			_, _, _ = terminal.RunCommand(c, host, port, user, pass, "rm -f '"+remote+"'", 1024)
		}
	}()

	command, err := flashDumpCommand(index, remote)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, code, err := run(flashDumpTimeout, command, 16<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	if code != 0 {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver could not read the partition: " + cleanText(out, 200), "code": "dump_failed"})
		return
	}
	remoteSum := ""
	if sumOut, sumCode, sumErr := run(5*time.Minute, "md5sum '"+remote+"'", 4<<10); sumErr == nil && sumCode == 0 {
		remoteSum = parseMD5(sumOut)
	}

	localDir, err := os.MkdirTemp("", "dcclite-flash-")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a temporary folder")
		return
	}
	defer os.RemoveAll(localDir)
	local := filepath.Join(localDir, "partition.bin")

	ftpCtx, cancelFTP := context.WithTimeout(ctx, flashFTPTimeout)
	defer cancelFTP()
	ftp, err := filetransfer.OpenFTP(ftpCtx, filetransfer.FTPOptions{Host: host, Port: profile.EffectiveFTPPort(), Username: user, Password: pass})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "Could not open FTP to fetch the copy: " + cleanText(err.Error(), 160), "code": "ftp_failed"})
		return
	}
	defer ftp.Close()
	size, err := ftp.Size(ftpCtx, remote)
	if err != nil || size != part.Size {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("The copy on the receiver has the wrong size (%d bytes, expected %d). Nothing was saved.", size, part.Size), "code": "size_mismatch"})
		return
	}
	if err := ftp.Download(ftpCtx, remote, local, nil); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The download was interrupted: " + cleanText(err.Error(), 160), "code": "ftp_failed"})
		return
	}
	sum, localSize, err := fileMD5(local)
	if err != nil || localSize != part.Size {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The downloaded copy is incomplete. Nothing was saved.", "code": "size_mismatch"})
		return
	}
	verified := remoteSum != ""
	if verified && sum != remoteSum {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The downloaded copy does not match the receiver's checksum. Nothing was saved; try again.", "code": "checksum_mismatch"})
		return
	}

	file, err := os.Open(local)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the downloaded copy")
		return
	}
	defer file.Close()
	name := fmt.Sprintf("flash-mtd%d-%s-%s-%s.bin", part.Index, safeFileWord(part.Name), backupHostLabel(profile.Host), time.Now().Format("20060102-1504"))
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("Content-Length", strconv.FormatInt(localSize, 10))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Flash-MD5", sum)
	h.Set("X-Flash-Verified", strconv.FormatBool(verified))
	h.Set("Access-Control-Expose-Headers", "Content-Disposition, X-Flash-MD5, X-Flash-Verified")
	_, _ = io.Copy(w, file)
}

func fileMD5(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := md5.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

var unsafeFileWord = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeFileWord(s string) string {
	s = strings.Trim(unsafeFileWord.ReplaceAllString(s, "-"), "-.")
	if s == "" {
		return "partition"
	}
	if len(s) > 24 {
		s = s[:24]
	}
	return s
}
