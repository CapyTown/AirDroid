package scrcpy

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// The expected bytes follow scrcpy's control_msg.c serialization.
func TestControlMessages(t *testing.T) {
	pos := Position{X: 100, Y: 200, Width: 1080, Height: 2400}
	for _, tc := range []struct {
		name string
		got  []byte
		want string
	}{
		{"keycode", InjectKeycode(ActionUp, 66, 5, MetaShiftOn), "00" + "01" + "00000042" + "00000005" + "00000041"},
		{"text", InjectText("hi"), "01" + "00000002" + "6869"},
		{"touch", InjectTouch(ActionDown, PointerMouse, pos, 1, ButtonPrimary, ButtonPrimary),
			"02" + "00" + "ffffffffffffffff" + "00000064" + "000000c8" + "0438" + "0960" + "ffff" + "00000001" + "00000001"},
		{"scroll", InjectScroll(pos, 0, 1, 0), "03" + "00000064" + "000000c8" + "0438" + "0960" + "0000" + "0800" + "00000000"},
		{"back", BackOrScreenOn(ActionDown), "0400"},
		{"clipboard", SetClipboard(7, "ab", true), "09" + "0000000000000007" + "01" + "00000002" + "6162"},
		{"power", SetDisplayPower(false), "0a00"},
		{"rotate", RotateDevice(), "0b"},
		{"reset", ResetVideo(), "11"},
	} {
		if got := hex.EncodeToString(tc.got); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestInjectTextTruncatesOnRuneBoundary(t *testing.T) {
	s := ""
	for len(s) < MaxTextLength-1 {
		s += "a"
	}
	s += "é" // 2 bytes, crossing the limit
	msg := InjectText(s)
	if n := len(msg) - 5; n != MaxTextLength-1 {
		t.Errorf("text of %d bytes, want %d", n, MaxTextLength-1)
	}
}

func TestParseHeader(t *testing.T) {
	// v4: session
	h, err := ParseHeader([]byte{0x80, 0, 0, 1, 0, 0, 0x04, 0x38, 0, 0, 0x09, 0x60}, ProtocolV4)
	if err != nil || !h.Session || h.Width != 1080 || h.Height != 2400 || !h.ClientResized {
		t.Errorf("session: %+v %v", h, err)
	}
	// v4: key frame at pts 1000
	h, err = ParseHeader([]byte{0x20, 0, 0, 0, 0, 0, 0x03, 0xe8, 0, 0, 0, 10}, ProtocolV4)
	if err != nil || h.Session || !h.Packet.KeyFrame || h.Packet.Config || h.Packet.PTS != 1000 || h.Size != 10 {
		t.Errorf("v4 frame: %+v %v", h, err)
	}
	// v2: config
	h, err = ParseHeader([]byte{0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 20}, ProtocolV2)
	if err != nil || !h.Packet.Config || h.Size != 20 {
		t.Errorf("v2 config: %+v %v", h, err)
	}
	// v2: key frame
	h, _ = ParseHeader([]byte{0x40, 0, 0, 0, 0, 0, 0, 5, 0, 0, 0, 1}, ProtocolV2)
	if !h.Packet.KeyFrame || h.Packet.PTS != 5 {
		t.Errorf("v2 frame: %+v", h)
	}
}

func TestProtocolFor(t *testing.T) {
	for v, want := range map[string]Protocol{"2.4": ProtocolV2, "3.3.4": ProtocolV2, "4.0": ProtocolV4, "5.0.1": ProtocolV4} {
		if got := ProtocolFor(v); got != want {
			t.Errorf("%s: %v", v, got)
		}
	}
}

func TestReadDeviceMessage(t *testing.T) {
	r := bytes.NewReader([]byte{0, 0, 0, 0, 3, 'a', 'b', 'c', 1, 0, 0, 0, 0, 0, 0, 0, 9})
	m, err := ReadDeviceMessage(r)
	if err != nil || m.Clipboard == nil || *m.Clipboard != "abc" {
		t.Fatalf("clipboard: %+v %v", m, err)
	}
	m, err = ReadDeviceMessage(r)
	if err != nil || m.Ack != 9 {
		t.Fatalf("ack: %+v %v", m, err)
	}
}

func TestParseVersions(t *testing.T) {
	if v := ParseScrcpyVersion("scrcpy 5.0.1 <https://github.com/Genymobile/scrcpy>\n"); v != "5.0.1" {
		t.Errorf("scrcpy --version: %q", v)
	}
	if v := ServerVersionFromMismatch("[server] ERROR: The server version (3.3.4) does not match the client (5.0)"); v != "3.3.4" {
		t.Errorf("mismatch: %q", v)
	}
}

// The bundling scripts record the server's version in origin.json.
func TestResolveServerVersionFromOrigin(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "scrcpy-server"), []byte("jar"), 0o644)
	os.WriteFile(filepath.Join(dir, "origin.json"), []byte(`{"version": "5.0.1", "architecture": "amd64"}`), 0o644)
	// ResolveServer resolves links, as /var to /private/var on macOS and a
	// short name on Windows.
	want, err := filepath.EvalSymlinks(filepath.Join(dir, "scrcpy-server"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "scrcpy-server"), dir} {
		s, err := ResolveServer(context.Background(), path)
		if err != nil || s.Version != "5.0.1" || s.Path != want {
			t.Errorf("%s: %+v %v", path, s, err)
		}
	}
}
