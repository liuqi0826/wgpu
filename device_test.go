//go:build windows && amd64

package wgpu

import (
	"testing"
	"time"

	"wgpu/native"
)

func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	instance := testInstance(t)
	t.Cleanup(instance.Release)
	adapter, err := instance.RequestAdapter(&native.RequestAdapterOptions{
		PowerPreference: native.PowerPreferenceHighPerformance,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adapter.Release)
	return adapter
}

type lostEvent struct {
	reason native.DeviceLostReason
	msg    string
}

type errorEvent struct {
	typ native.ErrorType
	msg string
}

func TestDeviceLostCallback(t *testing.T) {
	adapter := newTestAdapter(t)
	lost := make(chan lostEvent, 4)
	device, err := adapter.RequestDeviceWith(&DeviceOptions{
		Label:  "lost test",
		OnLost: func(r native.DeviceLostReason, msg string) { lost <- lostEvent{r, msg} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := device.Lost(); ok {
		t.Fatal("new device reports lost")
	}
	device.Destroy()
	select {
	case ev := <-lost:
		t.Logf("lost: %s %q", ev.reason, ev.msg)
		if ev.reason != native.DeviceLostReasonDestroyed {
			t.Errorf("reason = %s, want Destroyed", ev.reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnLost was not called after Destroy")
	}
	reason, _, ok := device.Lost()
	if !ok || reason != native.DeviceLostReasonDestroyed {
		t.Fatalf("Lost() = %s, %v", reason, ok)
	}
	if err := device.Err(); err == nil {
		t.Fatal("Err() is nil on a lost device")
	}
	// Using the lost device reports errors but must not call OnLost again.
	if buf, err := device.CreateBuffer(native.BufferUsageCopyDst, 16, false); err == nil {
		buf.Release()
	}
	device.Queue().Submit()
	select {
	case ev := <-lost:
		t.Fatalf("second OnLost: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
	device.Release()
	select {
	case ev := <-lost:
		t.Fatalf("callback after Release: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestReleaseSuppressesCallbacks(t *testing.T) {
	adapter := newTestAdapter(t)
	called := make(chan string, 4)
	device, err := adapter.RequestDeviceWith(&DeviceOptions{
		OnLost:            func(r native.DeviceLostReason, _ string) { called <- "lost " + r.String() },
		OnUncapturedError: func(typ native.ErrorType, _ string) { called <- "error " + typ.String() },
	})
	if err != nil {
		t.Fatal(err)
	}
	device.Release()
	device.Release() // second Release is a no-op
	select {
	case s := <-called:
		t.Fatalf("callback for Release: %s", s)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestUncapturedErrorCallback(t *testing.T) {
	adapter := newTestAdapter(t)
	errs := make(chan errorEvent, 4)
	device, err := adapter.RequestDeviceWith(&DeviceOptions{
		OnUncapturedError: func(typ native.ErrorType, msg string) { errs <- errorEvent{typ, msg} },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Release()
	module, err := device.CreateShaderModuleWGSL("bad", "fn broken(")
	if err != nil {
		t.Fatal(err)
	}
	defer module.Release()
	select {
	case ev := <-errs:
		if ev.typ != native.ErrorTypeValidation || ev.msg == "" {
			t.Fatalf("error event %s %q", ev.typ, ev.msg)
		}
		t.Logf("uncaptured %s: %.60q...", ev.typ, ev.msg)
	case <-time.After(5 * time.Second):
		t.Fatal("OnUncapturedError was not called")
	}
	if err := device.Err(); err == nil {
		t.Fatal("Err() is nil after an uncaptured error")
	}
	if _, _, ok := device.Lost(); ok {
		t.Fatal("validation error marked the device lost")
	}
}

func TestCallbackMayCallIntoDevice(t *testing.T) {
	adapter := newTestAdapter(t)
	done := make(chan error, 1)
	var device *Device
	device, err := adapter.RequestDeviceWith(&DeviceOptions{
		OnUncapturedError: func(native.ErrorType, string) {
			// Runs outside the native call, so using the device here must not deadlock.
			buf, err := device.CreateBuffer(native.BufferUsageCopyDst, 16, false)
			if err == nil {
				buf.Release()
			}
			done <- err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Release()
	bad, _ := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageMapWrite, 16, false) // invalid usage mix
	if bad != nil {
		defer bad.Release()
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callback did not run")
	}
}

func TestRequiredLimitsAndFeatures(t *testing.T) {
	adapter := newTestAdapter(t)
	best, err := adapter.Limits()
	if err != nil {
		t.Fatal(err)
	}
	limits := UndefinedLimits()
	limits.MaxTextureDimension2D = best.MaxTextureDimension2D
	limits.MaxBufferSize = best.MaxBufferSize
	var features []native.FeatureName
	if adapter.HasFeature(native.FeatureNameDepth32FloatStencil8) {
		features = append(features, native.FeatureNameDepth32FloatStencil8)
	}
	t.Logf("adapter: %d features, maxTexture2D %d, maxBuffer %d", len(adapter.Features()), best.MaxTextureDimension2D, best.MaxBufferSize)

	device, err := adapter.RequestDeviceWith(&DeviceOptions{RequiredLimits: &limits, RequiredFeatures: features})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Release()
	got, err := device.Limits()
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxTextureDimension2D != best.MaxTextureDimension2D || got.MaxBufferSize != best.MaxBufferSize {
		t.Fatalf("device limits %d/%d, want %d/%d", got.MaxTextureDimension2D, got.MaxBufferSize, best.MaxTextureDimension2D, best.MaxBufferSize)
	}
	for _, f := range features {
		if !device.HasFeature(f) {
			t.Fatalf("feature %s not enabled", f)
		}
	}

	// Asking for more than the adapter offers must fail cleanly.
	tooBig := UndefinedLimits()
	tooBig.MaxTextureDimension2D = best.MaxTextureDimension2D * 2
	if d, err := adapter.RequestDeviceWith(&DeviceOptions{RequiredLimits: &tooBig}); err == nil {
		d.Release()
		t.Fatal("over-limit request succeeded")
	} else {
		t.Logf("over-limit: %v", err)
	}
}
