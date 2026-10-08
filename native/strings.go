//go:build windows && amd64

package native

import "unsafe"

// MakeString packs s as a StringView with an explicit length.
// The returned buffer must stay reachable until the native call that uses the
// view has returned.
func MakeString(s string) (StringView, []byte) {
	if s == "" {
		return StringView{}, nil
	}
	buf := append([]byte(s), 0)
	return StringView{
		Data:   uintptr(unsafe.Pointer(&buf[0])),
		Length: uintptr(len(s)),
	}, buf
}

// String copies the view into a Go string. A null view is empty.
func (s StringView) String() string {
	if s.Data == 0 || s.Length == 0 {
		return ""
	}
	if s.Length == Strlen {
		const max = 1 << 20
		out := make([]byte, 0, 64)
		base := cPointer(s.Data)
		for i := uintptr(0); i < max; i++ {
			b := *(*byte)(unsafe.Add(base, i))
			if b == 0 {
				return string(out)
			}
			out = append(out, b)
		}
		return string(out)
	}
	if s.Length > 1<<20 {
		return ""
	}
	src := unsafe.Slice((*byte)(cPointer(s.Data)), s.Length)
	out := make([]byte, len(src))
	copy(out, src)
	return string(out)
}

// cPointer reinterprets a C address. The uintptr comes from the dynamic library,
// not from a Go pointer conversion that go vet can track.
func cPointer(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}
