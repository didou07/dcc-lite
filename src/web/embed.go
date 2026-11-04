// Package web holds the browser interface (HTML, CSS, JavaScript) and embeds it in the executable.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html login.html login.js app.css js vendor
var files embed.FS

// FS returns the interface files, rooted at the folder that holds index.html.
func FS() fs.FS { return files }
