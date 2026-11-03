// Package config holds MyGo's persistent settings.
//
// Settings live in a single JSON file under the user's Application Support
// directory. Global mirroring options act as defaults; each device may carry
// its own override, keyed by the device's hardware serial so that the same
// phone keeps its settings whether it is attached over USB or Wi-Fi.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Video codecs MyGo can render with VideoToolbox.
const (
	CodecH264 = "h264"
	CodecH265 = "h265"
)

// Recording containers.
const (
	FormatMP4 = "mp4"
	FormatMOV = "mov"
)

// MirrorOptions are the scrcpy options a mirroring session starts with.
type MirrorOptions struct {
	MaxSize         int    `json:"maxSize"`   // longest side in pixels, 0 = device resolution
	BitRateMbps     int    `json:"bitRate"`   // video bit rate in Mbit/s
	MaxFPS          int    `json:"maxFps"`    // 0 = unlimited
	Codec           string `json:"codec"`     // h264 or h265
	Audio           bool   `json:"audio"`     // forward device audio (Android 11+)
	StayAwake       bool   `json:"stayAwake"` // keep the device awake while plugged in
	TurnScreenOff   bool   `json:"screenOff"` // turn the device display off while mirroring
	ShowTouches     bool   `json:"showTouches"`
	AlwaysOnTop     bool   `json:"alwaysOnTop"`
	PowerOffOnClose bool   `json:"powerOffOnClose"`
}

// DefaultMirrorOptions mirror scrcpy's own defaults, with audio on.
func DefaultMirrorOptions() MirrorOptions {
	return MirrorOptions{
		MaxSize:     0,
		BitRateMbps: 8,
		MaxFPS:      60,
		Codec:       CodecH264,
		Audio:       true,
	}
}

// Normalize clamps values to what the UI offers.
func (o MirrorOptions) Normalize() MirrorOptions {
	if o.Codec != CodecH264 && o.Codec != CodecH265 {
		o.Codec = CodecH264
	}
	if o.BitRateMbps <= 0 || o.BitRateMbps > 200 {
		o.BitRateMbps = 8
	}
	if o.MaxFPS < 0 || o.MaxFPS > 240 {
		o.MaxFPS = 60
	}
	if o.MaxSize < 0 {
		o.MaxSize = 0
	}
	return o
}

// RequiresRestart reports whether switching from o to n needs a new stream.
// Options that are only window or device state can be applied live.
func (o MirrorOptions) RequiresRestart(n MirrorOptions) bool {
	return o.MaxSize != n.MaxSize || o.BitRateMbps != n.BitRateMbps ||
		o.MaxFPS != n.MaxFPS || o.Codec != n.Codec || o.Audio != n.Audio ||
		o.StayAwake != n.StayAwake || o.ShowTouches != n.ShowTouches || o.PowerOffOnClose != n.PowerOffOnClose
}

// DeviceProfile is what MyGo remembers about one phone.
type DeviceProfile struct {
	Name     string         `json:"name,omitempty"`
	Override *MirrorOptions `json:"override,omitempty"`
}

// KnownWiFi is a Wi-Fi address that connected successfully before.
type KnownWiFi struct {
	Address       string    `json:"address"` // host:port
	Name          string    `json:"name"`
	Identity      string    `json:"identity,omitempty"`
	LastConnected time.Time `json:"lastConnected"`
}

// Config is the whole settings file.
type Config struct {
	HideDockIconWhenClosed bool   `json:"hideDockIconWhenClosed"`
	ScreenshotDir          string `json:"screenshotDir"`
	CopyScreenshot         bool   `json:"copyScreenshot"`
	RecordingDir           string `json:"recordingDir"`
	RecordingFormat        string `json:"recordingFormat"`
	RecordAudio            bool   `json:"recordAudio"`
	ADBPath                string `json:"adbPath"`    // "" = bundled
	ScrcpyPath             string `json:"scrcpyPath"` // "" = bundled; scrcpy binary or scrcpy-server file

	Mirror    MirrorOptions             `json:"mirror"`
	Devices   map[string]*DeviceProfile `json:"devices"`
	KnownWiFi []KnownWiFi               `json:"knownWiFi"`
}

// Default returns the settings used on first launch.
func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		HideDockIconWhenClosed: true,
		ScreenshotDir:          filepath.Join(home, "Pictures", "MyGo"),
		CopyScreenshot:         true,
		RecordingDir:           filepath.Join(home, "Movies", "MyGo"),
		RecordingFormat:        FormatMP4,
		RecordAudio:            true,
		Mirror:                 DefaultMirrorOptions(),
		Devices:                map[string]*DeviceProfile{},
	}
}

// Store is a concurrency-safe handle on the settings file.
type Store struct {
	mu   sync.Mutex
	path string
	cfg  Config
}

// DefaultPath is ~/Library/Application Support/MyGo/config.json.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Library", "Application Support")
	}
	return filepath.Join(dir, "MyGo", "config.json")
}

// Open loads the settings at path, falling back to defaults when the file is
// missing or unreadable.
func Open(path string) (*Store, error) {
	s := &Store{path: path, cfg: Default()}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return s, err
	}
	s.cfg = sanitize(cfg)
	return s, nil
}

func sanitize(c Config) Config {
	d := Default()
	if c.ScreenshotDir == "" {
		c.ScreenshotDir = d.ScreenshotDir
	}
	if c.RecordingDir == "" {
		c.RecordingDir = d.RecordingDir
	}
	if c.RecordingFormat != FormatMP4 && c.RecordingFormat != FormatMOV {
		c.RecordingFormat = FormatMP4
	}
	if c.Devices == nil {
		c.Devices = map[string]*DeviceProfile{}
	}
	c.Mirror = c.Mirror.Normalize()
	for _, p := range c.Devices {
		if p != nil && p.Override != nil {
			n := p.Override.Normalize()
			p.Override = &n
		}
	}
	return c
}

// Get returns a deep copy of the current settings.
func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.cfg)
}

// Update applies fn to the settings and saves them.
func (s *Store) Update(fn func(c *Config)) error {
	s.mu.Lock()
	c := clone(s.cfg)
	fn(&c)
	s.cfg = sanitize(c)
	snapshot := clone(s.cfg)
	s.mu.Unlock()
	return s.save(snapshot)
}

// MirrorOptionsFor returns the effective options for a device identity and
// whether they are a device-specific override.
func (s *Store) MirrorOptionsFor(identity string) (MirrorOptions, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.cfg.Devices[identity]; p != nil && p.Override != nil {
		return *p.Override, true
	}
	return s.cfg.Mirror, false
}

// RememberWiFi records a successful Wi-Fi connection.
func (s *Store) RememberWiFi(address, name, identity string) {
	_ = s.Update(func(c *Config) {
		now := time.Now()
		for i := range c.KnownWiFi {
			if c.KnownWiFi[i].Address == address {
				c.KnownWiFi[i].Name = name
				c.KnownWiFi[i].Identity = identity
				c.KnownWiFi[i].LastConnected = now
				return
			}
		}
		c.KnownWiFi = append(c.KnownWiFi, KnownWiFi{Address: address, Name: name, Identity: identity, LastConnected: now})
		sort.Slice(c.KnownWiFi, func(i, j int) bool {
			return c.KnownWiFi[i].LastConnected.After(c.KnownWiFi[j].LastConnected)
		})
		if len(c.KnownWiFi) > 8 {
			c.KnownWiFi = c.KnownWiFi[:8]
		}
	})
}

// ForgetWiFi removes an address from the history.
func (s *Store) ForgetWiFi(address string) {
	_ = s.Update(func(c *Config) {
		out := c.KnownWiFi[:0]
		for _, k := range c.KnownWiFi {
			if k.Address != address {
				out = append(out, k)
			}
		}
		c.KnownWiFi = out
	})
}

func (s *Store) save(c Config) error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func clone(c Config) Config {
	out := c
	out.Devices = make(map[string]*DeviceProfile, len(c.Devices))
	for k, v := range c.Devices {
		if v == nil {
			continue
		}
		p := *v
		if v.Override != nil {
			o := *v.Override
			p.Override = &o
		}
		out.Devices[k] = &p
	}
	out.KnownWiFi = append([]KnownWiFi(nil), c.KnownWiFi...)
	return out
}
