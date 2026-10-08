//go:build windows && amd64

package wgpu

import (
	"time"

	"github.com/liuqi0826/wgpu/native"
)

// OnSubmittedWorkDone returns a channel that receives one value once all
// work submitted before the call has finished, then is closed. The value is
// nil on success.
//
// wgpu-native never runs the callback inside this call, even with an idle
// queue, and has no WaitAny: the result only arrives while the device is
// polled, by Device.Poll or a later Queue.Submit. Use WaitSubmittedWorkDone
// to block. Release and device loss complete the channel with an error.
func (q *Queue) OnSubmittedWorkDone() <-chan error {
	op, err := q.dev.newOp(func(status uintptr, msg string) error {
		if s := native.QueueWorkDoneStatus(status); s != native.QueueWorkDoneStatusSuccess {
			return &Error{Op: "QueueOnSubmittedWorkDone", Status: s.String(), Message: msg}
		}
		return nil
	}, nil)
	if err != nil {
		ch := make(chan error, 1)
		ch <- err
		close(ch)
		return ch
	}
	native.QueueOnSubmittedWorkDone(q.ptr, native.QueueWorkDoneCallbackInfo{
		Mode:      native.CallbackModeAllowProcessEvents,
		Callback:  native.QueueWorkDoneCallback(workDoneCB),
		Userdata1: op.id,
	})
	return op.ch
}

// WaitSubmittedWorkDone blocks, polling the device, until all work submitted
// so far has finished. A zero timeout waits without limit.
func (q *Queue) WaitSubmittedWorkDone(timeout time.Duration) error {
	ch := q.OnSubmittedWorkDone()
	select {
	case err := <-ch:
		return err
	default:
	}
	if timeout == 0 {
		// Blocks in wgpu-core until the queue is idle, which fires the callback.
		q.dev.Poll(true)
	}
	return q.dev.waitFor(ch, timeout, "QueueOnSubmittedWorkDone")
}
