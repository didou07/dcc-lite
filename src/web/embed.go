package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"regexp"
	"sort"
	"sync"
)

//go:embed index.html login.html login.js app.css js vendor
var files embed.FS

func FS() fs.FS { return files }

var (
	buildOnce sync.Once
	buildID   string
)

func Build() string {
	buildOnce.Do(func() {
		var names []string
		_ = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				names = append(names, path)
			}
			return nil
		})
		sort.Strings(names)
		h := sha256.New()
		for _, name := range names {
			data, err := files.ReadFile(name)
			if err != nil {
				continue
			}
			h.Write([]byte(name))
			h.Write([]byte{0})
			h.Write(data)
			h.Write([]byte{0})
		}
		buildID = hex.EncodeToString(h.Sum(nil))[:12]
	})
	return buildID
}

var assetRef = regexp.MustCompile(`(src|href)="(/(?:js/|vendor/|app\.css|login\.js)[^"?#]*)"`)

func Page(name string) ([]byte, error) {
	data, err := files.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return assetRef.ReplaceAll(data, []byte(`$1="$2?v=`+Build()+`"`)), nil
}
