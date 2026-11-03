//go:build !darwin

package vt

import "github.com/CapyTown/AirDroid/internal/media"

// Decoder is unavailable off macOS.
type Decoder struct{}

// NewDecoder returns ErrUnsupported off macOS.
func NewDecoder(out Output, needKey func()) (*Decoder, error) { return nil, ErrUnsupported }

func (d *Decoder) SetTargetSize(w, h int)                 {}
func (d *Decoder) Size() (int, int)                       { return 0, 0 }
func (d *Decoder) SetFormat(p *media.ParameterSets) error { return ErrUnsupported }
func (d *Decoder) Decode(data []byte, key bool) error     { return ErrUnsupported }
func (d *Decoder) Close()                                 {}
