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
	"context"
	"image"
	"image/draw"
	"math"

	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/cell"
)

// imageCutter cuts placements to the cells of a screen that show them.
type imageCutter struct {
	buf        *cell.BufferWriter
	cols, rows int
}

// cut appends to dst the parts of images over the cells of a cols by rows
// screen for which shows reports true, merging the cells of neighboring
// rows into as few placements as it can. It reads all of images before it
// appends to dst, so the two may share their backing array.
func (c *imageCutter) cut(
	dst, images []term.Image, cols, rows int, shows func(x, y int) bool,
) []term.Image {
	if len(images) == 0 || cols <= 0 || rows <= 0 {
		return dst
	}
	if c.buf == nil || c.cols != cols || c.rows != rows {
		c.buf = cell.NewBufferWriter(context.Background(), cols, rows)
		c.cols, c.rows = cols, rows
	}
	_ = c.buf.Clear(term.Attributes{})
	var area image.Rectangle
	for _, img := range images {
		c.buf.DrawImage(img)
		area = area.Union(img.Visible())
	}
	// A cell written over a placement hides it, which is how the buffer
	// cuts the placements to the cells left showing them.
	area = area.Intersect(image.Rect(0, 0, cols, rows))
	hide := term.NewCell(' ', 1, term.Attributes{})
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if !shows(x, y) {
				c.buf.SetCell(term.Coordinates{X: x, Y: y}, hide)
			}
		}
	}
	return append(dst, c.buf.Images()...)
}

// rgbaPixels returns the part of img's source it shows, with its origin
// at 0, 0, sharing the source's pixels when they already are.
func rgbaPixels(img term.Image) *image.RGBA {
	crop := cropOf(img)
	if rgba, ok := img.Src.(*image.RGBA); ok && crop == rgba.Rect && crop.Min == (image.Point{}) {
		return rgba
	}
	dst := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	draw.Draw(dst, dst.Rect, img.Src, crop.Min, draw.Src)
	return dst
}

// copyImage returns img with a copy of the pixels it shows, under an ID
// of its own, for placements kept past the frame they were drawn in.
func copyImage(img term.Image) term.Image {
	crop := cropOf(img)
	dst := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	draw.Draw(dst, dst.Rect, img.Src, crop.Min, draw.Src)
	img.Src, img.Crop, img.ID, img.Version = dst, image.Rectangle{}, term.NewImageID(), 0
	return img
}

// copyImages returns images copied by copyImage, sharing a copy among
// the placements of a picture, such as the parts of one cut by a window.
func copyImages(images []term.Image) []term.Image {
	if len(images) == 0 {
		return nil
	}
	type picture struct {
		id   term.ImageID
		crop image.Rectangle
	}
	copies := make(map[picture]term.Image, len(images))
	out := make([]term.Image, len(images))
	for i, img := range images {
		key := picture{img.ID, cropOf(img)}
		c, ok := copies[key]
		// Placements without an ID cannot tell their pictures apart.
		if !ok || img.ID == 0 {
			c = copyImage(img)
			copies[key] = c
		}
		img.Src, img.Crop, img.ID, img.Version = c.Src, c.Crop, c.ID, c.Version
		out[i] = img
	}
	return out
}

// cropOf returns the rectangle of img's source it shows.
func cropOf(img term.Image) image.Rectangle {
	if img.Src == nil {
		return image.Rectangle{}
	}
	if img.Crop.Empty() {
		return img.Src.Bounds()
	}
	return img.Crop.Intersect(img.Src.Bounds())
}

// desaturatePixels desaturates the pixels of dst by amount, from 0 for
// none to 1 for gray, the way [shaderutils.DesaturateColor] does colors.
func desaturatePixels(dst *image.RGBA, amount float64) {
	if amount <= 0 {
		return
	}
	amount = math.Min(1, amount)
	for y := dst.Rect.Min.Y; y < dst.Rect.Max.Y; y++ {
		row := dst.Pix[dst.PixOffset(dst.Rect.Min.X, y):dst.PixOffset(dst.Rect.Max.X, y)]
		for i := 0; i < len(row); i += 4 {
			// The channels are premultiplied, which leaves the blend
			// toward their luminance unchanged.
			r, g, b := float64(row[i]), float64(row[i+1]), float64(row[i+2])
			lum := 0.299*r + 0.587*g + 0.114*b
			row[i] = uint8(math.Round(r + (lum-r)*amount))
			row[i+1] = uint8(math.Round(g + (lum-g)*amount))
			row[i+2] = uint8(math.Round(b + (lum-b)*amount))
		}
	}
}
