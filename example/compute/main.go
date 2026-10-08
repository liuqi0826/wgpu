//go:build windows && amd64

// Command compute doubles a large uint32 array on the GPU and checks it.
//
//	go run ./example/compute -n 1048576
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/liuqi0826/wgpu"
	"github.com/liuqi0826/wgpu/native"
)

const shaderWGSL = `
@group(0) @binding(0) var<storage, read_write> data: array<u32>;

@compute @workgroup_size(64)
fn main(@builtin(global_invocation_id) id: vec3u) {
	let i = id.x + id.y * 65535u * 64u;
	if (i < arrayLength(&data)) {
		data[i] = data[i] * 2u;
	}
}
`

var backendFlag = flag.String("backend", os.Getenv(wgpu.BackendEnv), "backends: vulkan, dx12, gl or all (default $WGPU_BACKEND)")

func main() {
	n := flag.Int("n", 1<<20, "number of uint32 elements")
	flag.Parse()
	if err := run(*n); err != nil {
		fmt.Fprintln(os.Stderr, "compute:", err)
		os.Exit(1)
	}
}

func run(n int) error {
	if n <= 0 {
		return fmt.Errorf("-n must be positive")
	}
	backends, err := wgpu.ParseBackends(*backendFlag)
	if err != nil {
		return err
	}
	instance, err := wgpu.CreateInstance(&wgpu.InstanceOptions{Backends: backends})
	if err != nil {
		return err
	}
	defer instance.Release()
	adapter, err := instance.RequestAdapter(&native.RequestAdapterOptions{PowerPreference: native.PowerPreferenceHighPerformance})
	if err != nil {
		return err
	}
	defer adapter.Release()
	info, err := adapter.Info()
	if err != nil {
		return err
	}
	fmt.Printf("adapter: %s (%s, %s)\n", info.Device, info.Backend, info.Type)
	// GPU timing needs the TimestampQuery feature; without it only wall time is printed.
	timestamps := adapter.HasFeature(native.FeatureNameTimestampQuery)
	opts := &wgpu.DeviceOptions{}
	if timestamps {
		opts.RequiredFeatures = []native.FeatureName{native.FeatureNameTimestampQuery}
	}
	device, err := adapter.RequestDeviceWith(opts)
	if err != nil {
		return err
	}
	defer device.Release()
	queue := device.Queue()

	device.PushErrorScope(native.ErrorFilterValidation)
	module, err := device.CreateShaderModuleWGSL("double", shaderWGSL)
	if err != nil {
		return err
	}
	defer module.Release()
	pipeline, err := device.CreateComputePipeline(&wgpu.ComputePipelineDescriptor{
		Label:   "double",
		Compute: wgpu.ComputeState{Module: module, EntryPoint: "main"},
	})
	if err != nil {
		return err
	}
	defer pipeline.Release()
	bgl, err := pipeline.BindGroupLayout(0)
	if err != nil {
		return err
	}
	defer bgl.Release()

	size := uint64(n) * 4
	storage, err := device.CreateBuffer(native.BufferUsageStorage|native.BufferUsageCopyDst|native.BufferUsageCopySrc, size, false)
	if err != nil {
		return err
	}
	defer storage.Release()
	readback, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, size, false)
	if err != nil {
		return err
	}
	defer readback.Release()
	group, err := device.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Layout:  bgl,
		Entries: []wgpu.BindGroupEntry{{Binding: 0, Buffer: storage}},
	})
	if err != nil {
		return err
	}
	defer group.Release()
	var (
		queries         *wgpu.QuerySet
		resolve, stamps *wgpu.Buffer
		timestampWrites *wgpu.PassTimestampWrites
	)
	if timestamps {
		if queries, err = device.CreateQuerySet(&wgpu.QuerySetDescriptor{Label: "pass time", Type: native.QueryTypeTimestamp, Count: 2}); err != nil {
			return err
		}
		defer queries.Release()
		if resolve, err = device.CreateBuffer(native.BufferUsageQueryResolve|native.BufferUsageCopySrc, 16, false); err != nil {
			return err
		}
		defer resolve.Release()
		if stamps, err = device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, 16, false); err != nil {
			return err
		}
		defer stamps.Release()
		timestampWrites = &wgpu.PassTimestampWrites{QuerySet: queries, BeginningOfPassWriteIndex: 0, EndOfPassWriteIndex: 1}
	}
	if err := device.PopErrorScope(); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	input := make([]byte, size)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(input[4*i:], uint32(i))
	}
	queue.WriteBuffer(storage, 0, input)

	groups := (n + 63) / 64
	gx, gy := groups, 1
	if gx > 65535 {
		gx, gy = 65535, (groups+65534)/65535
	}

	start := time.Now()
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		return err
	}
	pass, err := enc.BeginComputePass(&wgpu.ComputePassDescriptor{Label: "double", TimestampWrites: timestampWrites})
	if err != nil {
		enc.Release()
		return err
	}
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, group)
	pass.DispatchWorkgroups(uint32(gx), uint32(gy), 1)
	pass.End()
	pass.Release()
	enc.CopyBufferToBuffer(storage, 0, readback, 0, size)
	if timestamps {
		enc.ResolveQuerySet(queries, 0, 2, resolve, 0)
		enc.CopyBufferToBuffer(resolve, 0, stamps, 0, 16)
	}
	commands, err := enc.Finish()
	if err != nil {
		return err
	}
	queue.Submit(commands)
	commands.Release()
	if err := readback.Map(native.MapModeRead, 0, size); err != nil {
		return err
	}
	elapsed := time.Since(start)
	out, err := readback.ReadMapped(0, size)
	readback.Unmap()
	if err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if got, want := binary.LittleEndian.Uint32(out[4*i:]), uint32(i)*2; got != want {
			return fmt.Errorf("data[%d] = %d, want %d", i, got, want)
		}
	}
	fmt.Printf("doubled %d elements (%d workgroups as %dx%d) in %v including readback\n", n, groups, gx, gy, elapsed.Round(time.Microsecond))
	if timestamps {
		if err := stamps.Map(native.MapModeRead, 0, 16); err != nil {
			return err
		}
		raw, err := stamps.ReadMapped(0, 16)
		stamps.Unmap()
		if err != nil {
			return err
		}
		begin, end := binary.LittleEndian.Uint64(raw[0:]), binary.LittleEndian.Uint64(raw[8:])
		gpu := time.Duration(float64(end-begin) * float64(queue.TimestampPeriod()))
		fmt.Printf("gpu time: %v (compute pass, timestamp query)\n", gpu)
	} else {
		fmt.Println("gpu time: unavailable (adapter has no TimestampQuery)")
	}
	fmt.Printf("first: %d %d %d %d, last: %d\n",
		binary.LittleEndian.Uint32(out[0:]), binary.LittleEndian.Uint32(out[4:]),
		binary.LittleEndian.Uint32(out[8:]), binary.LittleEndian.Uint32(out[12:]),
		binary.LittleEndian.Uint32(out[size-4:]))
	if err := device.Err(); err != nil {
		return err
	}
	fmt.Println("verified, no validation errors")
	return nil
}
