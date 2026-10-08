//go:build windows && amd64

package wgpu

import (
	"fmt"
	"os"
	"strings"

	"github.com/liuqi0826/wgpu/native"
)

// BackendEnv is the environment variable read by BackendsFromEnv.
const BackendEnv = "WGPU_BACKEND"

// ParseBackends maps backend names to instance backend bits.
// Names are vulkan (vk), dx12 (d3d12), gl (opengl), metal, primary,
// secondary and all; a comma-separated list combines them. An empty string
// or "all" returns zero, which enables every backend.
func ParseBackends(s string) (native.InstanceBackend, error) {
	var out native.InstanceBackend
	for _, name := range strings.Split(s, ",") {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "", "all":
			return native.InstanceBackendAll, nil
		case "vulkan", "vk":
			out |= native.InstanceBackendVulkan
		case "dx12", "d3d12":
			out |= native.InstanceBackendDX12
		case "gl", "opengl", "gles":
			out |= native.InstanceBackendGL
		case "metal", "mtl":
			out |= native.InstanceBackendMetal
		case "primary":
			out |= native.InstanceBackendPrimary
		case "secondary":
			out |= native.InstanceBackendSecondary
		default:
			return 0, fmt.Errorf("wgpu: unknown backend %q", name)
		}
	}
	return out, nil
}

// BackendsFromEnv parses WGPU_BACKEND. An unset or empty variable returns def.
func BackendsFromEnv(def native.InstanceBackend) (native.InstanceBackend, error) {
	s := strings.TrimSpace(os.Getenv(BackendEnv))
	if s == "" {
		return def, nil
	}
	return ParseBackends(s)
}
