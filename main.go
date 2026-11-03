// MyGo mirrors and controls Android devices from the macOS menu bar.
//
// It is written in Go alone with the MyGo framework: its windows are
// MyGo's native UI, drawn on the GPU, and it reaches the system through
// purego, without cgo. Devices are reached with adb, and scrcpy-server
// streams their screens.
package main

import (
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"

	"github.com/CapyTown/AirDroid/internal/app"
)

func main() {
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	setupLogging()
	a, err := app.New()
	if err != nil {
		log.Fatal(err)
	}
	mygo.App.WhenReady(a.Start)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// setupLogging writes the log to ~/Library/Logs/MyGo/mygo.log, and to the
// terminal during development.
func setupLogging() {
	level := slog.LevelInfo
	if os.Getenv("MYGO_DEBUG") != "" {
		level = slog.LevelDebug
	}
	var out io.Writer = os.Stderr
	if dir, err := mygo.App.Path(mygo.PathLogs); err == nil {
		path := filepath.Join(dir, "mygo.log")
		if st, err := os.Stat(path); err == nil && st.Size() > 8<<20 {
			_ = os.Rename(path, path+".1")
		}
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			if mygo.IsDev() {
				out = io.MultiWriter(os.Stderr, f)
			} else {
				out = f
			}
		}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})))
}
