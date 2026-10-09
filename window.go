//go:build windows && amd64

package wgpu

import "syscall"

// wgpu-native aborts the process when it cannot create or configure a surface
// for a window, so the window is checked first.
var (
	procIsWindow         = syscall.NewLazyDLL("user32.dll").NewProc("IsWindow")
	procGetWindowLongPtr = syscall.NewLazyDLL("user32.dll").NewProc("GetWindowLongPtrW")
	procGetModuleHandle  = syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW")
)

// isWindow reports whether hwnd identifies an existing window.
func isWindow(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	r, _, _ := procIsWindow.Call(hwnd)
	return r != 0
}

// windowInstance returns the module instance of the window, or else of the
// executable.
func windowInstance(hwnd uintptr) uintptr {
	const gwlpHInstance = ^uintptr(5) // GWLP_HINSTANCE, -6
	if h, _, _ := procGetWindowLongPtr.Call(hwnd, gwlpHInstance); h != 0 {
		return h
	}
	h, _, _ := procGetModuleHandle.Call(0)
	return h
}
