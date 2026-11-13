package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/connection"
	"github.com/didou07/dcc-lite/src/filetransfer"
	"github.com/didou07/dcc-lite/src/logx"
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

	backgroundCancel context.CancelFunc
	backgroundDone   chan struct{}
	closeOnce        sync.Once
}

func New(version string, profiles *config.ProfileStore, dirs ...string) *App {
	localFiles, _ := filetransfer.NewLocalFS()
	files := filetransfer.NewManager()
	staging := newStaging(dirs)
	a := &App{
		Version:     version,
		Profiles:    profiles,
		Connections: connection.NewManager(connection.NewTCPChecker()),
		Terminals:   terminal.NewManager(),
		Files:       files,
		LocalFiles:  localFiles,
		Staging:     staging,
	}
	if staging != nil {
		ctx, cancel := context.WithCancel(context.Background())
		a.backgroundCancel = cancel
		a.backgroundDone = make(chan struct{})
		logx.Go(logx.For("app"), "staging sweeper", func() {
			defer close(a.backgroundDone)
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-ticker.C:
					files.SweepStaged(staging, now)
				}
			}
		})
	}
	return a
}

func (a *App) Close() {
	if a == nil {
		return
	}
	a.closeOnce.Do(func() {
		if a.backgroundCancel != nil {
			a.backgroundCancel()
			<-a.backgroundDone
		}
		if a.Terminals != nil {
			a.Terminals.CloseAll()
		}
		if a.Files != nil {
			a.Files.CloseAll()
		}
	})
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
