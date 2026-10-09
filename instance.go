//go:build windows && amd64

package wgpu

import (
	"fmt"
	"runtime"
	"sync/atomic"

	"github.com/liuqi0826/wgpu/native"
)

// InstanceOptions selects backends and flags for wgpuCreateInstance.
// A zero Backends value enables every backend built into the DLL.
type InstanceOptions struct {
	Backends native.InstanceBackend

	// Flags are the wgpu-native instance flags. Zero means
	// native.InstanceFlagDefault, the same flags CreateInstance(nil) uses;
	// they include InstanceFlagValidationIndirectCall, which D3D12 needs for
	// @builtin(vertex_index) to include firstVertex in indirect draws. A non-zero
	// value is used as given, so add InstanceFlagDefault to keep the defaults.
	Flags native.InstanceFlag

	// ExplicitFlags passes Flags unchanged even when it is zero, which
	// creates the instance with no flags at all.
	ExplicitFlags bool
}

// flags returns the flags passed to wgpu-native.
func (o *InstanceOptions) flags() native.InstanceFlag {
	if o.Flags == 0 && !o.ExplicitFlags {
		return native.InstanceFlagDefault
	}
	return o.Flags
}

// Instance is a WGPUInstance.
type Instance struct {
	ptr atomic.Uintptr
}

// CreateInstance loads the DLL if needed and creates an instance.
func CreateInstance(opts *InstanceOptions) (*Instance, error) {
	if err := Load(""); err != nil {
		return nil, err
	}
	var m cmem
	var desc *native.InstanceDescriptor
	if opts != nil {
		extras := cnew(&m, native.InstanceExtras{
			Chain:    native.ChainedStruct{SType: native.STypeInstanceExtras},
			Backends: opts.Backends,
			Flags:    opts.flags(),
		})
		desc = cnew(&m, native.InstanceDescriptor{NextInChain: addr(extras)})
	}
	handle := native.CreateInstance(desc)
	m.done()
	if handle == 0 {
		return nil, fmt.Errorf("wgpu: CreateInstance returned a null instance")
	}
	inst := &Instance{}
	inst.ptr.Store(uintptr(handle))
	return inst, nil
}

// Native returns the raw handle. It is zero after Release.
func (i *Instance) Native() native.Instance {
	if i == nil {
		return 0
	}
	return native.Instance(i.ptr.Load())
}

// Release drops the instance reference.
func (i *Instance) Release() {
	handle := native.Instance(i.ptr.Swap(0))
	if handle != 0 {
		native.InstanceRelease(handle)
	}
}

// ProcessEvents runs callbacks registered with CallbackModeAllowProcessEvents.
func (i *Instance) ProcessEvents() {
	native.InstanceProcessEvents(i.Native())
}

// EnumerateAdapters returns every adapter for the selected backends.
// The caller owns the adapters and must Release them.
// A zero backends value does not filter by backend.
func (i *Instance) EnumerateAdapters(backends native.InstanceBackend) ([]*Adapter, error) {
	var opts *native.InstanceEnumerateAdapterOptions
	var stored native.InstanceEnumerateAdapterOptions
	if backends != 0 {
		stored.Backends = backends
		opts = &stored
	}
	count := native.InstanceEnumerateAdapters(i.Native(), opts, nil)
	runtime.KeepAlive(stored)
	if count == 0 {
		return nil, nil
	}
	buf := make([]native.Adapter, count)
	n := native.InstanceEnumerateAdapters(i.Native(), opts, &buf[0])
	runtime.KeepAlive(stored)
	if n > count {
		n = count
	}
	out := make([]*Adapter, 0, n)
	for _, handle := range buf[:n] {
		if handle == 0 {
			continue
		}
		item := &Adapter{inst: i}
		item.ptr.Store(uintptr(handle))
		out = append(out, item)
	}
	return out, nil
}

// RequestAdapter asks the instance for one adapter and waits for the result.
func (i *Instance) RequestAdapter(opts *native.RequestAdapterOptions) (*Adapter, error) {
	ensureCallbacks()
	id, _ := newWaiter(&adapterWaiters)
	info := native.RequestAdapterCallbackInfo{
		Mode:      native.CallbackModeWaitAnyOnly,
		Callback:  native.RequestAdapterCallback(adapterCB),
		Userdata1: id,
	}
	// This wgpu-native build invokes the callback before returning and does not
	// implement wgpuInstanceWaitAny.
	native.InstanceRequestAdapter(i.Native(), opts, info)
	res, ok := tryTake(&adapterWaiters, id)
	if !ok {
		return nil, &Error{Op: "RequestAdapter", Status: "callback was not invoked"}
	}
	if native.RequestAdapterStatus(res.status) != native.RequestAdapterStatusSuccess || res.handle == 0 {
		status := native.RequestAdapterStatus(res.status)
		return nil, &Error{Op: "RequestAdapter", Status: status.String(), Message: res.message}
	}
	adapter := &Adapter{inst: i}
	adapter.ptr.Store(res.handle)
	return adapter, nil
}

// CreateSurfaceHWND wraps a Win32 window. hinstance may be zero.
func (i *Instance) CreateSurfaceHWND(hwnd, hinstance uintptr) (*Surface, error) {
	var m cmem
	source := cnew(&m, native.SurfaceSourceWindowsHWND{
		Chain:     native.ChainedStruct{SType: native.STypeSurfaceSourceWindowsHWND},
		Hinstance: hinstance,
		Hwnd:      hwnd,
	})
	desc := cnew(&m, native.SurfaceDescriptor{NextInChain: addr(source)})
	handle := native.InstanceCreateSurface(i.Native(), desc)
	m.done()
	if handle == 0 {
		return nil, fmt.Errorf("wgpu: CreateSurface returned a null surface")
	}
	surface := &Surface{inst: i}
	surface.ptr.Store(uintptr(handle))
	return surface, nil
}
