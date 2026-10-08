//go:build windows && amd64

package wgpu

import (
	"testing"

	"wgpu/native"
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
