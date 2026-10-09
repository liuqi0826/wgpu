//go:build windows && amd64

package wgpu

// usedRefs records the objects a command buffer, bind group or render bundle
// uses. wgpu-native aborts the process when Queue.Submit fails validation, so
// Submit checks them on the Go side first: a used buffer must not be mapped,
// have a MapAsync in flight or be released (Buffer.Release destroys it), and a
// used texture or query set must not be destroyed. Buffers must also belong to
// the submitting device. Textures are reached
// through texture views and copies, and the objects of bind groups and render
// bundles are checked as well.
type usedRefs struct {
	buffers   map[*Buffer]struct{}
	textures  map[*Texture]struct{}
	querySets map[*QuerySet]struct{}
	groups    map[*BindGroup]struct{}
	bundles   map[*RenderBundle]struct{}
}

func addRef[T comparable](m *map[T]struct{}, v T) {
	if *m == nil {
		*m = make(map[T]struct{})
	}
	(*m)[v] = struct{}{}
}

func (r *usedRefs) buffer(b *Buffer) {
	if b != nil {
		addRef(&r.buffers, b)
	}
}

func (r *usedRefs) texture(t *Texture) {
	if t != nil {
		addRef(&r.textures, t)
	}
}

func (r *usedRefs) view(v *TextureView) {
	if v != nil {
		r.texture(v.tex)
	}
}

func (r *usedRefs) querySet(q *QuerySet) {
	if q != nil {
		addRef(&r.querySets, q)
	}
}

func (r *usedRefs) group(g *BindGroup) {
	if g != nil {
		addRef(&r.groups, g)
	}
}

func (r *usedRefs) bundle(b *RenderBundle) {
	if b != nil {
		addRef(&r.bundles, b)
	}
}

// check returns why the recorded objects cannot be submitted, or "".
func (r *usedRefs) check(dev *Device) string {
	for b := range r.buffers {
		if status := b.submitStatus(dev); status != "" {
			return status
		}
	}
	for t := range r.textures {
		if t.destroyed.Load() {
			return "uses a destroyed texture"
		}
		if t.surf != nil && t.surf.finished() {
			return "uses a surface texture that was already presented or released"
		}
	}
	for q := range r.querySets {
		if q.destroyed.Load() {
			return "uses a destroyed query set"
		}
	}
	for g := range r.groups {
		if status := g.refs.check(dev); status != "" {
			return "bind group " + status
		}
	}
	for b := range r.bundles {
		if status := b.refs.check(dev); status != "" {
			return "render bundle " + status
		}
	}
	return ""
}

// submitStatus reports why a command buffer using b cannot be submitted.
func (b *Buffer) submitStatus(dev *Device) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.Native() == 0:
		return "uses a released buffer"
	case b.dev != dev:
		// wgpu-core 29 does not catch this, and the raw buffer belongs to
		// another device.
		return "uses a buffer of another device"
	case b.state == mapPending:
		return "uses a buffer with a pending MapAsync"
	case b.state == mapActive:
		return "uses a buffer that is still mapped"
	}
	return ""
}
