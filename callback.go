//go:build windows && amd64

package wgpu

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"wgpu/native"
)

type callbackResult struct {
	status  uintptr
	handle  uintptr
	extra   uintptr
	message string
}

var (
	callbackOnce sync.Once

	adapterCB uintptr
	deviceCB  uintptr
	mapCB     uintptr
	scopeCB   uintptr
	lostCB    uintptr
	errorCB   uintptr

	adapterWaiters sync.Map
	deviceWaiters  sync.Map
	mapWaiters     sync.Map
	scopeWaiters   sync.Map
	waiterSeq      atomic.Uint64

	deviceStates sync.Map
)

// deviceState is shared between a Device and the native callbacks, which may
// run on a wgpu-internal thread. User callbacks are queued and run in order on
// a separate goroutine, so they never execute inside a native call and may
// call back into wgpu.
type deviceState struct {
	mu         sync.Mutex
	lost       bool
	lostReason native.DeviceLostReason
	lostMsg    string
	err        string
	errType    native.ErrorType
	released   bool

	onLost  func(reason native.DeviceLostReason, message string)
	onError func(typ native.ErrorType, message string)
	queue   []func()
	running bool
}

// dispatch queues fn unless the device was released. Call without s.mu held.
func (s *deviceState) dispatch(fn func()) {
	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return
	}
	s.queue = append(s.queue, fn)
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()
	go s.drain()
}

func (s *deviceState) drain() {
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.running = false
			s.mu.Unlock()
			return
		}
		fn := s.queue[0]
		s.queue[0] = nil
		s.queue = s.queue[1:]
		s.mu.Unlock()
		fn()
	}
}

func ensureCallbacks() {
	callbackOnce.Do(func() {
		// Aggregates larger than 8 bytes, including StringView, are passed as
		// pointers under the Windows x64 ABI. purego callbacks cannot take
		// those structs directly, so the message argument is that pointer.
		adapterCB = purego.NewCallback(onRequestAdapter)
		deviceCB = purego.NewCallback(onRequestDevice)
		mapCB = purego.NewCallback(onBufferMap)
		scopeCB = purego.NewCallback(onPopErrorScope)
		lostCB = purego.NewCallback(onDeviceLost)
		errorCB = purego.NewCallback(onUncapturedError)
	})
}

func newWaiter(m *sync.Map) (uintptr, chan callbackResult) {
	id := uintptr(waiterSeq.Add(1))
	ch := make(chan callbackResult, 1)
	m.Store(id, ch)
	return id, ch
}

func tryTake(m *sync.Map, id uintptr) (callbackResult, bool) {
	v, ok := m.Load(id)
	if !ok {
		return callbackResult{}, false
	}
	select {
	case res := <-v.(chan callbackResult):
		m.Delete(id)
		return res, true
	default:
		return callbackResult{}, false
	}
}

func deliver(m *sync.Map, id uintptr, res callbackResult) {
	v, ok := m.Load(id)
	if !ok {
		return
	}
	select {
	case v.(chan callbackResult) <- res:
	default:
	}
}

func messageAt(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	view := *(*native.StringView)(cPointer(ptr))
	return view.String()
}

func onRequestAdapter(status, adapter, message, userdata1, _ uintptr) uintptr {
	deliver(&adapterWaiters, userdata1, callbackResult{
		status:  status,
		handle:  adapter,
		message: messageAt(message),
	})
	return 0
}

func onRequestDevice(status, device, message, userdata1, _ uintptr) uintptr {
	deliver(&deviceWaiters, userdata1, callbackResult{
		status:  status,
		handle:  device,
		message: messageAt(message),
	})
	return 0
}

func onBufferMap(status, message, userdata1, _ uintptr) uintptr {
	deliver(&mapWaiters, userdata1, callbackResult{
		status:  status,
		message: messageAt(message),
	})
	return 0
}

func onPopErrorScope(status, typ, message, userdata1, _ uintptr) uintptr {
	deliver(&scopeWaiters, userdata1, callbackResult{
		status:  status,
		extra:   typ,
		message: messageAt(message),
	})
	return 0
}

// markLost records the first loss and queues OnLost once.
func (s *deviceState) markLost(reason native.DeviceLostReason, msg string) {
	s.mu.Lock()
	if s.lost {
		s.mu.Unlock()
		return
	}
	s.lost = true
	s.lostReason = reason
	s.lostMsg = msg
	cb := s.onLost
	s.mu.Unlock()
	if cb != nil {
		s.dispatch(func() { cb(reason, msg) })
	}
}

// onDeviceLost receives (WGPUDevice const *, reason, message, userdata1, userdata2).
// A slot that is gone belongs to a released device and is ignored.
//
// wgpu-native 29 does not forward wgpu-core's device-lost closure. It calls
// this only when a later call fails with DeviceError::Lost, always with reason
// Destroyed. Device.Destroy therefore records the loss itself.
func onDeviceLost(_, reason, message, userdata1, _ uintptr) uintptr {
	if v, ok := deviceStates.Load(userdata1); ok {
		v.(*deviceState).markLost(native.DeviceLostReason(reason), messageAt(message))
	}
	return 0
}

// onUncapturedError receives (WGPUDevice const *, type, message, userdata1, userdata2).
func onUncapturedError(_, typ, message, userdata1, _ uintptr) uintptr {
	v, ok := deviceStates.Load(userdata1)
	if !ok {
		return 0
	}
	state := v.(*deviceState)
	t := native.ErrorType(typ)
	msg := messageAt(message)
	state.mu.Lock()
	state.err = msg
	state.errType = t
	cb := state.onError
	state.mu.Unlock()
	if cb != nil {
		state.dispatch(func() { cb(t, msg) })
	}
	return 0
}

func cPointer(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}
