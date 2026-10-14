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

package shader_test

import (
	"context"
	"image"
	"image/color"
	"slices"

	"github.com/unstablebuild/rune-go-sdk/term"
	"github.com/unstablebuild/rune-go-sdk/tui"
	"unstable.build/rune/internal/cell"
	"unstable.build/rune/internal/component/braille"
)

func makeCharCells(cols, rows int) [][]term.Cell {
	out := make([][]term.Cell, rows)
	for y := range rows {
		out[y] = make([]term.Cell, cols)
		for x := range cols {
			out[y][x] = term.NewCell('A', 1,
				term.Attributes{Fg: term.ColorWhite, Bg: term.ColorBlack})
		}
	}
	return out
}

func makeBlankCells(cols, rows int) [][]term.Cell {
	out := make([][]term.Cell, rows)
	for y := range rows {
		out[y] = make([]term.Cell, cols)
		for x := range cols {
			out[y][x] = term.NewCell(' ', 1,
				term.Attributes{Fg: term.ColorWhite, Bg: term.ColorBlack})
		}
	}
	return out
}

func cloneCells(in [][]term.Cell) [][]term.Cell {
	out := make([][]term.Cell, len(in))
	for y, row := range in {
		out[y] = make([]term.Cell, len(row))
		copy(out[y], row)
	}
	return out
}

// wallpaperBg is the background of the cells of the wallpapers drawn by
// blockWallpaper, which their braille rendering does not carry.
var wallpaperBg = term.ColorMaroon

// blockWallpaper draws a text row on top of a cols x rows wallpaper of
// blank braille whose art is made of blocks colored fg filling arts, in
// screen coordinates, with a cell drawn over the wallpaper at 2, rows-2.
func blockWallpaper(cols, rows int, fg term.Color, arts ...image.Rectangle) [][]term.Cell {
	text := term.Attributes{Fg: term.ColorWhite, Bg: term.ColorNavy}
	out := make([][]term.Cell, rows)
	for y := range out {
		out[y] = make([]term.Cell, cols)
		for x := range out[y] {
			switch {
			case y == 0:
				out[y][x] = term.NewCell('t', 1, text)
			case x == 2 && y == rows-2:
				out[y][x] = term.NewCell('P', 1, text)
			case inAny(image.Pt(x, y), arts):
				out[y][x] = term.NewCell(braille.Block, 1, term.Attributes{Fg: fg, Bg: wallpaperBg})
			default:
				out[y][x] = term.NewCell(braille.Blank, 1, term.Attributes{Bg: wallpaperBg})
			}
		}
	}
	return out
}

// blockWallpaperDots returns the braille rendering of a wallpaper drawn
// by blockWallpaper with arts: a fully raised cell in place of each
// block, and a column of dots without blocks right after each art. It
// appends the size of every area it draws to drawn, when not nil.
func blockWallpaperDots(fg term.Color, drawn *[][2]int, arts ...image.Rectangle) tui.Component {
	return &blockDots{fg: fg, drawn: drawn, arts: arts}
}

type blockDots struct {
	fg            term.Color
	drawn         *[][2]int
	arts          []image.Rectangle
	width, height int
}

func (d *blockDots) Resize(width, height int) { d.width, d.height = width, height }

func (d *blockDots) Draw(w term.Writer) {
	if d.drawn != nil {
		*d.drawn = append(*d.drawn, [2]int{d.width, d.height})
	}
	for y := range d.height {
		for x := range d.width {
			// The wallpaper starts below the text row.
			p := image.Pt(x, y+1)
			c := term.NewCell(braille.Blank, 1, term.Attributes{})
			switch {
			case inAny(p, d.arts):
				c = term.NewCell('⣿', 1, term.Attributes{Fg: d.fg})
			case inAny(p.Sub(image.Pt(1, 0)), d.arts):
				c = term.NewCell('⡇', 1, term.Attributes{Fg: d.fg})
			}
			w.SetCell(term.Coordinates{X: x, Y: y}, c)
		}
	}
}

// drawCells returns the cells c draws when it is width by height cells.
func drawCells(c tui.Component, width, height int) [][]term.Cell {
	c.Resize(width, height)
	w := cell.NewBufferWriter(context.Background(), width, height)
	c.Draw(w)
	return w.RawCells()
}

func inAny(p image.Point, rects []image.Rectangle) bool {
	return slices.ContainsFunc(rects, p.In)
}

// pictureCellW and pictureCellH are the size, in pixels, of the cells of
// the pictures drawn by pictureWallpaper.
const pictureCellW, pictureCellH = 4, 8

// pictureWallpaper draws the wallpaper of blockWallpaper with a picture
// of its arts instead of blocks: every cell of the wallpaper is blank
// braille, under a picture colored fg covering the dots that
// blockWallpaperDots draws for arts.
func pictureWallpaper(
	cols, rows int, fg color.RGBA, arts ...image.Rectangle,
) ([][]term.Cell, term.Image) {
	cells := blockWallpaper(cols, rows, term.ColorDefault, arts...)
	for _, row := range cells {
		for x, c := range row {
			if c.Ch == braille.Block {
				row[x] = term.NewCell(braille.Blank, 1, term.Attributes{Bg: wallpaperBg})
			}
		}
	}
	var bounds image.Rectangle
	for _, art := range arts {
		bounds = bounds.Union(art)
	}
	bounds.Max.X++
	pixels := image.NewRGBA(image.Rect(0, 0, bounds.Dx()*pictureCellW, bounds.Dy()*pictureCellH))
	for py := range pixels.Rect.Dy() {
		for px := range pixels.Rect.Dx() {
			p := bounds.Min.Add(image.Pt(px/pictureCellW, py/pictureCellH))
			if inAny(p, arts) || inAny(p.Sub(image.Pt(1, 0)), arts) && px%pictureCellW < pictureCellW/2 {
				pixels.SetRGBA(px, py, fg)
			}
		}
	}
	return cells, term.Image{
		Src:    pixels,
		ID:     term.NewImageID(),
		Pos:    term.Coordinates{X: bounds.Min.X, Y: bounds.Min.Y},
		Width:  bounds.Dx(),
		Height: bounds.Dy(),
		Fit:    term.ImageFitFill,
	}
}
