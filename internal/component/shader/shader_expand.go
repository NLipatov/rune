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
	"unstable.build/rune/internal/component/shader/shaderutils"
)

// Expand bursts the text out of the center of the canvas: every
// character starts there and travels to its place at one shared speed,
// so the nearer ones settle first, while its color warms from
// StartColor to its own along the way.
//
// The last frame of the animation matches the input exactly.
//
// Zero params take their defaults; params out of their documented range
// panic with an error wrapping [ErrInvalidParams].
//
// Inspired by terminaltexteffects' "expand" effect.
func Expand(params ExpandParams, defaultAttr term.Attributes) Shader {
	d := DefaultExpandParams()
	params.StartColor = orDefault(params.StartColor, d.StartColor)
	params.Easing = checkEasing("ExpandParams.Easing", params.Easing, d.Easing)
	params.TravelDuration = checkFloat("ExpandParams.TravelDuration",
		orDefault(params.TravelDuration, d.TravelDuration), 0, 1, true, false)
	return &expand{ExpandParams: params, defaultAttr: defaultAttr}
}

// ExpandParams configures [Expand].
type ExpandParams struct {
	// StartColor is the color every character has at the center.
	StartColor term.Color
	// Easing shapes each character's travel.
	Easing shaderutils.Easing
	// TravelDuration is the fraction of the animation the farthest
	// character spends traveling; nearer ones scale it by their
	// distance. (range (0, 1])
	TravelDuration float64
}

// DefaultExpandParams returns the terminaltexteffects defaults: the
// first stop of its final gradient as start color, and an in-out quart
// easing.
func DefaultExpandParams() ExpandParams {
	return ExpandParams{
		StartColor:     term.NewRGBColor(0x8A, 0x00, 0x8A),
		Easing:         shaderutils.EaseInOutQuart,
		TravelDuration: 1,
	}
}

type expand struct {
	ExpandParams
	defaultAttr term.Attributes
}

func (s *expand) Shade(frame, total int, cells [][]term.Cell) {
	t, ok := progress(frame, total)
	if !ok {
		return
	}
	rows, cols := canvasSize(cells)
	if rows == 0 || cols == 0 {
		return
	}
	glyphs := collectContent(cells, s.defaultAttr.Bg)
	cx, cy := float64(cols-1)/2, float64(rows-1)/2
	maxDist := cellDistance(0, 0, cx, cy)
	places := make([]placement, len(glyphs))
	for i, g := range glyphs {
		dur := s.TravelDuration
		if maxDist > 0 {
			dur *= cellDistance(cx, cy, float64(g.x), float64(g.y)) / maxDist
		}
		p := window(t, 0, dur)
		e := s.Easing(p)
		places[i] = placement{
			x:  roundInt(lerp(cx, float64(g.x), e)),
			y:  roundInt(lerp(cy, float64(g.y), e)),
			fg: fadeFg(p, s.StartColor, g.cell.Fg, s.defaultAttr.Fg),
		}
	}
	place(cells, glyphs, places, s.defaultAttr.Bg)
}
