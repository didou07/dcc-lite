package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html login.html login.js app.css js vendor
var files embed.FS

func FS() fs.FS { return files }
