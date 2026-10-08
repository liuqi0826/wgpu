//go:build windows && amd64

package wgpu

import (
	"fmt"
	"runtime"
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
}

// Native returns the raw device handle.
func (d *Device) Native() native.Device {
	if d == nil {
		return 0
	}
	return native.Device(d.ptr.Load())
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
// It returns false without polling on a lost device.
func (d *Device) Poll(wait bool) bool {
	if d.isLost() {
		return false
	}
	flag := native.False
	if wait {
		flag = native.True
	}
	return native.DevicePoll(d.Native(), flag, nil) == native.True
}

// Release destroys the device and drops its queue reference. User callbacks
// are not called for this destruction or for any later event.
func (d *Device) Release() {
	if d.state != nil {
		d.state.mu.Lock()
		d.state.released = true
		d.state.mu.Unlock()
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
	return buf, nil
}

// CreateShaderModuleWGSL compiles a WGSL shader module.
func (d *Device) CreateShaderModuleWGSL(label, code string) (*ShaderModule, error) {
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
	if handle == 0 {
		return nil, d.fail("CreateShaderModule")
	}
	mod := &ShaderModule{}
	mod.ptr.Store(uintptr(handle))
	return mod, nil
}

// CreateCommandEncoder creates an encoder with an empty descriptor.
func (d *Device) CreateCommandEncoder() (*CommandEncoder, error) {
	handle := native.DeviceCreateCommandEncoder(d.Native(), nil)
	if handle == 0 {
		return nil, d.fail("CreateCommandEncoder")
	}
	enc := &CommandEncoder{}
	enc.ptr.Store(uintptr(handle))
	return enc, nil
}

// PushErrorScope pushes a validation error scope.
func (d *Device) PushErrorScope(filter native.ErrorFilter) {
	native.DevicePushErrorScope(d.Native(), filter)
}

// PopErrorScope waits for the most recent error scope.
// A nil error means the scope completed with no error.
func (d *Device) PopErrorScope() error {
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
// by the caller after submission. On a lost device it does nothing, because
// wgpu-native aborts the process when a submit fails; check Device.Err.
func (q *Queue) Submit(buffers ...*CommandBuffer) {
	if q.dev.isLost() {
		return
	}
	if len(buffers) == 0 {
		native.QueueSubmit(q.ptr, 0, nil)
		return
	}
	raw := make([]native.CommandBuffer, len(buffers))
	for i, buf := range buffers {
		raw[i] = buf.Native()
	}
	native.QueueSubmit(q.ptr, uintptr(len(raw)), &raw[0])
	runtime.KeepAlive(raw)
}

// ShaderModule is a compiled shader.
type ShaderModule struct {
	ptr atomic.Uintptr
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
	ptr atomic.Uintptr
}

// Native returns the raw encoder handle.
func (e *CommandEncoder) Native() native.CommandEncoder {
	if e == nil {
		return 0
	}
	return native.CommandEncoder(e.ptr.Load())
}

// Finish ends the encoder and returns a command buffer.
func (e *CommandEncoder) Finish() (*CommandBuffer, error) {
	handle := native.CommandEncoderFinish(e.Native(), nil)
	e.ptr.Store(0)
	if handle == 0 {
		return nil, fmt.Errorf("wgpu: CommandEncoderFinish returned null")
	}
	buf := &CommandBuffer{}
	buf.ptr.Store(uintptr(handle))
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
	ptr atomic.Uintptr
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
