package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

func cleanSettingsArchive(data []byte) ([]byte, int, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, 0, errors.New("The backup could not be read.")
	}
	tr := tar.NewReader(io.LimitReader(gz, settingsMaxArchive*4))
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	skipped := 0
	kept := 0
	var total int64
	for {
		hd, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, errors.New("The backup file is damaged.")
		}
		if skipped+kept >= settingsMaxEntries {
			return nil, 0, errors.New("The backup has too many files.")
		}
		name := strings.TrimPrefix(hd.Name, "./")
		clean := path.Clean(name)
		ok := name != "" && !strings.HasPrefix(name, "/") && clean == strings.TrimSuffix(name, "/") &&
			clean != ".." && !strings.HasPrefix(clean, "../") && settingsPathAllowed(clean) &&
			(hd.Typeflag == tar.TypeReg || hd.Typeflag == tar.TypeRegA || hd.Typeflag == tar.TypeDir)
		if ok {
			for _, part := range strings.Split(clean, "/") {
				if !utf8.ValidString(part) || !settingsEntryRe.MatchString(part) {
					ok = false
					break
				}
			}
		}
		if ok && hd.Typeflag != tar.TypeDir && hd.Size > restoreMaxFile {
			ok = false
		}
		if !ok {
			skipped++
			continue
		}
		if hd.Typeflag != tar.TypeDir {
			total += hd.Size
			if total > settingsMaxArchive*2 {
				return nil, 0, errors.New("The backup is too large.")
			}
		}
		h := &tar.Header{Name: clean, Typeflag: hd.Typeflag, Mode: hd.Mode, Uid: hd.Uid, Gid: hd.Gid, ModTime: hd.ModTime, Size: hd.Size}
		if hd.Typeflag == tar.TypeDir {
			h.Name = clean + "/"
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			return nil, 0, err
		}
		if hd.Typeflag != tar.TypeDir {
			if _, err := io.Copy(tw, tr); err != nil {
				return nil, 0, errors.New("The backup is damaged.")
			}
			kept++
		}
	}
	if err := tw.Close(); err != nil {
		return nil, 0, err
	}
	if err := zw.Close(); err != nil {
		return nil, 0, err
	}
	if kept == 0 {
		return nil, skipped, errNoSettingsFiles
	}
	return out.Bytes(), skipped, nil
}

type settingsEntry struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
	Size  int64  `json:"size"`
}

type settingsReview struct {
	Entries []settingsEntry
	Scopes  map[string]int
	Bytes   int64
}

func reviewSettingsArchive(data []byte) (settingsReview, error) {
	rev := settingsReview{Scopes: map[string]int{}}
	if len(data) == 0 {
		return rev, errors.New("Choose a backup file (.tar.gz).")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return rev, errors.New("This is not a DCC Lite backup (.tar.gz).")
	}
	tr := tar.NewReader(io.LimitReader(gz, settingsMaxArchive*4))
	for {
		hd, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return rev, errors.New("The backup file is damaged.")
		}
		if len(rev.Entries) >= settingsMaxEntries {
			return rev, errors.New("The backup has too many files.")
		}
		name := strings.TrimPrefix(hd.Name, "./")
		clean := path.Clean(name)
		if name == "" || strings.HasPrefix(name, "/") || clean != strings.TrimSuffix(name, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return rev, fmt.Errorf("Unsafe path: %s", cleanText(hd.Name, 60))
		}
		for _, part := range strings.Split(clean, "/") {
			if !utf8.ValidString(part) || !settingsEntryRe.MatchString(part) {
				return rev, fmt.Errorf("Unexpected file name: %s", cleanText(hd.Name, 60))
			}
		}
		switch hd.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if hd.Size > restoreMaxFile {
				return rev, fmt.Errorf("%s is too large.", cleanText(clean, 60))
			}
		case tar.TypeDir:
		default:
			return rev, fmt.Errorf("Links and special files are not accepted: %s", cleanText(clean, 60))
		}
		if !settingsPathAllowed(clean) {
			return rev, fmt.Errorf("File outside the backup folders: %s", cleanText(clean, 60))
		}
		if hd.Typeflag == tar.TypeDir {
			continue
		}
		rev.Bytes += hd.Size
		if rev.Bytes > settingsMaxArchive*2 {
			return rev, errors.New("The backup is too large.")
		}
		sc := settingsScopeOf(clean)
		rev.Scopes[sc]++
		rev.Entries = append(rev.Entries, settingsEntry{Name: clean, Scope: sc, Size: hd.Size})
	}
	if len(rev.Entries) == 0 {
		return rev, errors.New("The backup is empty.")
	}
	return rev, nil
}

func prepareSettingsUpload(upload []byte) ([]byte, int, error) {
	if len(upload) == 0 {
		return nil, 0, errors.New("Choose a backup file (.tar.gz).")
	}
	if _, err := gzip.NewReader(bytes.NewReader(upload)); err != nil {
		return nil, 0, errors.New("This is not a DCC Lite backup (.tar.gz).")
	}
	clean, skipped, err := cleanSettingsArchive(upload)
	if errors.Is(err, errNoSettingsFiles) {
		return nil, skipped, errors.New("The backup has no files that can be restored.")
	}
	return clean, skipped, err
}
