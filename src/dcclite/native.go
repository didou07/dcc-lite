package main

import "github.com/didou07/dcc-lite/src/app"

// nativeDeps is what the Windows window needs from the running program.
type nativeDeps struct {
	App     *app.App
	URL     string // the local address the interface is served on
	DataDir string
}
