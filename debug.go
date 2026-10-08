//go:build windows && amd64

package wgpu

import (
	"strconv"

	"github.com/liuqi0826/wgpu/native"
)

// Debug groups and markers show up in graphics debuggers such as RenderDoc.
//
// wgpu-native validates push/pop pairs itself and records a mismatch as a
// validation error of the encoder, which Finish reports through its error
// scope; nothing aborts as long as the command buffer is not submitted. The
// binding still tracks the nesting depth on the Go side so that a stray pop
// is never sent to wgpu-native and the error names the culprit: popping an
// empty stack records an error at once, and groups still open when a pass
// ends or an encoder finishes make CommandEncoder.Finish (or
// RenderBundleEncoder.Finish) fail.
//
// wgpu-native v29 implements none of the wgpuXxxSetLabel functions (they
// panic with "not implemented" and abort the process), so the binding has no
// SetLabel methods. Set labels through the Label field of the descriptors.

func withLabel(label string, fn func(native.StringView)) {
	var m cmem
	fn(m.str(label))
	m.done()
}

func openGroupsStatus(n int) string {
	if n == 1 {
		return "1 debug group still open"
	}
	return strconv.Itoa(n) + " debug groups still open"
}

// PushDebugGroup opens a named debug group. Close it with PopDebugGroup
// before Finish.
func (e *CommandEncoder) PushDebugGroup(label string) {
	if e.Native() == 0 {
		return
	}
	withLabel(label, func(s native.StringView) { native.CommandEncoderPushDebugGroup(e.Native(), s) })
	e.debugDepth++
}

// PopDebugGroup closes the innermost debug group. Popping with no open group
// records an error that Finish returns.
func (e *CommandEncoder) PopDebugGroup() {
	if e.Native() == 0 {
		return
	}
	if e.debugDepth == 0 {
		e.setErr("CommandEncoderPopDebugGroup", "no debug group to pop")
		return
	}
	native.CommandEncoderPopDebugGroup(e.Native())
	e.debugDepth--
}

// InsertDebugMarker inserts a single named marker.
func (e *CommandEncoder) InsertDebugMarker(label string) {
	if e.Native() == 0 {
		return
	}
	withLabel(label, func(s native.StringView) { native.CommandEncoderInsertDebugMarker(e.Native(), s) })
}

// PushDebugGroup opens a named debug group inside the pass. Close it with
// PopDebugGroup before End.
func (p *RenderPassEncoder) PushDebugGroup(label string) {
	if p.Native() == 0 {
		return
	}
	withLabel(label, func(s native.StringView) { native.RenderPassEncoderPushDebugGroup(p.Native(), s) })
	p.debugDepth++
}

// PopDebugGroup closes the innermost debug group of the pass. Popping with no
// open group records an error that CommandEncoder.Finish returns.
func (p *RenderPassEncoder) PopDebugGroup() {
	if p.Native() == 0 {
		return
	}
	if p.debugDepth == 0 {
		p.enc.setErr("RenderPassEncoderPopDebugGroup", "no debug group to pop")
		return
	}
	native.RenderPassEncoderPopDebugGroup(p.Native())
	p.debugDepth--
}

// InsertDebugMarker inserts a single named marker into the pass.
func (p *RenderPassEncoder) InsertDebugMarker(label string) {
	if p.Native() == 0 {
		return
	}
	withLabel(label, func(s native.StringView) { native.RenderPassEncoderInsertDebugMarker(p.Native(), s) })
}

// PushDebugGroup opens a named debug group inside the pass. Close it with
// PopDebugGroup before End.
func (p *ComputePassEncoder) PushDebugGroup(label string) {
	if p.Native() == 0 {
		return
	}
	withLabel(label, func(s native.StringView) { native.ComputePassEncoderPushDebugGroup(p.Native(), s) })
	p.debugDepth++
}

// PopDebugGroup closes the innermost debug group of the pass. Popping with no
// open group records an error that CommandEncoder.Finish returns.
func (p *ComputePassEncoder) PopDebugGroup() {
	if p.Native() == 0 {
		return
	}
	if p.debugDepth == 0 {
		p.enc.setErr("ComputePassEncoderPopDebugGroup", "no debug group to pop")
		return
	}
	native.ComputePassEncoderPopDebugGroup(p.Native())
	p.debugDepth--
}

// InsertDebugMarker inserts a single named marker into the pass.
func (p *ComputePassEncoder) InsertDebugMarker(label string) {
	if p.Native() == 0 {
		return
	}
	withLabel(label, func(s native.StringView) { native.ComputePassEncoderInsertDebugMarker(p.Native(), s) })
}

// PushDebugGroup records a debug group for the bundle. Close it with
// PopDebugGroup before Finish.
//
// wgpu-native v29 accepts but ignores debug commands in bundles, so they do
// not reach graphics debuggers; they are still checked for balance.
func (e *RenderBundleEncoder) PushDebugGroup(label string) {
	if e.done {
		e.fail("encoder already finished")
		return
	}
	e.record(bundleCmd{op: opPushDebugGroup, label: label})
	e.debugDepth++
}

// PopDebugGroup records the end of the innermost debug group. Popping with no
// open group makes Finish fail.
func (e *RenderBundleEncoder) PopDebugGroup() {
	if e.done {
		e.fail("encoder already finished")
		return
	}
	if e.debugDepth == 0 {
		e.fail("no debug group to pop")
		return
	}
	e.record(bundleCmd{op: opPopDebugGroup})
	e.debugDepth--
}

// InsertDebugMarker records a single named marker for the bundle.
func (e *RenderBundleEncoder) InsertDebugMarker(label string) {
	e.record(bundleCmd{op: opInsertDebugMarker, label: label})
}

func (p passRecorder) pushDebugGroup(label string) {
	withLabel(label, func(s native.StringView) { native.RenderPassEncoderPushDebugGroup(native.RenderPassEncoder(p), s) })
}
func (p passRecorder) popDebugGroup() {
	native.RenderPassEncoderPopDebugGroup(native.RenderPassEncoder(p))
}
func (p passRecorder) insertDebugMarker(label string) {
	withLabel(label, func(s native.StringView) { native.RenderPassEncoderInsertDebugMarker(native.RenderPassEncoder(p), s) })
}

func (r bundleRecorder) pushDebugGroup(label string) {
	withLabel(label, func(s native.StringView) { native.RenderBundleEncoderPushDebugGroup(native.RenderBundleEncoder(r), s) })
}
func (r bundleRecorder) popDebugGroup() {
	native.RenderBundleEncoderPopDebugGroup(native.RenderBundleEncoder(r))
}
func (r bundleRecorder) insertDebugMarker(label string) {
	withLabel(label, func(s native.StringView) {
		native.RenderBundleEncoderInsertDebugMarker(native.RenderBundleEncoder(r), s)
	})
}
