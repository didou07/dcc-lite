package filetransfer

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestDecodeEncodeRoundTripKeepsBytes(t *testing.T) {
	for name, raw := range map[string]string{
		"utf8":          "héllo مرحبا\n",
		"crlf":          "a\r\nb\r\n",
		"bom":           "\xef\xbb\xbfx\n",
		"latin1":        "caf\xe9\n",
		"cp1256":        "\xe3\xd1\xcd\xc8\xc7\r\n",
		"all high byte": "\x80\x81\xfe\xff\n",
		"empty":         "",
	} {
		text, le, enc, err := decodeText([]byte(raw))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		back, err := encodeText(text, le, enc)
		if err != nil || string(back) != raw {
			t.Errorf("%s: round trip gave %q (%v), want %q", name, back, err, raw)
		}
	}
}

func TestDecodeTextEncodings(t *testing.T) {
	if _, _, enc, _ := decodeText([]byte("plain")); enc != EncodingUTF8 {
		t.Errorf("ASCII should be UTF-8, got %s", enc)
	}
	text, _, enc, _ := decodeText([]byte("caf\xe9"))
	if enc != EncodingLatin1 || text != "café" {
		t.Errorf("got %q %s", text, enc)
	}
	for _, bin := range []string{"a\x00b", "\x00", "caf\xe9\x00"} {
		if _, _, _, err := decodeText([]byte(bin)); !errors.Is(err, ErrEditNotText) {
			t.Errorf("%q: err = %v, want ErrEditNotText", bin, err)
		}
	}
}

func TestEncodeTextRejectsWhatTheEncodingCannotStore(t *testing.T) {
	if _, err := encodeText("مرحبا", LineEndingLF, EncodingLatin1); !errors.Is(err, ErrEditEncoding) {
		t.Errorf("err = %v, want ErrEditEncoding", err)
	}
	if _, err := encodeText("مرحبا", LineEndingLF, EncodingUTF8); err != nil {
		t.Errorf("UTF-8 should store Arabic: %v", err)
	}
	if _, err := encodeText("a\x00", LineEndingLF, ""); !errors.Is(err, ErrEditNotText) {
		t.Errorf("NUL accepted: %v", err)
	}
	big := make([]byte, MaxEditBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if _, err := encodeText(string(big), LineEndingLF, ""); !errors.Is(err, ErrEditTooLarge) {
		t.Errorf("oversize accepted: %v", err)
	}
}

func TestSettleLineEndingUsesMostCommon(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want string
		mix  bool
	}{
		"mostly crlf": {"a\r\nb\r\nc\r\nd\ne\r\n", LineEndingCRLF, true},
		"mostly lf":   {"a\nb\nc\r\nd\n", LineEndingLF, true},
		"tie":         {"a\r\nb\n", LineEndingLF, true},
		"pure crlf":   {"a\r\nb\r\n", LineEndingCRLF, false},
	}
	for name, c := range cases {
		_, le, _, err := decodeText([]byte(c.raw))
		if err != nil {
			t.Fatal(err)
		}
		got, mixed := settleLineEnding([]byte(c.raw), le)
		if got != c.want || mixed != c.mix {
			t.Errorf("%s: got %s mixed=%v, want %s mixed=%v", name, got, mixed, c.want, c.mix)
		}
	}
	// The odd line is the only one that changes.
	raw := "a\r\nb\r\nc\r\nd\ne\r\n"
	text, le, enc, _ := decodeText([]byte(raw))
	le, _ = settleLineEnding([]byte(raw), le)
	back, _ := encodeText(text, le, enc)
	if string(back) != "a\r\nb\r\nc\r\nd\r\ne\r\n" {
		t.Errorf("saved %q", back)
	}
}

func TestUTF16FilesOpenAndSaveBackTheSameWay(t *testing.T) {
	le := []byte{0xff, 0xfe, '<', 0, 'a', 0, '>', 0, 0xe9, 0, '<', 0, '/', 0, 'a', 0, '>', 0, '\r', 0, '\n', 0}
	be := []byte{0xfe, 0xff, 0, '<', 0, 'a', 0, '>', 0, 0xe9, 0, '<', 0, '/', 0, 'a', 0, '>', 0, '\r', 0, '\n'}
	for name, raw := range map[string][]byte{"le": le, "be": be} {
		text, lineEnding, enc, err := decodeText(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if text != "<a>é</a>\n" || lineEnding != LineEndingCRLF || (enc != EncodingUTF16LE && enc != EncodingUTF16BE) {
			t.Fatalf("%s: %q %s %s", name, text, lineEnding, enc)
		}
		back, err := encodeText(text, lineEnding, enc)
		if err != nil || !bytes.Equal(back, raw) {
			t.Fatalf("%s: saving changed the file: %v % x", name, err, back)
		}
	}
}

func TestBinaryFilesSayWhyTheyCannotBeEdited(t *testing.T) {
	cases := map[string][]byte{
		"gzip":       {0x1f, 0x8b, 0x08, 0x00, 0x00},
		"xz":         {0xfd, '7', 'z', 'X', 'Z', 0x00, 0x01},
		"binary":     {'e', 'p', 'g', 0x00, 0x01, 0x02},
		"odd utf-16": {0xff, 0xfe, '<', 0, 'a'},
	}
	for name, raw := range cases {
		_, _, _, err := decodeText(raw)
		if !errors.Is(err, ErrEditNotText) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if name == "gzip" && !strings.Contains(err.Error(), "gzip") {
			t.Fatalf("the message should name the format: %v", err)
		}
		if name == "binary" && !strings.Contains(err.Error(), "binary") {
			t.Fatalf("the message should say it is binary: %v", err)
		}
	}
}
