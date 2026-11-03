package app

import (
	"github.com/egoist/mygo/ui"

	"github.com/CapyTown/AirDroid/internal/scrcpy"
)

// Android key codes (android.view.KeyEvent).
const (
	akHome         = 3
	akBack         = 4
	akVolumeUp     = 24
	akVolumeDown   = 25
	akPower        = 26
	akA            = 29
	akComma        = 55
	akPeriod       = 56
	akTab          = 61
	akSpace        = 62
	akEnter        = 66
	akDel          = 67
	akGrave        = 68
	akMinus        = 69
	akEquals       = 70
	akLeftBracket  = 71
	akRightBracket = 72
	akBackslash    = 73
	akSemicolon    = 74
	akApostrophe   = 75
	akSlash        = 76
	akMenu         = 82
	akPageUp       = 92
	akPageDown     = 93
	akEscape       = 111
	akForwardDel   = 112
	akMoveHome     = 122
	akMoveEnd      = 123
	akInsert       = 124
	akF1           = 131
	akAppSwitch    = 187
	akCut          = 277
	akCopy         = 278
	akPaste        = 279
	akDpadUp       = 19
	akDpadDown     = 20
	akDpadLeft     = 21
	akDpadRight    = 22
	ak0            = 7
)

// androidKey maps a key to its Android key code; 0 when it has none.
func androidKey(k ui.Key) int {
	switch {
	case k >= ui.KeyA && k <= ui.KeyZ:
		return akA + int(k-ui.KeyA)
	case k >= ui.Key0 && k <= ui.Key9:
		return ak0 + int(k-ui.Key0)
	case k >= ui.KeyF1 && k <= ui.KeyF12:
		return akF1 + int(k-ui.KeyF1)
	}
	switch k {
	case ui.KeyEnter:
		return akEnter
	case ui.KeyEscape:
		return akEscape
	case ui.KeyBackspace:
		return akDel
	case ui.KeyDelete:
		return akForwardDel
	case ui.KeyTab:
		return akTab
	case ui.KeySpace:
		return akSpace
	case ui.KeyInsert:
		return akInsert
	case ui.KeyHome:
		return akMoveHome
	case ui.KeyEnd:
		return akMoveEnd
	case ui.KeyPageUp:
		return akPageUp
	case ui.KeyPageDown:
		return akPageDown
	case ui.KeyLeft:
		return akDpadLeft
	case ui.KeyRight:
		return akDpadRight
	case ui.KeyUp:
		return akDpadUp
	case ui.KeyDown:
		return akDpadDown
	case ui.KeyMinus:
		return akMinus
	case ui.KeyEqual:
		return akEquals
	case ui.KeyComma:
		return akComma
	case ui.KeyPeriod:
		return akPeriod
	case ui.KeySlash:
		return akSlash
	case ui.KeySemicolon:
		return akSemicolon
	case ui.KeyQuote:
		return akApostrophe
	case ui.KeyBracketLeft:
		return akLeftBracket
	case ui.KeyBracketRight:
		return akRightBracket
	case ui.KeyBackslash:
		return akBackslash
	case ui.KeyBackquote:
		return akGrave
	case ui.KeyContextMenu:
		return akMenu
	case ui.KeyBack:
		return akBack
	}
	return 0
}

// textKey reports keys whose characters are sent as text rather than as
// key events when no modifier but Shift is held: letters, digits, signs and
// space. The input method turns them into text, of any layout or language.
func textKey(k ui.Key) bool {
	switch {
	case k >= ui.KeyA && k <= ui.KeyZ, k >= ui.Key0 && k <= ui.Key9:
		return true
	}
	switch k {
	case ui.KeySpace, ui.KeyMinus, ui.KeyEqual, ui.KeyComma, ui.KeyPeriod, ui.KeySlash,
		ui.KeySemicolon, ui.KeyQuote, ui.KeyBracketLeft, ui.KeyBracketRight,
		ui.KeyBackslash, ui.KeyBackquote:
		return true
	}
	return false
}

// metaState converts modifiers to Android's meta state. Command is not
// forwarded: it drives MyGo's own shortcuts.
func metaState(m ui.Modifiers) int {
	s := 0
	if m&ui.Shift != 0 {
		s |= scrcpy.MetaShiftOn
	}
	if m&ui.Ctrl != 0 {
		s |= scrcpy.MetaCtrlOn
	}
	if m&ui.Alt != 0 {
		s |= scrcpy.MetaAltOn
	}
	return s
}

// isASCII reports whether Android's key character map types s; other text
// goes through the clipboard.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 || (s[i] < 0x20 && s[i] != '\n' && s[i] != '\t') {
			return false
		}
	}
	return true
}
