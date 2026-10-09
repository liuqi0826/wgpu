//go:build windows && amd64

package wgpu

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/liuqi0826/wgpu/native"
)

// Device is a WGPUDevice and the queue returned with it.
type Device struct {
	ptr   atomic.Uintptr
	queue atomic.Uintptr
	state *deviceState
	slot  uintptr

	// scopeMu serialises error scopes. wgpu-native keeps one scope stack per
	// device, so a guarded call must not interleave with another push or pop.
	scopeMu sync.Mutex

	// inst is the instance of the adapter, and adapter holds a reference to
	// it, see Surface.ConfigureWith.
	inst    *Instance
	adapter atomic.Uintptr

	// adapterInfo is read from the adapter by RequestDeviceWith.
	adapterInfo    AdapterInfo
	adapterInfoErr error
}

// Native returns the raw device handle.
func (d *Device) Native() native.Device {
	if d == nil {
		return 0
	}
	return native.Device(d.ptr.Load())
}

// AdapterInfo returns the info of the adapter the device was requested from.
// wgpu-native 29 does not implement wgpuDeviceGetAdapterInfo (it aborts the
// process), so RequestDeviceWith reads Adapter.Info once and this returns that
// copy. It stays available after the adapter or the device is released.
func (d *Device) AdapterInfo() (AdapterInfo, error) {
	if d == nil {
		return AdapterInfo{}, &Error{Op: "DeviceGetAdapterInfo", Status: "nil device"}
	}
	return d.adapterInfo, d.adapterInfoErr
}

// Queue returns the device queue. The handle remains owned by the device.
func (d *Device) Queue() *Queue {
	return &Queue{dev: d, ptr: native.Queue(d.queue.Load())}
}

// Err returns the device-lost state or else the latest uncaptured error.
func (d *Device) Err() error {
	if d == nil || d.state == nil {
		return nil
	}
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	switch {
	case d.state.lost:
		return &Error{Op: "DeviceLost", Status: d.state.lostReason.String(), Message: d.state.lostMsg}
	case d.state.err != "":
		return &Error{Op: "UncapturedError", Status: d.state.errType.String(), Message: d.state.err}
	default:
		return nil
	}
}

// Lost reports whether the device was lost, with the reason and message.
func (d *Device) Lost() (reason native.DeviceLostReason, message string, lost bool) {
	if d == nil || d.state == nil {
		return 0, "", false
	}
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	return d.state.lostReason, d.state.lostMsg, d.state.lost
}

// Destroy destroys the device now. The device becomes lost with reason
// Destroyed and OnLost runs once; the handle stays valid until Release.
func (d *Device) Destroy() {
	handle := d.Native()
	if handle == 0 {
		return
	}
	native.DeviceDestroy(handle)
	// wgpu-native does not report a loss caused by destroy, so record it here.
	if d.state != nil {
		d.state.markLost(native.DeviceLostReasonDestroyed, "device destroyed")
	}
}

// isLost reports a recorded device loss. Calls that wgpu-native treats as
// fatal on a lost device check it first.
func (d *Device) isLost() bool {
	if d == nil || d.state == nil {
		return false
	}
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	return d.state.lost
}

// Poll blocks when wait is true until the device queue is idle.
// It reports whether the queue is empty, and returns false without polling on
// a lost device.
func (d *Device) Poll(wait bool) bool {
	if d.isLost() {
		return false
	}
	return native.DevicePoll(d.Native(), wait, nil)
}

// Release destroys the device and drops its queue reference. User callbacks
// are not called for this destruction or for any later event.
func (d *Device) Release() {
	if d.state != nil {
		d.state.mu.Lock()
		d.state.released = true
		d.state.mu.Unlock()
		d.state.cancelPending(&Error{Op: "Device", Status: "released"})
	}
	queue := native.Queue(d.queue.Swap(0))
	if queue != 0 {
		native.QueueRelease(queue)
	}
	handle := native.Device(d.ptr.Swap(0))
	if handle != 0 {
		native.DeviceDestroy(handle)
		native.DeviceRelease(handle)
	}
	if adapter := native.Adapter(d.adapter.Swap(0)); adapter != 0 {
		native.AdapterRelease(adapter)
	}
	if slot := d.slot; slot != 0 {
		d.slot = 0
		deviceStates.Delete(slot)
	}
}

// CreateBuffer creates a buffer. When mapped is true the buffer is mapped
// for writing at creation and must be unmapped after the initial write.
func (d *Device) CreateBuffer(usage native.BufferUsage, size uint64, mapped bool) (*Buffer, error) {
	mappedAtCreation := native.False
	if mapped {
		mappedAtCreation = native.True
	}
	desc := native.BufferDescriptor{
		Usage:            usage,
		Size:             size,
		MappedAtCreation: mappedAtCreation,
	}
	handle := native.DeviceCreateBuffer(d.Native(), &desc)
	if handle == 0 {
		return nil, d.fail("CreateBuffer")
	}
	buf := &Buffer{dev: d, size: size}
	buf.ptr.Store(uintptr(handle))
	if mapped {
		buf.state, buf.writable, buf.end = mapActive, true, size
	}
	return buf, nil
}

// CreateShaderModuleWGSL compiles a WGSL shader module.
func (d *Device) CreateShaderModuleWGSL(label, code string) (*ShaderModule, error) {
	handle := d.createShaderModuleWGSL(label, code)
	if handle == 0 {
		return nil, d.fail("CreateShaderModule")
	}
	mod := &ShaderModule{dev: d, label: label, code: code}
	mod.ptr.Store(uintptr(handle))
	return mod, nil
}

func (d *Device) createShaderModuleWGSL(label, code string) native.ShaderModule {
	var m cmem
	source := cnew(&m, native.ShaderSourceWGSL{
		Chain: native.ChainedStruct{SType: native.STypeShaderSourceWGSL},
		Code:  m.str(code),
	})
	desc := cnew(&m, native.ShaderModuleDescriptor{
		NextInChain: addr(source),
		Label:       m.str(label),
	})
	handle := native.DeviceCreateShaderModule(d.Native(), desc)
	m.done()
	return handle
}

// CreateCommandEncoder creates an encoder with an empty descriptor.
func (d *Device) CreateCommandEncoder() (*CommandEncoder, error) {
	handle := native.DeviceCreateCommandEncoder(d.Native(), nil)
	if handle == 0 {
		return nil, d.fail("CreateCommandEncoder")
	}
	enc := &CommandEncoder{dev: d}
	enc.ptr.Store(uintptr(handle))
	return enc, nil
}

// PushErrorScope pushes an error scope. wgpu-native 29 only accepts the
// Validation and OutOfMemory filters and aborts the process on any other, so
// other filters panic here instead.
func (d *Device) PushErrorScope(filter native.ErrorFilter) {
	if filter != native.ErrorFilterValidation && filter != native.ErrorFilterOutOfMemory {
		panic(fmt.Sprintf("wgpu: unsupported error filter %v", filter))
	}
	d.scopeMu.Lock()
	defer d.scopeMu.Unlock()
	native.DevicePushErrorScope(d.Native(), filter)
}

// PopErrorScope pops the most recent error scope; its callback runs
// synchronously. A nil error means the scope completed with no error.
func (d *Device) PopErrorScope() error {
	d.scopeMu.Lock()
	defer d.scopeMu.Unlock()
	return d.popScope()
}

// guard runs fn inside internal validation and out-of-memory error scopes
// (wgpu-native has no internal-error scope) and returns the first error. It is used before calls whose failure
// would later make wgpu-native abort the process. Errors raised by other
// goroutines on this device while fn runs land in the same scopes.
func (d *Device) guard(fn func()) error {
	if d == nil || d.Native() == 0 || d.isLost() {
		fn()
		return nil
	}
	d.scopeMu.Lock()
	defer d.scopeMu.Unlock()
	h := d.Native()
	native.DevicePushErrorScope(h, native.ErrorFilterOutOfMemory)
	native.DevicePushErrorScope(h, native.ErrorFilterValidation)
	fn()
	var first error
	for range 2 {
		if err := d.popScope(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (d *Device) popScope() error {
	ensureCallbacks()
	id, _ := newWaiter(&scopeWaiters)
	info := native.PopErrorScopeCallbackInfo{
		Mode:      native.CallbackModeWaitAnyOnly,
		Callback:  native.PopErrorScopeCallback(scopeCB),
		Userdata1: id,
	}
	native.DevicePopErrorScope(d.Native(), info)
	res, ok := tryTake(&scopeWaiters, id)
	if !ok {
		return &Error{Op: "PopErrorScope", Status: "callback was not invoked"}
	}
	if native.PopErrorScopeStatus(res.status) != native.PopErrorScopeStatusSuccess {
		status := native.PopErrorScopeStatus(res.status)
		return &Error{Op: "PopErrorScope", Status: status.String(), Message: res.message}
	}
	if native.ErrorType(res.extra) == native.ErrorTypeNoError || res.message == "" {
		return nil
	}
	kind := native.ErrorType(res.extra)
	return &Error{Op: "ErrorScope", Status: kind.String(), Message: res.message}
}

func (d *Device) fail(op string) error {
	if err := d.Err(); err != nil {
		return err
	}
	return fmt.Errorf("wgpu: %s failed", op)
}

// Queue is the device's command queue.
type Queue struct {
	dev *Device
	ptr native.Queue
}

// Native returns the raw queue handle.
func (q *Queue) Native() native.Queue { return q.ptr }

// WriteBuffer copies data into a buffer that has BufferUsageCopyDst.
func (q *Queue) WriteBuffer(buf *Buffer, offset uint64, data []byte) {
	if len(data) == 0 {
		return
	}
	native.QueueWriteBuffer(q.ptr, buf.Native(), offset, unsafe.Pointer(&data[0]), uintptr(len(data)))
	runtime.KeepAlive(data)
}

// Submit submits finished command buffers. Each buffer must still be released
// by the caller after submission.
//
// wgpu-native aborts the process when a submission fails validation, so the
// command buffers are checked on the Go side first and nothing is submitted
// when one of them fails. Submit returns an error for a nil, released or
// already submitted command buffer, one created on another device, the same
// command buffer twice in one call, and a command buffer that uses (directly
// or through a bind group or render bundle) a buffer that is mapped, has a
// MapAsync in flight, was released or belongs to another device, or a texture
// or query set that was destroyed. The command buffers are left unsubmitted then, so they can be
// submitted once the problem is fixed, for example after Unmap.
//
// On a released device it returns an error and on a lost device it returns
// Device.Err, without calling wgpu-native.
func (q *Queue) Submit(buffers ...*CommandBuffer) error {
	const op = "QueueSubmit"
	if q.dev.Native() == 0 || q.ptr == 0 {
		return &Error{Op: op, Status: "device released"}
	}
	if q.dev.isLost() {
		return q.dev.Err()
	}
	raw := make([]native.CommandBuffer, len(buffers))
	for i, buf := range buffers {
		status := buf.submitStatus(q.dev)
		if status == "" && slices.Contains(buffers[:i], buf) {
			status = "passed twice"
		}
		if status != "" {
			return &Error{Op: op, Status: "command buffer " + strconv.Itoa(i) + " " + status}
		}
		raw[i] = buf.Native()
	}
	if len(raw) == 0 {
		native.QueueSubmit(q.ptr, 0, nil)
		return nil
	}
	native.QueueSubmit(q.ptr, uintptr(len(raw)), &raw[0])
	runtime.KeepAlive(raw)
	for _, buf := range buffers {
		buf.submitted.Store(true)
		buf.refs = usedRefs{}
	}
	return nil
}

// ShaderModule is a compiled shader.
type ShaderModule struct {
	ptr atomic.Uintptr

	// dev, label and code are kept so CompilationInfo can recompile.
	dev   *Device
	label string
	code  string

	infoMu   sync.Mutex
	info     []CompilationMessage
	infoDone bool
}

// CompilationMessage is one diagnostic reported by ShaderModule.CompilationInfo.
type CompilationMessage struct {
	Type    native.CompilationMessageType
	Message string // full naga diagnostic, including the source excerpt
	LineNum uint64 // 1-based line of the first source location, 0 if none
	LinePos uint64 // 1-based column of that location as naga reports it, 0 if none
}

// diagLocation matches the codespan location line "  ┌─ file:line:col".
var diagLocation = regexp.MustCompile(`(?m)\x{250C}\x{2500} .*:(\d+):(\d+)\s*$`)

// CompilationInfo returns the diagnostics of the shader module. An empty
// result means it compiled without errors. wgpu-native 29 does not implement
// wgpuShaderModuleGetCompilationInfo (it aborts the process), so the first call
// compiles the WGSL source again inside internal error scopes and turns a
// validation error into one CompilationMessage of type Error; the result is
// cached. naga reports no warnings through wgpu-core, so only errors appear.
// It fails on a released module or device and on a lost device.
func (m *ShaderModule) CompilationInfo() ([]CompilationMessage, error) {
	const op = "ShaderModuleGetCompilationInfo"
	if m.Native() == 0 {
		return nil, &Error{Op: op, Status: "released shader module"}
	}
	m.infoMu.Lock()
	defer m.infoMu.Unlock()
	if m.infoDone {
		return slices.Clone(m.info), nil
	}
	d := m.dev
	if d.Native() == 0 {
		return nil, &Error{Op: op, Status: "released device"}
	}
	if d.isLost() {
		return nil, d.Err()
	}
	var tmp native.ShaderModule
	err := d.guard(func() { tmp = d.createShaderModuleWGSL(m.label, m.code) })
	if tmp != 0 {
		native.ShaderModuleRelease(tmp)
	}
	if d.isLost() {
		return nil, d.Err()
	}
	var msgs []CompilationMessage
	if err != nil {
		var e *Error
		if !errors.As(err, &e) || e.Op != "ErrorScope" || e.Status != native.ErrorTypeValidation.String() {
			return nil, err
		}
		msg := CompilationMessage{Type: native.CompilationMessageTypeError, Message: e.Message}
		if loc := diagLocation.FindStringSubmatch(e.Message); loc != nil {
			msg.LineNum, _ = strconv.ParseUint(loc[1], 10, 64)
			msg.LinePos, _ = strconv.ParseUint(loc[2], 10, 64)
		}
		msgs = []CompilationMessage{msg}
	}
	m.info, m.infoDone = msgs, true
	return slices.Clone(msgs), nil
}

// Native returns the raw shader module handle.
func (m *ShaderModule) Native() native.ShaderModule {
	if m == nil {
		return 0
	}
	return native.ShaderModule(m.ptr.Load())
}

// Release drops the shader module.
func (m *ShaderModule) Release() {
	handle := native.ShaderModule(m.ptr.Swap(0))
	if handle != 0 {
		native.ShaderModuleRelease(handle)
	}
}

// CommandEncoder records commands.
type CommandEncoder struct {
	dev  *Device
	ptr  atomic.Uintptr
	err  error
	refs usedRefs // objects used so far, see Queue.Submit

	debugDepth int
}

// setErr records the first error found on the Go side; Finish returns it.
func (e *CommandEncoder) setErr(op, status string) {
	if e != nil && e.err == nil {
		e.err = &Error{Op: op, Status: status}
	}
}

// Native returns the raw encoder handle.
func (e *CommandEncoder) Native() native.CommandEncoder {
	if e == nil {
		return 0
	}
	return native.CommandEncoder(e.ptr.Load())
}

// Finish ends the encoder, releases it and returns a command buffer.
//
// Validation errors recorded by the encoder or its passes are returned here
// instead of reaching OnUncapturedError. wgpu-native aborts the process when
// an invalid command buffer is submitted, so no buffer is returned then.
func (e *CommandEncoder) Finish() (*CommandBuffer, error) {
	handle := native.CommandEncoder(e.ptr.Swap(0))
	if handle == 0 {
		return nil, &Error{Op: "CommandEncoderFinish", Status: "encoder already finished or released"}
	}
	if e.debugDepth > 0 {
		e.setErr("CommandEncoderFinish", openGroupsStatus(e.debugDepth))
	}
	if e.err != nil {
		native.CommandEncoderRelease(handle)
		return nil, e.err
	}
	var raw native.CommandBuffer
	err := e.dev.guard(func() { raw = native.CommandEncoderFinish(handle, nil) })
	// The command buffer lives in its own registry, so the encoder can go now.
	native.CommandEncoderRelease(handle)
	if err == nil && raw == 0 {
		err = &Error{Op: "CommandEncoderFinish", Status: "null command buffer"}
	}
	if err != nil {
		if raw != 0 {
			native.CommandBufferRelease(raw)
		}
		return nil, err
	}
	buf := &CommandBuffer{dev: e.dev, refs: e.refs}
	e.refs = usedRefs{}
	buf.ptr.Store(uintptr(raw))
	return buf, nil
}

// Release drops an unfinished encoder.
func (e *CommandEncoder) Release() {
	handle := native.CommandEncoder(e.ptr.Swap(0))
	if handle != 0 {
		native.CommandEncoderRelease(handle)
	}
}

// CommandBuffer is a finished command encoder.
type CommandBuffer struct {
	ptr       atomic.Uintptr
	dev       *Device
	refs      usedRefs
	submitted atomic.Bool
}

// submitStatus reports why b cannot be submitted to the queue of dev, or "".
func (b *CommandBuffer) submitStatus(dev *Device) string {
	switch {
	case b.Native() == 0:
		return "is nil or released"
	case b.submitted.Load():
		return "was already submitted"
	case b.dev != dev:
		return "was created on another device"
	}
	return b.refs.check(dev)
}

// Native returns the raw command buffer handle.
func (b *CommandBuffer) Native() native.CommandBuffer {
	if b == nil {
		return 0
	}
	return native.CommandBuffer(b.ptr.Load())
}

// Release drops the command buffer.
func (b *CommandBuffer) Release() {
	handle := native.CommandBuffer(b.ptr.Swap(0))
	if handle != 0 {
		native.CommandBufferRelease(handle)
	}
}
