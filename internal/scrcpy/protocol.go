// Package scrcpy is a client of scrcpy-server, the program that the scrcpy
// project pushes to an Android device to capture its screen and audio and
// inject input.
//
// AirDroid starts the server itself (adb push, app_process), receives its
// encoded video and audio streams over adb tunnels, and sends it control
// messages, as the scrcpy client does. The encoded stream reaches the
// decoder and, while recording, the file as it is: nothing is decoded or
// encoded again to record.
package scrcpy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

// Codec ids, the four ASCII bytes the server sends.
const (
	CodecH264 uint32 = 0x68323634 // "h264"
	CodecH265 uint32 = 0x68323635 // "h265"
	CodecAV1  uint32 = 0x00617631 // "av1"
	CodecOpus uint32 = 0x6f707573 // "opus"
	CodecAAC  uint32 = 0x00616163 // "aac"
	CodecFLAC uint32 = 0x666c6163 // "flac"
	CodecRaw  uint32 = 0x00726177 // "raw": PCM s16le, 48 kHz, stereo
)

// CodecName returns the name of a codec id.
func CodecName(id uint32) string {
	switch id {
	case CodecH264:
		return "H.264"
	case CodecH265:
		return "H.265"
	case CodecAV1:
		return "AV1"
	case CodecOpus:
		return "Opus"
	case CodecAAC:
		return "AAC"
	case CodecFLAC:
		return "FLAC"
	case CodecRaw:
		return "PCM"
	}
	return fmt.Sprintf("0x%08x", id)
}

// Protocol is the stream framing a server version speaks.
type Protocol int

const (
	// ProtocolV2 is scrcpy 2.x and 3.x: a 12-byte codec header with the
	// video size, then packets flagged config (bit 63) and key frame
	// (bit 62).
	ProtocolV2 Protocol = 2
	// ProtocolV4 is scrcpy 4.0 and later: a 4-byte codec id, then session
	// headers (bit 63) carrying the video size before each new
	// configuration, and packets flagged config (bit 62) and key frame
	// (bit 61).
	ProtocolV4 Protocol = 4
)

// ProtocolFor returns the framing of a server version, as "5.0.1".
func ProtocolFor(version string) Protocol {
	var major int
	fmt.Sscanf(version, "%d", &major)
	if major >= 4 {
		return ProtocolV4
	}
	return ProtocolV2
}

// Packet is a media packet of a stream.
type Packet struct {
	PTS      int64 // microseconds, on the device's monotonic clock
	Config   bool  // codec configuration (SPS/PPS…), not media
	KeyFrame bool
	Data     []byte
}

// Header is what precedes a packet: a media packet's flags and size, or a
// session's video size.
type Header struct {
	Session       bool
	Width, Height int // a session's
	ClientResized bool
	Packet        Packet // a media packet's flags and PTS
	Size          int    // a media packet's size
}

// ReadHeader reads the 12-byte header of the next packet.
func ReadHeader(r io.Reader, p Protocol, buf *[12]byte) (Header, error) {
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return Header{}, err
	}
	return ParseHeader(buf[:], p)
}

// ParseHeader parses a 12-byte packet header.
func ParseHeader(b []byte, p Protocol) (Header, error) {
	v := binary.BigEndian.Uint64(b[0:8])
	size := binary.BigEndian.Uint32(b[8:12])
	if p == ProtocolV4 {
		if v&(1<<63) != 0 {
			return Header{
				Session:       true,
				ClientResized: b[3]&1 != 0,
				Width:         int(binary.BigEndian.Uint32(b[4:8])),
				Height:        int(size),
			}, nil
		}
		h := Header{Size: int(size)}
		h.Packet.Config = v&(1<<62) != 0
		h.Packet.KeyFrame = v&(1<<61) != 0
		if !h.Packet.Config {
			h.Packet.PTS = int64(v & (1<<61 - 1))
		}
		return h, validSize(h.Size)
	}
	h := Header{Size: int(size)}
	h.Packet.Config = v&(1<<63) != 0
	h.Packet.KeyFrame = v&(1<<62) != 0
	if !h.Packet.Config {
		h.Packet.PTS = int64(v & (1<<62 - 1))
	}
	return h, validSize(h.Size)
}

func validSize(n int) error {
	if n <= 0 || n > 64<<20 {
		return fmt.Errorf("scrcpy: invalid packet size %d", n)
	}
	return nil
}

// Control message types.
const (
	msgInjectKeycode           = 0
	msgInjectText              = 1
	msgInjectTouch             = 2
	msgInjectScroll            = 3
	msgBackOrScreenOn          = 4
	msgExpandNotificationPanel = 5
	msgExpandSettingsPanel     = 6
	msgCollapsePanels          = 7
	msgGetClipboard            = 8
	msgSetClipboard            = 9
	msgSetDisplayPower         = 10
	msgRotateDevice            = 11
	msgOpenHardKeyboardSetting = 15
	msgStartApp                = 16
	msgResetVideo              = 17
)

// Android key and motion event actions.
const (
	ActionDown = 0
	ActionUp   = 1
	ActionMove = 2
)

// Mouse buttons of Android motion events.
const (
	ButtonPrimary   = 1 << 0
	ButtonSecondary = 1 << 1
	ButtonTertiary  = 1 << 2
)

// Pointer ids the server treats specially.
const (
	PointerMouse         = ^uint64(0)     // -1
	PointerGenericFinger = ^uint64(0) - 1 // -2
)

// Android meta state flags.
const (
	MetaShiftOn  = 0x01 | 0x40
	MetaAltOn    = 0x02 | 0x10
	MetaCtrlOn   = 0x1000 | 0x2000
	MetaMetaOn   = 0x10000 | 0x20000
	MetaCapsLock = 0x100000
)

// Copy keys of GetClipboard.
const (
	CopyKeyNone = 0
	CopyKeyCopy = 1
	CopyKeyCut  = 2
)

// MaxTextLength is the longest text InjectText sends at once.
const MaxTextLength = 300

// Position is a point on the device's screen, in the pixels of the video
// frame it was computed on: the server drops events computed for another
// frame size.
type Position struct {
	X, Y          int32
	Width, Height uint16
}

func putPosition(b []byte, p Position) {
	binary.BigEndian.PutUint32(b[0:], uint32(p.X))
	binary.BigEndian.PutUint32(b[4:], uint32(p.Y))
	binary.BigEndian.PutUint16(b[8:], p.Width)
	binary.BigEndian.PutUint16(b[10:], p.Height)
}

// InjectKeycode encodes a key event.
func InjectKeycode(action, keycode int, repeat int, metastate int) []byte {
	b := make([]byte, 14)
	b[0] = msgInjectKeycode
	b[1] = byte(action)
	binary.BigEndian.PutUint32(b[2:], uint32(keycode))
	binary.BigEndian.PutUint32(b[6:], uint32(repeat))
	binary.BigEndian.PutUint32(b[10:], uint32(metastate))
	return b
}

// InjectText encodes text to type, truncated to MaxTextLength bytes on a
// character boundary.
func InjectText(text string) []byte {
	text = truncateUTF8(text, MaxTextLength)
	b := make([]byte, 5+len(text))
	b[0] = msgInjectText
	binary.BigEndian.PutUint32(b[1:], uint32(len(text)))
	copy(b[5:], text)
	return b
}

// InjectTouch encodes a touch or mouse event.
func InjectTouch(action int, pointerID uint64, pos Position, pressure float32, actionButton, buttons int) []byte {
	b := make([]byte, 32)
	b[0] = msgInjectTouch
	b[1] = byte(action)
	binary.BigEndian.PutUint64(b[2:], pointerID)
	putPosition(b[10:], pos)
	binary.BigEndian.PutUint16(b[22:], floatToU16FP(pressure))
	binary.BigEndian.PutUint32(b[24:], uint32(actionButton))
	binary.BigEndian.PutUint32(b[28:], uint32(buttons))
	return b
}

// InjectScroll encodes a scroll by hscroll and vscroll notches, each
// within [-16, 16]; positive vscroll scrolls up, positive hscroll right.
func InjectScroll(pos Position, hscroll, vscroll float32, buttons int) []byte {
	b := make([]byte, 21)
	b[0] = msgInjectScroll
	putPosition(b[1:], pos)
	binary.BigEndian.PutUint16(b[13:], uint16(floatToI16FP(clamp(hscroll/16, -1, 1))))
	binary.BigEndian.PutUint16(b[15:], uint16(floatToI16FP(clamp(vscroll/16, -1, 1))))
	binary.BigEndian.PutUint32(b[17:], uint32(buttons))
	return b
}

// BackOrScreenOn presses Back, or turns the screen on when it is off.
func BackOrScreenOn(action int) []byte { return []byte{msgBackOrScreenOn, byte(action)} }

// ExpandNotificationPanel pulls the notification shade down.
func ExpandNotificationPanel() []byte { return []byte{msgExpandNotificationPanel} }

// ExpandSettingsPanel pulls the quick settings down.
func ExpandSettingsPanel() []byte { return []byte{msgExpandSettingsPanel} }

// CollapsePanels closes the shade.
func CollapsePanels() []byte { return []byte{msgCollapsePanels} }

// GetClipboard asks for the device's clipboard, after pressing Copy or Cut.
func GetClipboard(copyKey int) []byte { return []byte{msgGetClipboard, byte(copyKey)} }

// SetClipboard sets the device's clipboard, and pastes it when paste is
// set; the device acknowledges sequence when it is not 0.
func SetClipboard(sequence uint64, text string, paste bool) []byte {
	const max = 1<<18 - 14
	text = truncateUTF8(text, max)
	b := make([]byte, 14+len(text))
	b[0] = msgSetClipboard
	binary.BigEndian.PutUint64(b[1:], sequence)
	if paste {
		b[9] = 1
	}
	binary.BigEndian.PutUint32(b[10:], uint32(len(text)))
	copy(b[14:], text)
	return b
}

// SetDisplayPower turns the device's display on or off, mirroring on.
func SetDisplayPower(on bool) []byte {
	b := []byte{msgSetDisplayPower, 0}
	if on {
		b[1] = 1
	}
	return b
}

// RotateDevice turns the device between portrait and landscape.
func RotateDevice() []byte { return []byte{msgRotateDevice} }

// ResetVideo has the server restart its encoder, which then sends a new
// configuration and a key frame.
func ResetVideo() []byte { return []byte{msgResetVideo} }

// StartApp starts an app by package name.
func StartApp(name string) []byte {
	name = truncateUTF8(name, 255)
	b := make([]byte, 2+len(name))
	b[0] = msgStartApp
	b[1] = byte(len(name))
	copy(b[2:], name)
	return b
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	i := max
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

func clamp(v, lo, hi float32) float32 {
	return float32(math.Max(float64(lo), math.Min(float64(hi), float64(v))))
}

func floatToU16FP(f float32) uint16 {
	f = clamp(f, 0, 1)
	u := uint32(f * 65536)
	if u >= 0xffff {
		u = 0xffff
	}
	return uint16(u)
}

func floatToI16FP(f float32) int16 {
	i := int32(f * 32768)
	if i >= 0x7fff {
		i = 0x7fff
	}
	if i < -0x8000 {
		i = -0x8000
	}
	return int16(i)
}

// Device messages.
const (
	deviceMsgClipboard    = 0
	deviceMsgAckClipboard = 1
	deviceMsgUHIDOutput   = 2
)

// DeviceMessage is a message from the device on the control socket.
type DeviceMessage struct {
	Clipboard *string // the device's clipboard changed
	Ack       uint64  // the device set the clipboard of this sequence
}

// ReadDeviceMessage reads the next device message.
func ReadDeviceMessage(r io.Reader) (DeviceMessage, error) {
	var t [1]byte
	if _, err := io.ReadFull(r, t[:]); err != nil {
		return DeviceMessage{}, err
	}
	switch t[0] {
	case deviceMsgClipboard:
		var n [4]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return DeviceMessage{}, err
		}
		size := binary.BigEndian.Uint32(n[:])
		if size > 1<<18 {
			return DeviceMessage{}, errors.New("scrcpy: clipboard too large")
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			return DeviceMessage{}, err
		}
		s := string(buf)
		return DeviceMessage{Clipboard: &s}, nil
	case deviceMsgAckClipboard:
		var n [8]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return DeviceMessage{}, err
		}
		return DeviceMessage{Ack: binary.BigEndian.Uint64(n[:])}, nil
	case deviceMsgUHIDOutput:
		var h [4]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return DeviceMessage{}, err
		}
		size := binary.BigEndian.Uint16(h[2:])
		if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
			return DeviceMessage{}, err
		}
		return DeviceMessage{}, nil
	default:
		return DeviceMessage{}, fmt.Errorf("scrcpy: unknown device message %d", t[0])
	}
}
