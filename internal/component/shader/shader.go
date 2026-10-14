// Copyright (C) 2017-2026 The Rune Authors
// SPDX-License-Identifier: GPL-3.0-or-later
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or (at
// your option) any later version.
//
// This program is distributed in the hope that it will be useful, but
// WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU
// General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package shader

import (
	"github.com/unstablebuild/rune-go-sdk/term"
)

// Shader abstracts the ability to apply shader-like effects
// to other components.
type Shader interface {
	// Shade applies the shader's transformation
	// to the given component turning it into an animated component,
	// or returns false if this Shader's animation is done.
	Shade(frame, total int, in [][]term.Cell)
}

// ImageShader is a Shader that also shades the image placements drawn
// over the cells it shades.
type ImageShader interface {
	Shader
	// ShadeImages shades in like Shade, and returns the placements to
	// draw over it in place of images, in drawing order. It may modify
	// images and return a slice sharing its backing array.
	ShadeImages(frame, total int, in [][]term.Cell, images []term.Image) []term.Image
}

// Reveal is an [ImageShader] that starts on a snapshot of the screen it
// takes over, rather than on the first cells it shades, and uncovers the
// cells it shades in its place.
type Reveal interface {
	ImageShader
	// SetInitialCells sets the snapshot. The slice is retained as-is, so
	// callers that may mutate it should pass a clone.
	SetInitialCells(cells [][]term.Cell)
	// SetInitialImages sets the placements drawn over the snapshot.
	// Their pixels are copied, since a writer only keeps those until its
	// next frame.
	SetInitialImages(images []term.Image)
	// SetInitialDesaturation desaturates the snapshot and its placements
	// toward gray by amount, clamped to [0, 1]. It applies to what the
	// setters above have set so far.
	SetInitialDesaturation(amount float64)
}

// ShadeImages shades in with s, and returns the placements to draw over
// it: the ones s returns if it is an [ImageShader], and images otherwise.
func ShadeImages(s Shader, frame, total int, in [][]term.Cell, images []term.Image) []term.Image {
	if is, ok := s.(ImageShader); ok {
		return is.ShadeImages(frame, total, in, images)
	}
	s.Shade(frame, total, in)
	return images
}
