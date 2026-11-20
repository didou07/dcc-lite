package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed index.html login.html login.js app.css js vendor boxes assets
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

var assetRef = regexp.MustCompile(`(src|href)="(/(?:js/|vendor/|assets/|app\.css|login\.js)[^"?#]*)"`)

func Page(name string) ([]byte, error) {
	data, err := files.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return assetRef.ReplaceAll(data, []byte(`$1="$2?v=`+Build()+`"`)), nil
}

var (
	boxOnce  sync.Once
	boxFiles map[string]string
)

func boxKey(name string) string {
	name = strings.NewReplacer("+", "plus", "\u00b2", "2").Replace(strings.ToLower(name))
	var b strings.Builder
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// Box returns the bundled picture of a receiver model, matching names without regard to…
func Box(name string) ([]byte, bool) {
	boxOnce.Do(func() {
		boxFiles = map[string]string{}
		entries, _ := fs.ReadDir(files, "boxes")
		for _, e := range entries {
			if n := e.Name(); strings.HasSuffix(n, ".webp") {
				boxFiles[boxKey(strings.TrimSuffix(n, ".webp"))] = "boxes/" + n
			}
		}
	})
	key := boxKey(name)
	path, ok := boxFiles[key]
	if key == "" || !ok {
		return nil, false
	}
	data, err := files.ReadFile(path)
	return data, err == nil
}
