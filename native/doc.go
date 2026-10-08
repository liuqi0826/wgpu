//go:build windows && amd64

// Package native is a CGO-free binding to the wgpu-native C API.
//
// Types and functions are generated from the webgpu.h and wgpu.h that match
// wgpu-core 29. Call Load before any function variable. Those variables use
// purego, so the process does not need a C compiler or CGO.
//
// Pointer fields inside structs are uintptr. Memory that a struct points at
// must stay reachable until the native call returns.
//
//go:generate python ../gen/generate.py
package native
