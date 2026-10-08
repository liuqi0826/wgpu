//go:build windows && amd64

package wgpu

import (
	"reflect"

	"wgpu/native"
)

// UndefinedLimits returns limits with every field undefined, which requests
// the default for each limit. Set only the fields you need.
func UndefinedLimits() native.Limits {
	var l native.Limits
	v := reflect.ValueOf(&l).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if v.Type().Field(i).Name == "NextInChain" {
			continue
		}
		switch f.Kind() {
		case reflect.Uint32:
			f.SetUint(uint64(native.LimitU32Undefined))
		case reflect.Uint64:
			f.SetUint(native.LimitU64Undefined)
		}
	}
	return l
}

// Limits returns the limits the device was created with.
func (d *Device) Limits() (native.Limits, error) {
	var limits native.Limits
	if status := native.DeviceGetLimits(d.Native(), &limits); status != native.StatusSuccess {
		return native.Limits{}, &Error{Op: "DeviceGetLimits", Status: status.String()}
	}
	return limits, nil
}

// Features lists the features enabled on the device.
func (d *Device) Features() []native.FeatureName {
	var raw native.SupportedFeatures
	native.DeviceGetFeatures(d.Native(), &raw)
	out := readSlice[native.FeatureName](raw.Features, raw.FeatureCount)
	native.SupportedFeaturesFreeMembers(raw)
	return out
}

// HasFeature reports whether f is enabled on the device.
func (d *Device) HasFeature(f native.FeatureName) bool {
	return native.DeviceHasFeature(d.Native(), f) == native.True
}
