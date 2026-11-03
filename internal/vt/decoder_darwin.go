//go:build darwin

// Package vt decodes H.264 and H.265 with the system's hardware decoder,
// VideoToolbox, called from Go through purego: no cgo.
//
// Frames decode on the GPU, are scaled there to the size they show at, and
// come out as RGBA pictures for a ui.Bitmap. Only that picture, at the
// window's size rather than the device's, crosses to the CPU.
package vt

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/CapyTown/AirDroid/internal/media"
	"github.com/CapyTown/AirDroid/internal/video"
)

// Native types are pointers (CFTypeRef) or integers, passed as uintptr.
type cfRef = uintptr

var (
	loadOnce sync.Once
	loadErr  error

	cfRelease          func(cfRef)
	cfRetain           func(cfRef) cfRef
	cfDictionaryCreate func(alloc uintptr, keys, values unsafe.Pointer, n int, keyCB, valueCB uintptr) cfRef
	cfNumberCreate     func(alloc uintptr, typ int, value unsafe.Pointer) cfRef

	cmFormatFromH264 func(alloc uintptr, n uintptr, ptrs, sizes unsafe.Pointer, nalLen int32, out *cfRef) int32
	cmFormatFromHEVC func(alloc uintptr, n uintptr, ptrs, sizes unsafe.Pointer, nalLen int32, ext uintptr, out *cfRef) int32
	cmBlockCreate    func(alloc, mem uintptr, length uintptr, blockAlloc, source uintptr, offset, dataLen uintptr, flags uint32, out *cfRef) int32
	cmBlockReplace   func(src unsafe.Pointer, dest cfRef, offset, length uintptr) int32
	cmSampleCreate   func(alloc, data, format uintptr, numSamples, numTiming int, timing unsafe.Pointer, numSizes int, sizes unsafe.Pointer, out *cfRef) int32

	vtSessionCreate   func(alloc, format, spec, attrs uintptr, cb unsafe.Pointer, out *cfRef) int32
	vtDecodeFrame     func(session, sample uintptr, flags uint32, refcon uintptr, infoOut *uint32) int32
	vtWaitAsync       func(session uintptr) int32
	vtInvalidate      func(session uintptr)
	vtCanAccept       func(session, format uintptr) uint8
	vtTransferCreate  func(alloc uintptr, out *cfRef) int32
	vtTransferImage   func(session, src, dst uintptr) int32
	vtTransferInvalid func(session uintptr)

	cvBufferCreate    func(alloc uintptr, w, h uintptr, format uint32, attrs uintptr, out *cfRef) int32
	cvLock            func(buf uintptr, flags uint64) int32
	cvUnlock          func(buf uintptr, flags uint64) int32
	cvBaseAddress     func(buf uintptr) uintptr
	cvBytesPerRow     func(buf uintptr) uintptr
	cvWidth           func(buf uintptr) uintptr
	cvHeight          func(buf uintptr) uintptr
	vImagePermuteARGB func(src, dst unsafe.Pointer, perm unsafe.Pointer, flags uint32) int

	keyCallbacks, valueCallbacks uintptr
	keyPixelFormat, keyIOSurface uintptr

	outputCallback uintptr // the decoder's output callback, made once
)

const (
	pixelFormatBGRA      = 0x42475241 // 'BGRA'
	cfNumberSInt32       = 3
	blockAssureMemoryNow = 1
	pixelBufferLockRead  = 1
	errInvalidSession    = -12903
	errBadData           = -12909
	errMalfunction       = -12911
)

func load() error {
	loadOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				loadErr = fmt.Errorf("vt: %v", r)
			}
		}()
		open := func(path string) uintptr {
			h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil {
				panic(err)
			}
			return h
		}
		cf := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		cm := open("/System/Library/Frameworks/CoreMedia.framework/CoreMedia")
		cv := open("/System/Library/Frameworks/CoreVideo.framework/CoreVideo")
		vt := open("/System/Library/Frameworks/VideoToolbox.framework/VideoToolbox")
		acc := open("/System/Library/Frameworks/Accelerate.framework/Accelerate")

		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cfRetain, cf, "CFRetain")
		purego.RegisterLibFunc(&cfDictionaryCreate, cf, "CFDictionaryCreate")
		purego.RegisterLibFunc(&cfNumberCreate, cf, "CFNumberCreate")
		purego.RegisterLibFunc(&cmFormatFromH264, cm, "CMVideoFormatDescriptionCreateFromH264ParameterSets")
		purego.RegisterLibFunc(&cmFormatFromHEVC, cm, "CMVideoFormatDescriptionCreateFromHEVCParameterSets")
		purego.RegisterLibFunc(&cmBlockCreate, cm, "CMBlockBufferCreateWithMemoryBlock")
		purego.RegisterLibFunc(&cmBlockReplace, cm, "CMBlockBufferReplaceDataBytes")
		purego.RegisterLibFunc(&cmSampleCreate, cm, "CMSampleBufferCreateReady")
		purego.RegisterLibFunc(&vtSessionCreate, vt, "VTDecompressionSessionCreate")
		purego.RegisterLibFunc(&vtDecodeFrame, vt, "VTDecompressionSessionDecodeFrame")
		purego.RegisterLibFunc(&vtWaitAsync, vt, "VTDecompressionSessionWaitForAsynchronousFrames")
		purego.RegisterLibFunc(&vtInvalidate, vt, "VTDecompressionSessionInvalidate")
		purego.RegisterLibFunc(&vtCanAccept, vt, "VTDecompressionSessionCanAcceptFormatDescription")
		purego.RegisterLibFunc(&vtTransferCreate, vt, "VTPixelTransferSessionCreate")
		purego.RegisterLibFunc(&vtTransferImage, vt, "VTPixelTransferSessionTransferImage")
		purego.RegisterLibFunc(&vtTransferInvalid, vt, "VTPixelTransferSessionInvalidate")
		purego.RegisterLibFunc(&cvBufferCreate, cv, "CVPixelBufferCreate")
		purego.RegisterLibFunc(&cvLock, cv, "CVPixelBufferLockBaseAddress")
		purego.RegisterLibFunc(&cvUnlock, cv, "CVPixelBufferUnlockBaseAddress")
		purego.RegisterLibFunc(&cvBaseAddress, cv, "CVPixelBufferGetBaseAddress")
		purego.RegisterLibFunc(&cvBytesPerRow, cv, "CVPixelBufferGetBytesPerRow")
		purego.RegisterLibFunc(&cvWidth, cv, "CVPixelBufferGetWidth")
		purego.RegisterLibFunc(&cvHeight, cv, "CVPixelBufferGetHeight")
		purego.RegisterLibFunc(&vImagePermuteARGB, acc, "vImagePermuteChannels_ARGB8888")

		sym := func(h uintptr, name string) uintptr {
			p, err := purego.Dlsym(h, name)
			if err != nil {
				panic(err)
			}
			return p
		}
		keyCallbacks = sym(cf, "kCFTypeDictionaryKeyCallBacks")
		valueCallbacks = sym(cf, "kCFTypeDictionaryValueCallBacks")
		// CFStringRef globals: the symbol is the variable's address.
		keyPixelFormat = deref(sym(cv, "kCVPixelBufferPixelFormatTypeKey"))
		keyIOSurface = deref(sym(cv, "kCVPixelBufferIOSurfacePropertiesKey"))

		// VTDecompressionOutputCallback(refCon, sourceFrameRefCon, status,
		// infoFlags, imageBuffer, pts, duration). The two CMTimes after
		// imageBuffer are not read.
		outputCallback = purego.NewCallback(func(refCon, frameRefCon, status, flags, imageBuffer uintptr) {
			decoders.mu.Lock()
			d := decoders.m[refCon]
			decoders.mu.Unlock()
			if d != nil {
				d.decoded(int32(status), imageBuffer)
			}
		})
	})
	return loadErr
}

var decoders = struct {
	mu   sync.Mutex
	next uintptr
	m    map[uintptr]*Decoder
}{m: map[uintptr]*Decoder{}}

// callbackRecord is VTDecompressionOutputCallbackRecord. VideoToolbox
// copies it, but it lives with the decoder anyway.
type callbackRecord struct {
	callback uintptr
	refCon   uintptr
}

// Decoder decodes one stream. Its methods are called from one goroutine,
// the stream's, except SetTargetSize.
type Decoder struct {
	id       uintptr
	out      video.Output
	needKey  func()
	pool     video.Pool
	record   *callbackRecord
	format   cfRef
	codec    media.Codec
	session  cfRef
	transfer cfRef
	scaled   cfRef // the scaled picture, reused while the size holds
	scaledW  int
	scaledH  int
	waitKey  bool
	target   atomic.Uint64 // box to fit pictures in, w<<32 | h

	// Set by the output callback during DecodeFrame.
	lastStatus int32
	lastImage  cfRef
	width      atomic.Int64
	height     atomic.Int64
}

// NewDecoder makes a decoder that gives its pictures to out and calls
// needKey when it needs a key frame to go on, as after an error.
func NewDecoder(out video.Output, needKey func()) (*Decoder, error) {
	if err := load(); err != nil {
		return nil, err
	}
	d := &Decoder{out: out, needKey: needKey, waitKey: true}
	decoders.mu.Lock()
	decoders.next++
	d.id = decoders.next
	decoders.m[d.id] = d
	decoders.mu.Unlock()
	d.record = &callbackRecord{callback: outputCallback, refCon: d.id}
	return d, nil
}

// SetTargetSize sets the box, in pixels, that pictures are scaled to fit;
// 0×0 keeps the stream's size.
func (d *Decoder) SetTargetSize(w, h int) {
	d.target.Store(uint64(uint32(w))<<32 | uint64(uint32(h)))
}

// Size returns the size of the pictures the stream decodes to.
func (d *Decoder) Size() (int, int) { return int(d.width.Load()), int(d.height.Load()) }

// SetFormat starts decoding frames of new parameter sets.
func (d *Decoder) SetFormat(p *media.ParameterSets) error {
	sets := p.All()
	ptrs := make([]uintptr, len(sets))
	sizes := make([]uintptr, len(sets))
	for i, s := range sets {
		ptrs[i] = uintptr(unsafe.Pointer(&s[0]))
		sizes[i] = uintptr(len(s))
	}
	var format cfRef
	var st int32
	if p.Codec == media.H265 {
		st = cmFormatFromHEVC(0, uintptr(len(sets)), unsafe.Pointer(&ptrs[0]), unsafe.Pointer(&sizes[0]), 4, 0, &format)
	} else {
		st = cmFormatFromH264(0, uintptr(len(sets)), unsafe.Pointer(&ptrs[0]), unsafe.Pointer(&sizes[0]), 4, &format)
	}
	runtime.KeepAlive(sets)
	if st != 0 || format == 0 {
		return fmt.Errorf("vt: invalid parameter sets (%d)", st)
	}
	if d.session != 0 && d.codec == p.Codec && vtCanAccept(d.session, format) != 0 {
		cfRelease(d.format)
		d.format = format
		return nil
	}
	d.closeSession()
	if d.format != 0 {
		cfRelease(d.format)
	}
	d.format, d.codec = format, p.Codec
	return d.openSession()
}

func (d *Decoder) openSession() error {
	// Decode to BGRA on IOSurfaces, which the GPU scales without copies.
	surface := cfDictionaryCreate(0, nil, nil, 0, keyCallbacks, valueCallbacks)
	pf := int32(pixelFormatBGRA)
	pfNum := cfNumberCreate(0, cfNumberSInt32, unsafe.Pointer(&pf))
	keys := []uintptr{keyPixelFormat, keyIOSurface}
	vals := []uintptr{pfNum, surface}
	attrs := cfDictionaryCreate(0, unsafe.Pointer(&keys[0]), unsafe.Pointer(&vals[0]), 2, keyCallbacks, valueCallbacks)
	cfRelease(pfNum)
	cfRelease(surface)
	defer cfRelease(attrs)
	var session cfRef
	if st := vtSessionCreate(0, d.format, 0, attrs, unsafe.Pointer(d.record), &session); st != 0 {
		return fmt.Errorf("vt: cannot decode this stream (%d)", st)
	}
	d.session = session
	d.waitKey = true
	return nil
}

func (d *Decoder) closeSession() {
	if d.session == 0 {
		return
	}
	vtWaitAsync(d.session)
	vtInvalidate(d.session)
	cfRelease(d.session)
	d.session = 0
}

// decoded runs in VideoToolbox's output callback.
func (d *Decoder) decoded(status int32, img cfRef) {
	d.lastStatus = status
	if status == 0 && img != 0 {
		if d.lastImage != 0 {
			cfRelease(d.lastImage)
		}
		d.lastImage = cfRetain(img)
	}
}

// Decode decodes a length-prefixed access unit and hands its picture to
// the output.
func (d *Decoder) Decode(data []byte, key bool) error {
	if d.session == 0 || len(data) == 0 {
		return nil
	}
	if d.waitKey {
		if !key {
			return nil
		}
		d.waitKey = false
	}
	var block cfRef
	if st := cmBlockCreate(0, 0, uintptr(len(data)), 0, 0, 0, uintptr(len(data)), blockAssureMemoryNow, &block); st != 0 {
		return fmt.Errorf("vt: block buffer (%d)", st)
	}
	defer cfRelease(block)
	if st := cmBlockReplace(unsafe.Pointer(&data[0]), block, 0, uintptr(len(data))); st != 0 {
		return fmt.Errorf("vt: block buffer copy (%d)", st)
	}
	size := uintptr(len(data))
	var sample cfRef
	if st := cmSampleCreate(0, block, d.format, 1, 0, nil, 1, unsafe.Pointer(&size), &sample); st != 0 {
		return fmt.Errorf("vt: sample buffer (%d)", st)
	}
	defer cfRelease(sample)

	d.lastStatus = 0
	var info uint32
	st := vtDecodeFrame(d.session, sample, 0, d.id, &info)
	if st == 0 && d.lastImage == 0 && d.lastStatus == 0 {
		vtWaitAsync(d.session)
	}
	if st == 0 {
		st = d.lastStatus
	}
	if st != 0 {
		if d.lastImage != 0 {
			cfRelease(d.lastImage)
			d.lastImage = 0
		}
		return d.recover(st)
	}
	img := d.lastImage
	d.lastImage = 0
	if img == 0 {
		return nil // a frame that shows nothing
	}
	defer cfRelease(img)
	return d.deliver(img)
}

// recover restarts decoding after an error, from the next key frame.
func (d *Decoder) recover(st int32) error {
	if st == errInvalidSession || st == errMalfunction {
		// The GPU went away (sleep, a display change): a new session.
		d.closeSession()
		if err := d.openSession(); err != nil {
			return err
		}
	}
	d.waitKey = true
	if d.needKey != nil {
		d.needKey()
	}
	return fmt.Errorf("vt: decoding failed (%d)", st)
}

// deref reads the pointer stored at address p, a global of a framework.
func deref(p uintptr) uintptr {
	return **(**uintptr)(unsafe.Pointer(&p))
}

type vImageBuffer struct {
	data     uintptr
	height   uintptr
	width    uintptr
	rowBytes uintptr
}

// deliver scales a decoded picture to the target size on the GPU, then
// copies it as RGBA into a picture of the pool.
func (d *Decoder) deliver(img cfRef) error {
	srcW, srcH := int(cvWidth(img)), int(cvHeight(img))
	d.width.Store(int64(srcW))
	d.height.Store(int64(srcH))
	t := d.target.Load()
	w, h := video.FitSize(srcW, srcH, int(t>>32), int(uint32(t)))
	src := img
	if w != srcW || h != srcH {
		if d.transfer == 0 {
			if st := vtTransferCreate(0, &d.transfer); st != 0 {
				d.transfer = 0
				return fmt.Errorf("vt: pixel transfer (%d)", st)
			}
		}
		if d.scaled == 0 || d.scaledW != w || d.scaledH != h {
			if d.scaled != 0 {
				cfRelease(d.scaled)
				d.scaled = 0
			}
			surface := cfDictionaryCreate(0, nil, nil, 0, keyCallbacks, valueCallbacks)
			keys := []uintptr{keyIOSurface}
			vals := []uintptr{surface}
			attrs := cfDictionaryCreate(0, unsafe.Pointer(&keys[0]), unsafe.Pointer(&vals[0]), 1, keyCallbacks, valueCallbacks)
			st := cvBufferCreate(0, uintptr(w), uintptr(h), pixelFormatBGRA, attrs, &d.scaled)
			cfRelease(attrs)
			cfRelease(surface)
			if st != 0 {
				d.scaled = 0
				return fmt.Errorf("vt: pixel buffer (%d)", st)
			}
			d.scaledW, d.scaledH = w, h
		}
		if st := vtTransferImage(d.transfer, img, d.scaled); st != 0 {
			return fmt.Errorf("vt: scaling (%d)", st)
		}
		src = d.scaled
	}
	if cvLock(src, pixelBufferLockRead) != 0 {
		return errors.New("vt: cannot read the picture")
	}
	pic := d.pool.Get(w, h)
	srcBuf := vImageBuffer{
		data:     cvBaseAddress(src),
		height:   uintptr(h),
		width:    uintptr(w),
		rowBytes: cvBytesPerRow(src),
	}
	dstBuf := vImageBuffer{data: uintptr(unsafe.Pointer(&pic.Pix[0])), height: uintptr(h), width: uintptr(w), rowBytes: uintptr(pic.Stride)}
	perm := [4]uint8{2, 1, 0, 3} // BGRA → RGBA
	vImagePermuteARGB(unsafe.Pointer(&srcBuf), unsafe.Pointer(&dstBuf), unsafe.Pointer(&perm[0]), 0)
	cvUnlock(src, pixelBufferLockRead)
	runtime.KeepAlive(pic)
	d.out(pic, func() { d.pool.Put(pic) })
	return nil
}

// Close releases the decoder.
func (d *Decoder) Close() {
	d.closeSession()
	if d.transfer != 0 {
		vtTransferInvalid(d.transfer)
		cfRelease(d.transfer)
		d.transfer = 0
	}
	if d.scaled != 0 {
		cfRelease(d.scaled)
		d.scaled = 0
	}
	if d.format != 0 {
		cfRelease(d.format)
		d.format = 0
	}
	decoders.mu.Lock()
	delete(decoders.m, d.id)
	decoders.mu.Unlock()
}
