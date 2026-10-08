//go:build windows && amd64

// Package wgpu 是 wgpu-native 的 Go 绑定，不使用 CGO。
//
//	import "github.com/liuqi0826/wgpu"
//
// 动态库通过 syscall.LoadLibrary 加载，函数调用走 purego。完整的 C API
// 在 native 包里，类型和函数与 webgpu.h / wgpu.h（wgpu 29）一一对应。
// 本包在此之上提供创建实例、枚举适配器、请求设备、缓冲区、着色器和窗口表面的常用路径，
// 以及纹理、采样器、绑定组、渲染管线、计算管线、RenderPass、ComputePass、RenderBundle、QuerySet 和拷贝命令。
// 描述符用 Go 结构体表示，零值字段取 WebGPU 的常用默认值，内部负责组装 native 结构体和 StringView。
// 示例见 example/triangle、example/textured 和 example/compute。
//
// 各编码器（CommandEncoder、RenderPassEncoder、ComputePassEncoder、RenderBundleEncoder）
// 都有 PushDebugGroup、PopDebugGroup、InsertDebugMarker，可以在 RenderDoc 等工具里看到。
// 嵌套深度在 Go 侧跟踪：空栈 pop 不会下发给 wgpu-native，pass 结束或编码器 Finish 时还有
// 未关闭的组，Finish 返回错误。wgpu-native 29 的 wgpuXxxSetLabel 全部未实现（调用即终止进程），
// 所以本包没有 SetLabel，标签通过描述符的 Label 字段设置。
//
// Pipeline statistics 查询：先用 Adapter.HasFeature(FeaturePipelineStatisticsQuery) 检查，
// 再放进 DeviceOptions.RequiredFeatures；QuerySetDescriptor 用 QueryTypePipelineStatistics
// 并列出 PipelineStatistics，pass 里用 Begin/EndPipelineStatisticsQuery 包住绘制或派发，
// ResolveQuerySet 之后用 QuerySet.ParsePipelineStatistics 解析成 PipelineStatistics 结构体。
// 本机测试中只有 Vulkan 后端支持这个特性，D3D12 和 GL 不支持。
//
// wgpu-native 29 没有实现 wgpuBufferGetMapState、wgpuDeviceGetAdapterInfo 和
// wgpuShaderModuleGetCompilationInfo（调用即终止进程），所以 Buffer.MapState、Device.AdapterInfo、
// ShaderModule.CompilationInfo 在 Go 侧实现：分别来自映射跟踪、请求设备时保存的 Adapter.Info、
// 以及在内部错误域里重新编译一次 WGSL 得到的错误（只有错误，没有警告）。
//
// Immediate data：用 FeatureImmediates 并提高 RequiredLimits.MaxImmediateSize 请求设备，
// PipelineLayoutDescriptor.ImmediateSize 声明大小，RenderPass、ComputePass、RenderBundle 的
// SetImmediates 写入。offset 和长度必须是 4 的倍数，长度不能达到 4 GiB（wgpu-native 按 32 位读取），
// 这些在 Go 侧检查，错误由 Finish 返回。
//
// 多重间接绘制：RenderPassEncoder.MultiDrawIndirect、MultiDrawIndexedIndirect 不需要特性；
// MultiDrawIndirectCount、MultiDrawIndexedIndirectCount 需要 FeatureMultiDrawIndirectCount
// （本机 Vulkan 和 D3D12 支持，GL 不支持）。空 buffer、未对齐和越界在 Go 侧拦下。
//
// 后端用 InstanceOptions.Backends 选择，ParseBackends 和 BackendsFromEnv
// 可以把 vulkan、dx12、gl 这样的名字（或环境变量 WGPU_BACKEND）转成对应的位。
//
// RequestDeviceWith 接受 DeviceOptions：OnLost、OnUncapturedError 回调，以及
// RequiredLimits、RequiredFeatures。回调按顺序在设备自己的 goroutine 上执行，可以再调用本包；
// Device.Release 之后不再触发。wgpu-native 29 不转发设备丢失事件，Device.Destroy
// 由 Go 侧记录丢失并触发 OnLost。wgpu-native 在 QueueSubmit、SurfaceConfigure 等调用
// 失败时会直接终止进程，所以设备丢失后这些方法什么也不做，用 Device.Lost 或 Device.Err 查询状态。
//
// 当前这份 wgpu-native 会在请求适配器、请求设备和弹出错误域时同步调用回调，
// 并且没有实现 wgpuInstanceWaitAny，所以这些调用不会进入 WaitAny。缓冲区映射（MapAsync）
// 和 Queue.OnSubmittedWorkDone 只在轮询设备时完成（Device.Poll 或之后的 Queue.Submit）。
// 安装方式、各示例说明和已知限制见 README.md。
//
// 使用前把 wgpu_native.dll 放在 lib/ 目录，或设置环境变量 WGPU_NATIVE_PATH。
// 句柄需要调用 Release。native 包中的指针字段是 uintptr，指向的 Go 内存必须在
// 对应的本地调用返回前保持可达。
package wgpu
