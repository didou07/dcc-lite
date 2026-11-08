// Package app is the application object shared by the HTTP handlers: the version,
// system information and the saved device profiles.
package app

import (
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/connection"
	"github.com/didou07/dcc-lite/src/filetransfer"
	"github.com/didou07/dcc-lite/src/terminal"
)

// App wires the three things DCC Lite does: keep a list of devices, move files
// over FTP, open Telnet terminals and watch the live screen.
type App struct {
	Version     string
	Profiles    *config.ProfileStore
	Connections *connection.Manager
	Terminals   *terminal.Manager
	Files       *filetransfer.Manager
	LocalFiles  *filetransfer.LocalFS
	// Staging is the private folder that carries files between the user's
	// browser and the receiver (see filetransfer/staging.go).
	Staging *filetransfer.LocalFS
	// RemoteAccess manages the optional HTTPS listener (set by the executable).
	RemoteAccess RemoteController
}

func New(version string, profiles *config.ProfileStore, dirs ...string) *App {
	localFiles, _ := filetransfer.NewLocalFS()
	files := filetransfer.NewManager()
	staging := newStaging(dirs)
	if staging != nil {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for now := range ticker.C {
				files.SweepStaged(staging, now)
			}
		}()
	}
	return &App{
		Version:     version,
		Profiles:    profiles,
		Connections: connection.NewManager(connection.NewTCPChecker()),
		Terminals:   terminal.NewManager(),
		Files:       files,
		LocalFiles:  localFiles,
		Staging:     staging,
	}
}

// newStaging prepares an empty staging folder inside the data directory (or the
// system temp folder when none was given). Whatever a previous run left in it is
// removed: no transfer survives a restart, so nothing there is needed.
func newStaging(dirs []string) *filetransfer.LocalFS {
	var dir string
	if len(dirs) > 0 && dirs[0] != "" {
		dir = filepath.Join(dirs[0], "staging")
		_ = os.RemoveAll(dir)
	} else {
		// No data directory: use a private folder of our own so that several
		// instances never delete each other's files.
		made, err := os.MkdirTemp("", "dcclite-staging-")
		if err != nil {
			return nil
		}
		dir = made
	}
	staging, err := filetransfer.NewLocalFSAt(dir)
	if err != nil {
		return nil
	}
	return staging
}

func (a *App) SystemInfo() map[string]string {
	return map[string]string{
		"application":  "DCC Lite",
		"version":      a.Version,
		"os":           runtime.GOOS,
		"architecture": runtime.GOARCH,
		"go_version":   runtime.Version(),
	}
}
