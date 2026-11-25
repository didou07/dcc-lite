package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"path"
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

type pageEntry struct{ raw, gz []byte }

var pages sync.Map

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(data)
	_ = zw.Close()
	return buf.Bytes()
}

func page(name string) (*pageEntry, error) {
	if v, ok := pages.Load(name); ok {
		return v.(*pageEntry), nil
	}
	data, err := files.ReadFile(name)
	if err != nil {
		return nil, err
	}
	raw := assetRef.ReplaceAll(data, []byte(`$1="$2?v=`+Build()+`"`))
	v, _ := pages.LoadOrStore(name, &pageEntry{raw: raw, gz: gzipBytes(raw)})
	return v.(*pageEntry), nil
}

func Page(name string) ([]byte, error) {
	e, err := page(name)
	if err != nil {
		return nil, err
	}
	return e.raw, nil
}

func PageGzip(name string) ([]byte, error) {
	e, err := page(name)
	if err != nil {
		return nil, err
	}
	return e.gz, nil
}

var compressible = map[string]bool{".js": true, ".css": true, ".html": true, ".svg": true, ".json": true, ".txt": true}

var assets sync.Map

func Gzipped(name string) []byte {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if !compressible[path.Ext(name)] {
		return nil
	}
	if v, ok := assets.Load(name); ok {
		b, _ := v.([]byte)
		return b
	}
	data, err := files.ReadFile(name)
	if err != nil {
		return nil
	}
	var out []byte
	if len(data) >= 1024 {
		out = gzipBytes(data)
	}
	assets.Store(name, out)
	return out
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
