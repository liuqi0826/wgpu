//go:build windows && amd64

package wgpu

import (
	"runtime"
	"unsafe"

	"wgpu/native"
)

// Native structs store pointers as uintptr. Escape analysis does not follow
// those, so a pointee could live on the goroutine stack and move if the stack
// grows inside the purego call. cmem forces every pointee onto the heap and
// keeps it reachable until done is called after the native call.
type cmem struct {
	keep []any
}

var (
	escapeSink    any
	escapeEnabled bool
)

// escape makes v escape to the heap. escapeEnabled is never set.
func escape[T any](v T) T {
	if escapeEnabled {
		escapeSink = v
	}
	return v
}

// str packs s as a StringView. An empty string is the empty view.
func (m *cmem) str(s string) native.StringView {
	view, buf := native.MakeString(s)
	if buf != nil {
		m.keep = append(m.keep, escape(buf))
	}
	return view
}

// optStr packs s, using the null view for an empty string.
func (m *cmem) optStr(s string) native.StringView {
	if s == "" {
		return native.NullString
	}
	return m.str(s)
}

func (m *cmem) done() {
	runtime.KeepAlive(m.keep)
}

func cnew[T any](m *cmem, v T) *T {
	p := escape(new(T))
	*p = v
	m.keep = append(m.keep, p)
	return p
}

func cslice[T any](m *cmem, n int) []T {
	if n <= 0 {
		return nil
	}
	s := escape(make([]T, n))
	m.keep = append(m.keep, s)
	return s
}

func addr[T any](p *T) uintptr {
	if p == nil {
		return 0
	}
	return uintptr(unsafe.Pointer(p))
}

func sliceAddr[T any](s []T) uintptr {
	if len(s) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&s[0]))
}

func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

func cbool(b bool) native.Bool {
	if b {
		return native.True
	}
	return native.False
}
