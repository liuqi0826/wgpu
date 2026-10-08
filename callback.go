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

type deviceState struct {
	mu   sync.Mutex
	lost string
	err  string
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

func onDeviceLost(_, _, message, userdata1, _ uintptr) uintptr {
	if v, ok := deviceStates.Load(userdata1); ok {
		state := v.(*deviceState)
		state.mu.Lock()
		state.lost = messageAt(message)
		state.mu.Unlock()
	}
	return 0
}

func onUncapturedError(_, _, message, userdata1, _ uintptr) uintptr {
	if v, ok := deviceStates.Load(userdata1); ok {
		state := v.(*deviceState)
		state.mu.Lock()
		state.err = messageAt(message)
		state.mu.Unlock()
	}
	return 0
}

func cPointer(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}
