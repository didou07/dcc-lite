//go:build darwin && cgo

package vlc

/*
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct dcc_track {
	int id;
	char *name;
	struct dcc_track *next;
} dcc_track;

static void *h;

static void *(*p_new)(int, const char *const *);
static void (*p_release)(void *);
static void *(*p_media_new_location)(void *, const char *);
static void (*p_media_add_option)(void *, const char *);
static void (*p_media_release)(void *);
static void *(*p_mp_new)(void *);
static void (*p_mp_release)(void *);
static void (*p_mp_set_media)(void *, void *);
static void (*p_mp_set_nsobject)(void *, void *);
static int (*p_mp_play)(void *);
static void (*p_mp_stop)(void *);
static void (*p_mp_set_pause)(void *, int);
static int (*p_mp_get_state)(void *);
static int64_t (*p_mp_get_time)(void *);
static int (*p_audio_set_volume)(void *, int);
static void (*p_audio_set_mute)(void *, int);
static dcc_track *(*p_audio_get_track_description)(void *);
static void (*p_track_description_list_release)(dcc_track *);
static int (*p_audio_get_track)(void *);
static int (*p_audio_set_track)(void *, int);
static int (*p_video_get_size)(void *, unsigned, unsigned *, unsigned *);
static int (*p_video_take_snapshot)(void *, unsigned, const char *, unsigned, unsigned);
static void (*p_video_set_mouse_input)(void *, unsigned);
static void (*p_video_set_key_input)(void *, unsigned);
static void (*p_video_set_deinterlace)(void *, const char *);
static void (*p_video_set_aspect_ratio)(void *, const char *);
static void (*p_video_set_crop_geometry)(void *, const char *);
static void (*p_video_set_logo_int)(void *, unsigned, int);
static void (*p_video_set_logo_string)(void *, unsigned, const char *);

static const char *missing;

#define NEED(var, name) do { *(void **)(&var) = dlsym(h, name); if (!var) { missing = name; return 0; } } while (0)
#define WANT(var, name) do { *(void **)(&var) = dlsym(h, name); } while (0)

// dcc_open loads the library and resolves every call; on failure it returns 0 and sets dcc_error.
static int dcc_open(const char *path) {
	missing = NULL;
	h = dlopen(path, RTLD_NOW | RTLD_GLOBAL);
	if (!h) return -1;
	NEED(p_new, "libvlc_new");
	NEED(p_release, "libvlc_release");
	NEED(p_media_new_location, "libvlc_media_new_location");
	NEED(p_media_add_option, "libvlc_media_add_option");
	NEED(p_media_release, "libvlc_media_release");
	NEED(p_mp_new, "libvlc_media_player_new");
	NEED(p_mp_release, "libvlc_media_player_release");
	NEED(p_mp_set_media, "libvlc_media_player_set_media");
	NEED(p_mp_set_nsobject, "libvlc_media_player_set_nsobject");
	NEED(p_mp_play, "libvlc_media_player_play");
	NEED(p_mp_stop, "libvlc_media_player_stop");
	NEED(p_mp_set_pause, "libvlc_media_player_set_pause");
	NEED(p_mp_get_state, "libvlc_media_player_get_state");
	NEED(p_mp_get_time, "libvlc_media_player_get_time");
	NEED(p_audio_set_volume, "libvlc_audio_set_volume");
	NEED(p_audio_set_mute, "libvlc_audio_set_mute");
	NEED(p_audio_get_track_description, "libvlc_audio_get_track_description");
	NEED(p_track_description_list_release, "libvlc_track_description_list_release");
	NEED(p_audio_get_track, "libvlc_audio_get_track");
	NEED(p_audio_set_track, "libvlc_audio_set_track");
	NEED(p_video_get_size, "libvlc_video_get_size");
	NEED(p_video_take_snapshot, "libvlc_video_take_snapshot");
	NEED(p_video_set_mouse_input, "libvlc_video_set_mouse_input");
	NEED(p_video_set_key_input, "libvlc_video_set_key_input");
	WANT(p_video_set_deinterlace, "libvlc_video_set_deinterlace");
	WANT(p_video_set_aspect_ratio, "libvlc_video_set_aspect_ratio");
	WANT(p_video_set_crop_geometry, "libvlc_video_set_crop_geometry");
	WANT(p_video_set_logo_int, "libvlc_video_set_logo_int");
	WANT(p_video_set_logo_string, "libvlc_video_set_logo_string");
	return 0;
}

static const char *dcc_missing(void) { return missing; }
static const char *dcc_dlerror(void) { return dlerror(); }
static void dcc_preload(const char *path) { dlopen(path, RTLD_NOW | RTLD_GLOBAL); }

static void *dcc_new(int argc, char **argv) { return p_new(argc, (const char *const *)argv); }
static void dcc_free(void *i) { p_release(i); }
static void *dcc_media(void *i, const char *mrl) { return p_media_new_location(i, mrl); }
static void dcc_media_option(void *m, const char *o) { p_media_add_option(m, o); }
static void dcc_media_free(void *m) { p_media_release(m); }
static void *dcc_mp_new(void *i, int mouse_key_off) {
	void *mp = p_mp_new(i);
	if (mp && mouse_key_off) { p_video_set_mouse_input(mp, 0); p_video_set_key_input(mp, 0); }
	return mp;
}
static void dcc_mp_free(void *mp) { p_mp_release(mp); }
static void dcc_mp_set_media(void *mp, void *m) { p_mp_set_media(mp, m); }
static void dcc_mp_xwindow(void *mp, unsigned long view) { p_mp_set_nsobject(mp, (void *)view); }
static int dcc_mp_play(void *mp) { return p_mp_play(mp); }
static void dcc_mp_stop(void *mp) { p_mp_stop(mp); }
static void dcc_mp_pause(void *mp, int v) { p_mp_set_pause(mp, v); }
static int dcc_mp_state(void *mp) { return p_mp_get_state(mp); }
static long long dcc_mp_time(void *mp) { return (long long)p_mp_get_time(mp); }
static void dcc_volume(void *mp, int v) { p_audio_set_volume(mp, v); }
static void dcc_mute(void *mp, int v) { p_audio_set_mute(mp, v); }
static int dcc_track_current(void *mp) { return p_audio_get_track(mp); }
static void dcc_track_set(void *mp, int id) { p_audio_set_track(mp, id); }
static dcc_track *dcc_tracks(void *mp) { return p_audio_get_track_description(mp); }
static void dcc_tracks_free(dcc_track *t) { p_track_description_list_release(t); }
static int dcc_video_size(void *mp, unsigned *w, unsigned *h) { return p_video_get_size(mp, 0, w, h); }
static int dcc_snapshot(void *mp, const char *path) { return p_video_take_snapshot(mp, 0, path, 0, 0); }
static int dcc_has(const char *what) {
	if (!strcmp(what, "deinterlace")) return p_video_set_deinterlace != NULL;
	if (!strcmp(what, "aspect")) return p_video_set_aspect_ratio != NULL;
	if (!strcmp(what, "crop")) return p_video_set_crop_geometry != NULL;
	if (!strcmp(what, "logo")) return p_video_set_logo_int != NULL && p_video_set_logo_string != NULL;
	return 0;
}
static void dcc_deinterlace(void *mp, const char *mode) { if (p_video_set_deinterlace) p_video_set_deinterlace(mp, mode); }
static void dcc_aspect(void *mp, const char *v) { if (p_video_set_aspect_ratio) p_video_set_aspect_ratio(mp, v); }
static void dcc_crop(void *mp, const char *v) { if (p_video_set_crop_geometry) p_video_set_crop_geometry(mp, v); }
static void dcc_logo_int(void *mp, unsigned opt, int v) { if (p_video_set_logo_int) p_video_set_logo_int(mp, opt, v); }
static void dcc_logo_string(void *mp, unsigned opt, const char *v) { if (p_video_set_logo_string) p_video_set_logo_string(mp, opt, v); }
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"
)

// State is libvlc's player state.
type State int

const (
	StateIdle State = iota
	StateOpening
	StateBuffering
	StatePlaying
	StatePaused
	StateStopped
	StateEnded
	StateError
)

// Track is one audio track of the current stream.
type Track struct {
	ID   int
	Name string
}

// Lib is a loaded libvlc.
type Lib struct {
	dir  string
	inst unsafe.Pointer
}

var loadMu sync.Mutex

func Load() (*Lib, error) {
	path, err := FindLib()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

func LoadFrom(path string) (*Lib, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	if filepath.IsAbs(path) {
		dir := filepath.Dir(path)
		core := C.CString(filepath.Join(dir, "libvlccore.dylib"))
		C.dcc_preload(core)
		C.free(unsafe.Pointer(core))
		if os.Getenv("VLC_PLUGIN_PATH") == "" {
			plugins := filepath.Join(filepath.Dir(dir), "plugins")
			if st, err := os.Stat(plugins); err == nil && st.IsDir() {
				_ = os.Setenv("VLC_PLUGIN_PATH", plugins)
			}
		}
	}
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	if C.dcc_open(cp) != 0 || C.dcc_missing() != nil {
		if m := C.dcc_missing(); m != nil {
			return nil, fmt.Errorf("this VLC is too old or not a VLC 3 build (%s is missing)", C.GoString(m))
		}
		msg := "unknown error"
		if e := C.dcc_dlerror(); e != nil {
			msg = C.GoString(e)
		}
		return nil, fmt.Errorf("cannot load libvlc from %s: %s", path, msg)
	}
	args := []string{
		"--no-video-title-show", "--no-osd", "--no-snapshot-preview", "--no-stats",
		"--no-media-library", "--network-caching=1500", "--live-caching=1500",
	}
	argv := make([]*C.char, len(args))
	for i, a := range args {
		argv[i] = C.CString(a)
	}
	defer func() {
		for _, a := range argv {
			C.free(unsafe.Pointer(a))
		}
	}()
	inst := C.dcc_new(C.int(len(argv)), (**C.char)(unsafe.Pointer(&argv[0])))
	if inst == nil {
		return nil, errors.New("VLC could not start (libvlc_new failed); try reinstalling VLC")
	}
	return &Lib{dir: path, inst: inst}, nil
}

// Dir is the library VLC was loaded from.
func (l *Lib) Dir() string { return l.dir }

// Close releases the libvlc instance.
func (l *Lib) Close() {
	if l.inst != nil {
		C.dcc_free(l.inst)
		l.inst = nil
	}
}

// Player plays one stream at a time into a window.
type Player struct {
	lib *Lib
	mu  sync.Mutex
	mp  unsafe.Pointer

	deint string

	xid    uintptr
	vol    int
	volSet bool
	muted  bool

	overlayOn bool
}

type PlayOptions struct {
	CachingMs   int
	Deinterlace string // "auto" (VLC decides), "yadif" or "off"
	NoHardware  bool   // decode in software
}

const (
	FitOriginal = "fit"
	FitFill     = "fill"
	FitStretch  = "stretch"
	Fit169      = "16:9"
	Fit43       = "4:3"
)

// Deinterlace modes.
const (
	DeinterlaceAuto  = "auto"
	DeinterlaceYadif = "yadif"
	DeinterlaceOff   = "off"
)

func (l *Lib) NewPlayer() (*Player, error) {
	mp := C.dcc_mp_new(l.inst, 1)
	if mp == nil {
		return nil, errors.New("VLC could not create a player")
	}
	return &Player{lib: l, mp: mp, deint: DeinterlaceAuto}, nil
}

func (p *Player) SetWindow(xid uintptr) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return
	}
	p.xid = xid
	C.dcc_mp_xwindow(p.mp, C.ulong(xid))
}

func (p *Player) Play(mrl string, opt PlayOptions) error {
	mode := opt.Deinterlace
	if mode != DeinterlaceYadif && mode != DeinterlaceOff {
		mode = DeinterlaceAuto
	}
	loc := C.CString(mrl)
	defer C.free(unsafe.Pointer(loc))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.overlayOn && p.mp != nil && p.OverlaySupported() {
		C.dcc_logo_int(p.mp, 0, 0)
	}
	p.overlayOn = false
	if mode != p.deint && p.mp != nil {
		C.dcc_mp_stop(p.mp)
		C.dcc_mp_free(p.mp)
		mp := C.dcc_mp_new(p.lib.inst, 1)
		if mp == nil {
			p.mp = nil
			return errors.New("VLC could not create a player")
		}
		p.mp = mp
		if p.xid != 0 {
			C.dcc_mp_xwindow(p.mp, C.ulong(p.xid))
		}
		if p.volSet {
			C.dcc_volume(p.mp, C.int(p.vol))
		}
		m := C.int(0)
		if p.muted {
			m = 1
		}
		C.dcc_mute(p.mp, m)
	}
	if p.mp == nil {
		return errors.New("the player is not ready")
	}
	p.deint = mode
	media := C.dcc_media(p.lib.inst, loc)
	if media == nil {
		return errors.New("VLC does not accept this stream address")
	}
	defer C.dcc_media_free(media)
	opts := []string{":http-reconnect"}
	if opt.CachingMs > 0 {
		opts = append(opts, fmt.Sprintf(":network-caching=%d", opt.CachingMs), fmt.Sprintf(":live-caching=%d", opt.CachingMs))
	}
	switch mode {
	case DeinterlaceYadif:
		opts = append(opts, ":deinterlace=1", ":deinterlace-mode=yadif")
	case DeinterlaceOff:
		opts = append(opts, ":deinterlace=0")
	}
	if opt.NoHardware {
		opts = append(opts, ":avcodec-hw=none")
	}
	for _, o := range opts {
		c := C.CString(o)
		C.dcc_media_option(media, c)
		C.free(unsafe.Pointer(c))
	}
	C.dcc_mp_set_media(p.mp, media)
	if C.dcc_mp_play(p.mp) != 0 {
		return errors.New("VLC could not start playing")
	}
	return nil
}

func (p *Player) SetFit(mode string, w, h int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return
	}
	ratio := ""
	if w > 0 && h > 0 {
		ratio = fmt.Sprintf("%d:%d", w, h)
	}
	aspect, crop := "", ""
	switch mode {
	case FitFill:
		crop = ratio
	case FitStretch:
		aspect = ratio
	case Fit169, Fit43:
		aspect = mode
	}
	set := func(fn func(unsafe.Pointer, *C.char), value string) {
		if value == "" {
			fn(p.mp, nil) // NULL: back to VLC's own choice
			return
		}
		c := C.CString(value)
		fn(p.mp, c)
		C.free(unsafe.Pointer(c))
	}
	if C.dcc_has(cstrConst("crop")) != 0 {
		set(func(mp unsafe.Pointer, c *C.char) { C.dcc_crop(mp, c) }, crop)
	}
	if C.dcc_has(cstrConst("aspect")) != 0 {
		set(func(mp unsafe.Pointer, c *C.char) { C.dcc_aspect(mp, c) }, aspect)
	}
}

func cstrConst(s string) *C.char {
	constMu.Lock()
	defer constMu.Unlock()
	if c, ok := consts[s]; ok {
		return c
	}
	c := C.CString(s)
	consts[s] = c
	return c
}

var (
	constMu sync.Mutex
	consts  = map[string]*C.char{}
)

func (p *Player) ApplyDeinterlace() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil || C.dcc_has(cstrConst("deinterlace")) == 0 {
		return
	}
	switch p.deint {
	case DeinterlaceYadif:
		c := C.CString("yadif")
		C.dcc_deinterlace(p.mp, c)
		C.free(unsafe.Pointer(c))
	case DeinterlaceOff:
		C.dcc_deinterlace(p.mp, nil)
	}
}

func (p *Player) Time() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return -1
	}
	return int64(C.dcc_mp_time(p.mp))
}

// Stop stops playback.
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp != nil {
		C.dcc_mp_stop(p.mp)
	}
}

// SetPause pauses or resumes.
func (p *Player) SetPause(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return
	}
	v := C.int(0)
	if paused {
		v = 1
	}
	C.dcc_mp_pause(p.mp, v)
}

func (p *Player) SetVolume(v int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return
	}
	p.vol, p.volSet = v, true
	C.dcc_volume(p.mp, C.int(v))
}

// SetMute mutes or unmutes.
func (p *Player) SetMute(m bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return
	}
	v := C.int(0)
	if m {
		v = 1
	}
	p.muted = m
	C.dcc_mute(p.mp, v)
}

// State reports what the player is doing.
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return StateError
	}
	return State(C.dcc_mp_state(p.mp))
}

func (p *Player) AudioTracks() ([]Track, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return nil, -1
	}
	head := C.dcc_tracks(p.mp)
	var out []Track
	for node := head; node != nil; node = node.next {
		if node.id >= 0 { // -1 is VLC's own "Disable"
			name := "Track"
			if node.name != nil {
				name = C.GoString(node.name)
			}
			out = append(out, Track{ID: int(node.id), Name: strings.TrimSpace(name)})
		}
	}
	if head != nil {
		C.dcc_tracks_free(head)
	}
	return out, int(C.dcc_track_current(p.mp))
}

// SetAudioTrack selects an audio track by id.
func (p *Player) SetAudioTrack(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp != nil {
		C.dcc_track_set(p.mp, C.int(id))
	}
}

func (p *Player) VideoSize() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return 0, 0
	}
	var w, h C.uint
	if C.dcc_video_size(p.mp, &w, &h) != 0 {
		return 0, 0
	}
	return int(w), int(h)
}

// Snapshot saves the current picture as a PNG.
func (p *Player) Snapshot(path string) error {
	c := C.CString(path)
	defer C.free(unsafe.Pointer(c))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return errors.New("the player is not ready")
	}
	if C.dcc_snapshot(p.mp, c) != 0 {
		return errors.New("VLC could not take the snapshot")
	}
	return nil
}

func (p *Player) OverlaySupported() bool {
	return C.dcc_has(cstrConst("logo")) != 0
}

func (p *Player) SetOverlay(path string) error {
	if !p.OverlaySupported() {
		return errors.New("this VLC cannot draw a picture over the video")
	}
	if strings.ContainsAny(path, ",;") {
		return errors.New("the temporary folder's name has a comma or semicolon, which VLC reads as a list")
	}
	c := C.CString(path)
	defer C.free(unsafe.Pointer(c))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil {
		return errors.New("the player is not ready")
	}
	C.dcc_logo_string(p.mp, 1, c) // libvlc_logo_file
	if !p.overlayOn {
		C.dcc_logo_int(p.mp, 2, 0)   // x
		C.dcc_logo_int(p.mp, 3, 0)   // y
		C.dcc_logo_int(p.mp, 7, 5)   // position: left + top
		C.dcc_logo_int(p.mp, 6, 255) // opacity
		C.dcc_logo_int(p.mp, 0, 1)   // enable
		p.overlayOn = true
	}
	return nil
}

func (p *Player) ClearOverlay() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == nil || !p.overlayOn || !p.OverlaySupported() {
		return
	}
	C.dcc_logo_int(p.mp, 0, 0)
	p.overlayOn = false
}

// Close stops and releases the player.
func (p *Player) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp != nil {
		C.dcc_mp_stop(p.mp)
		C.dcc_mp_free(p.mp)
		p.mp = nil
	}
}
