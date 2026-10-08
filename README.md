# wgpu

[wgpu-native](https://github.com/gfx-rs/wgpu-native) 的 Go 绑定，**不使用 CGO**。

动态库用 `syscall.LoadLibrary` 加载，函数调用走 [purego](https://github.com/ebitengine/purego)，
不需要 C 编译器。`native` 包是按 `webgpu.h` / `wgpu.h`（wgpu 29）生成的完整 C API，
根包在它之上提供 Go 风格的封装。

```go
import "github.com/liuqi0826/wgpu"
```

## 要求

- Windows amd64（目前唯一支持的平台，所有文件都带 `//go:build windows && amd64`）
- Go 1.26.8 或更高（见 `go.mod`）
- wgpu-native 29 的 `wgpu_native.dll`，开发和测试使用 v29 系列

唯一的依赖是 `github.com/ebitengine/purego`。

## 安装

1. 获取模块：

   ```sh
   go get github.com/liuqi0826/wgpu
   ```

2. 从 [wgpu-native releases](https://github.com/gfx-rs/wgpu-native/releases) 下载 v29 的
   `wgpu-windows-x86_64-msvc-release.zip`，取出其中的 `wgpu_native.dll`。

3. 让程序能找到 DLL，下面两种方式任选其一：

   - 设置环境变量 `WGPU_NATIVE_PATH` 为 **DLL 文件的完整路径**，例如
     `set WGPU_NATIVE_PATH=C:\libs\wgpu\wgpu_native.dll`。设置了这个变量就只用它，
     文件不存在会直接报错。
   - 把 DLL 放在下列位置之一，按顺序查找：
     1. `<工作目录>\lib\wgpu_native.dll`
     2. `<工作目录>\wgpu_native.dll`
     3. `<工作目录>\..\lib\wgpu_native.dll`
     4. `<工作目录>\..\..\lib\wgpu_native.dll`
     5. `<exe 所在目录>\wgpu_native.dll`
     6. `<exe 所在目录>\lib\wgpu_native.dll`

   也可以在创建实例之前调用 `wgpu.Load(path)` 显式指定路径。`go run` 生成的 exe 在临时目录里，
   所以用 `go run` 时靠的是工作目录或 `WGPU_NATIVE_PATH`。

本仓库自己的测试和示例使用 `lib\wgpu_native.dll`（DLL 不提交到仓库，见 `.gitignore`）。

## 最小用法

把一段数据拷到另一个缓冲区再读回来：

```go
package main

import (
	"fmt"
	"log"

	"github.com/liuqi0826/wgpu"
	"github.com/liuqi0826/wgpu/native"
)

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	// 加载 DLL 并创建实例；nil 表示启用 DLL 支持的所有后端。
	instance, err := wgpu.CreateInstance(nil)
	check(err)
	defer instance.Release()

	adapter, err := instance.RequestAdapter(&native.RequestAdapterOptions{
		PowerPreference: native.PowerPreferenceHighPerformance,
	})
	check(err)
	defer adapter.Release()
	info, err := adapter.Info()
	check(err)
	fmt.Println(info.Device, info.Backend)

	device, err := adapter.RequestDevice()
	check(err)
	defer device.Release()
	queue := device.Queue()

	src, err := device.CreateBuffer(native.BufferUsageCopySrc|native.BufferUsageCopyDst, 16, false)
	check(err)
	defer src.Release()
	dst, err := device.CreateBuffer(native.BufferUsageMapRead|native.BufferUsageCopyDst, 16, false)
	check(err)
	defer dst.Release()
	queue.WriteBuffer(src, 0, []byte("hello, wgpu-go!!"))

	enc, err := device.CreateCommandEncoder()
	check(err)
	enc.CopyBufferToBuffer(src, 0, dst, 0, 16)
	commands, err := enc.Finish() // 命令里的验证错误在这里返回
	check(err)
	queue.Submit(commands)
	commands.Release()

	check(dst.Map(native.MapModeRead, 0, 0)) // 阻塞，内部轮询设备
	data, err := dst.ReadMapped(0, 16)
	check(err)
	dst.Unmap()
	fmt.Printf("%s\n", data)
}
```

所有句柄都要 `Release`。更完整的用法见 `example/` 和各个 `_test.go`。

## 已封装的功能

- 实例、适配器（`Info`、`Limits`、`Features`）、设备（`RequestDeviceWith` 支持
  `RequiredFeatures`、`RequiredLimits`、`OnLost`、`OnUncapturedError`；`Device.AdapterInfo`）
- 缓冲区：`WriteBuffer`、阻塞的 `Map`、异步的 `MapAsync`（返回 channel）和 `MapAsyncFunc`（回调）、`MapState`
- 纹理、纹理视图、采样器、`Queue.WriteTexture`、缓冲区和纹理之间的拷贝
- 着色器（WGSL，`ShaderModule.CompilationInfo`）、绑定组和布局、管线布局、渲染管线、计算管线（含 override 常量和自动布局）
- RenderPass、ComputePass（含间接绘制和间接派发）、RenderBundle
- 多重间接绘制：`MultiDrawIndirect`、`MultiDrawIndexedIndirect`、`MultiDrawIndirectCount`、`MultiDrawIndexedIndirectCount`
- Immediate data：RenderPass、ComputePass、RenderBundle 的 `SetImmediates`
- QuerySet：遮挡查询、pass 的时间戳写入、pipeline statistics 查询、`ResolveQuerySet`、`Queue.TimestampPeriod`
- 调试标记：四种编码器的 `PushDebugGroup` / `PopDebugGroup` / `InsertDebugMarker`
- `Queue.OnSubmittedWorkDone`（返回 channel）和阻塞的 `WaitSubmittedWorkDone`
- Win32 窗口表面：`Instance.CreateSurfaceHWND`、`Surface.ConfigureWith`、`GetCurrentTexture`、`Present`
- 错误域 `PushErrorScope` / `PopErrorScope`

## 后端选择

`InstanceOptions.Backends` 决定实例启用哪些后端，零值表示 DLL 支持的全部后端：

```go
instance, err := wgpu.CreateInstance(&wgpu.InstanceOptions{Backends: native.InstanceBackendDX12})
```

`wgpu.ParseBackends("vulkan")` 把名字转成对应的位。可用的名字有 `vulkan`（`vk`）、`dx12`（`d3d12`）、
`gl`（`opengl`）、`metal`、`primary`、`secondary`、`all`，多个名字用逗号分隔。
`wgpu.BackendsFromEnv(def)` 读取环境变量 `WGPU_BACKEND`，没设置时返回 `def`。

- 测试读取 `WGPU_BACKEND`，默认用 primary 后端：

  ```sh
  set WGPU_BACKEND=dx12
  go test -count=1 ./...
  ```

- 示例都有 `-backend` 参数，默认取 `WGPU_BACKEND`。

在 RTX 4060 Ti 上，Vulkan、D3D12、GL 三个后端都跑通了全部测试和示例。

## 示例

在仓库根目录运行（DLL 在 `lib\` 下）：

| 示例 | 作用 | 参数 |
| --- | --- | --- |
| `go run ./example/info` | 列出所有适配器 | `-backend` |
| `go run ./example/compute` | 在 GPU 上把数组每个元素乘 2 并校验，支持时打印时间戳查询测出的 GPU 耗时 | `-n` 元素个数（默认 1048576），`-backend` |
| `go run ./example/triangle` | Win32 窗口里画一个三角形，处理窗口缩放和 Outdated/Lost/Suboptimal，每帧带调试组便于用 RenderDoc 查看 | `-frames N` 画 N 帧后退出，`-resize` 中途改变窗口大小，`-ignore-wmsize` 只依靠 Outdated/Suboptimal 重新配置，`-backend` |
| `go run ./example/textured` | 窗口里画贴了代码生成的棋盘格纹理的旋转四边形，使用采样器、绑定组和深度纹理，窗口大小变化时重建深度纹理 | `-frames N`，`-resize`，`-backend` |

例如：`go run ./example/textured -backend dx12 -frames 300 -resize`。

## 调试标记

`CommandEncoder`、`RenderPassEncoder`、`ComputePassEncoder`、`RenderBundleEncoder` 都有
`PushDebugGroup`、`PopDebugGroup`、`InsertDebugMarker`，在 RenderDoc 等 GPU 调试工具里可以看到分组。
`example/triangle` 每帧用 `frame` / `triangle` 两层调试组包住绘制。

```go
enc.PushDebugGroup("frame")
pass, _ := enc.BeginRenderPass(desc)
pass.PushDebugGroup("triangle")
pass.InsertDebugMarker("draw")
pass.DrawIndexed(3, 1, 0, 0, 0)
pass.PopDebugGroup()
pass.End()
enc.PopDebugGroup()
commands, err := enc.Finish() // 不配对时在这里返回错误
```

- 嵌套深度在 Go 侧跟踪。每个 pass 有自己的栈，不能跨 pass 或跨编码器配对。
- 栈为空时 `PopDebugGroup` 不会下发给 wgpu-native，立即记一个错误，由 `Finish` 返回。
- pass `End` 时或编码器 `Finish` 时还有未关闭的组，`Finish` 返回 `N debug group(s) still open`。
- 实测 wgpu-native 29 对不配对的 push/pop 只记录验证错误，不会终止进程；但带错误的命令缓冲区一旦提交就会终止进程，
  所以这些错误都由 `Finish` 返回，不交出命令缓冲区。
- RenderBundle 的调试命令同样在 Go 侧记录、重放并检查配对，但 wgpu-native 29 里
  `wgpuRenderBundleEncoderPushDebugGroup` 等是空实现，bundle 内的调试组不会出现在调试工具里。
- **没有 `SetLabel`**：wgpu-native 29 的所有 `wgpuXxxSetLabel` 都是 `unimplemented!()`，调用会直接终止进程
  （已在 Buffer、Texture、CommandEncoder、ComputePipeline 上实测）。标签请通过各描述符的 `Label` 字段设置。

## Pipeline statistics 查询

wgpu-native 扩展特性 `PipelineStatisticsQuery`。请求设备前**先检查适配器**：

```go
if adapter.HasFeature(wgpu.FeaturePipelineStatisticsQuery) {
	device, err := adapter.RequestDeviceWith(&wgpu.DeviceOptions{
		RequiredFeatures: []native.FeatureName{wgpu.FeaturePipelineStatisticsQuery},
	})
	q, err := device.CreateQuerySet(&wgpu.QuerySetDescriptor{
		Type:  wgpu.QueryTypePipelineStatistics,
		Count: 1,
		PipelineStatistics: []native.PipelineStatisticName{
			native.PipelineStatisticNameVertexShaderInvocations,
			native.PipelineStatisticNameFragmentShaderInvocations,
		},
	})
	// pass.BeginPipelineStatisticsQuery(q, 0) ... pass.EndPipelineStatisticsQuery()
	// enc.ResolveQuerySet(q, 0, 1, resolve, 0)，每个查询占 q.ResultStride() 字节
	stats, err := q.ParsePipelineStatistics(data) // []wgpu.PipelineStatistics
	fmt.Println(stats[0].VertexShaderInvocations, stats[0].FragmentShaderInvocations)
}
```

- 统计项可以是 `VertexShaderInvocations`、`ClipperInvocations`、`ClipperPrimitivesOut`、
  `FragmentShaderInvocations`、`ComputeShaderInvocations`。顺序和重复不影响结果：wgpu-core 按位集合保存，
  结果按统计项的枚举值升序排列，`QuerySet.PipelineStatistics()` 返回实际顺序。
- `PipelineStatistics` 只填写请求过的字段，`Has(name)` 判断某项是否采集，`Value(name)` 按名字取值。
  没有 QuerySet 时可以用 `wgpu.DecodePipelineStatistics(stats, data)` 解析。
- Go 侧检查：未知查询类型、未知统计项、空统计项列表（这三种 wgpu-native 会 panic）、设备未启用特性、
  Begin 传入空或非统计类型的 query set、查询嵌套、End 没有对应的 Begin、pass 结束时查询未关闭
  （wgpu-core 29 不检查 compute pass 里未关闭的统计查询）。这些都由 `CommandEncoder.Finish` 或 `CreateQuerySet` 返回错误。
- `RequestDeviceWith` 会先检查 `RequiredFeatures` 是否都被适配器支持，不支持时返回带特性名的错误
  （wgpu-native 本身只返回笼统的 `Validation Error`）。
- 本机（RTX 4060 Ti）支持情况：**Vulkan 支持；D3D12 和 GL 不支持**（适配器不报告该特性，相关测试 Skip）。
- 实测数值（Vulkan，64x64 目标上画一个全屏三角形）：顶点 3、裁剪 1、裁剪输出图元 1、片元 4096；
  同一个三角形画 4 个实例时裁剪 4、片元 16384，但顶点仍是 3（着色器没用 instance_index，驱动复用了顶点结果），
  所以顶点调用次数只能当作下限参考。compute 调用次数严格等于工作组数 × 每组线程数。

## Go 侧实现的查询：MapState、AdapterInfo、CompilationInfo

wgpu-native 29 的 `wgpuBufferGetMapState`、`wgpuDeviceGetAdapterInfo`、`wgpuShaderModuleGetCompilationInfo`
都在 `unimplemented.rs` 里，调用即终止进程（实测退出码 0xC0000409）。本包不调用它们，改在 Go 侧实现：

- `Buffer.MapState()`：来自 Go 侧的映射跟踪。`MapAsync` 发出后是 `Pending`，成功后是 `Mapped`；
  映射失败、`Unmap`、`Release`、设备释放或丢失都会回到 `Unmapped`。
- `Device.AdapterInfo()`：`RequestDeviceWith` 请求设备时读取一次 `Adapter.Info` 并保存，
  adapter 或 device 释放之后仍然可以读取。
- `ShaderModule.CompilationInfo()`：第一次调用时在内部错误域里用保存的 WGSL 源码重新创建一个临时模块，
  把验证错误转成一条 `CompilationMessageTypeError` 消息，结果缓存。`Message` 是 naga 的完整诊断，
  `LineNum` / `LinePos` 取自诊断里的第一个源码位置（解析错误和多数验证错误都有，没有位置时为 0）。
  合法的着色器返回空切片。限制：wgpu-core 不把 naga 的警告传出来，所以只有错误；不提供 UTF-16 的
  offset/length；模块或设备已释放、设备丢失时返回错误。`CreateShaderModuleWGSL` 本身的行为不变，
  无效着色器的错误仍然进入当前错误域或 `OnUncapturedError`。

## Immediate data（SetImmediates）

wgpu-native 扩展特性 `Immediates`（`wgpu.FeatureImmediates`）。默认的 `MaxImmediateSize` 限制是 0，
所以请求设备时要同时提高这个限制：

```go
if adapter.HasFeature(wgpu.FeatureImmediates) {
	best, _ := adapter.Limits()
	limits := wgpu.UndefinedLimits()
	limits.MaxImmediateSize = best.MaxImmediateSize
	device, err := adapter.RequestDeviceWith(&wgpu.DeviceOptions{
		RequiredFeatures: []native.FeatureName{wgpu.FeatureImmediates},
		RequiredLimits:   &limits,
	})
	layout, err := device.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{ImmediateSize: 32})
	// WGSL：var<immediate> imm: S;（不需要 enable 指令）
	pass.SetPipeline(pipeline) // 设置 pipeline 会把 immediate data 清零，所以 SetImmediates 放在它后面
	pass.SetImmediates(0, data)
}
```

- `RenderPassEncoder`、`ComputePassEncoder`、`RenderBundleEncoder` 都有 `SetImmediates(offset uint32, data []byte)`，
  调用 wgpu-native 的 `wgpu*EncoderSetImmediates`。空数据什么也不做。
- Go 侧拦截，错误由 `CommandEncoder.Finish` 或 `RenderBundleEncoder.Finish` 返回：设备没启用 `FeatureImmediates`、
  pass 已释放、offset 或数据长度不是 4 的倍数、数据长度达到 4 GiB、offset + 长度超过 4 GiB。
  - 4 GiB：头文件里 size 是 `size_t`，wgpu-native 的 Rust 实现声明为 `u32`，只取低 32 位。
    实测 2^32+16 字节会被静默当成 16 字节，所以 Go 侧拒绝。
  - RenderBundle 里 wgpu-core 用 `assert_eq!` 检查对齐，不对齐会终止进程，所以必须在 Go 侧拦下。
- 范围超出 pipeline layout 的 `ImmediateSize`、没有设置 pipeline：pass 里由 wgpu-core 报验证错误（`Finish` 返回）；
  bundle 里由 `Finish` 的验证 pass 报出，不会走到会终止进程的 bundle finish。
- 本机（RTX 4060 Ti）三个后端都支持，适配器的 `MaxImmediateSize`：Vulkan 256、D3D12 128、GL 256。

## 多重间接绘制

`RenderPassEncoder` 的四个方法，调用 wgpu-native 对应的 `wgpuRenderPassEncoderMultiDraw*`：

| 方法 | 每条记录 | 需要的特性 |
| --- | --- | --- |
| `MultiDrawIndirect(buf, offset, count)` | 16 字节：vertexCount、instanceCount、firstVertex、firstInstance | 无 |
| `MultiDrawIndexedIndirect(buf, offset, count)` | 20 字节：indexCount、instanceCount、firstIndex、baseVertex（int32）、firstInstance | 无 |
| `MultiDrawIndirectCount(buf, offset, countBuf, countOffset, maxCount)` | 同上，绘制数量由 GPU 从 `countBuf` 读取并截断到 `maxCount` | `FeatureMultiDrawIndirectCount` |
| `MultiDrawIndexedIndirectCount(...)` | 同上 | `FeatureMultiDrawIndirectCount` |

- wgpu.h 只有 `MultiDrawIndirectCount` 这个特性，非 Count 版本不需要特性（没有该特性时 wgpu 可能用多次间接绘制模拟）。
- Go 侧拦截（错误由 `Finish` 返回）：pass 已释放；indirect 或 count buffer 为 nil 或已释放（wgpu-native 里是 `expect`，
  会终止进程）；offset、count offset 不是 4 的倍数（count offset 的对齐 wgpu-core 不检查）；记录超出 buffer
  （按 16/20 字节做无溢出检查，wgpu-core 的 `offset + stride * count` 没有溢出检查）；count buffer 剩余不足 4 字节；
  Count 版本要求设备启用 `FeatureMultiDrawIndirectCount`。缺少 `INDIRECT` 用途等其他问题由 wgpu-core 报验证错误。
- count 为 0 时什么也不画。
- 本机支持情况：非 Count 版本三个后端都可用；`FeatureMultiDrawIndirectCount` **Vulkan 和 D3D12 支持，GL 不支持**（Count 测试 Skip）。
- D3D12 上的 `@builtin(vertex_index)`：只有实例带 `InstanceFlagValidationIndirectCall` 时，间接绘制（包括原有的
  `DrawIndirect`）里的 `vertex_index` 才包含 firstVertex / baseVertex。`CreateInstance(nil)` 带这个标志；
  `InstanceOptions` 的 `Flags` 为 0 时不带。Count 版本在 D3D12 上无论如何都不包含（wgpu-hal 29 的限制）。
  从顶点缓冲区取顶点不受影响。Vulkan 和 GL 没有这个问题。

## 异步回调与轮询

这份 wgpu-native 没有实现 `wgpuInstanceWaitAny`，也没有后台线程：

- 请求适配器、请求设备、弹出错误域的回调在调用内部同步执行，对应的 Go 函数直接返回结果。
- 缓冲区映射和 `OnSubmittedWorkDone` 的回调**只在轮询设备时触发**，触发时机是 `Device.Poll`，
  或者之后某次 `Queue.Submit` 做内部维护的时候。空闲队列也不会在调用内部立即触发。
  - `MapAsync` / `OnSubmittedWorkDone` 返回的 channel 需要有人轮询，任何 goroutine 调用 `Device.Poll` 都可以。
  - 阻塞的 `Map` 和 `WaitSubmittedWorkDone` 自己会轮询。`Map` 最多等 5 秒。
- 本地回调只把结果写进带缓冲的 channel。用户回调（`OnLost`、`OnUncapturedError`、`MapAsyncFunc`）
  在单独的 goroutine 上执行，可以在里面再调用本包。
- 设备 `Release` 或丢失时，尚未完成的映射和 work-done 会立即以错误结束。
  `Release` 之后不再调用 `OnLost` / `OnUncapturedError`。

## 错误处理

- 同步的失败返回 `*wgpu.Error`（`Op`、`Status`、`Message`）。
- `CommandEncoder.Finish` 在内部错误域里执行。编码器或 pass 里记录的验证错误从这里返回，
  这时不返回命令缓冲区，原因见下面的“已知限制”。
- `CreateQuerySet` 和 `RenderBundleEncoder.Finish` 也直接返回验证错误。
- 其他 `Create*` 和 wgpu-native 一样：即使描述符无效也会返回句柄，错误进入当前错误域或
  `OnUncapturedError`，也可以用 `Device.Err()` 查询。
- `PushErrorScope` 只支持 `Validation` 和 `OutOfMemory`。wgpu-native 遇到其他过滤器会终止进程，
  所以本包改为在 Go 侧 panic。

## 已知限制

- **只支持 Windows amd64。**

- **wgpu-native 在不少调用失败时直接终止整个进程**（Rust panic，`handle_error_fatal`），
  不会走错误域，也不会调用回调。本包在 Go 侧尽量提前拦截：

  | 情况 | wgpu-native 的行为 | 本包的处理 |
  | --- | --- | --- |
  | 设备丢失后调用 `Queue.Submit` | 终止进程 | 直接跳过，什么也不做 |
  | 设备丢失后调用 `Surface.ConfigureWith` / `Configure` | 终止进程 | 直接跳过 |
  | 设备丢失后调用 `Device.Poll` | 失败时终止进程 | 不调用，返回 `false` |
  | 设备丢失后调用 `Buffer.Map` / `MapAsync` / `OnSubmittedWorkDone` | 永远不会完成 | 立即返回丢失错误 |
  | 对未配置的 surface 或在设备丢失后调用 `GetCurrentTexture` | 终止进程 | 不调用，返回 `SurfaceGetCurrentTextureStatusError` |
  | 提交带验证错误的命令缓冲区 | 终止进程 | `Finish` 返回错误，不给出命令缓冲区 |
  | 创建 RenderBundle 编码器或 bundle `Finish` 时出错 | 终止进程 | 命令先在 Go 侧记录；创建时检查格式数量和采样数；`Finish` 先在 1x1 临时附件上用一个不提交的 RenderPass 重放命令做验证，通过后才交给 wgpu-native |
  | 取未映射、未对齐或越界的映射范围 | 终止进程 | Go 侧跟踪映射状态和范围，`MappedBytes` / `ReadMapped` 返回错误 |
  | 传入空句柄（pipeline、bundle、query set 等） | 终止进程 | 能检查的地方返回错误，或由 `CommandEncoder.Finish` 报告 |
  | `PushErrorScope` 用 `Internal` 过滤器 | 终止进程 | Go 侧 panic |
  | 任何 `wgpuXxxSetLabel` | 终止进程（`unimplemented!()`） | 不提供 `SetLabel` |
  | `CreateQuerySet` 用未知查询类型、统计查询没有统计项列表或含未知统计项 | 终止进程 | Go 侧返回错误 |
  | 遮挡查询没关、嵌套、没有 Begin 就 End、query index 越界或在同一 pass 内重复使用、pass 没设 `OcclusionQuerySet` 就 Begin | 不终止进程，也不会漏检带进提交：记一个验证错误，`Finish` 时报出，但对外只有笼统的 `Validation Error` | Go 侧跟踪每个 pass 的遮挡查询状态，有问题的 Begin/End 不下发给 native，`Finish` 返回具体原因（例如 `occlusion query 1 still active`）；`OcclusionQuerySet` 不是遮挡类型时 `BeginRenderPass` 直接返回错误。同一 index 在后续 pass 里可以再用 |
  | compute pass 结束时 pipeline statistics 查询没关 | wgpu-core 不检查，会带进提交 | Go 侧跟踪 Begin/End，`Finish` 返回错误 |
  | `wgpuBufferGetMapState`、`wgpuDeviceGetAdapterInfo`、`wgpuShaderModuleGetCompilationInfo` | 终止进程（`unimplemented!()`） | 不调用，`MapState`、`AdapterInfo`、`CompilationInfo` 在 Go 侧实现 |
  | RenderBundle 里 `SetImmediates` 的 offset 或长度不是 4 的倍数 | `assert_eq!` 失败，终止进程 | Go 侧拒绝，`RenderBundleEncoder.Finish` 返回错误 |
  | `SetImmediates` 数据达到 4 GiB | Rust 侧按 `u32` 读取，静默截断 | Go 侧拒绝 |
  | `MultiDraw*` 传入 nil 或已释放的 indirect / count buffer | `expect` 失败，终止进程 | Go 侧拒绝，`Finish` 返回错误 |
  | `MultiDraw*` 的 `offset + 记录大小 × 数量` 溢出、count offset 不对齐 | wgpu-core 不检查 | Go 侧检查，`Finish` 返回错误 |

  仍然没有拦截、会终止进程的情况：
  - 提交时才发现的问题，例如命令缓冲区引用的缓冲区在提交时仍处于映射状态或已经被销毁；
  - `Surface.ConfigureWith` 的参数本身无效（格式或尺寸不受支持）；
  - `GetCurrentTexture` 返回状态以外的失败；
  - 创建 surface 失败。

- **设备丢失只能部分感知。** wgpu-native 29 不转发 wgpu-core 的设备丢失回调，只有当后续某个调用
  因为设备丢失而失败时才报告。`Device.Destroy` 由 Go 侧记录丢失并触发一次 `OnLost`；
  驱动层面的真实丢失要等 wgpu-native 报告之后，上面这些拦截才会生效。

- **错误域按设备共享，不按线程区分。** 内部的错误域（`Finish`、`MapAsync`、`CreateQuerySet` 等）
  用设备级的锁串行化。如果其他 goroutine 恰好在同一时刻向同一设备产生错误，这些错误可能被归到内部错误域里。

- 本 DLL 的 `wgpuGetVersion` 是空实现，`wgpu.Version()` 总是返回 0。

- D3D12 后端下适配器的 `Description` 是空字符串。

- D3D12 上间接绘制的 `vertex_index` 需要 `InstanceFlagValidationIndirectCall`，Count 版本的多重间接绘制始终不含
  firstVertex，见“多重间接绘制”。

## 测试

```sh
go vet ./...
go test -count=1 ./...        # 默认 primary 后端

set WGPU_BACKEND=dx12
go test -count=1 ./...        # 指定后端（cmd 写法）
```

测试需要真实的 GPU 和 DLL。`TestTimestampQuery` 在适配器不支持 `TimestampQuery` 时会跳过，
`TestPipelineStatisticsRender` / `TestPipelineStatisticsCompute` 在不支持 `PipelineStatisticsQuery` 时跳过（本机 D3D12 和 GL）。
`SetImmediates` 的测试在不支持 `Immediates` 时跳过（本机三个后端都支持），多重间接绘制的 Count 子测试
在不支持 `MultiDrawIndirectCount` 时跳过（本机 GL）。

## 代码生成

`native/types.go`、`native/funcs.go`、`native/layout_test.go` 由 `gen/generate.py` 根据
`third_party/` 下的头文件生成（`go generate ./native`，需要 Python 3），不要手改。
手写的补充放在 `native/extra.go` 等文件里。
