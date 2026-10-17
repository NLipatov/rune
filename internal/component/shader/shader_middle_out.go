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

// MiddleOut grows the text out of the center of the canvas in two
// moves: every character first spreads from the center along the middle
// row (or column, when Horizontal), then rises or sinks to its place,
// its color warming from StartColor to its own during the second move.
//
// The last frame of the animation matches the input exactly.
//
// Zero params take their defaults; params out of their documented range
// panic with an error wrapping [ErrInvalidParams].
//
// Inspired by terminaltexteffects' "middleout" effect.
func MiddleOut(params MiddleOutParams, defaultAttr term.Attributes) Shader {
	d := DefaultMiddleOutParams()
	params.StartColor = orDefault(params.StartColor, d.StartColor)
	params.CenterEasing = checkEasing("MiddleOutParams.CenterEasing", params.CenterEasing, d.CenterEasing)
	params.FullEasing = checkEasing("MiddleOutParams.FullEasing", params.FullEasing, d.FullEasing)
	params.CenterDuration = checkDuration("MiddleOutParams.CenterDuration",
		orDefault(params.CenterDuration, d.CenterDuration))
	return &middleOut{MiddleOutParams: params, defaultAttr: defaultAttr}
}

// MiddleOutParams configures [MiddleOut].
type MiddleOutParams struct {
	// StartColor is the color every character has until it leaves the
	// middle line.
	StartColor term.Color
	// Horizontal spreads the characters along the middle column first,
	// so the second move is horizontal, instead of along the middle row.
	Horizontal bool
	// CenterEasing shapes the first move, FullEasing the second.
	CenterEasing shaderutils.Easing
	FullEasing   shaderutils.Easing
	// CenterDuration is the fraction of the animation the first move
	// takes; the second takes the rest. (range (0, 1))
	CenterDuration float64
}

// DefaultMiddleOutParams returns the terminaltexteffects defaults: a
// white start, vertical expansion and in-out sine easings.
func DefaultMiddleOutParams() MiddleOutParams {
	return MiddleOutParams{
		StartColor:     term.NewRGBColor(255, 255, 255),
		CenterEasing:   shaderutils.EaseInOutSine,
		FullEasing:     shaderutils.EaseInOutSine,
		CenterDuration: 0.5,
	}
}

type middleOut struct {
	MiddleOutParams
	defaultAttr term.Attributes
}

func (s *middleOut) Shade(frame, total int, cells [][]term.Cell) {
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
	firstSpan, secondSpan := cx, cy*rowScale
	if s.Horizontal {
		firstSpan, secondSpan = secondSpan, firstSpan
	}
	places := make([]placement, len(glyphs))
	for i, g := range glyphs {
		gx, gy := float64(g.x), float64(g.y)
		// The middle line point is reached after the first move.
		mx, my := gx, cy
		if s.Horizontal {
			mx, my = cx, gy
		}
		dur := s.CenterDuration
		if firstSpan > 0 {
			dur *= cellDistance(cx, cy, mx, my) / firstSpan
		}
		p1 := s.CenterEasing(window(t, 0, dur))
		dur = 1 - s.CenterDuration
		if secondSpan > 0 {
			dur *= cellDistance(mx, my, gx, gy) / secondSpan
		}
		p2 := window(t, s.CenterDuration, dur)
		e2 := s.FullEasing(p2)
		x := lerp(lerp(cx, mx, p1), gx, e2)
		y := lerp(lerp(cy, my, p1), gy, e2)
		places[i] = placement{
			x:  roundInt(x),
			y:  roundInt(y),
			fg: fadeFg(p2, s.StartColor, g.cell.Fg, s.defaultAttr.Fg),
		}
	}
	place(cells, glyphs, places, s.defaultAttr.Bg)
}
