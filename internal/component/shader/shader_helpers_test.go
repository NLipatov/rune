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
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/unstablebuild/rune-go-sdk/term"
	"github.com/unstablebuild/rune-go-sdk/tui"
	"unstable.build/rune/internal/cell"
	"unstable.build/rune/internal/component/braille"
	"unstable.build/rune/internal/component/shader"
	"unstable.build/rune/internal/component/shader/shaderutils"
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
	if in == nil {
		return nil
	}
	out := make([][]term.Cell, len(in))
	for y, row := range in {
		if row == nil {
			continue
		}
		out[y] = make([]term.Cell, len(row))
		copy(out[y], row)
	}
	return out
}

// assertLastFrameMatchesInput shades the last frame of a total-frame
// animation and checks it shows in unchanged.
func assertLastFrameMatchesInput(t *testing.T, sh shader.Shader, in [][]term.Cell, total int) {
	t.Helper()
	last := cloneCells(in)
	sh.Shade(total-1, total, last)
	assert.Equal(t, in, last, "the last frame must match the input")
}

// assertFramesAfterTotalLeaveInput shades frames past the animation and
// checks they leave in untouched.
func assertFramesAfterTotalLeaveInput(t *testing.T, sh shader.Shader, in [][]term.Cell, total int) {
	t.Helper()
	for _, frame := range []int{total, total + 1, total * 2} {
		got := cloneCells(in)
		sh.Shade(frame, total, got)
		assert.Equal(t, in, got, "frame %d of %d must leave the input untouched", frame, total)
	}
}

// countChanged returns how many cells differ between a and b.
func countChanged(a, b [][]term.Cell) int {
	changed := 0
	for y := range a {
		for x := range a[y] {
			if a[y][x] != b[y][x] {
				changed++
			}
		}
	}
	return changed
}

// countChar returns how many cells of in show ch.
func countChar(in [][]term.Cell, ch rune) int {
	n := 0
	for _, row := range in {
		for _, c := range row {
			if c.Ch == ch {
				n++
			}
		}
	}
	return n
}

// highlightCanvasBg is the background of the canvas of makeHighlightedCells.
var highlightCanvasBg = term.ColorBlack

// makeHighlightedCells returns a 24x8 canvas of blank cells on
// highlightCanvasBg with four highlighted lines of text on rows 2 to 5,
// as a status bar or a selected row shows: the line's characters, at
// columns 3 to 12, and the blanks around them, from column 1 to 17,
// each carry a background of their own, unique to the cell.
func makeHighlightedCells() [][]term.Cell {
	out := makeBlankCells(24, 8)
	for y := 2; y < 6; y++ {
		for x := 1; x < 18; x++ {
			ch := ' '
			if x >= 3 && x < 13 {
				ch = rune('a' + x - 3)
			}
			out[y][x] = term.NewCell(ch, 1, term.Attributes{
				Fg: term.ColorWhite,
				Bg: term.NewRGBColor(int32(x+1), int32(y+1), 7),
			})
		}
	}
	return out
}

// assertBackgroundTravels checks that sh carries the backgrounds of
// the cells of makeHighlightedCells along with their content when it
// moves or hides them rather than leaving them behind: in no frame does
// a cell keep the background a character had without showing a
// character, no frame has more painted cells than the input, and, when
// movesBlanks, the painted blanks do not just stay put either.
func assertBackgroundTravels(t *testing.T, sh shader.Shader, total int, movesBlanks bool) {
	t.Helper()
	in := makeHighlightedCells()
	painted := func(cells [][]term.Cell) int {
		n := 0
		for _, row := range cells {
			for _, c := range row {
				if c.Bg != highlightCanvasBg && c.Bg != term.ColorDefault {
					n++
				}
			}
		}
		return n
	}
	blanksMoved := false
	for frame := 0; frame < total; frame += max(1, total/50) {
		out := cloneCells(in)
		sh.Shade(frame, total, out)
		assert.LessOrEqual(t, painted(out), painted(in), "frame %d paints backgrounds of its own", frame)
		for y := range in {
			for x := range in[y] {
				src := in[y][x]
				if src.Bg == highlightCanvasBg {
					continue
				}
				if out[y][x].Bg != src.Bg {
					if src.Ch == ' ' {
						blanksMoved = true
					}
					continue
				}
				if src.Ch != ' ' {
					assert.False(t, out[y][x].Ch == ' ' || out[y][x].Ch == 0 || out[y][x].Width == 0,
						"frame %d leaves the background of %d,%d behind", frame, x, y)
				}
			}
		}
	}
	if movesBlanks {
		assert.True(t, blanksMoved, "the painted blanks never move or hide")
	}
	assertLastFrameMatchesInput(t, sh, in, total)
}

// shadeCopy returns a copy of in shaded by sh at frame of total.
func shadeCopy(sh shader.Shader, frame, total int, in [][]term.Cell) [][]term.Cell {
	out := cloneCells(in)
	sh.Shade(frame, total, out)
	return out
}

// assertSameShape checks got has the rows of want, each as long and
// nil where want's is.
func assertSameShape(t *testing.T, want, got [][]term.Cell) {
	t.Helper()
	if !assert.Equal(t, want == nil, got == nil, "nil canvas") || !assert.Len(t, got, len(want), "rows") {
		return
	}
	for y := range want {
		assert.Equal(t, want[y] == nil, got[y] == nil, "nil row %d", y)
		assert.Len(t, got[y], len(want[y]), "length of row %d", y)
	}
}

// assertWideCharsIntact checks every wide character of cells is
// followed by its continuation cells, where its row has room for them.
func assertWideCharsIntact(t *testing.T, cells [][]term.Cell) {
	t.Helper()
	for y, row := range cells {
		for x, c := range row {
			for i := 1; i < int(c.Width) && x+i < len(row); i++ {
				assert.Zero(t, row[x+i].Width, "continuation %d of the wide character at %d,%d", i, x, y)
			}
		}
	}
}

// edgeWant is what the shaded cells must show at an edgeTiming.
type edgeWant int

const (
	// wantAnything only checks the invariants of every frame.
	wantAnything edgeWant = iota
	// wantInput expects the input untouched.
	wantInput
	// wantFirstFrame expects what frame 0 shows.
	wantFirstFrame
	// wantEnd expects the input, as the last frame of an animation shows
	// it, unless the shader plays on a loop.
	wantEnd
)

// edgeTimings are frames of animations, including out of range frames
// and totals that a buggy shader collection could pass.
var edgeTimings = []struct {
	name         string
	frame, total int
	want         edgeWant
}{
	{"zero total", 0, 0, wantInput},
	{"zero total, negative frame", -1, 0, wantInput},
	{"negative total", 0, -1, wantInput},
	{"negative total, negative frame", -5, -3, wantInput},
	{"minimum total", 0, math.MinInt, wantInput},
	{"frame at total", 10, 10, wantInput},
	{"frame past total", 11, 10, wantInput},
	{"maximum frame", math.MaxInt, 10, wantInput},
	{"maximum frame of maximum total", math.MaxInt, math.MaxInt, wantInput},
	{"single frame", 0, 1, wantEnd},
	{"last of two frames", 1, 2, wantEnd},
	{"last of ten frames", 9, 10, wantEnd},
	{"last of maximum total", math.MaxInt - 1, math.MaxInt, wantEnd},
	{"negative frame", -1, 10, wantFirstFrame},
	{"minimum frame", math.MinInt, 10, wantFirstFrame},
	{"negative frame of single frame", -1, 1, wantFirstFrame},
	{"negative frame of maximum total", -1, math.MaxInt, wantFirstFrame},
	{"first of two frames", 0, 2, wantAnything},
	{"middle of three frames", 1, 3, wantAnything},
	{"early frame", 2, 10, wantAnything},
	{"late frame", 7, 10, wantAnything},
	{"first of maximum total", 0, math.MaxInt, wantAnything},
	{"middle of maximum total", math.MaxInt / 2, math.MaxInt, wantAnything},
	{"second to last of maximum total", math.MaxInt - 2, math.MaxInt, wantAnything},
}

// edgeText are the attributes of the text of the edge canvases.
var edgeText = term.Attributes{Fg: term.NewRGBColor(200, 200, 200), Bg: term.ColorBlack}

func edgeChar(ch rune) term.Cell { return term.NewCell(ch, 1, edgeText) }

func edgePainted(ch rune) term.Cell {
	return term.NewCell(ch, 1, term.Attributes{Fg: edgeText.Fg, Bg: term.NewRGBColor(40, 40, 60)})
}

func edgeGap() term.Cell { return term.NewCell(0, 0, edgeText) }

func edgeRow(s string) []term.Cell {
	row := make([]term.Cell, 0, len(s))
	for _, ch := range s {
		row = append(row, edgeChar(ch))
	}
	return row
}

// edgeWide returns a wide character and its continuation cell.
func edgeWide(ch rune) []term.Cell {
	return []term.Cell{term.NewCell(ch, 2, edgeText), {Bg: edgeText.Bg}}
}

// edgeCanvases are degenerate and unusual inputs for shaders.
var edgeCanvases = []struct {
	name  string
	cells func() [][]term.Cell
}{
	{"nil", func() [][]term.Cell { return nil }},
	{"no rows", func() [][]term.Cell { return [][]term.Cell{} }},
	{"one empty row", func() [][]term.Cell { return [][]term.Cell{{}} }},
	{"empty and nil rows", func() [][]term.Cell { return [][]term.Cell{{}, nil, {}} }},
	{"nil row between rows", func() [][]term.Cell {
		return [][]term.Cell{edgeRow("ab"), nil, edgeRow("cd")}
	}},
	{"1x1 character", func() [][]term.Cell { return [][]term.Cell{{edgeChar('a')}} }},
	{"1x1 blank", func() [][]term.Cell { return [][]term.Cell{{edgeChar(' ')}} }},
	{"1x1 painted blank", func() [][]term.Cell { return [][]term.Cell{{edgePainted(' ')}} }},
	{"1x1 gap", func() [][]term.Cell { return [][]term.Cell{{edgeGap()}} }},
	{"1x1 default colors", func() [][]term.Cell {
		return [][]term.Cell{{term.NewCell('a', 1, term.Attributes{})}}
	}},
	{"1x1 wide character", func() [][]term.Cell {
		return [][]term.Cell{{term.NewCell('世', 2, edgeText)}}
	}},
	{"wide characters", func() [][]term.Cell {
		row := append(edgeWide('世'), edgeChar('a'))
		row = append(row, edgeWide('界')...)
		return [][]term.Cell{row, append(edgeRow("b"), edgeWide('日')...)}
	}},
	{"single row", func() [][]term.Cell { return [][]term.Cell{edgeRow("ab c  d e")} }},
	{"single column", func() [][]term.Cell {
		return [][]term.Cell{
			{edgeChar('a')}, {edgeChar('b')}, {edgeChar(' ')}, {edgePainted('c')}, {edgeChar('d')},
		}
	}},
	{"ragged rows", func() [][]term.Cell {
		return [][]term.Cell{edgeRow("abc"), edgeRow("a"), {}, edgeRow("abcdefg"), edgeRow("  x")}
	}},
	{"only blanks", func() [][]term.Cell { return makeBlankCells(6, 3) }},
	{"only gaps", func() [][]term.Cell {
		return [][]term.Cell{{edgeGap(), edgeGap(), edgeGap()}, {edgeGap(), edgeGap(), edgeGap()}}
	}},
	{"dense", func() [][]term.Cell { return makeCharCells(12, 5) }},
	{"highlighted rows", makeHighlightedCells},
	{"mixed", func() [][]term.Cell {
		italic := term.NewCell('i', 1, term.Attributes{Fg: edgeText.Fg, Bg: edgeText.Bg, Attrs: term.AttrItalic})
		return [][]term.Cell{
			edgeRow("  Rune  "),
			{edgePainted(' '), edgePainted('b'), edgePainted('a'), edgePainted('r'), edgePainted(' '), edgeGap(), italic, edgeChar(' ')},
			append(append(edgeRow("x"), edgeWide('世')...), edgeGap(), edgeGap(), term.NewCell('d', 1, term.Attributes{}), edgeChar(' '), edgeChar('z')),
		}
	}},
	{"unprintable runes", func() [][]term.Cell {
		return [][]term.Cell{{edgeChar('\n'), edgeChar(0x7f), edgeChar(-1), edgeChar(0x110000), edgeChar(0xd800)}}
	}},
}

// testShaderEdgeCases shades every edge canvas at every edge timing with
// the shaders newShader makes for default attributes left unset and
// set, checking every frame keeps the shape of the canvas and its wide
// characters intact and is the same when shaded again, that frames out
// of the animation leave the input untouched, that frames before it
// show the first frame, and that the last frame shows the input.
func testShaderEdgeCases(t *testing.T, newShader func(defaultAttr term.Attributes) shader.Shader) {
	testEdgeCases(t, newShader, false)
}

// testLoopEdgeCases is testShaderEdgeCases for the shaders that play on
// a loop, whose last frame leads back into their first rather than
// showing the input.
func testLoopEdgeCases(t *testing.T, newShader func(defaultAttr term.Attributes) shader.Shader) {
	testEdgeCases(t, newShader, true)
}

func testEdgeCases(t *testing.T, newShader func(defaultAttr term.Attributes) shader.Shader, loops bool) {
	attrs := []struct {
		name string
		attr term.Attributes
	}{
		{"unset default attributes", term.Attributes{}},
		{"set default attributes", edgeText},
	}
	for _, a := range attrs {
		sh := newShader(a.attr)
		for _, c := range edgeCanvases {
			for _, tm := range edgeTimings {
				t.Run(a.name+"/"+c.name+"/"+tm.name, func(t *testing.T) {
					in := c.cells()
					got := shadeCopy(sh, tm.frame, tm.total, in)
					assertSameShape(t, in, got)
					assertWideCharsIntact(t, got)
					assert.Equal(t, got, shadeCopy(sh, tm.frame, tm.total, in), "shading again")
					switch tm.want {
					case wantInput:
						assert.Equal(t, in, got)
					case wantEnd:
						if !loops {
							assert.Equal(t, in, got)
						}
					case wantFirstFrame:
						assert.Equal(t, shadeCopy(sh, 0, tm.total, in), got, "the first frame")
					}
				})
			}
		}
	}
}

// paramsCase is a case of a table of params of a shader: params shade
// the same as sameAs when sameAs is set, and are rejected otherwise.
type paramsCase[P any] struct {
	name   string
	params P
	sameAs *P
}

// testParams runs a table of params of the shader newShader makes.
func testParams[P any](t *testing.T, newShader func(P, term.Attributes) shader.Shader, tcases []paramsCase[P]) {
	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sameAs == nil {
				assertInvalidParams(t, func() { newShader(tc.params, edgeText) })
				return
			}
			assertSameShading(t, newShader(*tc.sameAs, edgeText), newShader(tc.params, edgeText))
		})
	}
}

// with returns a copy of p changed by change.
func with[P any](p P, change func(*P)) P {
	change(&p)
	return p
}

// same returns a pointer to p, for paramsCase.sameAs.
func same[P any](p P) *P { return &p }

// accepted returns a case of valid params at the edge of their range.
func accepted[P any](name string, p P) paramsCase[P] {
	return paramsCase[P]{name: name, params: p, sameAs: &p}
}

// invalidFloats returns rejected cases setting a float field of base to
// NaN, either infinity, and each of out.
func invalidFloats[P any](base P, field string, set func(*P, float64), out ...float64) []paramsCase[P] {
	values := append([]float64{math.NaN(), math.Inf(1), math.Inf(-1)}, out...)
	tcases := make([]paramsCase[P], 0, len(values))
	for _, v := range values {
		tcases = append(tcases, paramsCase[P]{
			name:   fmt.Sprintf("%s %v", field, v),
			params: with(base, func(p *P) { set(p, v) }),
		})
	}
	return tcases
}

// invalidSymbols returns rejected cases setting a symbols field of base
// to symbols that do not draw as a single cell.
func invalidSymbols[P any](base P, field string, set func(*P, []rune)) []paramsCase[P] {
	symbols := []rune{0, '\n', 0x7f, '世', 0x301, -1, 0x110000, 0xd800}
	tcases := make([]paramsCase[P], 0, len(symbols))
	for _, ch := range symbols {
		tcases = append(tcases, paramsCase[P]{
			name:   fmt.Sprintf("%s with %U", field, ch),
			params: with(base, func(p *P) { set(p, []rune{'a', ch}) }),
		})
	}
	return tcases
}

// brokenEasings break the shaderutils.Easing contract.
var brokenEasings = []struct {
	name   string
	easing shaderutils.Easing
}{
	{"NaN", func(float64) float64 { return math.NaN() }},
	{"infinite midway", func(t float64) float64 {
		if t == 0.5 {
			return math.Inf(1)
		}
		return t
	}},
	{"not starting at 0", func(t float64) float64 { return 0.5 + t/2 }},
	{"not ending at 1", func(t float64) float64 { return t / 2 }},
	{"overshooting wildly", func(t float64) float64 { return t + 100*t*(1-t) }},
}

// invalidEasings returns rejected cases setting an easing field of base
// to each of brokenEasings.
func invalidEasings[P any](base P, field string, set func(*P, shaderutils.Easing)) []paramsCase[P] {
	tcases := make([]paramsCase[P], 0, len(brokenEasings))
	for _, e := range brokenEasings {
		tcases = append(tcases, paramsCase[P]{
			name:   field + " " + e.name,
			params: with(base, func(p *P) { set(p, e.easing) }),
		})
	}
	return tcases
}

// assertInvalidParams checks build panics with an error wrapping
// shader.ErrInvalidParams.
func assertInvalidParams(t *testing.T, build func()) {
	t.Helper()
	defer func() {
		err, ok := recover().(error)
		assert.True(t, ok && errors.Is(err, shader.ErrInvalidParams),
			"want a panic wrapping shader.ErrInvalidParams, got %v", err)
	}()
	build()
}

// assertSameShading checks want and got shade every frame of an
// animation of a varied canvas the same.
func assertSameShading(t *testing.T, want, got shader.Shader) {
	t.Helper()
	in := makeHighlightedCells()
	in = append(in, edgeCanvases[len(edgeCanvases)-2].cells()...)
	const total = 40
	for frame := -1; frame <= total; frame++ {
		assert.Equal(t, shadeCopy(want, frame, total, in), shadeCopy(got, frame, total, in), "frame %d", frame)
	}
}

// fuzzCanvas decodes data into a canvas of up to 8x12 cells: data's
// first two bytes pick its size and the rest, cycled, its rows, which
// can be nil or short, and its cells, which can be characters, blanks,
// painted blanks, gaps, wide characters with or without room for their
// continuation, cells in default or arbitrary colors and unprintable
// runes.
func fuzzCanvas(data []byte) [][]term.Cell {
	if len(data) < 2 {
		return nil
	}
	rows, cols := int(data[0]%9), int(data[1]%13)
	body := data[2:]
	i := 0
	next := func() byte {
		if len(body) == 0 {
			return 0
		}
		b := body[i%len(body)]
		i++
		return b
	}
	out := make([][]term.Cell, rows)
	for y := range out {
		b := next()
		if b%17 == 0 {
			continue
		}
		n := cols
		if b%5 == 0 {
			n = int(b/5) % (cols + 1)
		}
		row := make([]term.Cell, n)
		for x := 0; x < n; x++ {
			kind, c := next(), int32(next())
			attr := term.Attributes{Fg: term.NewRGBColor(c, c*3, c*7), Bg: term.ColorBlack}
			switch kind % 9 {
			case 0:
				row[x] = term.NewCell(' ', 1, attr)
			case 1:
				attr.Bg = term.NewRGBColor(c*5, c, c*2)
				row[x] = term.NewCell(rune(" ab"[c%3]), 1, attr)
			case 2:
				row[x] = term.NewCell(0, 0, attr)
			case 3:
				row[x] = term.NewCell('世', 2, attr)
				if x+1 < n {
					x++
					row[x] = term.Cell{Bg: attr.Bg}
				}
			case 4:
				row[x] = term.NewCell('d', 1, term.Attributes{})
			case 5:
				row[x] = term.NewCell([]rune{'\n', 0x7f, -1, 0x110000, 0x301}[c%5], 1, attr)
			case 6:
				attr.Fg, attr.Bg = term.Color(uint32(c)<<20|uint32(kind)), term.Color(uint32(c)<<24)
				row[x] = term.NewCell('r', 1, attr)
			default:
				row[x] = term.NewCell('a'+rune(kind%26), 1, attr)
			}
		}
		out[y] = row
	}
	return out
}

// fuzzBuild returns the shader build makes, and false when build
// rejects its params, panicking with an error wrapping
// shader.ErrInvalidParams. Any other panic fails the test.
func fuzzBuild(t *testing.T, build func() shader.Shader) (sh shader.Shader, ok bool) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if err, isErr := r.(error); isErr && errors.Is(err, shader.ErrInvalidParams) {
			sh, ok = nil, false
			return
		}
		panic(r)
	}()
	return build(), true
}

// fuzzShade shades the canvas data decodes at frame of total with sh,
// checking the frame keeps the shape of the canvas and its wide
// characters intact and is the same when shaded again, that it leaves
// the input untouched when out of the animation, that a frame before
// the animation shows the first one, and that the last frame shows the
// input.
func fuzzShade(t *testing.T, sh shader.Shader, data []byte, frame, total int) {
	t.Helper()
	fuzzShadeFrames(t, sh, data, frame, total, false)
}

// fuzzLoopShade is fuzzShade for the shaders that play on a loop, whose
// last frame leads back into their first rather than showing the input.
func fuzzLoopShade(t *testing.T, sh shader.Shader, data []byte, frame, total int) {
	t.Helper()
	fuzzShadeFrames(t, sh, data, frame, total, true)
}

func fuzzShadeFrames(t *testing.T, sh shader.Shader, data []byte, frame, total int, loops bool) {
	t.Helper()
	in := fuzzCanvas(data)
	got := shadeCopy(sh, frame, total, in)
	assertSameShape(t, in, got)
	assertWideCharsIntact(t, got)
	assert.Equal(t, got, shadeCopy(sh, frame, total, in), "shading again")
	if total <= 0 || frame >= total {
		assert.Equal(t, in, got, "out of the animation")
		return
	}
	if !loops {
		assert.Equal(t, in, shadeCopy(sh, total-1, total, in), "the last frame")
	}
	assert.Equal(t, shadeCopy(sh, 0, total, in), shadeCopy(sh, min(frame, -1), total, in), "a frame before the first")
}

// fuzzEasings are the easings fuzzing picks from: none, standing for the
// default, valid ones and broken ones the shaders must reject.
var fuzzEasings = []shaderutils.Easing{
	nil, shaderutils.EaseLinear, shaderutils.EaseInOutBack, shaderutils.EaseOutBounce,
	shaderutils.EaseInExpo, shaderutils.EaseOutExpo, shaderutils.EaseInCirc,
	brokenEasings[0].easing, brokenEasings[2].easing, brokenEasings[4].easing,
}

func fuzzEasing(i uint8) shaderutils.Easing { return fuzzEasings[int(i)%len(fuzzEasings)] }

// fuzzColors decodes up to 8 colors of arbitrary bits from data, nil
// when it has none.
func fuzzColors(data []byte) []term.Color {
	var out []term.Color
	for i := 0; i+4 <= len(data) && len(out) < 8; i += 4 {
		out = append(out, term.Color(binary.LittleEndian.Uint32(data[i:])))
	}
	return out
}

// fuzzRunes decodes up to 16 symbols from s, nil when it has none.
func fuzzRunes(s string) []rune {
	out := []rune(s)
	if len(out) == 0 {
		return nil
	}
	return out[:min(len(out), 16)]
}

// fuzzSeedCanvas is a canvas for the seed corpora of the fuzz tests:
// 6 rows of 12 cells of every kind fuzzCanvas decodes.
var fuzzSeedCanvas = []byte{6, 12, 1, 7, 9, 0, 3, 1, 5, 2, 30, 3, 8, 4, 2, 5, 1, 6, 4, 7, 7, 8, 2, 25, 11}

// fuzzSeedTimings are frames and totals for the seed corpora of the
// fuzz tests.
var fuzzSeedTimings = [][2]int{
	{0, 10}, {5, 10}, {9, 10}, {-1, 10}, {10, 10}, {0, 1}, {0, 0}, {3, -4},
	{math.MinInt, math.MaxInt}, {math.MaxInt / 3, math.MaxInt},
}

// fuzzAttrs decodes the default attributes of a shader from v: its
// foreground from the low 32 bits and its background from the high ones.
func fuzzAttrs(v uint64) term.Attributes {
	return term.Attributes{Fg: term.Color(uint32(v)), Bg: term.Color(v >> 32)}
}

// fuzzSeedAttrs are default attributes for the seed corpora of the fuzz
// tests, as fuzzAttrs decodes them.
var fuzzSeedAttrs = []uint64{0, uint64(term.ColorWhite) | uint64(term.ColorBlack)<<32}

// fuzzColorSeed encodes colors as fuzzColors decodes them.
func fuzzColorSeed(colors []term.Color) []byte {
	out := make([]byte, 0, 4*len(colors))
	for _, c := range colors {
		out = binary.LittleEndian.AppendUint32(out, uint32(c))
	}
	return out
}

// fuzzSeed adds params, the arguments a fuzz test takes after the
// canvas, the frame, the total and the default attributes, to the seed
// corpus of f: once for each of fuzzSeedTimings on fuzzSeedCanvas and
// on a 1x1 canvas, under each of fuzzSeedAttrs in turn.
func fuzzSeed(f *testing.F, params ...any) {
	for i, tm := range fuzzSeedTimings {
		for j, canvas := range [][]byte{fuzzSeedCanvas, {1, 1, 7, 7}} {
			attrs := fuzzSeedAttrs[(i+j)%len(fuzzSeedAttrs)]
			f.Add(append([]any{canvas, tm[0], tm[1], attrs}, params...)...)
		}
	}
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
