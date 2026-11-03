package app

import (
	"github.com/CapyTown/AirDroid/internal/video"
	"github.com/CapyTown/AirDroid/internal/vt"
)

// newDecoder decodes with VideoToolbox, on the GPU.
func newDecoder(a *App, out video.Output, needKey func()) (video.Decoder, error) {
	return vt.NewDecoder(out, needKey)
}
