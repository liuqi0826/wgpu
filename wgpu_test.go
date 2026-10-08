//go:build windows && amd64

package wgpu

import (
	"testing"

	"wgpu/native"
)

func TestNoCGOInstanceDeviceAndShader(t *testing.T) {
	version, err := Version()
	if err != nil {
		t.Fatal(err)
	}
	// This DLL's wgpuGetVersion is a stub (xor eax, eax; ret). Creating an
	// instance is the check that the export was actually called.
	t.Logf("version %#x library %s", version, native.LibraryPath())

	instance, err := CreateInstance(&InstanceOptions{Backends: native.InstanceBackendPrimary})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Release()

	adapters, err := instance.EnumerateAdapters(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) == 0 {
		adapters, err = instance.EnumerateAdapters(native.InstanceBackendSecondary)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(adapters) == 0 {
		t.Fatal("no adapters")
	}
	defer func() {
		for _, adapter := range adapters {
			adapter.Release()
		}
	}()

	info, err := adapters[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("adapter %s %s %q %q", info.Backend, info.Type, info.Device, info.Description)
	if info.Device == "" && info.Description == "" {
		t.Fatal("adapter info strings are empty")
	}

	device, err := adapters[0].RequestDevice()
	if err != nil {
		t.Fatal(err)
	}
	defer device.Release()

	buffer, err := device.CreateBuffer(native.BufferUsageMapWrite|native.BufferUsageCopySrc, 4, true)
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Release()
	mapped, err := buffer.MappedBytes(0, 4)
	if err != nil {
		t.Fatal(err)
	}
	mapped[0] = 0x44
	mapped[1] = 0x33
	mapped[2] = 0x22
	mapped[3] = 0x11
	buffer.Unmap()

	readable, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	defer readable.Release()
	if err := readable.Map(native.MapModeRead, 0, 4); err != nil {
		t.Fatal(err)
	}
	got, err := readable.ReadMapped(0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("read %d bytes", len(got))
	}
	readable.Unmap()

	module, err := device.CreateShaderModuleWGSL("test", `
@compute @workgroup_size(1)
fn main() {}
`)
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}

	device.PushErrorScope(native.ErrorFilterValidation)
	bad, err := device.CreateShaderModuleWGSL("bad", "this is not wgsl")
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Release()
	if err := device.PopErrorScope(); err == nil {
		t.Fatal("invalid WGSL was accepted without a validation error")
	} else {
		t.Logf("validation: %v", err)
	}
}
