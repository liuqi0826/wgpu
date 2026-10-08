//go:build windows && amd64

// Command triangle opens a Win32 window and draws a rotating colored triangle.
//
//	go run ./example/triangle            # until the window is closed
//	go run ./example/triangle -frames 120 -resize
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"

	"wgpu"
	"wgpu/native"
)

func init() {
	// The window and its message pump must stay on one OS thread.
	runtime.LockOSThread()
}

const shaderWGSL = `
struct Uniforms {
	angle: f32,
	aspect: f32,
}
@group(0) @binding(0) var<uniform> u: Uniforms;

struct VSOut {
	@builtin(position) pos: vec4f,
	@location(0) color: vec3f,
}

@vertex
fn vs_main(@location(0) pos: vec2f, @location(1) color: vec3f) -> VSOut {
	let c = cos(u.angle);
	let s = sin(u.angle);
	let p = vec2f(pos.x * c - pos.y * s, pos.x * s + pos.y * c);
	var out: VSOut;
	out.pos = vec4f(p.x / u.aspect, p.y, 0.0, 1.0);
	out.color = color;
	return out;
}

@fragment
fn fs_main(in: VSOut) -> @location(0) vec4f {
	return vec4f(in.color, 1.0);
}
`

func main() {
	frames := flag.Int("frames", 0, "render N frames and exit (0 runs until the window is closed)")
	resize := flag.Bool("resize", false, "resize the window halfway through -frames to exercise reconfiguration")
	noSize := flag.Bool("ignore-wmsize", false, "ignore WM_SIZE and rely on Outdated/Suboptimal to reconfigure")
	flag.Parse()
	if err := run(*frames, *resize, *noSize); err != nil {
		fmt.Fprintln(os.Stderr, "triangle:", err)
		os.Exit(1)
	}
}

func f32Bytes(values ...float32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(v))
	}
	return out
}

func pickFormat(formats []native.TextureFormat) native.TextureFormat {
	for _, f := range formats {
		if f == native.TextureFormatBGRA8UnormSrgb || f == native.TextureFormatRGBA8UnormSrgb {
			return f
		}
	}
	return formats[0]
}

func pickAlpha(modes []native.CompositeAlphaMode) native.CompositeAlphaMode {
	for _, m := range modes {
		if m == native.CompositeAlphaModeOpaque {
			return m
		}
	}
	if len(modes) > 0 {
		return modes[0]
	}
	return native.CompositeAlphaModeAuto
}

func run(frames int, resizeTest, ignoreWMSize bool) error {
	win, err := createWindow("wgpu triangle", 800, 600)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	defer win.destroy()

	instance, err := wgpu.CreateInstance(nil)
	if err != nil {
		return err
	}
	defer instance.Release()
	surface, err := instance.CreateSurfaceHWND(win.hwnd, win.hinstance)
	if err != nil {
		return err
	}
	defer surface.Release()
	adapter, err := instance.RequestAdapter(&native.RequestAdapterOptions{
		PowerPreference:   native.PowerPreferenceHighPerformance,
		CompatibleSurface: surface.Native(),
	})
	if err != nil {
		return err
	}
	defer adapter.Release()
	info, err := adapter.Info()
	if err != nil {
		return err
	}
	fmt.Printf("adapter: %s (%s, %s)\n", info.Device, info.Backend, info.Type)

	device, err := adapter.RequestDevice()
	if err != nil {
		return err
	}
	defer device.Release()
	queue := device.Queue()

	caps, err := surface.Capabilities(adapter)
	if err != nil {
		return err
	}
	if len(caps.Formats) == 0 {
		return fmt.Errorf("surface reports no formats")
	}
	config := wgpu.SurfaceConfig{
		Format:      pickFormat(caps.Formats),
		PresentMode: native.PresentModeFifo,
		AlphaMode:   pickAlpha(caps.AlphaModes),
	}
	configure := func() bool {
		w, h := win.clientSize()
		if w == 0 || h == 0 {
			return false
		}
		config.Width, config.Height = w, h
		surface.ConfigureWith(device, config)
		return true
	}
	if !configure() {
		return fmt.Errorf("window has no client area")
	}
	fmt.Printf("surface: %s, %s, alpha %s, %dx%d\n", config.Format, config.PresentMode, config.AlphaMode, config.Width, config.Height)

	device.PushErrorScope(native.ErrorFilterValidation)
	module, err := device.CreateShaderModuleWGSL("triangle", shaderWGSL)
	if err != nil {
		return err
	}
	defer module.Release()
	bgl, err := device.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Entries: []wgpu.BindGroupLayoutEntry{{
			Binding:    0,
			Visibility: native.ShaderStageVertex,
			Buffer:     wgpu.BufferBinding{Type: native.BufferBindingTypeUniform},
		}},
	})
	if err != nil {
		return err
	}
	defer bgl.Release()
	layout, err := device.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{BindGroupLayouts: []*wgpu.BindGroupLayout{bgl}})
	if err != nil {
		return err
	}
	defer layout.Release()
	pipeline, err := device.CreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:  "triangle",
		Layout: layout,
		Vertex: wgpu.VertexState{
			Module:     module,
			EntryPoint: "vs_main",
			Buffers: []wgpu.VertexBufferLayout{{
				ArrayStride: 5 * 4,
				Attributes: []wgpu.VertexAttribute{
					{Format: native.VertexFormatFloat32x2, Offset: 0, ShaderLocation: 0},
					{Format: native.VertexFormatFloat32x3, Offset: 8, ShaderLocation: 1},
				},
			}},
		},
		Fragment: &wgpu.FragmentState{
			Module:     module,
			EntryPoint: "fs_main",
			Targets:    []wgpu.ColorTargetState{{Format: config.Format, Blend: &wgpu.BlendReplace}},
		},
	})
	if err != nil {
		return err
	}
	defer pipeline.Release()

	vertices, err := device.CreateBuffer(native.BufferUsageVertex|native.BufferUsageCopyDst, 3*5*4, false)
	if err != nil {
		return err
	}
	defer vertices.Release()
	queue.WriteBuffer(vertices, 0, f32Bytes(
		0.0, 0.6, 1, 0, 0,
		-0.52, -0.3, 0, 1, 0,
		0.52, -0.3, 0, 0, 1,
	))
	indices, err := device.CreateBuffer(native.BufferUsageIndex|native.BufferUsageCopyDst, 8, false)
	if err != nil {
		return err
	}
	defer indices.Release()
	queue.WriteBuffer(indices, 0, []byte{0, 0, 1, 0, 2, 0, 0, 0}) // uint16 0,1,2 + padding
	uniform, err := device.CreateBuffer(native.BufferUsageUniform|native.BufferUsageCopyDst, 16, false)
	if err != nil {
		return err
	}
	defer uniform.Release()
	group, err := device.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Layout:  bgl,
		Entries: []wgpu.BindGroupEntry{{Binding: 0, Buffer: uniform}},
	})
	if err != nil {
		return err
	}
	defer group.Release()
	if err := device.PopErrorScope(); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	start := time.Now()
	rendered, reconfigured, skipped := 0, 0, 0
	resizedOnce := false
	resized = false
	for frames == 0 || rendered < frames {
		if !win.pump() {
			break // window closed
		}
		if resizeTest && !resizedOnce && rendered >= frames/2 {
			resizedOnce = true
			win.setClientSize(640, 480)
		}
		if resized && ignoreWMSize {
			resized = false
		}
		if resized {
			resized = false
			if !configure() {
				time.Sleep(10 * time.Millisecond) // minimized
				continue
			}
			reconfigured++
			fmt.Printf("resized: %dx%d\n", config.Width, config.Height)
		}

		tex, status := surface.GetCurrentTexture()
		suboptimal := false
		switch status {
		case native.SurfaceGetCurrentTextureStatusSuccessOptimal:
		case native.SurfaceGetCurrentTextureStatusSuccessSuboptimal:
			suboptimal = true // draw this frame, then reconfigure
		case native.SurfaceGetCurrentTextureStatusTimeout, native.SurfaceGetCurrentTextureStatusOccluded:
			if tex != nil {
				tex.Release()
			}
			skipped++
			time.Sleep(5 * time.Millisecond)
			continue
		case native.SurfaceGetCurrentTextureStatusOutdated, native.SurfaceGetCurrentTextureStatusLost:
			if tex != nil {
				tex.Release()
			}
			skipped++
			fmt.Printf("surface %s, reconfiguring\n", status)
			if configure() {
				reconfigured++
			}
			continue
		default:
			if tex != nil {
				tex.Release()
			}
			return fmt.Errorf("get current texture: %s", status)
		}

		if err := drawFrame(device, queue, surface, tex, pipeline, group, vertices, indices, uniform,
			float32(time.Since(start).Seconds()), float32(config.Width)/float32(config.Height)); err != nil {
			return err
		}
		if err := device.Err(); err != nil {
			return fmt.Errorf("frame %d: %w", rendered, err)
		}
		rendered++
		if suboptimal {
			fmt.Println("surface suboptimal, reconfiguring")
			if configure() {
				reconfigured++
			}
		}
	}
	device.Poll(true)
	elapsed := time.Since(start)
	fmt.Printf("rendered %d frames in %.2fs (%.1f fps), reconfigured %d, skipped %d\n",
		rendered, elapsed.Seconds(), float64(rendered)/elapsed.Seconds(), reconfigured, skipped)
	if err := device.Err(); err != nil {
		return err
	}
	fmt.Println("no validation errors")
	return nil
}

func drawFrame(device *wgpu.Device, queue *wgpu.Queue, surface *wgpu.Surface, tex *wgpu.Texture,
	pipeline *wgpu.RenderPipeline, group *wgpu.BindGroup, vertices, indices, uniform *wgpu.Buffer,
	angle, aspect float32) error {
	defer tex.Release()
	view, err := tex.CreateView(nil)
	if err != nil {
		return err
	}
	defer view.Release()
	queue.WriteBuffer(uniform, 0, f32Bytes(angle, aspect, 0, 0))

	enc, err := device.CreateCommandEncoder()
	if err != nil {
		return err
	}
	pass, err := enc.BeginRenderPass(&wgpu.RenderPassDescriptor{
		ColorAttachments: []wgpu.RenderPassColorAttachment{{
			View:       view,
			LoadOp:     native.LoadOpClear,
			StoreOp:    native.StoreOpStore,
			ClearValue: native.Color{R: 0.05, G: 0.06, B: 0.08, A: 1},
		}},
	})
	if err != nil {
		enc.Release()
		return err
	}
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, group)
	pass.SetVertexBuffer(0, vertices, 0, 0)
	pass.SetIndexBuffer(indices, native.IndexFormatUint16, 0, 0)
	pass.DrawIndexed(3, 1, 0, 0, 0)
	pass.End()
	pass.Release()
	commands, err := enc.Finish()
	if err != nil {
		return err
	}
	queue.Submit(commands)
	commands.Release()
	return surface.Present()
}
