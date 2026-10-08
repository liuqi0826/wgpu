//go:build windows && amd64

package native

import (
	"reflect"
	"syscall"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// These functions are bool in Rust but WGPUBool in the headers. The generator
// binds them as Go bool (RUST_BOOL_FUNCS in gen/generate.py).
func TestRustBoolSignatures(t *testing.T) {
	cases := []struct {
		name   string
		fn     any
		params []int // indexes of bool parameters
	}{
		{"DevicePoll", DevicePoll, []int{1}},
		{"DeviceStartGraphicsDebuggerCapture", DeviceStartGraphicsDebuggerCapture, nil},
		{"InstanceHasWGSLLanguageFeature", InstanceHasWGSLLanguageFeature, nil},
	}
	for _, c := range cases {
		typ := reflect.TypeOf(c.fn)
		if typ.NumOut() != 1 || typ.Out(0).Kind() != reflect.Bool {
			t.Errorf("%s is %v, want a bool result", c.name, typ)
		}
		for _, i := range c.params {
			if typ.In(i).Kind() != reflect.Bool {
				t.Errorf("%s parameter %d is %v, want bool", c.name, i, typ.In(i))
			}
		}
	}
}

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procVirtualAlloc  = kernel32.NewProc("VirtualAlloc")
	procVirtualFree   = kernel32.NewProc("VirtualFree")
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

// stub copies x64 machine code into executable memory and returns its address.
func stub(t *testing.T, code []byte) uintptr {
	t.Helper()
	const (
		memCommitReserve     = 0x3000
		memRelease           = 0x8000
		pageExecuteReadWrite = 0x40
	)
	addr, _, err := procVirtualAlloc.Call(0, uintptr(len(code)), memCommitReserve, pageExecuteReadWrite)
	if addr == 0 {
		t.Fatalf("VirtualAlloc: %v", err)
	}
	t.Cleanup(func() { procVirtualFree.Call(addr, 0, memRelease) })
	procRtlMoveMemory.Call(addr, uintptr(unsafe.Pointer(&code[0])), uintptr(len(code)))
	return addr
}

// A Rust bool result only defines AL. wgpuDevicePoll in the bundled DLL ends
// with "sete al" after a call that leaves a stack address in RAX, so EAX reads
// like 0xb996a601. A Go bool result must be decoded from the low byte.
func TestBoolResultUsesLowByte(t *testing.T) {
	for _, c := range []struct {
		eax  uint32
		want bool
	}{
		{0xb996a601, true},
		{0xb996a600, false},
		{0x00000001, true},
		{0x00000000, false},
	} {
		// mov eax, imm32; ret
		code := []byte{0xb8, byte(c.eax), byte(c.eax >> 8), byte(c.eax >> 16), byte(c.eax >> 24), 0xc3}
		var result func() bool
		purego.RegisterFunc(&result, stub(t, code))
		if got := result(); got != c.want {
			t.Errorf("eax=%#08x decoded as %v, want %v", c.eax, got, c.want)
		}
	}
}

// A Rust bool parameter must hold exactly 0 or 1. A Go bool is passed that way.
func TestBoolArgumentIsZeroOrOne(t *testing.T) {
	var echo func(bool) uint64
	purego.RegisterFunc(&echo, stub(t, []byte{0x48, 0x89, 0xc8, 0xc3})) // mov rax, rcx; ret
	if got := echo(true); got != 1 {
		t.Errorf("true passed as %#x, want 1", got)
	}
	if got := echo(false); got != 0 {
		t.Errorf("false passed as %#x, want 0", got)
	}
}
