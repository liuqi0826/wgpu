//go:build windows && amd64

package wgpu

import (
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// wgpuDeviceGetAdapterInfo aborts the process in wgpu-native 29, so
// Device.AdapterInfo is the Adapter.Info copy taken by RequestDeviceWith.
func TestDeviceAdapterInfo(t *testing.T) {
	instance := testInstance(t)
	defer instance.Release()
	adapter, err := instance.RequestAdapter(&native.RequestAdapterOptions{
		PowerPreference: native.PowerPreferenceHighPerformance,
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := adapter.Info()
	if err != nil {
		t.Fatal(err)
	}
	device, err := adapter.RequestDevice()
	if err != nil {
		t.Fatal(err)
	}
	defer device.Release()
	check := func(when string) {
		t.Helper()
		got, err := device.AdapterInfo()
		if err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		if got != want {
			t.Errorf("%s: AdapterInfo = %+v, want %+v", when, got, want)
		}
	}
	check("new device")
	adapter.Release()
	check("after Adapter.Release")
	device.Destroy()
	check("after Device.Destroy")
	t.Logf("%s (%s, %s, vendor %#x device %#x)", want.Device, want.Backend, want.Type, want.VendorID, want.DeviceID)

	var nilDevice *Device
	if _, err := nilDevice.AdapterInfo(); err == nil {
		t.Error("AdapterInfo on a nil device returned no error")
	}
}
