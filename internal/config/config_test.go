package config

import (
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AirDroid", "config.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if o, own := s.MirrorOptionsFor("abc"); own || o != DefaultMirrorOptions() {
		t.Errorf("defaults: %+v %v", o, own)
	}
	err = s.Update(func(c *Config) {
		c.Mirror.BitRateMbps = 16
		o := c.Mirror
		o.Codec = "vp9" // not offered: normalized away
		o.MaxSize = 1280
		c.Devices["abc"] = &DeviceProfile{Name: "Pixel", Override: &o}
	})
	if err != nil {
		t.Fatal(err)
	}
	s.RememberWiFi("192.168.1.2:5555", "Pixel", "abc")

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := s2.Get()
	if c.Mirror.BitRateMbps != 16 {
		t.Errorf("bit rate %d", c.Mirror.BitRateMbps)
	}
	o, own := s2.MirrorOptionsFor("abc")
	if !own || o.MaxSize != 1280 || o.Codec != CodecH264 {
		t.Errorf("override %+v %v", o, own)
	}
	if len(c.KnownWiFi) != 1 || c.KnownWiFi[0].Address != "192.168.1.2:5555" {
		t.Errorf("known %+v", c.KnownWiFi)
	}
	// Get returns a copy.
	c.Devices["abc"].Override.MaxSize = 1
	if o, _ := s2.MirrorOptionsFor("abc"); o.MaxSize != 1280 {
		t.Error("Get shares the store's memory")
	}
}

func TestRequiresRestart(t *testing.T) {
	a := DefaultMirrorOptions()
	b := a
	b.AlwaysOnTop, b.TurnScreenOff = true, true
	if a.RequiresRestart(b) {
		t.Error("window and display options apply live")
	}
	b.BitRateMbps = 4
	if !a.RequiresRestart(b) {
		t.Error("the bit rate needs a new stream")
	}
}

func TestDefaultsSaveToDownloads(t *testing.T) {
	c := Default()
	if filepath.Base(c.ScreenshotDir) != "Downloads" || filepath.Base(c.RecordingDir) != "Downloads" {
		t.Errorf("screenshots %q, recordings %q", c.ScreenshotDir, c.RecordingDir)
	}
	// An emptied folder falls back to Downloads too.
	s, _ := Open(filepath.Join(t.TempDir(), "c.json"))
	s.Update(func(c *Config) { c.ScreenshotDir, c.RecordingDir = "", "" })
	if got := s.Get(); got.ScreenshotDir != c.ScreenshotDir || got.RecordingDir != c.RecordingDir {
		t.Errorf("empty folders: %q %q", got.ScreenshotDir, got.RecordingDir)
	}
}
