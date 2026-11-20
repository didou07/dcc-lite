package main

import "github.com/didou07/dcc-lite/src/app"

type nativeDeps struct {
	App     *app.App
	URL     string // the local address the interface is served on
	DataDir string
}
