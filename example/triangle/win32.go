//go:build windows && amd64

package main

import (
	"syscall"
	"unsafe"
)

// Minimal Win32 window through syscall; no CGO and no x/sys.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procShowWindow       = user32.NewProc("ShowWindow")
	procPeekMessageW     = user32.NewProc("PeekMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procGetClientRect    = user32.NewProc("GetClientRect")
	procLoadCursorW      = user32.NewProc("LoadCursorW")
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procAdjustWindowRect = user32.NewProc("AdjustWindowRect")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

const (
	csHRedraw          = 0x0002
	csVRedraw          = 0x0001
	wsOverlappedWindow = 0x00CF0000
	cwUseDefault       = 0x80000000
	swShow             = 5
	pmRemove           = 0x0001
	idcArrow           = 32512
	swpNoMove          = 0x0002
	swpNoZOrder        = 0x0004

	wmDestroy = 0x0002
	wmSize    = 0x0005
	wmQuit    = 0x0012
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type point struct{ X, Y int32 }

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type rect struct{ Left, Top, Right, Bottom int32 }

type window struct {
	hwnd      uintptr
	hinstance uintptr
}

// resized is set by WM_SIZE. The window procedure runs on the thread that
// pumps messages, which is the locked main thread.
var resized bool

func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch uint32(msg) {
	case wmSize:
		resized = true
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func createWindow(title string, width, height int32) (*window, error) {
	hinst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("wgpuTriangleWindow")
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return nil, err
	}
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	wc := wndClassEx{
		Style:     csHRedraw | csVRedraw,
		WndProc:   syscall.NewCallback(wndProc),
		Instance:  hinst,
		Cursor:    cursor,
		ClassName: className,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return nil, err
	}
	r := rect{Right: width, Bottom: height}
	procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), wsOverlappedWindow, 0)
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(titlePtr)),
		wsOverlappedWindow,
		cwUseDefault, cwUseDefault,
		uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top),
		0, 0, hinst, 0,
	)
	if hwnd == 0 {
		return nil, err
	}
	procShowWindow.Call(hwnd, swShow)
	return &window{hwnd: hwnd, hinstance: hinst}, nil
}

// pump drains the message queue. It returns false after WM_QUIT.
func (w *window) pump() bool {
	var m winMsg
	for {
		r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
		if r == 0 {
			return true
		}
		if m.Message == wmQuit {
			return false
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (w *window) clientSize() (uint32, uint32) {
	var r rect
	procGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	return uint32(max(r.Right-r.Left, 0)), uint32(max(r.Bottom-r.Top, 0))
}

// setClientSize resizes the window. WM_SIZE is delivered before it returns.
func (w *window) setClientSize(width, height int32) {
	r := rect{Right: width, Bottom: height}
	procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), wsOverlappedWindow, 0)
	procSetWindowPos.Call(w.hwnd, 0, 0, 0, uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), swpNoMove|swpNoZOrder)
}

func (w *window) destroy() {
	if w.hwnd != 0 {
		procDestroyWindow.Call(w.hwnd)
		w.hwnd = 0
		w.pump()
	}
}
