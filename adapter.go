//go:build windows && amd64

package wgpu

import (
	"sync/atomic"

	"github.com/liuqi0826/wgpu/native"
)

// Adapter is a WGPUAdapter.
type Adapter struct {
	ptr atomic.Uintptr
}

// Native returns the raw handle.
func (a *Adapter) Native() native.Adapter {
	if a == nil {
		return 0
	}
	return native.Adapter(a.ptr.Load())
}

// Release drops the adapter reference.
func (a *Adapter) Release() {
	handle := native.Adapter(a.ptr.Swap(0))
	if handle != 0 {
		native.AdapterRelease(handle)
	}
}

// AdapterInfo is a copy of the strings returned by wgpuAdapterGetInfo.
type AdapterInfo struct {
	Vendor       string
	Architecture string
	Device       string
	Description  string
	Backend      native.BackendType
	Type         native.AdapterType
	VendorID     uint32
	DeviceID     uint32
}

// Info returns a Go copy of the adapter info and frees the native strings.
func (a *Adapter) Info() (AdapterInfo, error) {
	var raw native.AdapterInfo
	status := native.AdapterGetInfo(a.Native(), &raw)
	if status != native.StatusSuccess {
		return AdapterInfo{}, &Error{Op: "AdapterGetInfo", Status: status.String()}
	}
	info := AdapterInfo{
		Vendor:       raw.Vendor.String(),
		Architecture: raw.Architecture.String(),
		Device:       raw.Device.String(),
		Description:  raw.Description.String(),
		Backend:      raw.BackendType,
		Type:         raw.AdapterType,
		VendorID:     raw.VendorID,
		DeviceID:     raw.DeviceID,
	}
	native.AdapterInfoFreeMembers(raw)
	return info, nil
}

// Limits returns the best limits the adapter supports.
func (a *Adapter) Limits() (native.Limits, error) {
	var limits native.Limits
	if status := native.AdapterGetLimits(a.Native(), &limits); status != native.StatusSuccess {
		return native.Limits{}, &Error{Op: "AdapterGetLimits", Status: status.String()}
	}
	return limits, nil
}

// Features lists the features the adapter supports.
func (a *Adapter) Features() []native.FeatureName {
	var raw native.SupportedFeatures
	native.AdapterGetFeatures(a.Native(), &raw)
	out := readSlice[native.FeatureName](raw.Features, raw.FeatureCount)
	native.SupportedFeaturesFreeMembers(raw)
	return out
}

// HasFeature reports whether the adapter supports f.
func (a *Adapter) HasFeature(f native.FeatureName) bool {
	return native.AdapterHasFeature(a.Native(), f) == native.True
}

// DeviceOptions configures Adapter.RequestDeviceWith.
//
// OnLost and OnUncapturedError run on a goroutine owned by the device, in the
// order the events arrived, never inside a native call. They are not called
// for the destruction caused by Device.Release or for events after Release.
// Device.Lost and Device.Err are updated before the callbacks run.
type DeviceOptions struct {
	Label string
	// RequiredFeatures must be a subset of Adapter.Features.
	RequiredFeatures []native.FeatureName
	// RequiredLimits nil uses the default limits. Start from Adapter.Limits
	// or UndefinedLimits; a zero field is a real zero limit.
	RequiredLimits    *native.Limits
	OnLost            func(reason native.DeviceLostReason, message string)
	OnUncapturedError func(typ native.ErrorType, message string)
}

// RequestDevice creates a device with default options.
func (a *Adapter) RequestDevice() (*Device, error) {
	return a.RequestDeviceWith(nil)
}

// RequestDeviceWith creates a device and waits until it is ready.
// Device-lost and uncaptured-error callbacks are always installed so
// wgpu-native does not treat those events as fatal.
func (a *Adapter) RequestDeviceWith(opts *DeviceOptions) (*Device, error) {
	ensureCallbacks()
	if opts == nil {
		opts = &DeviceOptions{}
	}
	state := &deviceState{onLost: opts.OnLost, onError: opts.OnUncapturedError}
	slot := uintptr(waiterSeq.Add(1))
	deviceStates.Store(slot, state)

	var m cmem
	desc := cnew(&m, native.DeviceDescriptor{
		Label: m.str(opts.Label),
		DeviceLostCallbackInfo: native.DeviceLostCallbackInfo{
			Mode:      native.CallbackModeAllowSpontaneous,
			Callback:  native.DeviceLostCallback(lostCB),
			Userdata1: slot,
		},
		UncapturedErrorCallbackInfo: native.UncapturedErrorCallbackInfo{
			Callback:  native.UncapturedErrorCallback(errorCB),
			Userdata1: slot,
		},
	})
	if n := len(opts.RequiredFeatures); n > 0 {
		features := cslice[native.FeatureName](&m, n)
		copy(features, opts.RequiredFeatures)
		desc.RequiredFeatureCount = uintptr(n)
		desc.RequiredFeatures = sliceAddr(features)
	}
	if opts.RequiredLimits != nil {
		limits := cnew(&m, *opts.RequiredLimits)
		limits.NextInChain = 0
		desc.RequiredLimits = addr(limits)
	}

	id, _ := newWaiter(&deviceWaiters)
	info := native.RequestDeviceCallbackInfo{
		Mode:      native.CallbackModeWaitAnyOnly,
		Callback:  native.RequestDeviceCallback(deviceCB),
		Userdata1: id,
	}
	native.AdapterRequestDevice(a.Native(), desc, info)
	m.done()
	res, ok := tryTake(&deviceWaiters, id)
	if !ok {
		deviceWaiters.Delete(id)
		deviceStates.Delete(slot)
		return nil, &Error{Op: "RequestDevice", Status: "callback was not invoked"}
	}
	if native.RequestDeviceStatus(res.status) != native.RequestDeviceStatusSuccess || res.handle == 0 {
		deviceStates.Delete(slot)
		status := native.RequestDeviceStatus(res.status)
		return nil, &Error{Op: "RequestDevice", Status: status.String(), Message: res.message}
	}
	queue := native.DeviceGetQueue(native.Device(res.handle))
	dev := &Device{state: state, slot: slot}
	dev.ptr.Store(res.handle)
	dev.queue.Store(uintptr(queue))
	return dev, nil
}
