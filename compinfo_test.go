//go:build windows && amd64

package wgpu

import (
	"strings"
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// createQuiet creates a shader module inside an error scope so an invalid
// source does not reach the uncaptured-error handler.
func createQuiet(t *testing.T, device *Device, label, code string) *ShaderModule {
	t.Helper()
	device.PushErrorScope(native.ErrorFilterValidation)
	module, err := device.CreateShaderModuleWGSL(label, code)
	if err != nil {
		t.Fatal(err)
	}
	_ = device.PopErrorScope()
	t.Cleanup(module.Release)
	return module
}

// wgpuShaderModuleGetCompilationInfo aborts the process in wgpu-native 29, so
// CompilationInfo recompiles the source inside error scopes.
func TestShaderModuleCompilationInfo(t *testing.T) {
	device := newTestDevice(t)

	t.Run("valid", func(t *testing.T) {
		module := createQuiet(t, device, "ok", triangleWGSL)
		for i := range 2 {
			msgs, err := module.CompilationInfo()
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 0 {
				t.Fatalf("call %d: %d messages for a valid shader: %+v", i, len(msgs), msgs)
			}
		}
	})

	cases := []struct {
		name, label, code string
		line, pos         uint64
		contains          string
	}{
		// Parse error: the stray ';' is line 3, column 13.
		{"parse", "parse", "@compute @workgroup_size(1)\nfn main() {\n    let x = ;\n}\n", 3, 13, "parsing"},
		// Validation error with a ':' in the label: a 4-byte array stride in a
		// uniform buffer; naga points at "var" on line 3, column 25.
		{"validation", "a:b", "// globals\n\n  @group(0) @binding(0) var<uniform> u: array<f32>;\n", 3, 25, "validation"},
		// Validation error without a source location: a vertex entry point without a position output.
		{"no location", "vs", "@vertex\nfn vs() -> @location(0) vec4f {\n    return vec4f(0.0);\n}\n", 0, 0, "position"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			module := createQuiet(t, device, tc.label, tc.code)
			msgs, err := module.CompilationInfo()
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 1 {
				t.Fatalf("%d messages, want 1: %+v", len(msgs), msgs)
			}
			m := msgs[0]
			t.Logf("%s line %d pos %d: %q", m.Type, m.LineNum, m.LinePos, m.Message)
			if m.Type != native.CompilationMessageTypeError || !strings.Contains(m.Message, tc.contains) {
				t.Fatalf("message %s %q", m.Type, m.Message)
			}
			if m.LineNum != tc.line || m.LinePos != tc.pos {
				t.Fatalf("location %d:%d, want %d:%d", m.LineNum, m.LinePos, tc.line, tc.pos)
			}
			msgs[0].Message = "changed"
			again, err := module.CompilationInfo()
			if err != nil || len(again) != 1 || again[0] != m {
				t.Fatalf("cached result %+v, %v", again, err)
			}
		})
	}
	if err := device.Err(); err != nil {
		t.Fatalf("CompilationInfo leaked an uncaptured error: %v", err)
	}

	t.Run("released module", func(t *testing.T) {
		module := createQuiet(t, device, "ok", triangleWGSL)
		module.Release()
		if _, err := module.CompilationInfo(); err == nil {
			t.Fatal("no error for a released module")
		}
		var nilModule *ShaderModule
		if _, err := nilModule.CompilationInfo(); err == nil {
			t.Fatal("no error for a nil module")
		}
	})

	t.Run("lost device", func(t *testing.T) {
		lost := newTestDevice(t)
		module := createQuiet(t, lost, "ok", triangleWGSL)
		lost.Destroy()
		_, err := module.CompilationInfo()
		if err == nil {
			t.Fatal("no error on a lost device")
		}
		t.Logf("lost device: %v", err)
	})
}
