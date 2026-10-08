//go:build windows && amd64

package wgpu

import (
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// testInstance honours WGPU_BACKEND=vulkan|dx12|gl and defaults to the
// primary backends.
func testInstance(t *testing.T) *Instance {
	t.Helper()
	backends, err := BackendsFromEnv(native.InstanceBackendPrimary)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := CreateInstance(&InstanceOptions{Backends: backends})
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestParseBackends(t *testing.T) {
	cases := map[string]native.InstanceBackend{
		"":           native.InstanceBackendAll,
		"all":        native.InstanceBackendAll,
		"vulkan":     native.InstanceBackendVulkan,
		"DX12":       native.InstanceBackendDX12,
		"d3d12":      native.InstanceBackendDX12,
		"gl":         native.InstanceBackendGL,
		"vulkan, gl": native.InstanceBackendVulkan | native.InstanceBackendGL,
		"primary":    native.InstanceBackendPrimary,
	}
	for in, want := range cases {
		got, err := ParseBackends(in)
		if err != nil || got != want {
			t.Errorf("ParseBackends(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseBackends("d3d11"); err == nil {
		t.Error("ParseBackends(d3d11) succeeded")
	}
}

func TestInstanceOptionsFlags(t *testing.T) {
	cases := []struct {
		opts InstanceOptions
		want native.InstanceFlag
	}{
		{InstanceOptions{}, native.InstanceFlagDefault},
		{InstanceOptions{ExplicitFlags: true}, native.InstanceFlagEmpty},
		{InstanceOptions{Flags: native.InstanceFlagDebug}, native.InstanceFlagDebug},
		{InstanceOptions{Flags: native.InstanceFlagDefault | native.InstanceFlagDebug}, native.InstanceFlagDefault | native.InstanceFlagDebug},
		{InstanceOptions{Flags: native.InstanceFlagDebug, ExplicitFlags: true}, native.InstanceFlagDebug},
	}
	for _, tc := range cases {
		if got := tc.opts.flags(); got != tc.want {
			t.Errorf("%+v: flags %#x, want %#x", tc.opts, got, tc.want)
		}
	}
	// Both kinds of instance must be creatable.
	for _, opts := range []InstanceOptions{{}, {ExplicitFlags: true}} {
		backends, err := BackendsFromEnv(native.InstanceBackendPrimary)
		if err != nil {
			t.Fatal(err)
		}
		opts.Backends = backends
		instance, err := CreateInstance(&opts)
		if err != nil {
			t.Fatal(err)
		}
		instance.Release()
	}
}
