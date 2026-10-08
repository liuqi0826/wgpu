//go:build windows && amd64

package wgpu

import (
	"strconv"
	"sync/atomic"

	"github.com/liuqi0826/wgpu/native"
)

// QueryIndexNone skips one write of PassTimestampWrites.
const QueryIndexNone = native.QuerySetIndexUndefined

// QuerySetDescriptor describes Device.CreateQuerySet. Timestamp queries need
// a device created with native.FeatureNameTimestampQuery.
//
// Pipeline statistics queries use QueryTypePipelineStatistics, list the
// statistics to collect in PipelineStatistics and need a device created with
// FeaturePipelineStatisticsQuery.
type QuerySetDescriptor struct {
	Label string
	Type  native.QueryType
	Count uint32
	// PipelineStatistics is only used with QueryTypePipelineStatistics.
	// Order and duplicates do not matter; results are laid out in ascending
	// native.PipelineStatisticName order, see QuerySet.PipelineStatistics.
	PipelineStatistics []native.PipelineStatisticName
}

// QuerySet is a WGPUQuerySet.
type QuerySet struct {
	ptr   atomic.Uintptr
	typ   native.QueryType
	stats []native.PipelineStatisticName
}

// CreateQuerySet creates a query set.
func (d *Device) CreateQuerySet(desc *QuerySetDescriptor) (*QuerySet, error) {
	if desc == nil {
		return nil, &Error{Op: "CreateQuerySet", Status: "nil descriptor"}
	}
	var m cmem
	raw := cnew(&m, native.QuerySetDescriptor{
		Label: m.str(desc.Label),
		Type:  desc.Type,
		Count: desc.Count,
	})
	// wgpu-native panics on an unknown query type, on a statistics query set
	// without its extras and on an unknown statistic.
	var stats []native.PipelineStatisticName
	switch desc.Type {
	case native.QueryTypeOcclusion, native.QueryTypeTimestamp:
		if len(desc.PipelineStatistics) > 0 {
			return nil, &Error{Op: "CreateQuerySet", Status: "pipeline statistics listed for query type " + desc.Type.String()}
		}
	case QueryTypePipelineStatistics:
		var err error
		if stats, err = normalizeStatistics(desc.PipelineStatistics); err != nil {
			return nil, err
		}
		if !d.HasFeature(FeaturePipelineStatisticsQuery) {
			return nil, &Error{Op: "CreateQuerySet", Status: "device was not created with FeaturePipelineStatisticsQuery"}
		}
		names := cslice[native.PipelineStatisticName](&m, len(stats))
		copy(names, stats)
		raw.NextInChain = addr(cnew(&m, native.QuerySetDescriptorExtras{
			Chain:                  native.ChainedStruct{SType: native.STypeQuerySetDescriptorExtras},
			PipelineStatistics:     sliceAddr(names),
			PipelineStatisticCount: uintptr(len(names)),
		}))
	default:
		return nil, &Error{Op: "CreateQuerySet", Status: "unknown query type 0x" + strconv.FormatUint(uint64(desc.Type), 16)}
	}
	var handle native.QuerySet
	err := d.guard(func() { handle = native.DeviceCreateQuerySet(d.Native(), raw) })
	m.done()
	if err != nil {
		if handle != 0 {
			native.QuerySetRelease(handle)
		}
		return nil, err
	}
	if handle == 0 {
		return nil, d.fail("CreateQuerySet")
	}
	q := &QuerySet{typ: desc.Type, stats: stats}
	q.ptr.Store(uintptr(handle))
	return q, nil
}

// Native returns the raw handle.
func (q *QuerySet) Native() native.QuerySet {
	if q == nil {
		return 0
	}
	return native.QuerySet(q.ptr.Load())
}

// Count returns the number of queries.
func (q *QuerySet) Count() uint32 { return native.QuerySetGetCount(q.Native()) }

// Type returns the query type.
func (q *QuerySet) Type() native.QueryType { return native.QuerySetGetType(q.Native()) }

// Destroy frees the queries now; the handle stays valid until Release.
func (q *QuerySet) Destroy() {
	if h := q.Native(); h != 0 {
		native.QuerySetDestroy(h)
	}
}

// Release drops the query set.
func (q *QuerySet) Release() {
	handle := native.QuerySet(q.ptr.Swap(0))
	if handle != 0 {
		native.QuerySetRelease(handle)
	}
}

// PassTimestampWrites writes GPU timestamps at the start and end of a pass.
// Use QueryIndexNone to skip one of them.
type PassTimestampWrites struct {
	QuerySet                  *QuerySet
	BeginningOfPassWriteIndex uint32
	EndOfPassWriteIndex       uint32
}

// raw returns zero for nil writes and an error for a missing query set,
// which wgpu-native would abort on.
func (w *PassTimestampWrites) raw(m *cmem, op string) (uintptr, error) {
	if w == nil {
		return 0, nil
	}
	if w.QuerySet.Native() == 0 {
		return 0, &Error{Op: op, Status: "timestamp writes without a query set"}
	}
	return addr(cnew(m, native.PassTimestampWrites{
		QuerySet:                  w.QuerySet.Native(),
		BeginningOfPassWriteIndex: w.BeginningOfPassWriteIndex,
		EndOfPassWriteIndex:       w.EndOfPassWriteIndex,
	})), nil
}

// ResolveQuerySet writes count results starting at first into dst as uint64
// values. dst needs BufferUsageQueryResolve and offset must be a multiple of
// 256. Timestamps resolve to nanoseconds when Queue.TimestampPeriod is 1.
func (e *CommandEncoder) ResolveQuerySet(q *QuerySet, first, count uint32, dst *Buffer, offset uint64) {
	if q.Native() == 0 || dst.Native() == 0 {
		e.setErr("CommandEncoderResolveQuerySet", "nil or released query set or buffer")
		return
	}
	native.CommandEncoderResolveQuerySet(e.Native(), q.Native(), first, count, dst.Native(), offset)
}

// WriteTimestamp writes a timestamp between passes. It needs
// native.NativeFeatureTimestampQueryInsideEncoders.
func (e *CommandEncoder) WriteTimestamp(q *QuerySet, index uint32) {
	if q.Native() == 0 {
		e.setErr("CommandEncoderWriteTimestamp", "nil or released query set")
		return
	}
	native.CommandEncoderWriteTimestamp(e.Native(), q.Native(), index)
}

// BeginOcclusionQuery starts occlusion query index of the pass's
// OcclusionQuerySet. The result counts samples that passed the depth and
// stencil tests; it is only guaranteed to be zero or non-zero.
func (p *RenderPassEncoder) BeginOcclusionQuery(index uint32) {
	native.RenderPassEncoderBeginOcclusionQuery(p.Native(), index)
}

// EndOcclusionQuery ends the current occlusion query.
func (p *RenderPassEncoder) EndOcclusionQuery() {
	native.RenderPassEncoderEndOcclusionQuery(p.Native())
}

// TimestampPeriod returns nanoseconds per timestamp tick. wgpu normalizes
// resolved timestamps when it can, in which case this is 1.
func (q *Queue) TimestampPeriod() float32 {
	return native.QueueGetTimestampPeriod(q.ptr)
}
