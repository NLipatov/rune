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
	"math"

	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/component/shader/shaderutils"
)

// PourDirection is the direction [Pour] pours the text in.
type PourDirection uint8

const (
	// PourDown pours from the top edge, filling the bottom rows first.
	PourDown PourDirection = iota
	// PourUp pours from the bottom edge, filling the top rows first.
	PourUp
	// PourLeft pours from the right edge, filling the left columns
	// first.
	PourLeft
	// PourRight pours from the left edge, filling the right columns
	// first.
	PourRight
)

// Pour pours the text in from one edge of the canvas: the characters
// stream in a steady trickle, line by line from the far side, snaking
// back and forth, each falling to its place with an easing and its
// color warming from StartColor to its own.
//
// The last frame of the animation matches the input exactly.
//
// Zero params take their defaults, except where noted; params out of
// their documented range panic with an error wrapping
// [ErrInvalidParams].
//
// Inspired by terminaltexteffects' "pour" effect.
func Pour(params PourParams, defaultAttr term.Attributes) Shader {
	d := DefaultPourParams()
	if params.Direction > PourRight {
		invalidParam("PourParams.Direction", params.Direction, "a PourDirection")
	}
	params.StartColor = orDefault(params.StartColor, d.StartColor)
	params.Easing = checkEasing("PourParams.Easing", params.Easing, d.Easing)
	params.FallDuration = checkDuration("PourParams.FallDuration", orDefault(params.FallDuration, d.FallDuration))
	checkFloat("PourParams.SpeedJitter", params.SpeedJitter, 0, 1, false, true)
	params.Seed = orDefault(params.Seed, d.Seed)
	return &pour{PourParams: params, defaultAttr: defaultAttr}
}

// PourParams configures [Pour].
type PourParams struct {
	// Direction is one of the PourDirection constants.
	Direction PourDirection
	// StartColor is the color every character has as it pours in.
	StartColor term.Color
	// Easing shapes each character's fall.
	Easing shaderutils.Easing
	// FallDuration is the fraction of the animation a character takes
	// to fall across the whole canvas; the trickle takes the rest.
	// (range (0, 1))
	FallDuration float64
	// SpeedJitter varies each character's fall time by up to this
	// fraction, so a line does not land as one; zero does not vary it.
	// (range [0, 1))
	SpeedJitter float64
	// Seed picks the jitter. Defaults to a fixed value if zero.
	Seed int64
}

// DefaultPourParams returns the terminaltexteffects defaults: pouring
// down, a white start, an in quad easing and speeds spread by a fifth.
func DefaultPourParams() PourParams {
	return PourParams{
		Direction:    PourDown,
		StartColor:   term.NewRGBColor(255, 255, 255),
		Easing:       shaderutils.EaseInQuad,
		FallDuration: 0.3,
		SpeedJitter:  0.2,
		Seed:         1,
	}
}

type pour struct {
	PourParams
	defaultAttr term.Attributes
}

func (s *pour) Shade(frame, total int, cells [][]term.Cell) {
	t, ok := progress(frame, total)
	if !ok {
		return
	}
	rows, cols := canvasSize(cells)
	if rows == 0 || cols == 0 {
		return
	}
	glyphs := collectContent(cells, s.defaultAttr.Bg)
	if len(glyphs) == 0 {
		return
	}
	// Lines pour from the far side, snaking: every other line streams
	// in reversed.
	ranks := ranksBy(glyphs, func(g glyph) (int, int) {
		var group, index, span int
		switch s.Direction {
		case PourDown:
			group, index, span = rows-1-g.y, g.x, cols
		case PourUp:
			group, index, span = g.y, g.x, cols
		case PourLeft:
			group, index, span = g.x, g.y, rows
		default:
			group, index, span = cols-1-g.x, g.y, rows
		}
		if group%2 == 1 {
			index = span - 1 - index
		}
		return group, index
	})
	trickle := 1 - s.FallDuration
	places := make([]placement, len(glyphs))
	for i, g := range glyphs {
		start := trickle * float64(ranks[i]) / float64(len(glyphs))
		var sx, sy, dist, span float64
		switch s.Direction {
		case PourDown:
			sx, sy, dist, span = float64(g.x), 0, float64(g.y), float64(rows-1)
		case PourUp:
			sx, sy, dist, span = float64(g.x), float64(rows-1), float64(rows-1-g.y), float64(rows-1)
		case PourLeft:
			sx, sy, dist, span = float64(cols-1), float64(g.y), float64(cols-1-g.x), float64(cols-1)
		default:
			sx, sy, dist, span = 0, float64(g.y), float64(g.x), float64(cols-1)
		}
		dur := s.FallDuration
		if span > 0 {
			dur *= dist / span
		}
		// A slowed fall must still land by the last frame.
		dur = math.Min(dur*(1+s.SpeedJitter*(2*unit3(s.Seed, g.x, g.y, 1)-1)), 1-start)
		if t < start {
			places[i] = placement{hidden: true}
			continue
		}
		p := window(t, start, dur)
		e := s.Easing(p)
		places[i] = placement{
			x:  roundInt(lerp(sx, float64(g.x), e)),
			y:  roundInt(lerp(sy, float64(g.y), e)),
			fg: fadeFg(p, s.StartColor, g.cell.Fg, s.defaultAttr.Fg),
		}
	}
	place(cells, glyphs, places, s.defaultAttr.Bg)
}
