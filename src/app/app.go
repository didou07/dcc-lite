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

type App struct {
	Version     string
	Profiles    *config.ProfileStore
	Connections *connection.Manager
	Terminals   *terminal.Manager
	Files       *filetransfer.Manager
	LocalFiles  *filetransfer.LocalFS

	Staging *filetransfer.LocalFS

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

func newStaging(dirs []string) *filetransfer.LocalFS {
	var dir string
	if len(dirs) > 0 && dirs[0] != "" {
		dir = filepath.Join(dirs[0], "staging")
		_ = os.RemoveAll(dir)
	} else {

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
