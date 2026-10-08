//go:build windows && amd64

package wgpu

import (
	"encoding/binary"
	"slices"
	"strconv"

	"github.com/liuqi0826/wgpu/native"
)

// QueryTypePipelineStatistics is the wgpu-native query type for pipeline
// statistics. Use it as QuerySetDescriptor.Type together with
// QuerySetDescriptor.PipelineStatistics.
const QueryTypePipelineStatistics = native.QueryType(native.NativeQueryTypePipelineStatistics)

// FeaturePipelineStatisticsQuery is the wgpu-native feature needed for
// pipeline statistics queries. Check Adapter.HasFeature before putting it in
// DeviceOptions.RequiredFeatures.
const FeaturePipelineStatisticsQuery = native.FeatureName(native.NativeFeaturePipelineStatisticsQuery)

// PipelineStatistics holds one resolved pipeline statistics query. Only the
// fields named in the query set's statistic list are filled in; Has reports
// which ones.
type PipelineStatistics struct {
	VertexShaderInvocations   uint64
	ClipperInvocations        uint64
	ClipperPrimitivesOut      uint64
	FragmentShaderInvocations uint64
	ComputeShaderInvocations  uint64

	mask uint32
}

// Has reports whether the statistic was collected.
func (s *PipelineStatistics) Has(name native.PipelineStatisticName) bool {
	return validStatistic(name) && s.mask&(1<<name) != 0
}

// Value returns the statistic by name, or 0 if it was not collected.
func (s *PipelineStatistics) Value(name native.PipelineStatisticName) uint64 {
	if p := s.field(name); p != nil && s.Has(name) {
		return *p
	}
	return 0
}

func (s *PipelineStatistics) field(name native.PipelineStatisticName) *uint64 {
	switch name {
	case native.PipelineStatisticNameVertexShaderInvocations:
		return &s.VertexShaderInvocations
	case native.PipelineStatisticNameClipperInvocations:
		return &s.ClipperInvocations
	case native.PipelineStatisticNameClipperPrimitivesOut:
		return &s.ClipperPrimitivesOut
	case native.PipelineStatisticNameFragmentShaderInvocations:
		return &s.FragmentShaderInvocations
	case native.PipelineStatisticNameComputeShaderInvocations:
		return &s.ComputeShaderInvocations
	}
	return nil
}

func validStatistic(name native.PipelineStatisticName) bool {
	return name <= native.PipelineStatisticNameComputeShaderInvocations
}

// normalizeStatistics checks the list and returns it sorted without
// duplicates. wgpu-native panics on an unknown name, and wgpu-core stores the
// statistics as a bit set, writing the results in bit order whatever order
// they were listed in.
func normalizeStatistics(stats []native.PipelineStatisticName) ([]native.PipelineStatisticName, error) {
	if len(stats) == 0 {
		return nil, &Error{Op: "CreateQuerySet", Status: "pipeline statistics query set without statistics"}
	}
	out := make([]native.PipelineStatisticName, 0, len(stats))
	for _, s := range stats {
		if !validStatistic(s) {
			return nil, &Error{Op: "CreateQuerySet", Status: "unknown pipeline statistic " + strconv.FormatUint(uint64(s), 10)}
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// PipelineStatistics returns the statistics collected by a pipeline
// statistics query set, in the order the results are laid out.
func (q *QuerySet) PipelineStatistics() []native.PipelineStatisticName {
	return slices.Clone(q.stats)
}

// ResultStride returns the number of bytes ResolveQuerySet writes per query:
// 8 for occlusion and timestamp queries and 8 per statistic for pipeline
// statistics queries.
func (q *QuerySet) ResultStride() uint64 {
	if len(q.stats) > 0 {
		return 8 * uint64(len(q.stats))
	}
	return 8
}

// ParsePipelineStatistics decodes data resolved from this query set (one
// ResultStride per query) into one PipelineStatistics per query.
func (q *QuerySet) ParsePipelineStatistics(data []byte) ([]PipelineStatistics, error) {
	if q.typ != QueryTypePipelineStatistics {
		return nil, &Error{Op: "ParsePipelineStatistics", Status: "not a pipeline statistics query set"}
	}
	return DecodePipelineStatistics(q.stats, data)
}

// DecodePipelineStatistics decodes resolved pipeline statistics results for
// a query set created with the statistics stats. data holds one uint64 per
// statistic per query.
func DecodePipelineStatistics(stats []native.PipelineStatisticName, data []byte) ([]PipelineStatistics, error) {
	stats, err := normalizeStatistics(stats)
	if err != nil {
		return nil, &Error{Op: "DecodePipelineStatistics", Status: err.(*Error).Status}
	}
	stride := 8 * len(stats)
	if len(data)%stride != 0 {
		return nil, &Error{Op: "DecodePipelineStatistics", Status: "data is not a whole number of queries",
			Message: strconv.Itoa(len(data)) + " bytes, " + strconv.Itoa(stride) + " per query"}
	}
	out := make([]PipelineStatistics, len(data)/stride)
	for i := range out {
		r := &out[i]
		for j, name := range stats {
			*r.field(name) = binary.LittleEndian.Uint64(data[i*stride+8*j:])
			r.mask |= 1 << name
		}
	}
	return out, nil
}

// pipelineStatisticsQuery checks the query set before it reaches wgpu-native,
// which aborts on a null handle.
func pipelineStatisticsQuery(q *QuerySet) string {
	switch {
	case q.Native() == 0:
		return "nil or released query set"
	case q.typ != QueryTypePipelineStatistics:
		return "not a pipeline statistics query set"
	}
	return ""
}

// Begin/End pairs are tracked on the Go side: wgpu-core 29 accepts a compute
// pass that ends with a statistics query still open, which would leave the
// query running at submission.

// BeginPipelineStatisticsQuery starts collecting statistics into query index
// of q, a pipeline statistics query set. Queries cannot nest; end each one
// with EndPipelineStatisticsQuery before the pass ends, otherwise
// CommandEncoder.Finish fails.
func (p *RenderPassEncoder) BeginPipelineStatisticsQuery(q *QuerySet, index uint32) {
	const op = "RenderPassEncoderBeginPipelineStatisticsQuery"
	if status := pipelineStatisticsQuery(q); status != "" {
		p.enc.setErr(op, status)
		return
	}
	if p.statsActive {
		p.enc.setErr(op, "pipeline statistics query already active")
		return
	}
	native.RenderPassEncoderBeginPipelineStatisticsQuery(p.Native(), q.Native(), index)
	p.statsActive = true
}

// EndPipelineStatisticsQuery ends the current pipeline statistics query.
func (p *RenderPassEncoder) EndPipelineStatisticsQuery() {
	if !p.statsActive {
		p.enc.setErr("RenderPassEncoderEndPipelineStatisticsQuery", "no active pipeline statistics query")
		return
	}
	native.RenderPassEncoderEndPipelineStatisticsQuery(p.Native())
	p.statsActive = false
}

// BeginPipelineStatisticsQuery starts collecting statistics into query index
// of q, a pipeline statistics query set. Queries cannot nest; end each one
// with EndPipelineStatisticsQuery before the pass ends, otherwise
// CommandEncoder.Finish fails.
func (p *ComputePassEncoder) BeginPipelineStatisticsQuery(q *QuerySet, index uint32) {
	const op = "ComputePassEncoderBeginPipelineStatisticsQuery"
	if status := pipelineStatisticsQuery(q); status != "" {
		p.enc.setErr(op, status)
		return
	}
	if p.statsActive {
		p.enc.setErr(op, "pipeline statistics query already active")
		return
	}
	native.ComputePassEncoderBeginPipelineStatisticsQuery(p.Native(), q.Native(), index)
	p.statsActive = true
}

// EndPipelineStatisticsQuery ends the current pipeline statistics query.
func (p *ComputePassEncoder) EndPipelineStatisticsQuery() {
	if !p.statsActive {
		p.enc.setErr("ComputePassEncoderEndPipelineStatisticsQuery", "no active pipeline statistics query")
		return
	}
	native.ComputePassEncoderEndPipelineStatisticsQuery(p.Native())
	p.statsActive = false
}
