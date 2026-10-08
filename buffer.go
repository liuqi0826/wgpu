//go:build windows && amd64

package wgpu

import (
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/liuqi0826/wgpu/native"
)

type mapState uint8

const (
	mapIdle mapState = iota
	mapPending
	mapActive
)

// Buffer is a WGPUBuffer.
//
// The map state is tracked in Go because wgpu-native aborts the process when
// a mapped range is requested that is not mapped, unaligned or out of range.
type Buffer struct {
	dev  *Device
	ptr  atomic.Uintptr
	size uint64

	mu       sync.Mutex
	state    mapState
	writable bool
	start    uint64
	end      uint64
}

// Native returns the raw buffer handle.
func (b *Buffer) Native() native.Buffer {
	if b == nil {
		return 0
	}
	return native.Buffer(b.ptr.Load())
}

// Size returns the buffer size in bytes.
func (b *Buffer) Size() uint64 { return b.size }

// MapState reports whether the buffer is unmapped, has a MapAsync in flight
// or is mapped. wgpu-native 29 does not implement wgpuBufferGetMapState (it
// aborts the process), so the state comes from the Go-side map tracking: a
// mapping request that fails, Unmap, Release, and the release or loss of the
// device all return the buffer to BufferMapStateUnmapped.
func (b *Buffer) MapState() native.BufferMapState {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.Native() == 0:
		return native.BufferMapStateUnmapped
	case b.state == mapPending:
		return native.BufferMapStatePending
	case b.state == mapActive:
		return native.BufferMapStateMapped
	}
	return native.BufferMapStateUnmapped
}

// checkRange validates a mapped-range request against the Go map state.
func (b *Buffer) checkRange(op string, offset, size uint64, write bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.Native() == 0:
		return &Error{Op: op, Status: "buffer released"}
	case b.state != mapActive:
		return &Error{Op: op, Status: "buffer is not mapped"}
	case write && !b.writable:
		return &Error{Op: op, Status: "buffer is mapped for reading; use ReadMapped"}
	case offset%8 != 0:
		return &Error{Op: op, Status: "offset is not a multiple of 8"}
	case size%4 != 0:
		return &Error{Op: op, Status: "size is not a multiple of 4"}
	case offset < b.start || offset > b.end || size > b.end-offset:
		return &Error{Op: op, Status: "range is outside the mapped range"}
	}
	return nil
}

// MappedBytes returns the currently mapped writable range as a slice.
// The slice is invalid after Unmap or Release. A read-only mapping must use ReadMapped.
func (b *Buffer) MappedBytes(offset, size uint64) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	if err := b.checkRange("BufferGetMappedRange", offset, size, true); err != nil {
		return nil, err
	}
	ptr := native.BufferGetMappedRange(b.Native(), uintptr(offset), uintptr(size))
	if ptr == nil {
		return nil, &Error{Op: "BufferGetMappedRange", Status: "null range"}
	}
	return unsafe.Slice((*byte)(ptr), size), nil
}

// ReadMapped copies a mapped range out of the buffer.
func (b *Buffer) ReadMapped(offset, size uint64) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	if err := b.checkRange("BufferGetConstMappedRange", offset, size, false); err != nil {
		return nil, err
	}
	ptr := native.BufferGetConstMappedRange(b.Native(), uintptr(offset), uintptr(size))
	if ptr == nil {
		return nil, &Error{Op: "BufferGetConstMappedRange", Status: "null range"}
	}
	src := unsafe.Slice((*byte)(ptr), size)
	out := make([]byte, size)
	copy(out, src)
	return out, nil
}

// MapAsync starts mapping size bytes at offset (zero size maps the rest of
// the buffer) and returns a channel that receives one value, nil on success,
// and is then closed.
//
// The result only arrives while the device is polled: Device.Poll, a blocking
// Map or WaitSubmittedWorkDone, or a later Queue.Submit. The callback itself
// only stores the result, so polling from any goroutine is fine. Release of
// the device or buffer, and device loss, complete the channel with an error.
func (b *Buffer) MapAsync(mode native.MapMode, offset, size uint64) <-chan error {
	failed := func(err error) <-chan error {
		ch := make(chan error, 1)
		ch <- err
		close(ch)
		return ch
	}
	if size == 0 && offset <= b.size {
		size = b.size - offset
	}
	b.mu.Lock()
	switch {
	case b.Native() == 0:
		b.mu.Unlock()
		return failed(&Error{Op: "MapAsync", Status: "buffer released"})
	case b.state != mapIdle:
		b.mu.Unlock()
		return failed(&Error{Op: "MapAsync", Status: "buffer is already mapped or mapping"})
	}
	b.state = mapPending
	b.mu.Unlock()

	op, err := b.dev.newOp(func(status uintptr, msg string) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s := native.MapAsyncStatus(status); s != native.MapAsyncStatusSuccess {
			if b.state == mapPending {
				b.state = mapIdle
			}
			return &Error{Op: "MapAsync", Status: s.String(), Message: msg}
		}
		if b.state != mapPending {
			// Unmapped or released while the request was in flight.
			return &Error{Op: "MapAsync", Status: "unmapped before the mapping completed"}
		}
		b.state = mapActive
		b.writable = mode&native.MapModeWrite != 0
		b.start, b.end = offset, offset+size
		return nil
	}, func() {
		// Device released or lost before the callback arrived.
		b.mu.Lock()
		if b.state == mapPending {
			b.state = mapIdle
		}
		b.mu.Unlock()
	})
	if err != nil {
		b.mu.Lock()
		b.state = mapIdle
		b.mu.Unlock()
		return failed(err)
	}
	info := native.BufferMapCallbackInfo{
		Mode:      native.CallbackModeAllowProcessEvents,
		Callback:  native.BufferMapCallback(mapAsyncCB),
		Userdata1: op.id,
	}
	// An immediate validation error also reaches the callback, which runs
	// inside this call; the scope keeps it out of OnUncapturedError.
	_ = b.dev.guard(func() {
		native.BufferMapAsync(b.Native(), mode, uintptr(offset), uintptr(size), info)
	})
	return op.ch
}

// MapAsyncFunc is MapAsync with a callback. fn runs on its own goroutine
// once the result arrives, so it may call back into wgpu.
func (b *Buffer) MapAsyncFunc(mode native.MapMode, offset, size uint64, fn func(error)) {
	ch := b.MapAsync(mode, offset, size)
	go func() { fn(<-ch) }()
}

// Map maps the range and waits, polling the device, for at most five
// seconds. A zero size maps the rest of the buffer.
func (b *Buffer) Map(mode native.MapMode, offset, size uint64) error {
	return b.dev.waitFor(b.MapAsync(mode, offset, size), 5*time.Second, "MapAsync")
}

// Unmap unmaps the buffer. A pending MapAsync completes with an error.
func (b *Buffer) Unmap() {
	b.mu.Lock()
	b.state = mapIdle
	b.mu.Unlock()
	if h := b.Native(); h != 0 {
		native.BufferUnmap(h)
	}
}

// Release destroys and drops the buffer.
func (b *Buffer) Release() {
	b.mu.Lock()
	b.state = mapIdle
	handle := native.Buffer(b.ptr.Swap(0))
	b.mu.Unlock()
	if handle != 0 {
		native.BufferDestroy(handle)
		native.BufferRelease(handle)
	}
}
