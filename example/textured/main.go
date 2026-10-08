//go:build windows && amd64

// Command textured draws a spinning checkerboard quad in front of a textured
// backdrop. The texture is generated in code and uploaded with WriteTexture;
// a depth buffer resolves where the two quads intersect.
//
//	go run ./example/textured -frames 120 -resize
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
	"wgpu/example/internal/win32"
	"wgpu/native"
)

func init() {
	runtime.LockOSThread()
}

const depthFormat = native.TextureFormatDepth24Plus

const shaderWGSL = `
struct Uniforms {
	angle: f32,
	aspect: f32,
}
@group(0) @binding(0) var<uniform> u: Uniforms;
@group(0) @binding(1) var tex: texture_2d<f32>;
@group(0) @binding(2) var samp: sampler;

struct VSOut {
	@builtin(position) pos: vec4f,
	@location(0) uv: vec2f,
	@location(1) shade: f32,
}

@vertex
fn vs_main(@location(0) pos: vec2f, @location(1) uv: vec2f, @builtin(instance_index) inst: u32) -> VSOut {
	var p: vec3f;
	var out: VSOut;
	if (inst == 0u) {
		// Spinning quad around the Y axis, z in [-0.7, 0.7].
		let c = cos(u.angle);
		let s = sin(u.angle);
		p = vec3f(pos.x * c, pos.y, pos.x * s);
		out.uv = uv * 2.0;
		out.shade = 1.0;
	} else {
		// Backdrop that the spinning quad cuts through.
		p = vec3f(pos.x * 2.2, pos.y * 1.6, 0.3);
		out.uv = uv * 6.0;
		out.shade = 0.45;
	}
	// Camera at z = -2.5 looking down +z; depth maps [0.1, 10] to [0, 1].
	let z = p.z + 2.5;
	let n = 0.1;
	let f = 10.0;
	let focal = 1.6;
	out.pos = vec4f(p.x * focal / u.aspect, p.y * focal, z * f / (f - n) - n * f / (f - n), z);
	return out;
}

@fragment
fn fs_main(in: VSOut) -> @location(0) vec4f {
	let c = textureSample(tex, samp, in.uv).rgb;
	return vec4f(c * in.shade, 1.0);
}
`

func main() {
	frames := flag.Int("frames", 0, "render N frames and exit (0 runs until the window is closed)")
	resize := flag.Bool("resize", false, "resize the window halfway through -frames")
	flag.Parse()
	if err := run(*frames, *resize); err != nil {
		fmt.Fprintln(os.Stderr, "textured:", err)
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

// checkerboard returns RGBA8 texels with cells x cells squares.
func checkerboard(size, cells int) []byte {
	a := [4]byte{240, 160, 40, 255}
	b := [4]byte{30, 50, 110, 255}
	out := make([]byte, size*size*4)
	cell := size / cells
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := a
			if (x/cell+y/cell)%2 == 1 {
				c = b
			}
			copy(out[(y*size+x)*4:], c[:])
		}
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

type depthTarget struct {
	tex  *wgpu.Texture
	view *wgpu.TextureView
}

func (d *depthTarget) release() {
	if d.view != nil {
		d.view.Release()
	}
	if d.tex != nil {
		d.tex.Destroy()
		d.tex.Release()
	}
	*d = depthTarget{}
}

func (d *depthTarget) recreate(device *wgpu.Device, width, height uint32) error {
	d.release()
	tex, err := device.CreateTexture(&wgpu.TextureDescriptor{
		Label:  "depth",
		Usage:  native.TextureUsageRenderAttachment,
		Width:  width,
		Height: height,
		Format: depthFormat,
	})
	if err != nil {
		return err
	}
	view, err := tex.CreateView(nil)
	if err != nil {
		tex.Release()
		return err
	}
	d.tex, d.view = tex, view
	return nil
}

func run(frames int, resizeTest bool) error {
	win, err := win32.CreateWindow("wgpu textured", 800, 600)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	defer win.Destroy()

	instance, err := wgpu.CreateInstance(nil)
	if err != nil {
		return err
	}
	defer instance.Release()
	surface, err := instance.CreateSurfaceHWND(win.HWND, win.HInstance)
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
	config := wgpu.SurfaceConfig{Format: pickFormat(caps.Formats), PresentMode: native.PresentModeFifo}
	var depth depthTarget
	defer depth.release()
	configure := func() (bool, error) {
		w, h := win.ClientSize()
		if w == 0 || h == 0 {
			return false, nil
		}
		config.Width, config.Height = w, h
		surface.ConfigureWith(device, config)
		return true, depth.recreate(device, w, h)
	}
	if ok, err := configure(); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("window has no client area")
	}
	fmt.Printf("surface: %s, %s, %dx%d, depth %s\n", config.Format, config.PresentMode, config.Width, config.Height, depthFormat)

	device.PushErrorScope(native.ErrorFilterValidation)
	const texSize = 256
	texture, err := device.CreateTexture(&wgpu.TextureDescriptor{
		Label:  "checkerboard",
		Usage:  native.TextureUsageTextureBinding | native.TextureUsageCopyDst,
		Width:  texSize,
		Height: texSize,
		Format: native.TextureFormatRGBA8UnormSrgb,
	})
	if err != nil {
		return err
	}
	defer texture.Release()
	queue.WriteTexture(wgpu.TexelCopyTexture{Texture: texture}, checkerboard(texSize, 8),
		native.TexelCopyBufferLayout{BytesPerRow: texSize * 4, RowsPerImage: texSize},
		native.Extent3D{Width: texSize, Height: texSize, DepthOrArrayLayers: 1})
	textureView, err := texture.CreateView(nil)
	if err != nil {
		return err
	}
	defer textureView.Release()
	sampler, err := device.CreateSampler(&wgpu.SamplerDescriptor{
		AddressModeU: native.AddressModeRepeat,
		AddressModeV: native.AddressModeRepeat,
		MagFilter:    native.FilterModeNearest,
		MinFilter:    native.FilterModeLinear,
	})
	if err != nil {
		return err
	}
	defer sampler.Release()

	module, err := device.CreateShaderModuleWGSL("textured", shaderWGSL)
	if err != nil {
		return err
	}
	defer module.Release()
	pipeline, err := device.CreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label: "textured",
		Vertex: wgpu.VertexState{
			Module: module,
			Buffers: []wgpu.VertexBufferLayout{{
				ArrayStride: 4 * 4,
				Attributes: []wgpu.VertexAttribute{
					{Format: native.VertexFormatFloat32x2, Offset: 0, ShaderLocation: 0},
					{Format: native.VertexFormatFloat32x2, Offset: 8, ShaderLocation: 1},
				},
			}},
		},
		DepthStencil: &wgpu.DepthStencilState{
			Format:            depthFormat,
			DepthWriteEnabled: true,
			DepthCompare:      native.CompareFunctionLess,
		},
		Fragment: &wgpu.FragmentState{
			Module:  module,
			Targets: []wgpu.ColorTargetState{{Format: config.Format}},
		},
	})
	if err != nil {
		return err
	}
	defer pipeline.Release()
	bgl, err := pipeline.BindGroupLayout(0) // automatic layout
	if err != nil {
		return err
	}
	defer bgl.Release()

	vertices, err := device.CreateBuffer(native.BufferUsageVertex|native.BufferUsageCopyDst, 4*4*4, false)
	if err != nil {
		return err
	}
	defer vertices.Release()
	queue.WriteBuffer(vertices, 0, f32Bytes(
		-0.7, -0.7, 0, 1,
		0.7, -0.7, 1, 1,
		0.7, 0.7, 1, 0,
		-0.7, 0.7, 0, 0,
	))
	indices, err := device.CreateBuffer(native.BufferUsageIndex|native.BufferUsageCopyDst, 12, false)
	if err != nil {
		return err
	}
	defer indices.Release()
	queue.WriteBuffer(indices, 0, []byte{0, 0, 1, 0, 2, 0, 0, 0, 2, 0, 3, 0})
	uniform, err := device.CreateBuffer(native.BufferUsageUniform|native.BufferUsageCopyDst, 16, false)
	if err != nil {
		return err
	}
	defer uniform.Release()
	group, err := device.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Layout: bgl,
		Entries: []wgpu.BindGroupEntry{
			{Binding: 0, Buffer: uniform},
			{Binding: 1, TextureView: textureView},
			{Binding: 2, Sampler: sampler},
		},
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
	win.TakeResized()
	for frames == 0 || rendered < frames {
		if !win.Pump() {
			break
		}
		if resizeTest && !resizedOnce && rendered >= frames/2 {
			resizedOnce = true
			win.SetClientSize(640, 480)
		}
		if win.TakeResized() {
			ok, err := configure()
			if err != nil {
				return err
			}
			if !ok {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			reconfigured++
			fmt.Printf("resized: %dx%d, depth texture %dx%d\n", config.Width, config.Height, depth.tex.Width(), depth.tex.Height())
		}

		tex, status := surface.GetCurrentTexture()
		switch status {
		case native.SurfaceGetCurrentTextureStatusSuccessOptimal, native.SurfaceGetCurrentTextureStatusSuccessSuboptimal:
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
			if ok, err := configure(); err != nil {
				return err
			} else if ok {
				reconfigured++
			}
			continue
		default:
			if tex != nil {
				tex.Release()
			}
			return fmt.Errorf("get current texture: %s", status)
		}

		queue.WriteBuffer(uniform, 0, f32Bytes(float32(time.Since(start).Seconds()), float32(config.Width)/float32(config.Height), 0, 0))
		if err := drawFrame(device, surface, tex, depth.view, pipeline, group, vertices, indices); err != nil {
			return err
		}
		if err := device.Err(); err != nil {
			return fmt.Errorf("frame %d: %w", rendered, err)
		}
		rendered++
		if status == native.SurfaceGetCurrentTextureStatusSuccessSuboptimal {
			if ok, err := configure(); err != nil {
				return err
			} else if ok {
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

func drawFrame(device *wgpu.Device, surface *wgpu.Surface, tex *wgpu.Texture, depthView *wgpu.TextureView,
	pipeline *wgpu.RenderPipeline, group *wgpu.BindGroup, vertices, indices *wgpu.Buffer) error {
	defer tex.Release()
	view, err := tex.CreateView(nil)
	if err != nil {
		return err
	}
	defer view.Release()
	enc, err := device.CreateCommandEncoder()
	if err != nil {
		return err
	}
	pass, err := enc.BeginRenderPass(&wgpu.RenderPassDescriptor{
		ColorAttachments: []wgpu.RenderPassColorAttachment{{
			View:       view,
			ClearValue: native.Color{R: 0.02, G: 0.02, B: 0.03, A: 1},
		}},
		DepthStencilAttachment: &wgpu.RenderPassDepthStencilAttachment{
			View:            depthView,
			DepthClearValue: 1,
		},
	})
	if err != nil {
		enc.Release()
		return err
	}
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, group)
	pass.SetVertexBuffer(0, vertices, 0, 0)
	pass.SetIndexBuffer(indices, native.IndexFormatUint16, 0, 0)
	pass.DrawIndexed(6, 2, 0, 0, 0)
	pass.End()
	pass.Release()
	commands, err := enc.Finish()
	if err != nil {
		return err
	}
	device.Queue().Submit(commands)
	commands.Release()
	return surface.Present()
}
