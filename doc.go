//go:build windows && amd64

// Package wgpu 是 wgpu-native 的 Go 绑定，不使用 CGO。
//
// 动态库通过 syscall.LoadLibrary 加载，函数调用走 purego。完整的 C API
// 在 native 包里，类型和函数与 webgpu.h / wgpu.h（wgpu 29）一一对应。
// 本包在此之上提供创建实例、枚举适配器、请求设备、缓冲区、着色器和窗口表面的常用路径。
// 当前这份 wgpu-native 会在请求适配器、请求设备和弹出错误域时同步调用回调，
// 并且没有实现 wgpuInstanceWaitAny，所以这些调用不会进入 WaitAny。
//
// 使用前把 wgpu_native.dll 放在 lib/ 目录，或设置环境变量 WGPU_NATIVE_PATH。
// 句柄需要调用 Release。native 包中的指针字段是 uintptr，指向的 Go 内存必须在
// 对应的本地调用返回前保持可达。
package wgpu
