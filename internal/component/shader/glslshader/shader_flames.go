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

package glslshader

import (
	"math"
	"math/rand"

	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/component/shader"
	"unstable.build/rune/internal/component/shader/shaderutils"
)

// Flames cross the screen from bottom to top.
func Flames(params FlamesParams, defaultAttr term.Attributes, fps float) shader.Shader {
	// Change normalized parameters to suitable ranges.
	params.Fuel *= 7.0       // from 0..1 to 0..7
	params.JitteryBirth *= 3 // from 0..1 to 0..3
	params.NoiseAmount *= 10 // from 0..1 to 0..10
	params.NoiseSize *= 8    // from 0..1 to 0..8
	params.ClumpHeight *= 10 // from 0..1 to 0..10

	return &flames{
		defaultAttr:  defaultAttr,
		FlamesParams: params,
		flamesOptimization: flamesOptimization{
			prevFrame: flamesUnset,
		},
		fps: fps,
	}
}

// FlamesParams allows you to customize the Flames effect.
//
// Most parameters are in the normalized range 0-1. Exceeding the range is
// allowed in most of the cases (it's specified in the docstring).
type FlamesParams struct {
	// Characters to use for painting the flames (first coolest; last hottest).
	Chars []rune
	// Colors to use for painting the flames (first coolest; last hottest).
	ColorGradient []term.Color
	// Describe how the background opacity of the flame characters evolves
	// throughout the course of the animation.
	BgContour FlamesBgOpacityContour
	// Flames are kickstarted if the given cell has been marked to do allow it.
	// This marking is done progressively on time at the pace specified with
	// this parameter: if too low it might happen the flames struggles to run,
	// if too high the flames will run too fast not looking organic exposing the
	// base shape (defined with the belly and clump parameters).
	// (range 0..1 unrestricted)
	Fuel float // each frame `Fuel` amount of rows will be activated to start fire
	// Amplifies the Fuel results by spacing out the markings that kickstart
	// the course of the flames.
	// (range 0..1 unrestricted)
	JitteryBirth float
	// A half-sine shape is drawn first, before adding the teeth or spikes that
	// give the fire look. This curve can be flatter or more pronounced based
	// on the magnitude of the value you pass.
	//
	// This value is the size of the belly (or dome) in pixels, positive values
	// give concave results and negative convex.
	Belly float
	// This noise also produces variation of how the flames advances in a
	// similar way to JitteryBirth but more stable and smoother, since it uses
	// a continuous noise (spatial-aware) instead of a random one.
	// (range 0..1 unrestricted)
	NoiseAmount float
	// Space resolution of the NoiseAmount: the smaller the larger the blobs.
	// (range 0..1 unrestricted)
	NoiseSize float
	// Number of base flamess (imagine shape of spikes or teeth) along the
	// horizontal axis that raises up "burning" the screen.
	Clumps float
	// How tall  those flamess (spikes or teeth) are.
	// (range 0..1 unrestricted)
	ClumpHeight float
}

// DefaultFlamesParams gives you a set of params that exhibits the Flames
// effect properly. It's a good starting point to customizing on top of with
// parameter overrides.
func DefaultFlamesParams() FlamesParams {
	return FlamesPresetBlobby()
}

// FlamesPresetBlobby makes big blobby blast on the flamess in convex shape.
func FlamesPresetBlobby() FlamesParams {
	return FlamesParams{
		Chars:         defaultFlamesChars(),
		ColorGradient: FlamesColorGradientRed(),
		BgContour: FlamesBgOpacityContour{
			inEdge0: 0.2, inEdge1: 0.4,
			outEdge0: 0.7, outEdge1: 0.9,
		},
		Fuel:         0.7,
		JitteryBirth: 1.0,
		Belly:        -25.0,
		NoiseAmount:  0.8,
		NoiseSize:    0.5,
		Clumps:       3.0,
		ClumpHeight:  0.7,
	}
}

// FlamesColorGradientRed provides natural fire-looking colors to grade the
// flames with.
//
// Aimed to be passed to FlamesParams.ColorGradient.
func FlamesColorGradientRed() []term.Color {
	return []term.Color{
		term.NewRGBColor(255, 255, 255),
		term.NewRGBColor(255, 247, 143),
		term.NewRGBColor(255, 247, 93),
		term.NewRGBColor(254, 101, 13),
		term.NewRGBColor(138, 0, 60),
		term.NewRGBColor(81, 1, 0),
		term.NewRGBColor(255, 20, 0),
	}
}

// FlamesBgOpacityContour defines a smooth soft opacity rise-fall through time:
// >             ____________
// >           -              -
// >          /                \   <- not linear but curvy! (see GLSL smoothstep)
// >         /                  \
// > _____ _-                    - _ _______
// >        A   B           C    D
// >
// > A: inEdge0, B: inEdge1, C: outEdge0, D: outEdge1
type FlamesBgOpacityContour struct {
	// Expressed in percentage 0.0-1.0 of the flame animation:
	// inEdge0 will be transparent up until inEdge1 which will be opaque.
	inEdge0, inEdge1 float
	// Expressed in percentage 0.0-1.0 of the flame animation:
	// outEdge0 will be opaque up until outEdge1 which will be transparent.
	outEdge0, outEdge1 float
}

type flames struct {
	FlamesParams
	defaultAttr term.Attributes
	// Stats used for skipping unnecessary calculations.
	flamesOptimization
	fps float
	// The keys of this map represent each column on the screen. The value is a
	// slice of numbers representing the base offset for the flame animation.
	// This allows to orchestrate the flame so it starts on lower parts on the
	// columns increasing towards the top (the natural ascending nature of
	// fire).
	activations map[int][]int
	// The total frame offset for the flame animation on a particular cell. The
	// activations field carries the base offset but the value in flamesOffsets
	// in addition to the base offset pull and push the offsets to achieve the
	// belly (or dome) shape, the saw teeth for the fire look, and more organic
	// noises, ...
	flamesOffsets [][]float
	// Indicates if the flame has not passed, is passing or has passed per cell.
	flamesCellStatuses [][]flamesCellStatus
}

type flamesOptimization struct {
	prevFrame               int                 // to identify play direction changes
	playingDirection        flamesPlayDirection // unknown, forward or backward
	playingDirectionChanged bool                // detects non-continuous playback
}

func (s *flames) Shade(frame, total int, cells [][]term.Cell) {
	if frame < 0 || frame >= total {
		return
	}

	s.analizePlayingDirection(frame)
	if s.wouldRenderNothing() {
		return
	}
	s.ensureSliceFieldsCap(frame, total, cells)
	s.jitterActivations(frame, total, cells)

	for y := range cells {
		for x := 0; x < len(cells[0]); x++ {
			s.processCell(x, y, frame, total, cells)
		}
	}
}

func (s *flames) processCell(x, y int, frame, total int, cells [][]term.Cell) {
	if y >= len(cells) || x >= len(cells[y]) {
		return
	}

	if !s.playingDirectionChanged &&
		s.playingDirection == flamesDirectionForward &&
		s.flamesCellStatuses[y][x] == flamesPassed {
		return
	}

	w := len(cells[0])
	h := len(cells)

	yy := h - y - 1 // complementary to flip vertical screen coords

	if len(s.activations[x]) <= yy {
		return
	}

	// The flame animation is simply showing the chars in FlamesParams.Chars
	// one after the other. To give the illusion of fire we offset the chars to
	// break the animation into different flames. The offset not only is when
	// the flame starts but also conditions the location within the flame
	// defining the general shape of the effect, that's why we speak in plural:
	var offsets float
	if false && s.flamesOffsets[y][x] != 0.0 {
		offsets = s.flamesOffsets[y][x]
	} else {
		baseOffset := s.activations[x][yy]

		// Offset: Non-spatial random.
		of1 := s.JitteryBirth * float(baseOffset)
		if s.JitteryBirth < 1.0 {
			of1 += (1.0 - s.JitteryBirth) * float(yy)
		}

		// Offset: Sine belly, lateral shows flames first and middle of scren last.
		of2 := s.Belly * sin(
			(math.Pi/2.0)*(float(x)-float(w)/2.0)/(float(w)/2.0)+ // freq
				(math.Pi/2.0), // offset
		)

		// Offset: [triangle waves]: https://thndl.com/triangle-waves.html.
		teethNum := s.Clumps
		teethHeight := s.ClumpHeight
		teeth := abs(-1.0 + 2.0*fract(((teethNum*float(x))/float(w))))
		teeth = 1.0 - (teeth-1.0)*(teeth-1.0) // sharp pointy ([timeshader.FastToSlow] algorithm)
		teeth *= teethHeight                  // from unit-scale to desired length
		of3 := teeth

		// Offset: break down for organic look.
		of4 := s.NoiseAmount * noiseSimplex(
			vec2(float(x)/float(w), float(yy)/float(h)).multSc(s.NoiseSize),
		)

		// Ofset: Combine them all.
		offsets = of1 + of2 + of3 + of4
		s.flamesOffsets[y][x] = offsets
	}

	// Do not bother drawing any flame if it's too early for it.
	offsetFrame := frame - int(math.Round(offsets))
	if offsetFrame < 0 {
		return
	}

	flamesFrame := offsetFrame

	var char rune
	var fg, bg term.Color

	// Perform some adjustement to the speed of the flame, to speed up its
	// animation on shorter shader runs or slow it down on longer ones.
	normalFlamesDur := 3.0
	minAcceptableFlamesDur := 0.1
	percentageFlamesDur := 0.43
	actualFlamesDur := math.Max(
		minAcceptableFlamesDur, float(total)/s.fps*percentageFlamesDur,
	)
	flamesSpeed := normalFlamesDur / actualFlamesDur
	flamesFrame = int(math.Round(flamesSpeed * float64(flamesFrame)))
	flamesAnimHighestIndex := len(s.Chars) - 1

	// Only draw if we are within the bounds of the animation characters.
	if flamesFrame >= 0 && flamesFrame < flamesAnimHighestIndex {
		char = s.Chars[flamesFrame]
		factor := float(flamesFrame) / float(len(s.Chars)-1)

		// Starts at 1.0 and after edge0 it starts decreasing up to edge1,
		// after which the result is 0.0.
		fadingTail := smoothstep(s.BgContour.outEdge1, s.BgContour.outEdge0, factor)

		// If dealing with a character-less cell the flame character should
		// fade into the same color of the background of that character-less
		// cell, but if there's a character we fade into that character's color
		// to prevent color jumps, otherwise you would have your flame
		// characters radically show the original character in another color
		// suddenly.
		if cells[y][x].Ch == rune(0) {
			fg = shaderutils.InterpolateColor(
				fadingTail,
				cells[y][x].Bg,
				s.sampleFlamesGradient(factor),
				s.defaultAttr.Bg,
			)
		} else {
			fg = shaderutils.InterpolateColor(
				fadingTail,
				cells[y][x].Bg,
				s.sampleFlamesGradient(factor),
				s.defaultAttr.Bg,
			)
		}

		// When drawing the flame chars, we paint the background with a slight
		// color offset, so characters that are not filling the entire cell
		// like quadrants ▙, ▚, ... become visually more interesting, that's
		// why we scale the color gradient from [0,1] to [0.2-0.8] (in the case
		// of kBgSampleStart=0.2 and kBgSampleScale=0.6).
		const (
			kBgSampleStart = 0.2
			kBgSampleScale = 0.6
		)
		bg = shaderutils.InterpolateColor(
			// At the beginning of the flame (0%) it will have the original
			// cell background (0.0) and progressively go opaque (1.0) towards
			// the middle (30%-40%) and then stay opaque and fade down again
			// towards the end, something like this:
			//
			//   0% (0) 20% (0) 30% (0.5) 40% (1.0) 50% (1.0)
			//   60% (1.0) 70% (1.0) 80% (0.5) 90% (0.0) 100% (0.0)
			//
			// Multiplying 0-1 ranges gives us logical intersection (an AND &&
			// boolean operation).
			fadingTail*smoothstep(s.BgContour.inEdge0, s.BgContour.inEdge1, factor),
			cells[y][x].Bg,
			// Mid part of the flame will paint some offset colors to make the
			// chars stand out as mentioned above (see docstring above consts).
			s.sampleFlamesGradient(kBgSampleStart+kBgSampleScale*factor),
			s.defaultAttr.Bg,
		)

		// If the flame animation char is not the fullblock (is fading) we can do something
		// more interesting than rendering a full cell. We replace it with the
		// original character it is covering and that character we give it a
		// color from the same gradient but mirroring across the middle, that
		// is, sampling from the end instead of from the start to make it stand
		// out more. And we darken it afterwards to avoid the ending of the
		// animation showing super bright foregrounds, since it should be
		// something ashy and burnt, that's why we dress it with a black fade.
		if char != blockChar {
			char = cells[y][x].Ch
			bg = fg
			const (
				kDarkenStart = 0.35
				kDarkenDone  = 0.6
			)
			const (
				kClearStart = 0.21
				kClearEnd   = 0.38
			)
			fg = shaderutils.InterpolateColor(
				smoothstep(kClearStart, kClearEnd, factor),
				fg,
				vecToCol(colToVec(
					s.sampleFlamesGradient(1.0-factor), s.defaultAttr.Fg).multSc(
					smoothstep(kDarkenDone, kDarkenStart, factor),
				)),
				s.defaultAttr.Fg,
			)
		}
		cells[y][x].Bg = bg
		cells[y][x].Fg = fg
		cells[y][x].Ch = char
		if char != rune(0) {
			cells[y][x].Width = 1
		}
		s.flamesCellStatuses[y][x] = flamesPassing
	} else {
		s.flamesCellStatuses[y][x] = flamesPassed
	}
}

// sampleFlamesGradient returns the blended color at point t (0...1)
// interpolating any color steps, 0 being the start of the gradient and 1 the
// end.
func (s *flames) sampleFlamesGradient(t float) term.Color {
	t = math.Min(1, math.Max(0, t))
	stops := float(len(s.ColorGradient) - 1)
	tt := t * stops

	currIdx := int(math.Floor(tt))
	nextIdx := currIdx + 1
	if nextIdx >= len(s.ColorGradient) {
		nextIdx = len(s.ColorGradient) - 1
	}

	tDec := tt - math.Floor(tt)

	return shaderutils.InterpolateColor(
		tDec,
		s.ColorGradient[currIdx],
		s.ColorGradient[nextIdx],
		s.defaultAttr.Bg,
	)
}

// ensureSliceFieldsCap protects dimensions of slices that represent terminal
// coordinates so if the window is resized doesn't panic.
func (s *flames) ensureSliceFieldsCap(frame, total int, in [][]term.Cell) {
	if len(s.flamesOffsets) < len(in) {
		s.flamesOffsets = append(s.flamesOffsets,
			make([][]float, len(in)-len(s.flamesOffsets))...)
	}
	if len(s.flamesCellStatuses) < len(in) {
		s.flamesCellStatuses = append(s.flamesCellStatuses,
			make([][]flamesCellStatus, len(in)-len(s.flamesCellStatuses))...)
	}
	for i, row := range in {
		if len(s.flamesOffsets[i]) < len(row) {
			s.flamesOffsets[i] = append(s.flamesOffsets[i],
				make([]float, len(row)-len(s.flamesOffsets[i]))...)
		}
		if len(s.flamesCellStatuses[i]) < len(row) {
			s.flamesCellStatuses[i] = append(s.flamesCellStatuses[i],
				make([]flamesCellStatus, len(row)-len(s.flamesCellStatuses[i]))...)
		}
	}
}

// jitterActivations fills in the activations map, check the fire.activations
// field docstring for more.
func (s *flames) jitterActivations(frame, total int, in [][]term.Cell) {
	h := len(in)
	w := len(in[0])

	// NOTE: Performance sacrifice here since we can't garantee we will receive
	// the frames linearly we run the simulation up to the frame we want so
	// this shader can be sampled at any point in time without having to run
	// the previous frames.
	s.activations = make(map[int][]int) // reset
	rng := rand.New(rand.NewSource(0))
	for f := range frame {
		for i := 0; i < int(s.Fuel*float(w)); i++ {
			randCol := rng.Intn(w + 1)
			if len(s.activations[randCol]) <= h {
				s.activations[randCol] = append(
					s.activations[randCol],
					f,
				)
			}
		}
	}
}

func (s *flames) analizePlayingDirection(frame int) {
	defer func() {
		s.prevFrame = frame
	}()

	if s.prevFrame == flamesUnset {
		return
	}

	playingBackwardNow := s.prevFrame > frame

	var directionNow flamesPlayDirection
	if playingBackwardNow {
		directionNow = flamesDirectionBackward
	} else {
		directionNow = flamesDirectionForward
	}

	if s.playingDirection != flamesDirectionUnknown {
		if s.playingDirection != directionNow {
			s.playingDirectionChanged = true
		}
	}

	s.playingDirection = directionNow
}

// wouldRenderNothing tells you if running the shader would produce no changes,
// and can therefore be used to optimize computation and skip unnecessary
// renders.
//
// The conditions  where we can infer nothing would be rendered  are very
// specific at the moment: shader must have played always forward and all
// cells must have seen the flame pass.
func (s *flames) wouldRenderNothing() bool {
	if len(s.flamesCellStatuses) == 0 ||
		s.playingDirectionChanged ||
		s.playingDirection != flamesDirectionForward {
		return false
	}

	// Only if finding one cell has not finished the flame animation it's
	// enough to consider the whole animation unfinished and therefore
	// something would render.
	for _, row := range s.flamesCellStatuses {
		for _, status := range row {
			if s.playingDirection == flamesDirectionForward &&
				status != flamesPassed {
				return false
			}
		}
	}

	return true
}

const blockChar = '█'

func defaultFlamesChars() []rune {
	return []rune{
		'░',
		'░',
		'▒',
		'▒',
		'▒',
		'▓',
		'▓',
		'▓',
		'▓',
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		blockChar,
		'▓',
		'▓',
		'▓',
		'▓',
		'▒',
		'▒',
		'▒',
		'░',
		'░',
	}
}

type flamesPlayDirection int

const (
	flamesDirectionUnknown flamesPlayDirection = iota
	flamesDirectionForward
	flamesDirectionBackward
)

type flamesCellStatus int

const (
	flamesNotPassed flamesCellStatus = iota
	flamesPassing
	flamesPassed
)

// flamesUnset can be used as an alternative when the int zero-value 0 is not
// meaningful for the recognition as uninitialized zero-value.
const flamesUnset = math.MaxInt
