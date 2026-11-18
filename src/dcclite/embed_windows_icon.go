//go:build ignore

// Embeds a .ico file into a built Windows executable as its application icon.
// Usage: go run src/dcclite/embed_windows_icon.go DCCLite.exe packaging/DCCLite.ico
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
)

var le = binary.LittleEndian

func align(v, boundary uint32) uint32 { return (v + boundary - 1) / boundary * boundary }

type icoImage struct {
	width, height, colors byte
	planes, bpp           uint16
	data                  []byte
}

func readICO(path string) ([]icoImage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 6 {
		return nil, errors.New("icon file is truncated")
	}
	if le.Uint16(data) != 0 || le.Uint16(data[2:]) != 1 || le.Uint16(data[4:]) == 0 {
		return nil, errors.New("expected a non-empty Windows .ico file")
	}
	count := int(le.Uint16(data[4:]))
	var images []icoImage
	for i := 0; i < count; i++ {
		off := 6 + i*16
		if off+16 > len(data) {
			return nil, errors.New("icon directory is truncated")
		}
		e := data[off : off+16]
		size, imgOff := int(le.Uint32(e[8:])), int(le.Uint32(e[12:]))
		if size == 0 || imgOff+size > len(data) {
			return nil, errors.New("icon image data is missing")
		}
		images = append(images, icoImage{e[0], e[1], e[2], le.Uint16(e[4:]), le.Uint16(e[6:]), data[imgOff : imgOff+size]})
	}
	return images, nil
}

type resEntry struct {
	id  uint32
	dir *resDir
	dat *resData
}
type resDir struct {
	entries []resEntry
	offset  int
}
type resData struct {
	payload                    []byte
	entryOffset, payloadOffset int
}

func makeResourceSection(icon string, sectionRVA uint32) ([]byte, error) {
	images, err := readICO(icon)
	if err != nil {
		return nil, err
	}
	const lang = 0x0409
	var iconNodes []resEntry
	var iconDatas []*resData
	for i, img := range images {
		d := &resData{payload: img.data}
		iconDatas = append(iconDatas, d)
		iconNodes = append(iconNodes, resEntry{id: uint32(i + 1), dir: &resDir{entries: []resEntry{{id: lang, dat: d}}}})
	}
	group := make([]byte, 6, 6+14*len(images))
	le.PutUint16(group[2:], 1)
	le.PutUint16(group[4:], uint16(len(images)))
	for i, img := range images {
		e := make([]byte, 14)
		e[0], e[1], e[2] = img.width, img.height, img.colors
		le.PutUint16(e[4:], img.planes)
		le.PutUint16(e[6:], img.bpp)
		le.PutUint32(e[8:], uint32(len(img.data)))
		le.PutUint16(e[12:], uint16(i+1))
		group = append(group, e...)
	}
	groupData := &resData{payload: group}
	groupLang := &resDir{entries: []resEntry{{id: lang, dat: groupData}}}
	iconType := &resDir{entries: iconNodes}
	groupType := &resDir{entries: []resEntry{{id: 1, dir: groupLang}}}
	root := &resDir{entries: []resEntry{{id: 3, dir: iconType}, {id: 14, dir: groupType}}}

	var dirs []*resDir
	size := func() int {
		n := 0
		for _, d := range dirs {
			n += 16 + 8*len(d.entries)
		}
		return n
	}
	var reserve func(*resDir)
	reserve = func(n *resDir) {
		n.offset = size()
		dirs = append(dirs, n)
		for _, e := range n.entries {
			if e.dir != nil {
				reserve(e.dir)
			}
		}
	}
	reserve(root)
	buf := make([]byte, size())

	dataNodes := append(append([]*resData{}, iconDatas...), groupData)
	for _, d := range dataNodes {
		d.entryOffset = len(buf)
		buf = append(buf, make([]byte, 16)...)
	}
	for _, d := range dataNodes {
		for len(buf)%4 != 0 {
			buf = append(buf, 0)
		}
		d.payloadOffset = len(buf)
		buf = append(buf, d.payload...)
	}
	for _, n := range dirs {
		le.PutUint16(buf[n.offset+14:], uint16(len(n.entries)))
		entries := append([]resEntry{}, n.entries...)
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
		pos := n.offset + 16
		for _, e := range entries {
			target := uint32(0)
			if e.dir != nil {
				target = uint32(e.dir.offset) | 0x80000000
			} else {
				target = uint32(e.dat.entryOffset)
			}
			le.PutUint32(buf[pos:], e.id&0xFFFF)
			le.PutUint32(buf[pos+4:], target)
			pos += 8
		}
	}
	for _, d := range dataNodes {
		le.PutUint32(buf[d.entryOffset:], sectionRVA+uint32(d.payloadOffset))
		le.PutUint32(buf[d.entryOffset+4:], uint32(len(d.payload)))
	}
	return buf, nil
}

func patchPE(exe, icon string) error {
	image, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	if len(image) < 0x40 || !bytes.HasPrefix(image, []byte("MZ")) {
		return errors.New("input is not a Windows PE executable")
	}
	peOff := int(le.Uint32(image[0x3C:]))
	if peOff+24 > len(image) || !bytes.Equal(image[peOff:peOff+4], []byte("PE\x00\x00")) {
		return errors.New("invalid PE signature")
	}
	coff := peOff + 4
	sectionCount := int(le.Uint16(image[coff+2:]))
	optionalSize := int(le.Uint16(image[coff+16:]))
	optional := coff + 20
	var directoryBase, numberDirsOffset int
	switch magic := le.Uint16(image[optional:]); magic {
	case 0x10B:
		directoryBase, numberDirsOffset = optional+96, optional+92
	case 0x20B:
		directoryBase, numberDirsOffset = optional+112, optional+108
	default:
		return fmt.Errorf("unsupported PE optional-header magic 0x%04x", magic)
	}
	sectionAlignment := le.Uint32(image[optional+32:])
	fileAlignment := le.Uint32(image[optional+36:])
	sizeOfImageOffset := optional + 56
	sizeOfHeaders := le.Uint32(image[optional+60:])
	sizeInitializedOffset := optional + 8
	if le.Uint32(image[numberDirsOffset:]) < 3 {
		return errors.New("PE optional header has no resource directory entry")
	}
	sectionTable := optional + optionalSize
	if sectionTable+sectionCount*40 > len(image) {
		return errors.New("truncated PE section table")
	}
	firstRaw := sizeOfHeaders
	first := true
	var maxVirtualEnd uint32
	for i := 0; i < sectionCount; i++ {
		off := sectionTable + i*40
		name := image[off : off+8]
		if n := bytes.IndexByte(name, 0); n >= 0 {
			name = name[:n]
		}
		if string(name) == ".rsrc" {
			return errors.New("the executable already contains a .rsrc section; refusing to overwrite it")
		}
		vs, va, rawSize, rawPtr := le.Uint32(image[off+8:]), le.Uint32(image[off+12:]), le.Uint32(image[off+16:]), le.Uint32(image[off+20:])
		if rawSize != 0 && rawPtr != 0 && (first || rawPtr < firstRaw) {
			firstRaw, first = rawPtr, false
		}
		if end := va + max(vs, rawSize); end > maxVirtualEnd {
			maxVirtualEnd = end
		}
	}
	if maxVirtualEnd == 0 {
		maxVirtualEnd = sizeOfHeaders
	}
	newHeader := sectionTable + sectionCount*40
	if uint32(newHeader+40) > min(firstRaw, sizeOfHeaders) {
		return errors.New("not enough room in the PE headers for a new resource section")
	}
	sectionRVA := align(maxVirtualEnd, sectionAlignment)
	res, err := makeResourceSection(icon, sectionRVA)
	if err != nil {
		return err
	}
	rawSize := align(uint32(len(res)), fileAlignment)
	rawPtr := align(uint32(len(image)), fileAlignment)
	if int(rawPtr) > len(image) {
		image = append(image, make([]byte, int(rawPtr)-len(image))...)
	}
	image = append(image, res...)
	image = append(image, make([]byte, int(rawSize)-len(res))...)

	hdr := image[newHeader : newHeader+40]
	for i := range hdr {
		hdr[i] = 0
	}
	copy(hdr, ".rsrc")
	le.PutUint32(hdr[8:], uint32(len(res)))
	le.PutUint32(hdr[12:], sectionRVA)
	le.PutUint32(hdr[16:], rawSize)
	le.PutUint32(hdr[20:], rawPtr)
	le.PutUint32(hdr[36:], 0x40000040)
	le.PutUint16(image[coff+2:], uint16(sectionCount+1))
	le.PutUint32(image[directoryBase+2*8:], sectionRVA)
	le.PutUint32(image[directoryBase+2*8+4:], uint32(len(res)))
	le.PutUint32(image[sizeOfImageOffset:], align(sectionRVA+uint32(len(res)), sectionAlignment))
	le.PutUint32(image[sizeInitializedOffset:], le.Uint32(image[sizeInitializedOffset:])+rawSize)
	return os.WriteFile(exe, image, 0o755)
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run embed_windows_icon.go <executable> <icon.ico>")
		os.Exit(2)
	}
	if err := patchPE(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "icon embedding failed:", err)
		os.Exit(1)
	}
	fmt.Printf("Embedded %s into %s\n", os.Args[2], os.Args[1])
}
