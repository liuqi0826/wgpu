//go:build windows && amd64

package wgpu

import (
	"sync"
	"time"
)

// pendingOp is an asynchronous native operation whose callback fires only
// while the device is polled (Device.Poll, or maintenance inside a later
// Queue.Submit). This wgpu-native has no WaitAny and no polling thread.
type pendingOp struct {
	id      uintptr
	state   *deviceState
	ch      chan error
	once    sync.Once
	convert func(status uintptr, message string) error
}

// pendingOps maps callback userdata to operations; a missing id belongs to
// an operation that was already completed or cancelled.
var pendingOps sync.Map

func (p *pendingOp) complete(err error) {
	p.once.Do(func() {
		p.ch <- err
		close(p.ch)
	})
}

// newOp registers an operation. It returns nil and an error when the device
// is lost or released, because the callback would never be delivered.
func (d *Device) newOp(convert func(uintptr, string) error) (*pendingOp, error) {
	ensureCallbacks()
	s := d.state
	op := &pendingOp{id: uintptr(waiterSeq.Add(1)), state: s, ch: make(chan error, 1), convert: convert}
	s.mu.Lock()
	switch {
	case s.released:
		s.mu.Unlock()
		return nil, &Error{Op: "Device", Status: "released"}
	case s.lost:
		s.mu.Unlock()
		return nil, d.Err()
	}
	if s.pending == nil {
		s.pending = make(map[uintptr]*pendingOp)
	}
	s.pending[op.id] = op
	s.mu.Unlock()
	pendingOps.Store(op.id, op)
	return op, nil
}

// finishOp runs on the thread that polls the device, inside a native call.
// It only hands the result to a buffered channel.
func finishOp(id, status uintptr, message string) {
	v, ok := pendingOps.LoadAndDelete(id)
	if !ok {
		return
	}
	op := v.(*pendingOp)
	op.state.mu.Lock()
	delete(op.state.pending, id)
	op.state.mu.Unlock()
	op.complete(op.convert(status, message))
}

// cancelPending completes every outstanding operation with err.
func (s *deviceState) cancelPending(err error) {
	s.mu.Lock()
	ops := s.pending
	s.pending = nil
	s.mu.Unlock()
	for id, op := range ops {
		pendingOps.Delete(id)
		op.complete(err)
	}
}

// waitFor polls the device until ch delivers or timeout passes. A zero
// timeout waits for as long as the operation stays pending. Lost or released
// devices complete pending channels, so this does not spin forever then.
func (d *Device) waitFor(ch <-chan error, timeout time.Duration, op string) error {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		select {
		case err := <-ch:
			return err
		default:
		}
		d.Poll(false)
		select {
		case err := <-ch:
			return err
		default:
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return &Error{Op: op, Status: "timed out"}
		}
		time.Sleep(200 * time.Microsecond)
	}
}
