//go:build windows && amd64

package wgpu

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/liuqi0826/wgpu/native"
)

var (
	testUser32          = syscall.NewLazyDLL("user32.dll")
	testCreateWindowExW = testUser32.NewProc("CreateWindowExW")
	testDestroyWindow   = testUser32.NewProc("DestroyWindow")
	testGetModuleHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW")
)

// newTestWindow creates a hidden window of the predefined STATIC class, so no
// window class or message loop is needed. The test goroutine stays locked to
// its OS thread, which owns the window; the window goes when the test ends.
func newTestWindow(t *testing.T, w, h int32) uintptr {
	t.Helper()
	runtime.LockOSThread()
	class, _ := syscall.UTF16PtrFromString("STATIC")
	hwnd, _, err := testCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0,
		0x00CF0000, 0, 0, uintptr(w), uintptr(h), 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW: %v", err)
	}
	t.Cleanup(func() { testDestroyWindow.Call(hwnd) })
	return hwnd
}

type surfaceFixture struct {
	instance *Instance
	surface  *Surface
	adapter  *Adapter
	device   *Device
	caps     SurfaceCapabilities
}

func newSurfaceFixture(t *testing.T) *surfaceFixture {
	t.Helper()
	hwnd := newTestWindow(t, 64, 64)
	f := &surfaceFixture{instance: testInstance(t)}
	t.Cleanup(f.instance.Release)
	var err error
	if f.surface, err = f.instance.CreateSurfaceHWND(hwnd, testHInstance()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.surface.Release)
	f.adapter, err = f.instance.RequestAdapter(&native.RequestAdapterOptions{
		PowerPreference:   native.PowerPreferenceHighPerformance,
		CompatibleSurface: f.surface.Native(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.adapter.Release)
	if f.device, err = f.adapter.RequestDevice(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.device.Release)
	if f.caps, err = f.surface.Capabilities(f.adapter); err != nil {
		t.Fatal(err)
	}
	return f
}

func testHInstance() uintptr {
	h, _, _ := testGetModuleHandle.Call(0)
	return h
}
