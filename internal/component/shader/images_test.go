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
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/unstablebuild/rune-go-sdk/term"
)

func TestImageCutterCutsPlacementsToTheCellsShowingThem(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	picture := term.Image{Src: src, ID: 7, Pos: term.Coordinates{X: 2, Y: 1}, Width: 4, Height: 3}
	clipped := picture
	clipped.Clip = image.Rect(2, 1, 6, 3)
	tests := []struct {
		name   string
		images []term.Image
		hidden []image.Point
		want   []image.Rectangle
	}{
		{
			name: "shown everywhere", images: []term.Image{picture},
			want: []image.Rectangle{image.Rect(2, 1, 6, 4)},
		},
		{
			name: "hidden elsewhere", images: []term.Image{picture},
			hidden: []image.Point{{0, 0}, {1, 1}, {6, 3}, {7, 4}},
			want:   []image.Rectangle{image.Rect(2, 1, 6, 4)},
		},
		{
			name: "hidden on a cell", images: []term.Image{picture},
			hidden: []image.Point{{3, 2}},
			want: []image.Rectangle{
				image.Rect(2, 1, 6, 2), image.Rect(2, 2, 3, 3),
				image.Rect(4, 2, 6, 3), image.Rect(2, 3, 6, 4),
			},
		},
		{
			name: "hidden on a column", images: []term.Image{picture},
			hidden: []image.Point{{4, 1}, {4, 2}, {4, 3}},
			want:   []image.Rectangle{image.Rect(2, 1, 4, 4), image.Rect(5, 1, 6, 4)},
		},
		{
			name: "keeps clips", images: []term.Image{clipped},
			hidden: []image.Point{{2, 1}},
			want:   []image.Rectangle{image.Rect(3, 1, 6, 2), image.Rect(2, 2, 6, 3)},
		},
		{
			name: "hidden everywhere", images: []term.Image{picture},
			hidden: []image.Point{
				{2, 1}, {3, 1}, {4, 1}, {5, 1}, {2, 2}, {3, 2}, {4, 2}, {5, 2},
				{2, 3}, {3, 3}, {4, 3}, {5, 3},
			},
		},
		{name: "nothing to cut"},
	}
	var c imageCutter
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shows := func(x, y int) bool {
				for _, p := range tt.hidden {
					if p == image.Pt(x, y) {
						return false
					}
				}
				return true
			}
			lead := term.Image{ID: 1}
			got := c.cut([]term.Image{lead}, tt.images, 8, 5, shows)
			require.Equal(t, lead, got[0], "appends to dst")
			var clips []image.Rectangle
			for _, img := range got[1:] {
				assert.Equal(t, picture.ID, img.ID)
				assert.Equal(t, picture.Bounds(), img.Bounds())
				clips = append(clips, img.Clip)
			}
			assert.Equal(t, tt.want, clips)
		})
	}
}

func TestImageCutterFollowsTheScreenSize(t *testing.T) {
	picture := term.Image{Src: image.NewRGBA(image.Rect(0, 0, 1, 1)), ID: 7, Width: 30, Height: 20}
	var c imageCutter
	all := func(int, int) bool { return true }
	for _, size := range []image.Point{{8, 5}, {20, 10}, {8, 5}} {
		got := c.cut(nil, []term.Image{picture}, size.X, size.Y, all)
		require.Len(t, got, 1)
		assert.Equal(t, image.Rectangle{Max: size}, got[0].Clip)
	}
}

func TestCopyImagesCopiesThePixelsShown(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for i := range src.Pix {
		src.Pix[i] = uint8(i)
	}
	picture := term.Image{Src: src, ID: 7, Version: 3, Pos: term.Coordinates{X: 1}, Width: 2, Height: 1}
	left, right := picture, picture
	left.Clip, right.Clip = image.Rect(1, 0, 2, 1), image.Rect(2, 0, 3, 1)
	cropped := picture
	cropped.Crop = image.Rect(1, 1, 3, 2)
	anonymous := term.Image{Src: src, Width: 1, Height: 1}

	got := copyImages([]term.Image{left, right, cropped, anonymous, anonymous})
	require.Len(t, got, 5)
	for i, want := range []term.Image{left, right, cropped, anonymous, anonymous} {
		img := got[i]
		assert.Equal(t, want.Bounds(), img.Bounds(), "placement %d", i)
		assert.Equal(t, want.Clip, img.Clip, "placement %d", i)
		assert.Empty(t, img.Crop, "placement %d", i)
		assert.NotEqual(t, want.ID, img.ID, "placement %d", i)
		assert.NotSame(t, src, img.Src, "placement %d", i)
	}
	assert.Same(t, got[0].Src, got[1].Src, "the parts of a picture share a copy")
	assert.Equal(t, got[0].ID, got[1].ID)
	assert.NotSame(t, got[3].Src, got[4].Src, "placements without an ID are told apart")
	assert.Equal(t, src.Pix, got[0].Src.(*image.RGBA).Pix)
	assert.Equal(t, src.Pix[src.PixOffset(1, 1):src.PixOffset(3, 1)], got[2].Src.(*image.RGBA).Pix,
		"copies the crop")

	src.Pix[0] = 99
	assert.Zero(t, got[0].Src.(*image.RGBA).Pix[0], "copies are left as they were")
	assert.Nil(t, copyImages(nil))
}

func TestDesaturatePixels(t *testing.T) {
	tests := []struct {
		amount float64
		in     color.RGBA
		want   color.RGBA
	}{
		{0, color.RGBA{R: 200, G: 100, B: 50, A: 255}, color.RGBA{R: 200, G: 100, B: 50, A: 255}},
		{1, color.RGBA{R: 200, G: 100, B: 50, A: 255}, color.RGBA{R: 124, G: 124, B: 124, A: 255}},
		{0.5, color.RGBA{R: 200, G: 100, B: 50, A: 255}, color.RGBA{R: 162, G: 112, B: 87, A: 255}},
		{2, color.RGBA{R: 200, G: 100, B: 50, A: 255}, color.RGBA{R: 124, G: 124, B: 124, A: 255}},
		{1, color.RGBA{R: 100, G: 50, B: 25, A: 128}, color.RGBA{R: 62, G: 62, B: 62, A: 128}},
	}
	for _, tt := range tests {
		img := image.NewRGBA(image.Rect(3, 2, 5, 3))
		img.SetRGBA(3, 2, tt.in)
		img.SetRGBA(4, 2, tt.in)
		desaturatePixels(img, tt.amount)
		assert.Equal(t, tt.want, img.RGBAAt(3, 2), "%v by %.1f", tt.in, tt.amount)
		assert.Equal(t, tt.want, img.RGBAAt(4, 2), "%v by %.1f", tt.in, tt.amount)
	}
}
