package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/filetransfer"
	"github.com/didou07/dcc-lite/src/terminal"
)

const (
	dreamOSCaptureCommandTimeout = 20 * time.Second
	dreamOSCaptureFTPTimeout     = 20 * time.Second
	dreamOSCaptureCleanupTimeout = 2 * time.Second
)

func dreamOSCaptureMode(mode string) (string, error) {
	switch mode {
	case "all":
		return "combined", nil
	case "video", "osd":
		return mode, nil
	default:
		return "", errors.New("unsupported DreamOS screenshot mode")
	}
}

func dreamOSCaptureDimensions(width int) (int, int) {
	resolution := dreamOSResolution(width)
	parts := strings.SplitN(resolution, "x", 2)
	if len(parts) != 2 {
		return width, width * 9 / 16
	}
	height, err := strconv.Atoi(parts[1])
	if err != nil || height < 1 {
		return width, width * 9 / 16
	}
	return width, height
}

func dreamOSCapturePath() (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "/tmp/.dcc-lite-screen-" + hex.EncodeToString(random[:]) + ".jpg", nil
}

func validDreamOSCapturePath(remotePath string) bool {
	const prefix = "/tmp/.dcc-lite-screen-"
	const suffix = ".jpg"
	if len(remotePath) != len(prefix)+24+len(suffix) || !strings.HasPrefix(remotePath, prefix) || !strings.HasSuffix(remotePath, suffix) {
		return false
	}
	for _, r := range remotePath[len(prefix) : len(remotePath)-len(suffix)] {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func dreamOSCaptureCommand(remotePath, mode string, width int) (string, error) {
	captureMode, err := dreamOSCaptureMode(mode)
	if err != nil {
		return "", err
	}
	if width < 1 || width > 3840 {
		return "", errors.New("invalid screenshot width")
	}
	imageWidth, imageHeight := dreamOSCaptureDimensions(width)
	if imageWidth < 1 || imageHeight < 1 {
		return "", errors.New("invalid screenshot dimensions")
	}
	if !validDreamOSCapturePath(remotePath) {
		return "", errors.New("invalid temporary screenshot path")
	}

	quoted := "'" + remotePath + "'"
	dreamboxctlFallback := `"$dbctl" screenshot -f ` + quoted
	if captureMode != "combined" {
		dreamboxctlFallback += " -m " + captureMode
	}
	grabCommand := `"$grabBin" -j 90 -r "$grabWidth" ` + quoted
	if captureMode == "osd" {
		grabCommand = `"$grabBin" -o -j 90 -r "$grabWidth" ` + quoted
	} else if captureMode == "video" {
		grabCommand = `"$grabBin" -v -j 90 -r "$grabWidth" ` + quoted
	}

	dreamboxctlCommand := `"$dbctl" screenshot -f ` + quoted + " -m " + captureMode + " -iw " + strconv.Itoa(imageWidth) + " -ih " + strconv.Itoa(imageHeight)
	dreamboxctlBlock := "if [ -n \"$dbctl\" ]; then " + dreamboxctlCommand + "; rc=$?; if [ $rc -ne 0 ] || [ ! -s " + quoted + " ]; then rm -f " + quoted + "; " + dreamboxctlFallback + "; fi; fi"
	grabBlock := "if [ ! -s " + quoted + " ] && [ -n \"$grabBin\" ]; then rm -f " + quoted + "; grabWidth=" + strconv.Itoa(min(width, 1920)) + "; " + grabCommand + "; fi"
	parts := []string{
		"umask 077",
		"rm -f " + quoted,
		"dbctl=",
		"if command -v dreamboxctl >/dev/null 2>&1; then dbctl=dreamboxctl; elif [ -x /usr/bin/dreamboxctl ]; then dbctl=/usr/bin/dreamboxctl; elif command -v dreamboxctl-one >/dev/null 2>&1; then dbctl=dreamboxctl-one; elif [ -x /usr/bin/dreamboxctl-one ]; then dbctl=/usr/bin/dreamboxctl-one; fi",
		dreamboxctlBlock,
		"grabBin=",
		"if command -v grab >/dev/null 2>&1; then grabBin=grab; elif [ -x /usr/bin/grab ]; then grabBin=/usr/bin/grab; fi",
		grabBlock,
		"test -s " + quoted,
	}
	return strings.Join(parts, "; "), nil
}

func dreamOSCaptureCleanupCommand(remotePath string) string {
	if !validDreamOSCapturePath(remotePath) {
		return ""
	}
	return "rm -f '" + remotePath + "'"
}

func captureDreamOSWithCommand(ctx context.Context, web deviceWeb, mode string, width int) ([]byte, error) {
	remotePath, err := dreamOSCapturePath()
	if err != nil {
		return nil, fmt.Errorf("create screenshot name: %w", err)
	}
	command, err := dreamOSCaptureCommand(remotePath, mode, width)
	if err != nil {
		return nil, err
	}

	user := strings.TrimSpace(web.user)
	if user == "" {
		user = config.DefaultUsername
	}

	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), dreamOSCaptureCleanupTimeout)
		defer cleanupCancel()
		_, _, _ = terminal.RunCommand(cleanupCtx, web.host, web.telnetPort, user, web.pass, dreamOSCaptureCleanupCommand(remotePath), 1024)
	}()

	commandCtx, cancelCommand := context.WithTimeout(ctx, dreamOSCaptureCommandTimeout)
	output, exitCode, commandErr := terminal.RunCommand(commandCtx, web.host, web.telnetPort, user, web.pass, command, 1<<20)
	cancelCommand()
	if commandErr != nil {
		return nil, fmt.Errorf("DreamOS screenshot command: %w", commandErr)
	}
	if exitCode != 0 {
		message := strings.TrimSpace(output)
		if message == "" {
			message = "no supported screenshot tool created an image"
		}
		return nil, fmt.Errorf("DreamOS screenshot command failed: %s", cleanText(message, 240))
	}

	localDir, err := os.MkdirTemp("", "dcclite-screen-")
	if err != nil {
		return nil, fmt.Errorf("create local screenshot directory: %w", err)
	}
	defer os.RemoveAll(localDir)
	localPath := filepath.Join(localDir, "capture.jpg")

	ftpCtx, cancelFTP := context.WithTimeout(ctx, dreamOSCaptureFTPTimeout)
	defer cancelFTP()
	ftp, err := filetransfer.OpenFTP(ftpCtx, filetransfer.FTPOptions{
		Host:     web.host,
		Port:     web.ftpPort,
		Username: user,
		Password: web.pass,
	})
	if err != nil {
		return nil, fmt.Errorf("retrieve DreamOS screenshot over FTP: %w", err)
	}
	defer ftp.Close()
	remoteSize, err := ftp.Size(ftpCtx, remotePath)
	if err != nil {
		return nil, fmt.Errorf("check DreamOS screenshot size over FTP: %w", err)
	}
	if remoteSize < 1 || remoteSize > maxScreenBytes {
		return nil, errors.New("DreamOS screenshot size is outside the allowed range")
	}
	if err := ftp.Download(ftpCtx, remotePath, localPath, nil); err != nil {
		return nil, fmt.Errorf("retrieve DreamOS screenshot over FTP: %w", err)
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return nil, fmt.Errorf("inspect downloaded screenshot: %w", err)
	}
	if info.Size() < 1 || info.Size() > maxScreenBytes {
		return nil, fmt.Errorf("DreamOS screenshot size is outside the allowed range")
	}
	body, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("read downloaded screenshot: %w", err)
	}
	if !isPicture(body) {
		return nil, errors.New("the DreamOS screenshot tool returned an invalid image file")
	}
	return body, nil
}
