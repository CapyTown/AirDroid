package app

import (
	"runtime"
	"strings"

	"github.com/CapyTown/AirDroid/internal/adb"
	"github.com/CapyTown/AirDroid/locales"
)

// goos is the platform the app's text follows; tests set it.
var goos = runtime.GOOS

// keys writes a shortcut as the platform does: "⇧⌘R" on macOS, "Ctrl+Shift+R"
// elsewhere. spec is "Shift+R", "R": the Command or Control key is implied.
func keys(spec string) string {
	parts := strings.Split(spec, "+")
	key := parts[len(parts)-1]
	shift := len(parts) > 1 && parts[0] == "Shift"
	if goos == "darwin" {
		s := "⌘" + key
		if shift {
			s = "⇧" + s
		}
		return s
	}
	if shift {
		return "Ctrl+Shift+" + key
	}
	return "Ctrl+" + key
}

// revealLabel names the file manager's action that shows a file.
func revealLabel() string {
	switch goos {
	case "darwin":
		return locales.ShowInFinder()
	case "windows":
		return locales.ShowInExplorer()
	}
	return locales.ShowInFolder()
}

// hasDock reports a Dock that AirDroid's icon can leave, on macOS.
func hasDock() bool { return goos == "darwin" }

// thisComputer is how the app's text names the computer it runs on.
func thisComputer() string {
	if goos == "darwin" {
		return locales.ThisMac()
	}
	return locales.ThisComputer()
}

// deviceSubtitle is what follows a device's name in its window's title
// bar: "· Android 15".
func deviceSubtitle(info adb.Info, serial string) string {
	if info.Release == "" {
		return ""
	}
	return "· Android " + info.Release
}
