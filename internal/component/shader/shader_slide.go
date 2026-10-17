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
	"sort"

	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/component/shader/shaderutils"
)

// SlideGrouping is the lines [Slide] slides the text in by.
type SlideGrouping uint8

const (
	// SlideRows slides rows in from the left, top row first.
	SlideRows SlideGrouping = iota
	// SlideColumns slides columns in from the top, left column first.
	SlideColumns
	// SlideDiagonals slides diagonals in from below the bottom-left
	// corner, the one nearest the top-left corner first.
	SlideDiagonals
)

// Slide slides the text in line by line: each line's characters enter
// from an edge one after the other, farthest first, and glide to their
// place with an easing, their color warming from StartColor to their
// own.
//
// The last frame of the animation matches the input exactly.
//
// Zero params take their defaults; params out of their documented range
// panic with an error wrapping [ErrInvalidParams].
//
// Inspired by terminaltexteffects' "slide" effect.
func Slide(params SlideParams, defaultAttr term.Attributes) Shader {
	d := DefaultSlideParams()
	if params.Grouping > SlideDiagonals {
		invalidParam("SlideParams.Grouping", params.Grouping, "a SlideGrouping")
	}
	params.Gap = capCount("SlideParams.Gap", orDefault(params.Gap, d.Gap), 1, 1024)
	params.Easing = checkEasing("SlideParams.Easing", params.Easing, d.Easing)
	params.StartColor = orDefault(params.StartColor, d.StartColor)
	params.SlideDuration = checkDuration("SlideParams.SlideDuration", orDefault(params.SlideDuration, d.SlideDuration))
	return &slide{SlideParams: params, defaultAttr: defaultAttr}
}

// SlideParams configures [Slide].
type SlideParams struct {
	// Grouping is one of the SlideGrouping constants.
	Grouping SlideGrouping
	// Gap is how many characters a line lets in before the next line
	// starts. (range [1, ∞), capped at 1024)
	Gap int
	// ReverseDirection slides the lines in from the opposite edge.
	ReverseDirection bool
	// Merge alternates the edge lines slide in from, so they meet in
	// the middle. It overrides ReverseDirection.
	Merge bool
	// Easing shapes each character's glide.
	Easing shaderutils.Easing
	// StartColor is the color every character has as it enters.
	StartColor term.Color
	// SlideDuration is the fraction of the animation a character takes
	// to cross the whole canvas; the lines are let in over the rest.
	// (range (0, 1))
	SlideDuration float64
}

// DefaultSlideParams returns the terminaltexteffects defaults: rows
// from the left, two characters apart, an in-out quad easing and the
// first stop of its final gradient as start color.
func DefaultSlideParams() SlideParams {
	return SlideParams{
		Grouping:      SlideRows,
		Gap:           2,
		Easing:        shaderutils.EaseInOutQuad,
		StartColor:    term.NewRGBColor(0x83, 0x3a, 0xb4),
		SlideDuration: 0.3,
	}
}

type slide struct {
	SlideParams
	defaultAttr term.Attributes
}

func (s *slide) Shade(frame, total int, cells [][]term.Cell) {
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
	// fromFar tells whether a line enters from the far edge: the right,
	// the bottom, or above the top-right corner.
	fromFar := func(group int) bool {
		if s.Merge {
			return group%2 == 0
		}
		return s.ReverseDirection
	}
	groupOf := func(g glyph) int {
		switch s.Grouping {
		case SlideColumns:
			return g.x
		case SlideDiagonals:
			return g.x + g.y
		default:
			return g.y
		}
	}
	// startOf is where a line's characters enter from.
	startOf := func(g glyph) (x, y float64) {
		far := fromFar(groupOf(g))
		switch s.Grouping {
		case SlideColumns:
			if far {
				return float64(g.x), float64(rows)
			}
			return float64(g.x), -1
		case SlideDiagonals:
			if far {
				return float64(g.x + g.y + 1), -1
			}
			return float64(g.x + g.y - rows), float64(rows)
		default:
			if far {
				return float64(cols), float64(g.y)
			}
			return -1, float64(g.y)
		}
	}
	// Characters enter farthest from the edge first. Ranking within a
	// line by the distance to its start does that for every grouping.
	ranks := ranksBy(glyphs, func(g glyph) (int, int) {
		sx, sy := startOf(g)
		return groupOf(g), -roundInt(cellDistance(sx, sy, float64(g.x), float64(g.y)) * 16)
	})
	groupSizes := make(map[int]int)
	for _, g := range glyphs {
		groupSizes[groupOf(g)]++
	}
	groupIDs := make([]int, 0, len(groupSizes))
	for id := range groupSizes {
		groupIDs = append(groupIDs, id)
	}
	sort.Ints(groupIDs)
	groupOrdinal := make(map[int]int, len(groupIDs))
	maxSize, before := 0, 0
	indexBase := make(map[int]int, len(groupIDs))
	for ordinal, id := range groupIDs {
		groupOrdinal[id] = ordinal
		indexBase[id] = before
		before += groupSizes[id]
		maxSize = max(maxSize, groupSizes[id])
	}
	// A line lets its characters in one step apart, the next line
	// starting Gap steps after it, and the last one in must still have
	// time to cross.
	steps := (len(groupIDs)-1)*s.Gap + maxSize - 1
	stepT := 0.0
	if steps > 0 {
		stepT = (1 - s.SlideDuration) / float64(steps)
	}
	// Every character crosses at the same speed, the longest way taking
	// SlideDuration. Diagonals entering above the top-right corner can
	// travel farther than the canvas is wide or tall.
	span := 0.0
	for _, g := range glyphs {
		sx, sy := startOf(g)
		span = max(span, cellDistance(sx, sy, float64(g.x), float64(g.y)))
	}
	places := make([]placement, len(glyphs))
	for i, g := range glyphs {
		id := groupOf(g)
		index := ranks[i] - indexBase[id]
		start := float64(groupOrdinal[id]*s.Gap+index) * stepT
		if t < start {
			places[i] = placement{hidden: true}
			continue
		}
		sx, sy := startOf(g)
		// Rounding must not push the last character in past the end.
		dur := math.Min(s.SlideDuration*cellDistance(sx, sy, float64(g.x), float64(g.y))/span, 1-start)
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
