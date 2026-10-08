//go:build windows && amd64

package wgpu

import (
	"sync/atomic"

	"wgpu/native"
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

// RequestDevice creates a device and waits until it is ready.
// Device-lost and uncaptured-error callbacks are installed so wgpu-native
// does not treat those events as fatal.
func (a *Adapter) RequestDevice() (*Device, error) {
	ensureCallbacks()
	state := &deviceState{}
	slot := uintptr(waiterSeq.Add(1))
	deviceStates.Store(slot, state)

	id, _ := newWaiter(&deviceWaiters)
	desc := native.DeviceDescriptor{
		DeviceLostCallbackInfo: native.DeviceLostCallbackInfo{
			Mode:      native.CallbackModeAllowSpontaneous,
			Callback:  native.DeviceLostCallback(lostCB),
			Userdata1: slot,
		},
		UncapturedErrorCallbackInfo: native.UncapturedErrorCallbackInfo{
			Callback:  native.UncapturedErrorCallback(errorCB),
			Userdata1: slot,
		},
	}
	info := native.RequestDeviceCallbackInfo{
		Mode:      native.CallbackModeWaitAnyOnly,
		Callback:  native.RequestDeviceCallback(deviceCB),
		Userdata1: id,
	}
	native.AdapterRequestDevice(a.Native(), &desc, info)
	res, ok := tryTake(&deviceWaiters, id)
	if !ok {
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
