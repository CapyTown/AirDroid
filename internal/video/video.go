// Package video is what the decoders share: the pictures they make, for a
// ui.Bitmap, and how big to make them.
package video

import (
	"image"
	"sync"

	"github.com/CapyTown/AirDroid/internal/media"
)

// Decoder decodes one stream of length-prefixed H.264 or H.265 access
// units into pictures. Its methods are called from the stream's goroutine,
// except SetTargetSize, which may be called from any.
type Decoder interface {
	// SetTargetSize sets the box, in pixels, that pictures are scaled to
	// fit; 0×0 keeps the stream's size.
	SetTargetSize(w, h int)
	// SetFormat starts decoding frames of new parameter sets.
	SetFormat(p *media.ParameterSets) error
	// Decode decodes an access unit.
	Decode(data []byte, key bool) error
	Close()
}

// Output receives decoded pictures. The picture belongs to the decoder
// again once Release is called for it.
type Output func(img *image.RGBA, release func())

// Pool hands out pictures for decoded frames, three at a time: one shown,
// one waiting to show, one being written.
type Pool struct {
	mu   sync.Mutex
	free []*image.RGBA
}

// Get returns a picture of w×h.
func (p *Pool) Get(w, h int) *image.RGBA {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, img := range p.free {
		if img.Rect.Dx() == w && img.Rect.Dy() == h {
			p.free = append(p.free[:i], p.free[i+1:]...)
			return img
		}
	}
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

// Put returns a picture to the pool.
func (p *Pool) Put(img *image.RGBA) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.free) < 3 {
		p.free = append(p.free, img)
	}
}

// FitSize returns the size to decode a srcW×srcH picture to so that it
// fills a box of boxW×boxH pixels, keeping its aspect, and is never larger
// than it was.
func FitSize(srcW, srcH, boxW, boxH int) (int, int) {
	if srcW <= 0 || srcH <= 0 {
		return 0, 0
	}
	if boxW <= 0 || boxH <= 0 || (boxW >= srcW && boxH >= srcH) {
		return srcW, srcH
	}
	w, h := boxW, boxW*srcH/srcW
	if h > boxH {
		w, h = boxH*srcW/srcH, boxH
	}
	w, h = max(w&^1, 2), max(h&^1, 2)
	return w, h
}
