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
}

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
}

// NewPlayer creates a player. Call SetWindow before Play.
func (l *Lib) NewPlayer() (*Player, error) {
	mp := l.call("libvlc_media_player_new", l.inst)
	if mp == 0 {
		return nil, errors.New("VLC could not create a player")
	}
	// The page, not VLC, owns the mouse and keyboard.
	l.call("libvlc_video_set_mouse_input", mp, 0)
	l.call("libvlc_video_set_key_input", mp, 0)
	return &Player{lib: l, mp: mp}, nil
}

// SetWindow tells VLC which window (HWND) to draw into.
func (p *Player) SetWindow(hwnd uintptr) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lib.call("libvlc_media_player_set_hwnd", p.mp, hwnd)
}

// Play opens the address and starts playing it.
func (p *Player) Play(mrl string) error {
	loc, err := cstr(mrl)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	media := p.lib.call("libvlc_media_new_location", p.lib.inst, uintptr(unsafe.Pointer(loc)))
	runtime.KeepAlive(loc)
	if media == 0 {
		return errors.New("VLC does not accept this stream address")
	}
	defer p.lib.call("libvlc_media_release", media)
	if opt, err := cstr(":http-reconnect"); err == nil {
		p.lib.call("libvlc_media_add_option", media, uintptr(unsafe.Pointer(opt)))
		runtime.KeepAlive(opt)
	}
	p.lib.call("libvlc_media_player_set_media", p.mp, media)
	if int32(p.lib.call("libvlc_media_player_play", p.mp)) != 0 {
		return errors.New("VLC could not start playing")
	}
	return nil
}

// Stop stops playback.
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lib.call("libvlc_media_player_stop", p.mp)
}

// SetPause pauses or resumes.
func (p *Player) SetPause(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
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
	p.lib.call("libvlc_audio_set_volume", p.mp, uintptr(v))
}

// SetMute mutes or unmutes.
func (p *Player) SetMute(m bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := uintptr(0)
	if m {
		v = 1
	}
	p.lib.call("libvlc_audio_set_mute", p.mp, v)
}

// State reports what the player is doing.
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return State(int32(p.lib.call("libvlc_media_player_get_state", p.mp)))
}

// AudioTracks lists the selectable audio tracks and the id of the active one.
func (p *Player) AudioTracks() ([]Track, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
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
	p.lib.call("libvlc_audio_set_track", p.mp, uintptr(id))
}

// VideoSize returns the size of the decoded picture, or zeros for radio and before the first frame.
func (p *Player) VideoSize() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
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
