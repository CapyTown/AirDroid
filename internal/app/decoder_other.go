//go:build !darwin

package app

import (
	"github.com/CapyTown/AirDroid/internal/ffdec"
	"github.com/CapyTown/AirDroid/internal/video"
)

// newDecoder decodes with FFmpeg, bundled with AirDroid on Windows and
// Linux.
func newDecoder(a *App, out video.Output, needKey func()) (video.Decoder, error) {
	return ffdec.New(a.ffmpegPath(), out, needKey)
}
