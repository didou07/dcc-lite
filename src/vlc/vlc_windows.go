//go:build windows

package vlc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
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

type trackDescription struct {
	ID   int32
	Name uintptr
	Next uintptr
}

// Lib is a loaded libvlc.
type Lib struct {
	dir    string
	handle windows.Handle
	procs  map[string]uintptr
	inst   uintptr
}

var required = []string{
	"libvlc_new", "libvlc_release",
	"libvlc_media_new_location", "libvlc_media_add_option", "libvlc_media_release",
	"libvlc_media_player_new", "libvlc_media_player_release", "libvlc_media_player_set_media",
	"libvlc_media_player_set_hwnd", "libvlc_media_player_play", "libvlc_media_player_stop",
	"libvlc_media_player_set_pause", "libvlc_media_player_get_state",
	"libvlc_audio_set_volume", "libvlc_audio_set_mute",
	"libvlc_audio_get_track_description", "libvlc_track_description_list_release",
	"libvlc_audio_get_track", "libvlc_audio_set_track",
	"libvlc_video_get_size", "libvlc_video_take_snapshot",
	"libvlc_video_set_mouse_input", "libvlc_video_set_key_input",
	"libvlc_media_player_get_time",
}

// optional procs are used when this VLC has them; a missing one never stops VLC from loading.
var optional = []string{"libvlc_video_set_deinterlace", "libvlc_video_set_aspect_ratio", "libvlc_video_set_crop_geometry"}

// Load finds VLC, loads libvlc.dll from it and starts a libvlc instance.
func Load() (*Lib, error) {
	dir, err := FindDir()
	if err != nil {
		return nil, err
	}
	return LoadFrom(dir)
}

// LoadFrom loads libvlc.dll from dir.
func LoadFrom(dir string) (*Lib, error) {
	// The altered search path makes libvlccore.dll resolve from the same folder.
	h, err := windows.LoadLibraryEx(filepath.Join(dir, "libvlc.dll"), 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH)
	if err != nil {
		return nil, fmt.Errorf("cannot load libvlc.dll from %s: %w", dir, err)
	}
	l := &Lib{dir: dir, handle: h, procs: map[string]uintptr{}}
	for _, name := range required {
		addr, err := windows.GetProcAddress(h, name)
		if err != nil {
			return nil, fmt.Errorf("this VLC is too old or not a VLC 3 build (%s is missing)", name)
		}
		l.procs[name] = addr
	}
	for _, name := range optional {
		if addr, err := windows.GetProcAddress(h, name); err == nil {
			l.procs[name] = addr
		}
	}
	if plugins := filepath.Join(dir, "plugins"); os.Getenv("VLC_PLUGIN_PATH") == "" {
		_ = os.Setenv("VLC_PLUGIN_PATH", plugins)
	}
	args := []string{
		"--no-video-title-show", "--no-osd", "--no-snapshot-preview", "--no-stats",
		"--network-caching=1500", "--live-caching=1500",
	}
	argv := make([]*byte, len(args))
	for i, a := range args {
		if argv[i], err = windows.BytePtrFromString(a); err != nil {
			return nil, err
		}
	}
	inst := l.call("libvlc_new", uintptr(len(argv)), uintptr(unsafe.Pointer(&argv[0])))
	runtime.KeepAlive(argv)
	if inst == 0 {
		return nil, errors.New("VLC could not start (libvlc_new failed); try reinstalling VLC")
	}
	l.inst = inst
	return l, nil
}

func (l *Lib) call(name string, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(l.procs[name], args...)
	return r
}

// Dir is the folder VLC was loaded from.
func (l *Lib) Dir() string { return l.dir }

// Close releases the libvlc instance.
func (l *Lib) Close() {
	if l.inst != 0 {
		l.call("libvlc_release", l.inst)
		l.inst = 0
	}
}

func cstr(s string) (*byte, error) { return windows.BytePtrFromString(s) }

// ptr turns an address handed out by libvlc into an unsafe.Pointer without tripping go vet.
func ptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

// Player plays one stream at a time into a window.
type Player struct {
	lib *Lib
	mu  sync.Mutex
	mp  uintptr

	deint string // the deinterlace mode the current picture pipeline was started with

	// What was last asked of the player, so a fresh libvlc player can be given the same.
	hwnd   uintptr
	vol    int
	volSet bool
	muted  bool
}

// PlayOptions are the per-channel settings. The zero value means VLC's own defaults.
type PlayOptions struct {
	CachingMs   int    // network and live buffering in milliseconds; 0 keeps the default
	Deinterlace string // "auto" (VLC decides), "yadif" or "off"
	NoHardware  bool   // decode in software
}

// Video fit modes: how the picture meets the edges of its window.
const (
	FitOriginal = "fit"     // the broadcast's own proportions, black bars where needed (default)
	FitFill     = "fill"    // zoom until the window is full; the overflow is cut off
	FitStretch  = "stretch" // stretch the picture to the window's shape
	Fit169      = "16:9"
	Fit43       = "4:3"
)

// Deinterlace modes.
const (
	DeinterlaceAuto  = "auto"
	DeinterlaceYadif = "yadif"
	DeinterlaceOff   = "off"
)

// NewPlayer creates a player. Call SetWindow before Play.
func (l *Lib) NewPlayer() (*Player, error) {
	mp, err := l.newMP()
	if err != nil {
		return nil, err
	}
	return &Player{lib: l, mp: mp, deint: DeinterlaceAuto}, nil
}

func (l *Lib) newMP() (uintptr, error) {
	mp := l.call("libvlc_media_player_new", l.inst)
	if mp == 0 {
		return 0, errors.New("VLC could not create a player")
	}
	// The page, not VLC, owns the mouse and keyboard.
	l.call("libvlc_video_set_mouse_input", mp, 0)
	l.call("libvlc_video_set_key_input", mp, 0)
	return mp, nil
}

// SetWindow tells VLC which window (HWND) to draw into.
func (p *Player) SetWindow(hwnd uintptr) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return
	}
	p.hwnd = hwnd
	p.lib.call("libvlc_media_player_set_hwnd", p.mp, hwnd)
}

// Play opens the address and starts it. A deinterlace change creates a fresh libvlc player;
// window, volume and mute settings carry over.
func (p *Player) Play(mrl string, opt PlayOptions) error {
	loc, err := cstr(mrl)
	if err != nil {
		return err
	}
	mode := opt.Deinterlace
	if mode != DeinterlaceYadif && mode != DeinterlaceOff {
		mode = DeinterlaceAuto
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if mode != p.deint && p.mp != 0 {
		p.lib.call("libvlc_media_player_stop", p.mp)
		p.lib.call("libvlc_media_player_release", p.mp)
		mp, err := p.lib.newMP()
		if err != nil {
			p.mp = 0
			return err
		}
		p.mp = mp
		if p.hwnd != 0 {
			p.lib.call("libvlc_media_player_set_hwnd", p.mp, p.hwnd)
		}
		if p.volSet {
			p.lib.call("libvlc_audio_set_volume", p.mp, uintptr(p.vol))
		}
		m := uintptr(0)
		if p.muted {
			m = 1
		}
		p.lib.call("libvlc_audio_set_mute", p.mp, m)
	}
	if p.mp == 0 {
		return errors.New("the player is not ready")
	}
	p.deint = mode
	media := p.lib.call("libvlc_media_new_location", p.lib.inst, uintptr(unsafe.Pointer(loc)))
	runtime.KeepAlive(loc)
	if media == 0 {
		return errors.New("VLC does not accept this stream address")
	}
	defer p.lib.call("libvlc_media_release", media)
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
		if c, err := cstr(o); err == nil {
			p.lib.call("libvlc_media_add_option", media, uintptr(unsafe.Pointer(c)))
			runtime.KeepAlive(c)
		}
	}
	p.lib.call("libvlc_media_player_set_media", p.mp, media)
	if int32(p.lib.call("libvlc_media_player_play", p.mp)) != 0 {
		return errors.New("VLC could not start playing")
	}
	return nil
}

func (p *Player) SetFit(mode string, w, h int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return
	}
	set := func(name, value string) {
		if p.lib.procs[name] == 0 {
			return
		}
		if value == "" {
			p.lib.call(name, p.mp, 0) // NULL: back to VLC's own choice
			return
		}
		if c, err := cstr(value); err == nil {
			p.lib.call(name, p.mp, uintptr(unsafe.Pointer(c)))
			runtime.KeepAlive(c)
		}
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
	set("libvlc_video_set_crop_geometry", crop)
	set("libvlc_video_set_aspect_ratio", aspect)
}

func (p *Player) ApplyDeinterlace() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 || p.lib.procs["libvlc_video_set_deinterlace"] == 0 {
		return
	}
	switch p.deint {
	case DeinterlaceYadif:
		if c, err := cstr("yadif"); err == nil {
			p.lib.call("libvlc_video_set_deinterlace", p.mp, uintptr(unsafe.Pointer(c)))
			runtime.KeepAlive(c)
		}
	case DeinterlaceOff:
		p.lib.call("libvlc_video_set_deinterlace", p.mp, 0)
	}
}

func (p *Player) Time() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return -1
	}
	return int64(int32(p.lib.call("libvlc_media_player_get_time", p.mp)))
}

// Stop stops playback.
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return
	}
	p.lib.call("libvlc_media_player_stop", p.mp)
}

// SetPause pauses or resumes.
func (p *Player) SetPause(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return
	}
	v := uintptr(0)
	if paused {
		v = 1
	}
	p.lib.call("libvlc_media_player_set_pause", p.mp, v)
}

// SetVolume sets the volume (0 to 100, VLC allows up to 200).
func (p *Player) SetVolume(v int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return
	}
	p.vol, p.volSet = v, true
	p.lib.call("libvlc_audio_set_volume", p.mp, uintptr(v))
}

// SetMute mutes or unmutes.
func (p *Player) SetMute(m bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return
	}
	v := uintptr(0)
	if m {
		v = 1
	}
	p.muted = m
	p.lib.call("libvlc_audio_set_mute", p.mp, v)
}

// State reports what the player is doing.
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return StateError
	}
	return State(int32(p.lib.call("libvlc_media_player_get_state", p.mp)))
}

// AudioTracks lists the selectable audio tracks and the id of the active one.
func (p *Player) AudioTracks() ([]Track, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return nil, -1
	}
	head := p.lib.call("libvlc_audio_get_track_description", p.mp)
	var out []Track
	for node := head; node != 0; {
		d := (*trackDescription)(ptr(node))
		if d.ID >= 0 { // -1 is VLC's own "Disable"
			name := "Track"
			if d.Name != 0 {
				name = windows.BytePtrToString((*byte)(ptr(d.Name)))
			}
			out = append(out, Track{ID: int(d.ID), Name: strings.TrimSpace(name)})
		}
		node = d.Next
	}
	if head != 0 {
		p.lib.call("libvlc_track_description_list_release", head)
	}
	return out, int(int32(p.lib.call("libvlc_audio_get_track", p.mp)))
}

// SetAudioTrack selects an audio track by id.
func (p *Player) SetAudioTrack(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return
	}
	p.lib.call("libvlc_audio_set_track", p.mp, uintptr(id))
}

// VideoSize returns the size of the decoded picture, or zeros for radio and before the first frame.
func (p *Player) VideoSize() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return 0, 0
	}
	w, h := new(uint32), new(uint32)
	if int32(p.lib.call("libvlc_video_get_size", p.mp, 0, uintptr(unsafe.Pointer(w)), uintptr(unsafe.Pointer(h)))) != 0 {
		return 0, 0
	}
	return int(*w), int(*h)
}

// Snapshot saves the current picture as a PNG.
func (p *Player) Snapshot(path string) error {
	c, err := cstr(path)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp == 0 {
		return errors.New("the player is not ready")
	}
	rc := int32(p.lib.call("libvlc_video_take_snapshot", p.mp, 0, uintptr(unsafe.Pointer(c)), 0, 0))
	runtime.KeepAlive(c)
	if rc != 0 {
		return errors.New("VLC could not take the snapshot")
	}
	return nil
}

// Close stops and releases the player.
func (p *Player) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mp != 0 {
		p.lib.call("libvlc_media_player_stop", p.mp)
		p.lib.call("libvlc_media_player_release", p.mp)
		p.mp = 0
	}
}
