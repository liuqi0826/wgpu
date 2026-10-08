//go:build windows && amd64

package wgpu

import (
	"strings"
	"testing"

	"github.com/liuqi0826/wgpu/native"
)

// TestOcclusionQueryMisuse checks that broken Begin/End sequences are caught
// on the Go side and reported by Finish. wgpu-native would also reject them,
// but only with a generic validation error from CommandEncoderFinish.
func TestOcclusionQueryMisuse(t *testing.T) {
	device := newTestDevice(t)
	_, view := newTarget(t, device, 4, 4, native.TextureFormatRGBA8Unorm)
	queries, err := device.CreateQuerySet(&QuerySetDescriptor{Label: "occlusion", Type: native.QueryTypeOcclusion, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Release()
	withSet := &RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}, OcclusionQuerySet: queries}
	noSet := &RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: view}}}

	cases := []struct {
		name   string
		desc   *RenderPassDescriptor
		record func(p *RenderPassEncoder)
		op     string
		want   string
	}{
		{"not ended", withSet, func(p *RenderPassEncoder) {
			p.BeginOcclusionQuery(1)
		}, "RenderPassEncoderEnd", "occlusion query 1 still active"},
		{"nested", withSet, func(p *RenderPassEncoder) {
			p.BeginOcclusionQuery(0)
			p.BeginOcclusionQuery(1)
			p.EndOcclusionQuery()
			p.EndOcclusionQuery()
		}, "RenderPassEncoderBeginOcclusionQuery", "occlusion query 0 already active"},
		{"end without begin", withSet, func(p *RenderPassEncoder) {
			p.EndOcclusionQuery()
		}, "RenderPassEncoderEndOcclusionQuery", "no active occlusion query"},
		{"extra end", withSet, func(p *RenderPassEncoder) {
			p.BeginOcclusionQuery(0)
			p.EndOcclusionQuery()
			p.EndOcclusionQuery()
		}, "RenderPassEncoderEndOcclusionQuery", "no active occlusion query"},
		{"index out of range", withSet, func(p *RenderPassEncoder) {
			p.BeginOcclusionQuery(2)
			p.EndOcclusionQuery()
		}, "RenderPassEncoderBeginOcclusionQuery", "query index 2 out of range for a query set of 2"},
		{"index reused in pass", withSet, func(p *RenderPassEncoder) {
			p.BeginOcclusionQuery(0)
			p.EndOcclusionQuery()
			p.BeginOcclusionQuery(1)
			p.EndOcclusionQuery()
			p.BeginOcclusionQuery(0)
			p.EndOcclusionQuery()
		}, "RenderPassEncoderBeginOcclusionQuery", "query index 0 already used in this render pass"},
		{"no occlusion query set", noSet, func(p *RenderPassEncoder) {
			p.BeginOcclusionQuery(0)
			p.EndOcclusionQuery()
		}, "RenderPassEncoderBeginOcclusionQuery", "render pass has no OcclusionQuerySet"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enc, err := device.CreateCommandEncoder()
			if err != nil {
				t.Fatal(err)
			}
			pass, err := enc.BeginRenderPass(c.desc)
			if err != nil {
				t.Fatal(err)
			}
			c.record(pass)
			pass.End()
			pass.Release()
			cb, err := enc.Finish()
			if err == nil {
				cb.Release()
				t.Fatal("Finish succeeded")
			}
			werr, ok := err.(*Error)
			if !ok || werr.Op != c.op || !strings.Contains(werr.Status, c.want) {
				t.Fatalf("error = %v, want op %s with %q", err, c.op, c.want)
			}
			t.Logf("%v", err)
		})
	}

	// The same index may be used again in a later pass, and the two
	// queries of one pass may run one after the other.
	t.Run("reuse across passes", func(t *testing.T) {
		enc, err := device.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			pass, err := enc.BeginRenderPass(withSet)
			if err != nil {
				t.Fatal(err)
			}
			pass.BeginOcclusionQuery(0)
			pass.EndOcclusionQuery()
			pass.BeginOcclusionQuery(1)
			pass.EndOcclusionQuery()
			pass.End()
			pass.Release()
		}
		got := readU64s(t, device, enc, queries, 2)
		t.Logf("results without draws: %v", got)
		if got[0] != 0 || got[1] != 0 {
			t.Errorf("empty queries counted samples: %v", got)
		}
	})

	t.Run("wrong query set type", func(t *testing.T) {
		adapter := newTestAdapter(t)
		if !adapter.HasFeature(native.FeatureNameTimestampQuery) {
			t.Skip("adapter has no TimestampQuery")
		}
		dev, err := adapter.RequestDeviceWith(&DeviceOptions{RequiredFeatures: []native.FeatureName{native.FeatureNameTimestampQuery}})
		if err != nil {
			t.Fatal(err)
		}
		defer dev.Release()
		ts, err := dev.CreateQuerySet(&QuerySetDescriptor{Type: native.QueryTypeTimestamp, Count: 2})
		if err != nil {
			t.Fatal(err)
		}
		defer ts.Release()
		_, v := newTarget(t, dev, 4, 4, native.TextureFormatRGBA8Unorm)
		enc, err := dev.CreateCommandEncoder()
		if err != nil {
			t.Fatal(err)
		}
		defer enc.Release()
		if pass, err := enc.BeginRenderPass(&RenderPassDescriptor{ColorAttachments: []RenderPassColorAttachment{{View: v}}, OcclusionQuerySet: ts}); err == nil {
			pass.End()
			pass.Release()
			t.Fatal("timestamp query set accepted as OcclusionQuerySet")
		} else {
			t.Logf("%v", err)
		}
	})

	if err := device.Err(); err != nil {
		t.Fatal(err)
	}
}
