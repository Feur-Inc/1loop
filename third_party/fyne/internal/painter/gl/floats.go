// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #474): fewer allocations per frame.

package gl

import "unsafe"

// floatsPtr is the address of the first point, nil for none.
func floatsPtr(points []float32) unsafe.Pointer {
	if len(points) == 0 {
		return nil
	}
	return unsafe.Pointer(&points[0])
}

// coords gives the painter's buffer of vertex data, emptied, for the
// object being drawn: it is uploaded before the next object's is made, so
// one buffer serves them all (each object drew from a slice of its own).
func (p *painter) coords() []float32 {
	if p.points == nil {
		p.points = make([]float32, 0, 32)
	}
	return p.points[:0]
}
