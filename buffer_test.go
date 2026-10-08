//go:build windows && amd64

package wgpu

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/liuqi0826/wgpu/native"
)

// newReadbacks fills count MapRead buffers with distinct data through a GPU copy.
func newReadbacks(t *testing.T, device *Device, count int, size uint64) ([]*Buffer, [][]byte) {
	t.Helper()
	queue := device.Queue()
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		t.Fatal(err)
	}
	bufs := make([]*Buffer, count)
	want := make([][]byte, count)
	for i := range bufs {
		src, err := device.CreateBuffer(native.BufferUsageCopySrc|native.BufferUsageCopyDst, size, false)
		if err != nil {
			t.Fatal(err)
		}
		defer src.Release()
		want[i] = bytes.Repeat([]byte{byte(i + 1), byte(0x40 + i), 0xA5, byte(i * 3)}, int(size/4))
		queue.WriteBuffer(src, 0, want[i])
		bufs[i], err = device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, size, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(bufs[i].Release)
		enc.CopyBufferToBuffer(src, 0, bufs[i], 0, size)
	}
	submit(t, device, enc)
	return bufs, want
}

func TestMapAsyncMany(t *testing.T) {
	device := newTestDevice(t)
	const n, size = 8, 64 << 10
	bufs, want := newReadbacks(t, device, n, size)

	// Start every mapping, then poll from one goroutine until all arrive.
	chans := make([]<-chan error, n)
	for i, b := range bufs {
		chans[i] = b.MapAsync(native.MapModeRead, 0, 0)
	}
	pending := n
	results := make([]error, n)
	got := make([]bool, n)
	deadline := time.Now().Add(5 * time.Second)
	for pending > 0 {
		device.Poll(false)
		for i, ch := range chans {
			if got[i] {
				continue
			}
			select {
			case results[i] = <-ch:
				got[i] = true
				pending--
			default:
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d mappings still pending", pending)
		}
	}
	for i, b := range bufs {
		if results[i] != nil {
			t.Fatalf("buffer %d: %v", i, results[i])
		}
		data, err := b.ReadMapped(0, size)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, want[i]) {
			t.Fatalf("buffer %d has wrong contents", i)
		}
		// Mapping twice is rejected without reaching wgpu-native.
		if err := <-b.MapAsync(native.MapModeRead, 0, 0); err == nil {
			t.Fatalf("buffer %d mapped twice", i)
		}
		b.Unmap()
	}

	// The callback form, with each mapping on a part of the buffer.
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i, b := range bufs {
		wg.Add(1)
		b.MapAsyncFunc(native.MapModeRead, 256, 1024, func(err error) {
			defer wg.Done()
			if err == nil {
				var data []byte
				data, err = b.ReadMapped(256, 1024)
				if err == nil && !bytes.Equal(data, want[i][256:1280]) {
					err = fmt.Errorf("wrong contents")
				}
				if _, rerr := b.ReadMapped(0, 16); rerr == nil {
					err = fmt.Errorf("read outside the mapped range succeeded")
				}
			}
			errs[i] = err
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	if err := device.waitFor(done2err(done), 5*time.Second, "MapAsyncFunc"); err != nil {
		t.Fatal(err)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("buffer %d: %v", i, err)
		}
		bufs[i].Unmap()
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

func done2err(done <-chan struct{}) <-chan error {
	ch := make(chan error, 1)
	go func() { <-done; ch <- nil }()
	return ch
}

// Several goroutines use the blocking Map at once; each one polls the device.
func TestMapConcurrentBlocking(t *testing.T) {
	device := newTestDevice(t)
	const n, size, rounds = 8, 16 << 10, 20
	bufs, want := newReadbacks(t, device, n, size)
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i, b := range bufs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				if err := b.Map(native.MapModeRead, 0, size); err != nil {
					errs <- fmt.Errorf("buffer %d round %d: %w", i, r, err)
					return
				}
				data, err := b.ReadMapped(0, size)
				b.Unmap()
				if err != nil {
					errs <- err
					return
				}
				if !bytes.Equal(data, want[i]) {
					errs <- fmt.Errorf("buffer %d round %d: wrong contents", i, r)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}

// Each case would abort the process inside wgpu-native without the Go checks.
func TestMapErrors(t *testing.T) {
	device := newTestDevice(t)
	bufs, _ := newReadbacks(t, device, 1, 1024)
	b := bufs[0]
	if _, err := b.ReadMapped(0, 16); err == nil {
		t.Error("ReadMapped on an unmapped buffer succeeded")
	}
	if err := b.Map(native.MapModeRead, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ off, size uint64 }{{4, 16}, {0, 6}, {1024, 4}, {512, 1024}} {
		if _, err := b.ReadMapped(c.off, c.size); err == nil {
			t.Errorf("ReadMapped(%d, %d) succeeded", c.off, c.size)
		}
	}
	if _, err := b.MappedBytes(0, 16); err == nil {
		t.Error("MappedBytes on a read mapping succeeded")
	}
	b.Unmap()

	// Usage errors come back through the callback.
	storage, err := device.CreateBuffer(native.BufferUsageStorage, 256, false)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Release()
	if err := storage.Map(native.MapModeRead, 0, 0); err == nil {
		t.Error("mapping a buffer without MapRead succeeded")
	} else {
		t.Logf("no MapRead: %.100q", err.Error())
	}
	if _, err := storage.ReadMapped(0, 16); err == nil {
		t.Error("ReadMapped after a failed map succeeded")
	}

	// Unmap while pending completes the channel with an error.
	ch := b.MapAsync(native.MapModeRead, 0, 0)
	b.Unmap()
	if err := device.waitFor(ch, 5*time.Second, "MapAsync"); err == nil {
		t.Error("mapping unmapped while pending succeeded")
	} else {
		t.Logf("unmapped while pending: %v", err)
	}
	// Mapped at creation: writable range, then unmapped.
	up, err := device.CreateBuffer(native.BufferUsageCopySrc, 64, true)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Release()
	if _, err := up.MappedBytes(0, 64); err != nil {
		t.Fatal(err)
	}
	up.Unmap()
	if _, err := up.MappedBytes(0, 64); err == nil {
		t.Error("MappedBytes after Unmap succeeded")
	}
	// Map errors are returned, not reported as uncaptured errors too.
	if err := device.Err(); err != nil {
		t.Fatalf("uncaptured error: %v", err)
	}
}

func TestMapCancelledOnRelease(t *testing.T) {
	adapter := newTestAdapter(t)
	device, err := adapter.RequestDevice()
	if err != nil {
		t.Fatal(err)
	}
	bufs, _ := newReadbacks(t, device, 2, 256)
	ch := bufs[0].MapAsync(native.MapModeRead, 0, 0)
	device.Release()
	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("mapping succeeded after device Release")
		}
		t.Logf("after Release: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("mapping never completed after Release")
	}
	if err := <-bufs[1].MapAsync(native.MapModeRead, 0, 0); err == nil {
		t.Fatal("MapAsync on a released device succeeded")
	}
}
