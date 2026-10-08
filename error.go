//go:build windows && amd64

package wgpu

import "fmt"

// Error is a failed native call or an asynchronous WebGPU status.
type Error struct {
	Op      string
	Status  string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("wgpu: %s: %s", e.Op, e.Status)
	}
	return fmt.Sprintf("wgpu: %s: %s: %s", e.Op, e.Status, e.Message)
}
