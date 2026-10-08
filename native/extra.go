//go:build windows && amd64

package native

// Sentinel values from webgpu.h. Bool is a uint32 in the C API, not a C _Bool.
const (
	False Bool = 0
	True  Bool = 1

	// Strlen marks a StringView as a null-terminated string.
	Strlen uintptr = ^uintptr(0)
	// WholeMapSize maps through the end of a buffer.
	WholeMapSize uintptr = ^uintptr(0)
	// WholeSize copies through the end of a buffer.
	WholeSize uint64 = ^uint64(0)

	LimitU32Undefined uint32 = ^uint32(0)
	LimitU64Undefined uint64 = ^uint64(0)
)
