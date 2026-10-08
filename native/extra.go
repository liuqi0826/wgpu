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

	// ArrayLayerCountUndefined covers the remaining array layers of a view.
	ArrayLayerCountUndefined uint32 = ^uint32(0)
	// MipLevelCountUndefined covers the remaining mip levels of a view.
	MipLevelCountUndefined uint32 = ^uint32(0)
	// CopyStrideUndefined leaves BytesPerRow or RowsPerImage unspecified.
	CopyStrideUndefined uint32 = ^uint32(0)
	// DepthSliceUndefined is the depth slice of a non-3D color attachment.
	DepthSliceUndefined uint32 = ^uint32(0)
	// QuerySetIndexUndefined disables a timestamp write slot.
	QuerySetIndexUndefined uint32 = ^uint32(0)
)

// NullString is the null StringView, which asks for a default value such as
// a shader's only entry point. The zero StringView is the empty string.
var NullString = StringView{Length: Strlen}
