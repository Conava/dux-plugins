package main

import (
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// GStreamer is loaded at run time through purego, so the binary stays static
// and CGO-free while still controlling the encoder of a running pipeline.
var (
	gstInit                 func(argc, argv unsafe.Pointer)
	gstParseLaunch          func(desc string, err unsafe.Pointer) uintptr
	gstBinGetByName         func(bin uintptr, name string) uintptr
	gstElementSetState      func(el uintptr, state int32) int32
	gstElementGetStaticPad  func(el uintptr, name string) uintptr
	gstElementGetBus        func(el uintptr) uintptr
	gstPadSendEvent         func(pad, event uintptr) bool
	gstBusTimedPopFiltered  func(bus uintptr, timeout uint64, types uint32) uintptr
	gstMessageGetStructure  func(msg uintptr) uintptr
	gstMessageParseError    func(msg uintptr, gerr, debug unsafe.Pointer)
	gstSampleGetBuffer      func(sample uintptr) uintptr
	gstBufferGetSize        func(buf uintptr) uint64
	gstBufferExtract        func(buf uintptr, offset uint64, dest unsafe.Pointer, size uint64) uint64
	gstMiniObjectUnref      func(obj uintptr)
	gstObjectUnref          func(obj uintptr)
	gstUtilSetObjectArg     func(obj uintptr, name, value string)
	gstElementFactoryFind   func(name string) uintptr
	gstAppSinkTryPullSample func(sink uintptr, timeout uint64) uintptr
	gstAppSinkIsEOS         func(sink uintptr) bool
	gstForceKeyUnit         func(runningTime uint64, allHeaders bool, count uint32) uintptr
)

const (
	gstStateNull    = 1
	gstStatePlaying = 4
	gstClockNone    = ^uint64(0)
	gstMsgEOS       = 1 << 0
	gstMsgError     = 1 << 1
)

var loadGst = sync.OnceValue(func() error {
	libs := map[string]map[string]any{
		"libgstreamer-1.0.so.0": {
			"gst_init": &gstInit, "gst_parse_launch": &gstParseLaunch, "gst_bin_get_by_name": &gstBinGetByName,
			"gst_element_set_state": &gstElementSetState, "gst_element_get_static_pad": &gstElementGetStaticPad,
			"gst_element_get_bus": &gstElementGetBus, "gst_pad_send_event": &gstPadSendEvent,
			"gst_bus_timed_pop_filtered": &gstBusTimedPopFiltered, "gst_message_get_structure": &gstMessageGetStructure,
			"gst_message_parse_error": &gstMessageParseError, "gst_sample_get_buffer": &gstSampleGetBuffer,
			"gst_buffer_get_size": &gstBufferGetSize, "gst_buffer_extract": &gstBufferExtract,
			"gst_mini_object_unref": &gstMiniObjectUnref, "gst_object_unref": &gstObjectUnref,
			"gst_util_set_object_arg":  &gstUtilSetObjectArg,
			"gst_element_factory_find": &gstElementFactoryFind,
		},
		"libgstapp-1.0.so.0": {
			"gst_app_sink_try_pull_sample": &gstAppSinkTryPullSample, "gst_app_sink_is_eos": &gstAppSinkIsEOS,
		},
		"libgstvideo-1.0.so.0": {
			"gst_video_event_new_upstream_force_key_unit": &gstForceKeyUnit,
		},
	}
	for name, fns := range libs {
		lib, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return err
		}
		for sym, fn := range fns {
			purego.RegisterLibFunc(fn, lib, sym)
		}
	}
	gstInit(nil, nil)
	return nil
})

// gstPipeline is a running pipeline with an encoder named "enc" and an appsink
// named "out".
type gstPipeline struct {
	pipe, enc, sink uintptr
	pending         []byte
	once            sync.Once
	stopped         atomic.Bool
	failed          chan string // receives the error message if the pipeline fails
}

func startGstPipeline(desc string) (*gstPipeline, error) {
	if err := loadGst(); err != nil {
		return nil, fmt.Errorf("loading GStreamer: %w", err)
	}
	debugf("pipeline: %s", desc)
	var gerr *gError
	pipe := gstParseLaunch(desc, unsafe.Pointer(&gerr))
	if pipe == 0 || gerr != nil {
		return nil, fmt.Errorf("parse: %s", gerrorMessage(gerr))
	}
	p := &gstPipeline{pipe: pipe, enc: gstBinGetByName(pipe, "enc"), sink: gstBinGetByName(pipe, "out"), failed: make(chan string, 1)}
	if p.enc == 0 || p.sink == 0 {
		return nil, fmt.Errorf("pipeline needs elements named enc and out")
	}
	if gstElementSetState(pipe, gstStatePlaying) == 0 {
		return nil, fmt.Errorf("pipeline refused to start")
	}
	go p.watchBus()
	return p, nil
}

func (p *gstPipeline) watchBus() {
	bus := gstElementGetBus(p.pipe)
	defer gstObjectUnref(bus)
	msg := gstBusTimedPopFiltered(bus, gstClockNone, gstMsgEOS|gstMsgError)
	if msg == 0 {
		return
	}
	defer gstMiniObjectUnref(msg)
	if gstMessageGetStructure(msg) == 0 {
		return // EOS carries no structure
	}
	var gerr *gError
	var debug uintptr
	gstMessageParseError(msg, unsafe.Pointer(&gerr), unsafe.Pointer(&debug))
	log.Printf("pipeline error: %s", gerrorMessage(gerr))
	p.failed <- gerrorMessage(gerr)
}

// Pull returns the next buffer from the appsink, one encoded frame or packet.
func (p *gstPipeline) Pull() ([]byte, error) {
	for {
		sample := gstAppSinkTryPullSample(p.sink, 100_000_000) // 100 ms
		if sample == 0 {
			if gstAppSinkIsEOS(p.sink) || p.stopped.Load() {
				return nil, io.EOF
			}
			continue
		}
		buf := gstSampleGetBuffer(sample)
		b := make([]byte, gstBufferGetSize(buf))
		if len(b) > 0 {
			gstBufferExtract(buf, 0, unsafe.Pointer(&b[0]), uint64(len(b)))
		}
		gstMiniObjectUnref(sample)
		return b, nil
	}
}

// Read hands out the appsink's buffers as one byte stream.
func (p *gstPipeline) Read(b []byte) (int, error) {
	for len(p.pending) == 0 {
		var err error
		if p.pending, err = p.Pull(); err != nil {
			return 0, err
		}
	}
	n := copy(b, p.pending)
	p.pending = p.pending[n:]
	return n, nil
}

// KeyFrame asks the encoder for a key frame with fresh SPS/PPS. The upstream
// event skips the encoder's stream lock, so it can't block on a full appsink.
func (p *gstPipeline) KeyFrame() {
	pad := gstElementGetStaticPad(p.enc, "src")
	defer gstObjectUnref(pad)
	gstPadSendEvent(pad, gstForceKeyUnit(gstClockNone, true, 0))
}

func (p *gstPipeline) SetBitrate(kbps int) {
	gstUtilSetObjectArg(p.enc, "bitrate", fmt.Sprint(kbps))
}

func (p *gstPipeline) Stop() {
	p.once.Do(func() {
		p.stopped.Store(true)
		gstElementSetState(p.pipe, gstStateNull)
	})
}

type gError struct {
	domain  uint32
	code    int32
	message *byte
}

func gerrorMessage(gerr *gError) string {
	if gerr == nil || gerr.message == nil {
		return "unknown error"
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(gerr.message), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(gerr.message, n))
}

// hasElement reports whether a GStreamer element is installed.
func hasElement(name string) bool {
	if loadGst() != nil {
		return false
	}
	f := gstElementFactoryFind(name)
	if f == 0 {
		return false
	}
	gstObjectUnref(f)
	return true
}
