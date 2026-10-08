//go:build windows && amd64

package main

import (
	"flag"
	"fmt"
	"os"

	"wgpu"
	"wgpu/native"
)

var backendFlag = flag.String("backend", os.Getenv(wgpu.BackendEnv), "backends: vulkan, dx12, gl or all (default $WGPU_BACKEND)")

func main() {
	flag.Parse()
	version, err := wgpu.Version()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wgpu-native version %#x\n", version)
	fmt.Printf("library %s\n", native.LibraryPath())

	backends, err := wgpu.ParseBackends(*backendFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	instance, err := wgpu.CreateInstance(&wgpu.InstanceOptions{Backends: backends})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer instance.Release()

	adapters, err := instance.EnumerateAdapters(0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(adapters) == 0 {
		fmt.Println("no adapters")
		return
	}
	for i, adapter := range adapters {
		info, err := adapter.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "adapter %d: %v\n", i, err)
			adapter.Release()
			continue
		}
		fmt.Printf("[%d] %s / %s\n    %s\n    %s\n", i, info.Backend, info.Type, info.Device, info.Description)
		adapter.Release()
	}
}
