// Package app is the application object shared by the HTTP handlers: the version,
// system information and the saved device profiles.
package app

import (
	"runtime"

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
	// RemoteAccess manages the optional HTTPS listener (set by the executable).
	RemoteAccess RemoteController
}

func New(version string, profiles *config.ProfileStore, _ ...string) *App {
	localFiles, _ := filetransfer.NewLocalFS()
	return &App{
		Version:     version,
		Profiles:    profiles,
		Connections: connection.NewManager(connection.NewTCPChecker()),
		Terminals:   terminal.NewManager(),
		Files:       filetransfer.NewManager(),
		LocalFiles:  localFiles,
	}
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
