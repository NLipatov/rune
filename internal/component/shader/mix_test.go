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
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/unstablebuild/rune-go-sdk/term"
)

func TestMixPassesImagesThroughItsShadersInOrder(t *testing.T) {
	var seen [][]term.Image
	adding := func(id term.ImageID) *imageShader {
		return &imageShader{images: func(images []term.Image) []term.Image {
			seen = append(seen, slices.Clone(images))
			return append(images, term.Image{ID: id})
		}}
	}
	plain := new(mockShader)
	mix := Mix(adding(2), plain, adding(3)).(ImageShader)

	got := mix.ShadeImages(0, 1, makeCellMatrix(2, 2), []term.Image{{ID: 1}})

	assert.Equal(t, []term.Image{{ID: 1}, {ID: 2}, {ID: 3}}, got)
	assert.Equal(t, [][]term.Image{{{ID: 1}}, {{ID: 1}, {ID: 2}}}, seen)
	assert.True(t, plain.shadeCalled)
}
