package app

import (
	"runtime"
	"strings"
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
		return "Show in Finder"
	case "windows":
		return "Show in Explorer"
	}
	return "Show in Folder"
}

// hasDock reports a Dock that AirDroid's icon can leave, on macOS.
func hasDock() bool { return goos == "darwin" }
