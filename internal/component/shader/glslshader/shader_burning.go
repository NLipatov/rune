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
	_ "embed"
	"fmt"
	"image"
	"math"
	"strconv"
	"time"
	"unicode"

	log "github.com/sirupsen/logrus"
	"github.com/unstablebuild/rune-go-sdk/component"
	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/cell"
	"unstable.build/rune/internal/component/asciiart"
	"unstable.build/rune/internal/component/shader"
	"unstable.build/rune/internal/component/shader/shaderutils"
)

// Burning shows fire building the given logo as flames touch it.
//
// As flame heat caresses the logo it ignites it and dissipates after some
// configurable time. Brighter parts of the logo will take longer to dissipate
// heat.
//
// Then flames start fading out and a cyclic ember ripples form, moving from
// darker parts of the logo to brighter ones (logo gradient) sampling from a
// configurable color gradient, conveying the idea it is radiating out the heat
// it has absorbed.
//
// To generate what we call the "logo gradient", which is a numerical value
// denoting the brightness of the logo cell, we use the [asciiart.Encode] using
// numbers as density characters.
//
// In order to perform effects on the wallpaper asciiart image we use the
// following contract: The wallpaper will have its background filled with
// special invisible characters. That way we know the bounds of the wallpaper
// image (by reading their unicode value in the cell matrix). Another asciiart
// image is recreated (logo gradient) and stretched over the same boundaries
// effectively covering the original. That way we can also mask out the origial
// logo and paint it out to produce the illusion the logo has disappeared.
//
// Designed taking https://www.shadertoy.com/view/XsXSWS as reference.
//
// NOTE: For maintenance of the flames algoritm it's better to work with the
// KodeLife (GLSL IDE) file that allows editing GLSL in real time, and many
// knobs in BurningParams are exposed in there too, the file is the
// company's Google Drive (engineering/Shaders). You can use KodeLife for free
// and tweak the knobs to explore the parameter space.
func Burning(
	params BurningParams,
	defaultAttr term.Attributes,
	duration time.Duration,
	fps float,
) shader.Shader {
	return &burning{
		helper:        newHelper(),
		BurningParams: params,
		defaultAttr:   defaultAttr,
		duration:      duration,
		fps:           fps,
	}
}

// BurningParams allows you to customize the Burning effect.
//
// Most parameters are in the normalized range 0-1. Exceeding the range is
// allowed in most of the cases (it's specified in the docstring).
type BurningParams struct {
	// SwitchLightsOff produces a very high contrast result that does not
	// gently blend the backgrounds showing interesting visual artifacts that
	// ressemble comets at night. It produces a more immersive sensation since
	// the entire background is covered like night fell with comets.
	SwitchLightsOff bool
	// FlamesSpeed controls how fast the flames move.
	//
	// (range 0..1 unrestricted)
	FlameSpeed float
	// FlameTimeOffset offsets the flame's time sample this amount of seconds.
	//
	// Useful to produce different samples of similar features. Or when you see
	// an interesting flame lick you want to move backward or forward.
	//
	// (value in seconds)
	FlameTimeOffset float
	// Squash controls how tall the flames are (lower values for taller flames).
	//
	// (range 0..1 unrestricted)
	Squash float
	// Laterals reveals fire columns on the laterals (higher values for more
	// centered flames). Complements BurningParams.Wide.
	//
	// (range 0..1 unrestricted)
	Laterals float
	// Fiery controls how harsh and clumpy the flames get. This also has an
	// effect on the noise size, so increasing gives you less unified smaller
	// flames on the middle, but a more fiery look.
	//
	// (range 0..1 unrestricted)
	Fiery float
	// HeatGain gives a more blinding hot effect by giving more burnt white-ish
	// tones, but it will get you less warmer colors from the palette.
	//
	// (range 0..1 unrestricted)
	HeatGain float
	// Intensity controls the presence of the flames at each moment throughout
	// the progress of the animation (first item of slice for progress=0%; last
	// is progress=100%). This is also height-aware, so turning this from 0 to
	// small values to higher values will start revealing flames from the
	// bottom upwards.
	//
	// (range 0..1 unrestricted)
	Intensity []float
	// Wide controls how wide is the middle column of fire in the middle
	// expressed as an animated value throughout the progress of the animation
	// (first item of slice for progress=0%; last is progress=100%). Very
	// similar to Laterals but this one gets more organic results when tweaking
	// it, and both could complement each other.
	//
	// Complements BurningParams.Laterals.
	//
	// (range 0..1 unrestricted)
	Wide []float
	// When reached this point flames start to disappear, and the logo cyclic
	// ember ripples start. This is a general fade out of the entire effect.
	//
	// (range 0..1 clamped)
	StartFadeOut float
	// Controls aspect of the superimposed logo on top of the fire.
	Logo LogoParams
	// HideLogo won't show the logo overlay and effects on it.
	//
	// See [BurningPresetGentleOnlyFlames].
	HideLogo bool
	// HideBackgroundFlames won't show the background flames.
	//
	// See [BurningPresetGentleOnlyLogo].
	HideBackgroundFlames bool
	// Controls the colors aesthetics of the effect.
	Colors BurningColors
	// When enabled it will show the screen and wallpaper logo bounds.
	DebugWallpaperBounds bool
}

// LogoParams allows you to customize the logo-related components of the
// Burning effect.
//
// Aimed to be passed to BurningParams.Logo.
//
// Most parameters are in the normalized range 0-1. Exceeding the range is
// allowed in most of the cases (it's specified in the docstring).
type LogoParams struct {
	// Image (decoded) to use as logo.
	//
	// Usually the caller would prepare use [png.Decode] and feed here its output.
	Image image.Image
	// FrameCharSet is used to detect a prompt in front of the shader.
	FrameCharSet component.FrameCharSet
	// WallpaperInvisibleChar specifies which character is used to analize the
	// boundaries of the wallpaper. Check the documentation on
	// [Burning] for details on why it's done like that.
	WallpaperInvisibleChar rune
	// HeatBrushFlow controls how strong is the affect of heat on painting the
	// logo. The "brush flow" takes the name from the homonymous tool in
	// Photoshop.
	//
	// (range 0..1 unrestricted)
	HeatBrushFlow float
	// HeatDissipation controls how fast the heat-tainted parts start clearing
	// up again.
	// (range 0..1 unrestricted)
	HeatDissipation float
	// Lower gradient values will dissipate the heat slower than higher values.
	// This parameter scales how pronounced is the aforementioned phenomena.
	//
	// NOTE: This effect is so subtle, consider going without it.
	//
	// (range 0..1 unrestricted)
	HeatDissipationGradientAware float
	// Lower temperatures than this one will be excluded from the effect of
	// energy remaining being dissipated.
	//
	// (range 0..1 unrestricted)
	HeatDissipationCutOff float
	// Ripples controls the development of the ember ripples described in
	// [Burning].
	Ripples LogoRipplesParams
}

// LogoRipplesParams controls the development of the ember ripples described in
// [Burning].
//
// Aimed to be passed to BurningParams.Logo.Ripples.
//
// Most parameters are in the normalized range 0-1. Exceeding the range is
// allowed in most of the cases (it's specified in the docstring).
type LogoRipplesParams struct {
	// StartFadeInAt and EndFadeInAt control the moment in percentage of the
	// timeline to start showing the ember ripples (StartFadeInAt) and when the
	// effect is fully shown (EndFadeInAt).
	//
	// (range: 0..1 clamped)
	StartFadeInAt, EndFadeInAt float
	// Detail controls how much of the color gradient to show at once. When
	// higher the more colors from
	// BurningColors.RipplesGradient(Start|End) you will see in a
	// single frame.
	//
	// (range 0..1 unrestricted)
	Detail float
	// Speed controls how fast those ripples travel at through the logo.
	//
	// (range 0..1 unrestricted)
	Speed float
}

// BurningColors defines the color-related parameters of the
// Burning effect.
type BurningColors struct {
	// HeatColorGradientOverride is optional, and when passed, instead of
	// painting natural-looking red values (or blue tones, depending on
	// SwapRedBlue value), when flames touch the logo will be painted with
	// colors of this gradient. The end part of the gradient is the hottest.
	HeatColorGradientOverride []term.Color
	// SwapRedBlue, when no HeatColorGradientOverride is passed, changes the
	// red and blue channels producing blue flames.
	SwapRedBlue bool
	// This color sequence will be painted from Logo.Ripples.StartFadeInAt
	// onwards respecting the gradient values of the logo. Through time it will
	// move from interpolate the gradient values from RipplesGradientStart
	// towards RipplesGradientEnd so the whole gradient to sample evolves.
	RipplesGradientStart, RipplesGradientEnd []term.Color
}

type burning struct {
	helper *glslHelper
	BurningParams
	defaultAttr term.Attributes
	// Controls if [burning.initialize] has run. Read its docstring.
	initialized bool
	// If true the shader early returns and leaves terminal matrix untouched.
	skipRenders bool
	// Total duration the shader will run.
	duration time.Duration
	// Framerate the shader is running at.
	fps float
	// Number of row and columns of the current matrix that's being processed.
	rows, cols int
	// Configuration for decoding the logo into "logo gradient".
	//
	// See [Burning] documentation to know what is the "logo gradient".
	asciiartConfig asciiart.Config
	// Stores the gradient of the passed in LogoParams.Image (or defaultLogoImage).
	//
	// See [Burning] documentation to know what is the "logo gradient".
	imgBuf *cell.Buffer
	// A matrix of numeric chars representing the "logo gradient".
	//
	// See [Burning] documentation to know what is the "logo gradient".
	logo [][]rune
	// The greatest value of the parsed "logo gradient".
	//
	// See [Burning] documentation to know what is the "logo gradient".
	logoMaxGradientValue int
	// Stores when and what was the highest temperature value at each cell coord.
	heatRecords [][]heatRecord
	// Stores wallpaper details after it has been detected.
	wallpaperBounds wallpaperBounds
	// marker is a wallpaper invisible-char cell near the screen center,
	// captured once the bounds are found and re-read in O(1) every frame
	// to detect when an overlay (e.g. the centered command prompt) is
	// drawn over the wallpaper so the shader can stop occluding it. It is
	// valid only for the matrix shape it was captured in.
	markerSet              bool
	markerX, markerY       int
	markerRows, markerCols int
}

// DefaultBurningParams gives you a set of params that exhibits the
// Burning effect properly. It's a good starting point to customizing
// on top of with parameter overrides.
//
// Must receive the same wallpaper image that's being used as wallpaper.
func DefaultBurningParams(
	wallpaperImage image.Image, fc component.FrameCharSet,
) BurningParams {
	return BurningPresetGentle(wallpaperImage, true, fc)
}

// BurningPresetGentle shows rapid fire in large clumps that lick the logo
// leaving hard heat imprints (unless 'wallpaperImage' is 'nil').
//
// When lights are switched off ('switchLightsOff') visual artifacts that look
// like comets appear and cover the original background creating a darker
// scene.
func BurningPresetGentle(
	wallpaperImage image.Image, switchLightsOff bool,
	frameCharSet component.FrameCharSet,
) BurningParams {
	params := BurningParams{
		SwitchLightsOff: switchLightsOff,
		FlameSpeed:      0.9461,
		FlameTimeOffset: 1803.2,
		Squash:          0.76,
		Laterals:        0.32,
		Fiery:           0.375,
		HeatGain:        0.212,
		Wide:            []float{1.0, 1.0, 1.0, 0.7, 1.0, 0.0},
		Intensity:       []float{0.2, 0.8, 1.0, 1.0, 0.8, 0.0},
		StartFadeOut:    0.7,
		Colors: BurningColors{
			RipplesGradientStart: []term.Color{
				// Natural fire colors (warm).
				term.NewRGBColor(216, 44, 12),
				term.NewRGBColor(235, 61, 14),
				term.NewRGBColor(255, 156, 73),
				term.NewRGBColor(255, 203, 104), // midpoint
				term.NewRGBColor(255, 156, 73),
				term.NewRGBColor(235, 61, 14),
				term.NewRGBColor(216, 44, 12),
			},
			RipplesGradientEnd: []term.Color{
				// Natural fire colors (warm).
				term.NewRGBColor(255, 67, 9),
				term.NewRGBColor(255, 187, 19),
				term.NewRGBColor(255, 255, 169),
				term.NewRGBColor(255, 255, 255), // midpoint
				term.NewRGBColor(255, 255, 169),
				term.NewRGBColor(255, 187, 19),
				term.NewRGBColor(255, 67, 9),
			},
		},
		// DebugWallpaperBounds: true,
	}
	if wallpaperImage != nil {
		params.Logo = LogoParams{
			Image:                        wallpaperImage,
			FrameCharSet:                 frameCharSet,
			WallpaperInvisibleChar:       '\u2009',
			HeatBrushFlow:                1.22,
			HeatDissipation:              1.3,
			HeatDissipationGradientAware: 0.9,
			HeatDissipationCutOff:        0.3,
			Ripples: LogoRipplesParams{
				StartFadeInAt: 0.686,
				EndFadeInAt:   0.76,
				Detail:        0.4,
				Speed:         0.8421,
			},
		}
	}

	// With lights off increasing heat effects look nicer.
	if switchLightsOff {
		params.Logo.HeatBrushFlow = 4.9
		params.Logo.HeatDissipation = 1.3
	}

	return params
}

// BurningPresetGentleOnlyLogo will hide the flames in the background and
// only will show them contained within the logo. This will mask the original
// wallpaper too.
func BurningPresetGentleOnlyLogo(
	wallpaperImage image.Image, fc component.FrameCharSet,
) BurningParams {
	params := BurningPresetGentle(wallpaperImage, false, fc)
	params.HideLogo = false
	params.HideBackgroundFlames = true
	return params
}

func (s *burning) Shade(frame, total int, in [][]term.Cell) {
	if s.skipRenders || frame < 0 || frame >= total {
		return
	}
	if len(in) == 0 || len(in[0]) == 0 {
		return
	}

	if !s.HideLogo && s.markerSet && !s.markerStillPresent(in) {
		s.skipRenders = true
		return
	}

	if !s.initialized {
		s.initialize()
	}

	s.rows = len(in)
	s.cols = len(in[0])

	if !s.HideLogo {
		s.processLogoFromWallpaper(in)
		s.ensureHeatRecordsCap(in)
	}

	// Parallelizes calls to [burning.runCell]
	s.helper.shadeGLSL(frame, total, s.fps, in, s)
}

// initialize configures logo gradient decoding, sanitizes parameters and
// remaps its values to useful ranges.
func (s *burning) initialize() {
	// STEP 1: Configure logo decoding.
	if !s.HideLogo {
		s.asciiartConfig = asciiart.DefaultConfig()
		invisibleChar := string(s.Logo.WallpaperInvisibleChar)
		if s.DebugWallpaperBounds {
			invisibleChar = "x"
		}

		// NOTE: IMPORTANT: the number of digits must align with the number of
		// density characters from the actual wallpaper. Example: if the
		// wallpaper has density chars "\u2009▓▓▓▓▓▓▓▓▓" then count the
		// positions after the special unicode: 123456789, or for " ░▒▓" it
		// would be " 0123".
		s.asciiartConfig.DensityCharacters = invisibleChar + "123456789"

		s.imgBuf = new(cell.Buffer)
		s.imgBuf.InitPerformance(s.rows, s.cols, ' ')
		if !s.HideLogo && s.Logo.Image == nil {
			panic("Logo.Image must be passed in BurningParams unless you s.HideLogo")
		}
	}

	// STEP 2: Sanitize parameters that need to be 0..1 by clamping.
	s.StartFadeOut = clamp(s.StartFadeOut, 0.0, 1.0)
	if !s.HideLogo {
		s.Logo.Ripples.StartFadeInAt = clamp(
			s.Logo.Ripples.StartFadeInAt, 0.0, 1.0,
		)
		s.Logo.Ripples.EndFadeInAt = clamp(
			s.Logo.Ripples.EndFadeInAt, 0.0, 1.0,
		)
		if s.Logo.Ripples.StartFadeInAt >= s.Logo.Ripples.EndFadeInAt {
			panic("LogoParams.Ripples's StartFadeInAt must come before StartEnd")
		}
	}

	// STEP 3: Map normalized ranges into useful ones.
	// from 0..1 to 0..5
	s.FlameSpeed *= 5.0
	// from 0..1 to 0..10
	s.Squash *= 3.0
	// from 0..1 to 0..3
	s.Laterals *= 3.0
	// from 0..1 to 0..4.5
	s.Fiery *= 4.5
	// from 0..1 to 0..0.8
	s.HeatGain *= 0.8
	// from 0..1 to 0..0.046
	s.Logo.HeatBrushFlow *= 0.046
	// from 0..1 to 0..8
	s.Logo.HeatDissipation *= 8.0
	// from 0..1 to 0..5.2
	s.Logo.HeatDissipationGradientAware *= 0.092
	// from 0..1 to 0.5..1
	s.Logo.Ripples.Detail = 0.5 + 0.5*s.Logo.Ripples.Detail
	// from 0..1 to 0.1..2.0
	s.Logo.Ripples.Speed = 0.1 + (2.0-0.1)*s.Logo.Ripples.Speed

	// DONE
	s.initialized = true
}

func (s *burning) runCell(
	frame, total int, fps float, time float,
	cellCoords term.Coordinates,
	fragCoordX, fragCoordY int,
	resolutionX, resolutionY int,
	inChar rune, inFg, inBg term.Color,
) (outChar rune, outFg, outBg term.Color) {
	outFg = inFg
	outBg = inBg
	outChar = inChar

	progress := float(frame) / float(total)

	// Screen texture coords 0..1: bottom left is 0,0; top right is 1,1.
	uv := vec2(
		float(fragCoordX)/float(resolutionX),
		float(fragCoordY)/float(resolutionY),
	)

	//
	rows := int(float(resolutionY) / asciiart.HeightToWidthCellAspectRatio)
	cols := resolutionX

	// cellCoords: Original coords from the cell matrix.
	//
	// The fragCoordX/Y are distorted to account aspect ratio of terminal cells
	// (which tend to be taller than wider).
	//
	// With this transformation, which undoes what is done in [glslshader.shadeRow]
	// we know the original cell coordinate in the [][]term.Cell matrix.
	//
	// This is used mainly as indices to store state in tables.
	x := cellCoords.X
	y := cellCoords.Y

	outBg = s.drawFlames(outBg, cellCoords, progress, frame, time, uv)

	if s.DebugWallpaperBounds {
		outBg, outChar = s.drawDebugWallpaperBounds(
			outBg, outChar,
			cellCoords,
			progress,
			rows, cols,
		)
	}

	if s.HideLogo {
		return
	}

	if !s.isWithinWallpaperBounds(x, y) {
		return
	}

	outChar, outFg, outBg = s.drawLogoEffects(
		outChar, outFg, outBg, cellCoords, frame, progress,
	)
	return
}

func (s *burning) drawFlames(
	inBg term.Color,
	termCoords term.Coordinates,
	progress float,
	frame int,
	time float,
	uv vec2D,
) (outBg term.Color) {
	outBg = inBg

	if s.DebugWallpaperBounds {
		return
	}

	q := uv

	if q.x >= 0.5 {
		q.x = 1.0 - q.x
	}

	q.x = 1 - q.x

	q.x = q.x * math.Pow(q.x+0.5, 1.5)
	q.y *= 1.0 - 0.5*smoothstep(0.0, 0.6, q.y)
	vertical := q.y

	q.y *= s.Squash
	q.x -= 0.1
	q.y -= 0.06
	q.x += 0.6 * q.y
	horizontal := q.x

	T3 := math.Max(3., 1.25*s.Fiery) * 0.15 * (s.FlameSpeed*time + s.FlameTimeOffset)
	n := s.fbm(q.multSc(s.Fiery).sub(vec2(0, T3)), vertical, horizontal, progress)

	c := 1.0 -
		16.0*math.Pow(
			math.Max(
				0,
				length2D(vec2FromScalar(s.Laterals).
					mult(q).
					mult(vec2(q.y*s.sampleAnimatedParameter(progress, s.Wide), 0.35)))-
					n*math.Max(0, q.y+0.27)),
			1.2,
		)

	c1 := n * c * (1.5 - math.Pow(1.00*uv.y, 9.0))
	c1 = clamp(c1, 0.0, 1.0)

	if !s.HideLogo {
		temperature := c * c1

		// Exclude lowest temperature from records to avoid showing ghost dim logo.
		const kCoarseCut = 0.1 // making this value smaller makes a coarser cut
		temperature *= smoothstep(
			s.Logo.HeatDissipationCutOff,
			s.Logo.HeatDissipationCutOff+
				kCoarseCut,
			temperature,
		)

		x := termCoords.X
		y := termCoords.Y
		if s.heatRecords[y][x].temperature < temperature {
			s.heatRecords[y][x].when = frame
			s.heatRecords[y][x].temperature += temperature * s.Logo.HeatBrushFlow
			// NOTE: Uncomment to have flatter less grainy heat painting at the
			// expense of clamping (flatter).
			// if s.heatRecords[cellY][cellX].temperature > 1.0 {
			// 	s.heatRecords[cellY][cellX].temperature = 1.0
			// }
		}
	}

	if s.HideBackgroundFlames {
		return
	}

	col := s.heatToCol(c1)

	var alpha float
	if s.SwitchLightsOff {
		// Ignoring the bg-blending you get higher contrast intense results.
		// Read the docstring SwitchLightsOff in [BurningParams] for a detail
		// explanation.
		alpha = 1.0
	} else {
		// Derive alpha from the flame intensity.
		alpha = c * (1.0 - math.Pow(uv.y, 1.0))
	}

	col = clamp3D(col.multSc(255.0), vec3FromScalar(0.0), vec3FromScalar(255.0))

	// Flames look bad on bright backgrounds, so darken them if it's the case.
	originalBg := inBg
	if !s.SwitchLightsOff {
		const (
			kDarken            = 0.7 // darken 30%
			kDarkenFadeInStart = 0.0 // start darkening fade in
			kDarkenFadeInEnd   = 0.1 // end darkening fade in
		)
		correctedBg := colToVec(originalBg, s.defaultAttr.Bg)
		if shaderutils.ColorBrightness(originalBg, s.defaultAttr.Bg) > 0.6 {
			correctedBg = mix3D(
				colToVec(originalBg, s.defaultAttr.Bg), colToVec(originalBg, s.defaultAttr.Bg).multSc(kDarken),
				smoothstep(kDarkenFadeInStart, kDarkenFadeInEnd, progress),
			)
		}
		col = mix3D(
			correctedBg,
			col,
			clamp(alpha, 0.0, 1.0),
		)
	}

	outBg = vecToCol(col)

	return
}

func (s *burning) drawDebugWallpaperBounds(
	inBg term.Color, inChar rune,
	termCoords term.Coordinates,
	progress float,
	rows, cols int,
) (outBg term.Color, outChar rune) {
	outBg = inBg
	outChar = inChar
	x := termCoords.X
	y := termCoords.Y
	bounds := s.wallpaperBounds
	if x == 0 && y == 0 {
		// Yellow screen top left corner.
		outBg = term.NewColor(255, 255, 0)
	} else if x == cols-1 && y == rows-1 {
		// Cyan screen bottom right corner.
		outBg = term.NewColor(0, 255, 255)
	} else if x == bounds.topLeftX && y == bounds.topLeftY {
		// Red wallpaper top left corner.
		outBg = term.NewColor(255, 0, 0)
	} else if x == bounds.bottomRightX && y == bounds.bottomRightY {
		// Green wallpaper bottom right corner.
		outBg = term.NewColor(0, 255, 0)
	} else if s.isWithinWallpaperBounds(x, y) {
		// Paint chars within the area of wallpaper and change bg.
		outBg = shaderutils.InterpolateColor(
			progress, term.NewColor(255, 255, 255), inBg, s.defaultAttr.Bg,
		)
		outChar = 'M'
	}
	return
}

func (s *burning) drawLogoEffects(
	inChar rune, inFg, inBg term.Color,
	termCoords term.Coordinates,
	frame int,
	progress float,
) (outChar rune, outFg, outBg term.Color) {
	outFg = inFg
	outBg = inBg

	// Hide the original char, we will only play with bg. Towards the end of
	// the animation the original char is brought back and fg color blended
	// with wallpaper's. That way we make it work with GUI
	// transparent/translucent backgrounds.
	outChar = ' '

	x := termCoords.X
	y := termCoords.Y

	logoX := x - s.wallpaperBounds.topLeftX
	logoY := y - s.wallpaperBounds.topLeftY

	ch := s.logo[logoY][logoX]

	// Do not bother with the effect if there isn't logo char.
	if ch == rune(0) || ch == ' ' || ch == s.Logo.WallpaperInvisibleChar {
		return
	}

	if s.DebugWallpaperBounds {
		outChar = ch
		outBg = outFg
		return
	}

	// The logo matrix carries values ranging from 0 to "s.logoMaxGradientValue".
	logoGradient := ch
	logoGradient01 := float(logoGradient) / float(s.logoMaxGradientValue)

	// NOTE: The code below transitions from a 1x zoom to a "maxStretch" zoom
	// (the number of cells away from the logo before touching a screen edge).
	// The zoom is scaled around the center of the screen and only happens
	// within the logo. It is left out because its aim was to separate a bit
	// the logo shape from the background but two other aspects of the effect
	// already do this and much better: the heat accumulation and the ember
	// ripples. If no heat accumulation nor ember ripple would exist then this
	// would be a good way to make the logo stand out.
	//
	// STEP 1: Sample flame from the lower edges of the screen so more fire is
	// visible within the logo shape (since it's centered in the middle).
	//
	// samplePos := vec2(float(x), float(y))
	// zoomStarts := 0.2 // 0%
	// zoomEnds := 0.7   // 20%
	// center := vec2(float(w)/2.0, float(h)/2.0)
	// maxStretch := math.Min(float(w)-logoW, float(h)-logoH) / 2.0
	// sampleStretch := maxStretch * smoothstep(zoomStarts, zoomEnds, progress)
	// direction := samplePos.sub(center).mult(vec2(1, asciiart.HeightToWidthCellAspectRatio))
	// length := length2D(direction)
	// if length > 1.0 {
	// 	directionUnit := direction.divSc(length)
	// 	samplePos = samplePos.add(directionUnit.multSc(sampleStretch))
	// }
	// sampleX := int(math.Min(float(w)-1, math.Max(0, math.Round(samplePos.x))))
	// sampleY := int(math.Min(float(h)-1, math.Max(0, math.Round(samplePos.y))))

	sampledFlame := outBg
	logoBg := sampledFlame

	// STEP 2: If the flame has touched the logo cell, show the heat, and also
	// dissipate it as time passes producing a trailing effect.

	temperature := s.heatRecords[y][x].temperature - math.Max(0,
		float(frame-s.heatRecords[y][x].when)/((s.Logo.HeatDissipation+
			// Heat dissipates slower on the brighter parts of the logo.
			s.Logo.HeatDissipationGradientAware*(1.0-logoGradient01))*s.fps),
	)
	temperature = clamp(temperature, 0.0, 1.0)
	heatCol := vecToCol(clamp3D(
		s.heatToCol(temperature), vec3FromScalar(0.0), vec3FromScalar(1.0),
	).multSc(255))
	logoBg = shaderutils.InterpolateColor(temperature, logoBg, heatCol, s.defaultAttr.Bg)

	// STEP 3: Show ember ripples travelling in the direction of the "logo
	// gradient" (from darker to brighter) after StartFadeInAt peaking at
	// EndFadeInAt.
	//
	// See [Burning] documentation to know what is the "logo gradient".

	if progress >= s.Logo.Ripples.StartFadeInAt {
		animGrad := fract(
			s.Logo.Ripples.Detail*logoGradient01 +
				float(frame)/s.fps*s.Logo.Ripples.Speed,
		)

		gradientRippleBg := shaderutils.InterpolateColor(
			// Remap so it is 0 at StartFadeInAt and 1 at frame=total.
			smoothstep(s.Logo.Ripples.StartFadeInAt, s.StartFadeOut, progress),
			shaderutils.SampleGradient(animGrad, s.Colors.RipplesGradientStart),
			shaderutils.SampleGradient(animGrad, s.Colors.RipplesGradientEnd),
			s.defaultAttr.Bg,
		)

		// Fade in the ember ripples (see "logo gradient" in [Burning] docstring).
		logoBg = shaderutils.InterpolateColor(
			smoothstep(s.Logo.Ripples.StartFadeInAt, s.Logo.Ripples.EndFadeInAt, progress),
			logoBg,
			gradientRippleBg,
			s.defaultAttr.Bg,
		)
	}

	logoFg := logoBg

	// NOTE: Could play with characters too based on temperaturee, but it
	// doesn't look very good. Leaving the code here for future explorers!
	//
	// Sequences you could map temperature to:
	//
	// 1. ← ↖ ↑ ↗ → ↘ ↓ ↙
	// 2. ⣾ ⣽ ⣻ ⢿ ⡿ ⣟ ⣯ ⣷
	// 3. ⠁ ⠂ ⠄ ⡀ ⢀ ⠠ ⠐ ⠈
	//
	// As follows:
	//
	// choices := []rune{'←', '↖', '↑', '↗', '→'}
	// in[y][x].Ch = rune(choices[int(float(len(choices)-1)*temperature*temperature)])
	// in[y][x].Width = 1
	// logoFg = vecToCol(colToVec(logoBg).multSc(0.9))

	outFg = logoFg
	outBg = logoFg

	// STEP 4: Fade out the effect.

	// By offsetting the fade out into the original colors a tiny bit we make
	// the ember ripples stand out a little more. The value has to be so small
	// that's not worth parametrizing.
	const kStartFadeOutOffset = 0.05 // 5% of the animation
	if progress >= (s.StartFadeOut + kStartFadeOutOffset) {
		// Fade out to the original background respecting the "logo gradient"
		// values, so brighter of the logo take longer to turn from embers into
		// original bg.
		factor := 1.0 - clamp(
			(1.0+logoGradient01)-
				2.1*smoothstep(s.StartFadeOut+kStartFadeOutOffset, 1.0, progress),
			0.0,
			1.0,
		)
		outBg = shaderutils.InterpolateColor(factor, logoBg, inBg, s.defaultAttr.Bg)

		// Align foreground color of the existing logo underneath to
		// the background color to give the illusion the density
		// characters do not exist, and then towards the end of the
		// animation bring them in, tricking the eye.
		outFg = shaderutils.InterpolateColor(
			smoothstep(s.StartFadeOut+kStartFadeOutOffset, 1.0, progress),
			logoBg,
			inFg,
			s.defaultAttr.Fg,
		)
	}

	// Slowly enter the original density characters as opposed to only be
	// playing with background blocks. This is so it looks well on GUI
	// transparent backgrounds.
	if logoGradient01 <= smoothstep(
		s.Logo.Ripples.StartFadeInAt, s.Logo.Ripples.EndFadeInAt, progress,
	) {
		outChar = inChar
	}

	return
}

func (s *burning) heatToCol(c1 float) vec3D {
	if len(s.Colors.HeatColorGradientOverride) > 0 {
		factor := c1 * c1 * c1 * c1 * c1
		if math.IsNaN(factor) {
			green := vec3(0.0, 1.0, 0.0)
			return green
		}
		return colToVec(
			shaderutils.SampleGradient(factor, s.Colors.HeatColorGradientOverride),
			s.defaultAttr.Bg,
		).divSc(255.0)
	}
	col := vec3(1.5*c1, 1.5*c1*c1*c1, c1*c1*c1*c1*c1*c1)
	if s.Colors.SwapRedBlue {
		col = vec3(col.z, col.y, col.x)
	}
	return col
}

func (s *burning) fbm(
	uv vec2D, vertical float, horizontal float, progress float,
) float {
	var f float
	m := mat2(1.6, 1.2, -1.2, 1.6)
	bn := s.noise(uv)

	f = 0.54000 * bn
	uv = m.multVec2D(uv)
	f += 0.2500 * s.noise(uv)
	uv = m.multVec2D(uv)
	f += 0.1250 * s.noise(uv)
	uv = m.multVec2D(uv)
	f += 0.0625 * s.noise(uv)

	f = 0.5 + 0.5*f

	pr := s.sampleAnimatedParameter(progress, s.Intensity)
	pr = 1.0 - pr

	loc := vertical - s.HeatGain*horizontal
	prog := pr + 0.7*loc

	f = prog*bn + (1.0-prog)*f
	f = f - 9.0*pr*pr*pr

	return f
}

func (s *burning) noise(p vec2D) float {
	const k1 = 0.366025404 // (sqrt(3)-1)/2;
	const k2 = 0.211324865 // (3-sqrt(3))/6;

	i := floor2D(p.addSc((p.x + p.y) * k1))
	a := p.sub(i).addSc((i.x + i.y) * k2)
	o := vec2(0.0, 1.0)
	if a.x > a.y {
		o = vec2(1.0, 0.0)
	}
	b := a.sub(o).addSc(k2)
	c := a.subSc(1.0).addSc(2.0 * k2)

	h := max3D(vec3FromScalar(0.5).sub(vec3(
		dot2D(a, a),
		dot2D(b, b),
		dot2D(c, c),
	)), vec3FromScalar(0.0))
	n := h.mult(h).mult(h).mult(h).mult(vec3(
		dot2D(a, s.hash(i.addSc(0.0))),
		dot2D(b, hash(i.add(o))),
		dot2D(c, hash(i.addSc(1.0)))),
	)

	return dot3D(n, vec3FromScalar(70.0))
}

func (s *burning) hash(p vec2D) vec2D {
	p = vec2(
		dot2D(p, vec2(127.1, 311.7)),
		dot2D(p, vec2(269.5, 183.3)),
	)
	return vec2FromScalar(-1.0).add(vec2FromScalar(2.0).mult(
		fract2D(sin2D(p).multSc(43758.5453123)),
	))
}

// sampleAnimatedParameter samples from a slice of values where the first one
// represents the timeline start and the last one its end.
func (s *burning) sampleAnimatedParameter(factor float, animatedParameter []float) float {
	if len(animatedParameter) == 0 {
		return 0.0
	}
	if len(animatedParameter) == 1 {
		return animatedParameter[0]
	}

	t := math.Min(1, math.Max(0, factor))
	stops := float64(len(animatedParameter) - 1)
	tt := t * stops

	currIdx := int(math.Floor(tt))
	nextIdx := currIdx + 1
	if nextIdx >= len(animatedParameter) {
		nextIdx = len(animatedParameter) - 1
	}

	tDec := tt - math.Floor(tt)

	col1 := animatedParameter[currIdx]
	col2 := animatedParameter[nextIdx]

	return mix(col1, col2, tDec)
}

func (s *burning) processLogoFromWallpaper(in [][]term.Cell) {
	// STEP 1: Don't show the effect if we can't guarantee it's going to look
	// as we expect, so if it's not a wallpaper we can mask out bail out.
	if !s.isMaskeableWallpaper(in) {
		log.Warn(
			"Skipping Burning because can't process a mask " +
				"needed for the effect from the current wallpaper.",
		)
		s.skipRenders = true
		return
	}

	// STEP 2: Get the bounds of the wallpaper.
	foundBounds, bounds := s.findWallpaperBounds(in)

	// Do not bother doing logo effects if the wallpaper bounds it sits on is
	// not found.
	if !foundBounds {
		log.Warn("Skipping Burning because can't find wappaper bounds")
		s.skipRenders = true
		return
	}

	prevCornerTopLeftX := s.wallpaperBounds.topLeftX
	prevCornerTopLeftY := s.wallpaperBounds.topLeftY
	prevCornerBottomRightX := s.wallpaperBounds.bottomRightX
	prevCornerBottomRightY := s.wallpaperBounds.bottomRightY

	cornersChanged := prevCornerTopLeftX != bounds.topLeftX ||
		prevCornerTopLeftY != bounds.topLeftY ||
		prevCornerBottomRightX != bounds.bottomRightX ||
		prevCornerBottomRightY != bounds.bottomRightY

	// No need to recompute logo array if corners have not moved.
	if !cornersChanged {
		return
	}

	s.wallpaperBounds = bounds

	// The top-left corner is itself a marker cell. Cache it together with
	// the matrix shape it was found in so later frames can detect, in
	// O(1), when the wallpaper stops being behind the shader (see Shade).
	// Anchor the probe near the screen center rather than at a bounds
	// corner: overlays such as the centered command prompt cover the
	// middle of the screen, so a corner marker would survive and the
	// shader would keep occluding the prompt.
	s.captureMarker(in)

	wallpaperWidth := bounds.bottomRightX - bounds.topLeftX
	wallpaperHeight := bounds.bottomRightY - bounds.topLeftY
	if wallpaperWidth <= 0 || wallpaperHeight <= 0 {
		log.Warn("Skipping Burning because wallpaper bounds are invalid")
		s.skipRenders = true
		return
	}

	// STEP 3: Encode wallpaper logo into the wallpaper bounds.
	asciiart.Encode(s.imgBuf, wallpaperWidth, wallpaperHeight, s.Logo.Image, s.asciiartConfig)

	// STEP 4: Populate the "logo gradient" matrix used for the ember ripples
	// effect (read [Burning] docstring for more information).
	s.logo = make([][]rune, wallpaperHeight)
	for y := range wallpaperHeight {
		s.logo[y] = make([]rune, wallpaperWidth)
		for x := range wallpaperWidth {
			cell, ok := s.imgBuf.Cell(term.Coordinates{X: x, Y: y})
			if !ok {
				continue
			}
			if !unicode.IsDigit(cell.Ch) {
				continue
			}
			v, err := strconv.Atoi(string(cell.Ch))
			if err != nil {
				panic(fmt.Errorf("parse max gradient value: %v", err))
			}
			if v > s.logoMaxGradientValue {
				s.logoMaxGradientValue = v
			}
			s.logo[y][x] = rune(v)
		}
	}
}

func (s *burning) isMaskeableWallpaper(in [][]term.Cell) bool {
	return s.hasExpectedWallpaper(in)
}

// markerStillPresent reports whether the cached marker cell is still the
// invisible char. It returns false when the matrix shape no longer
// matches the shape the marker was captured in, both because the cached
// coordinate would then be meaningless and because re-reading it could
// index out of range. Callers must guard on markerSet first.
func (s *burning) markerStillPresent(in [][]term.Cell) bool {
	if len(in) != s.markerRows || len(in) == 0 || len(in[0]) != s.markerCols {
		return false
	}
	if s.markerY >= len(in) || s.markerX >= len(in[s.markerY]) {
		return false
	}
	return in[s.markerY][s.markerX].Ch == s.Logo.WallpaperInvisibleChar
}

// captureMarker records a single wallpaper marker cell to re-probe each
// frame (see Shade). It walks the center column up from the vertical
// middle to the first invisible char, i.e. the background just above the
// centered logo. The command prompt opens centered with its full command
// list, so it is always tall enough to cover that cell; overwriting it
// cancels the shader. It falls back to the bounds' top-left corner if the
// center column carries no marker.
func (s *burning) captureMarker(in [][]term.Cell) {
	rows := len(in)
	cols := len(in[0])
	midX := cols / 2
	midY := rows / 2

	s.markerRows = rows
	s.markerCols = cols
	s.markerSet = true

	if midX < len(in[midY]) {
		for y := midY; y >= 0; y-- {
			if in[y][midX].Ch == s.Logo.WallpaperInvisibleChar {
				s.markerX, s.markerY = midX, y
				return
			}
		}
	}

	s.markerX = s.wallpaperBounds.topLeftX
	s.markerY = s.wallpaperBounds.topLeftY
}

// hasExpectedWallpaper will rely on the assumption that our asciiart density
// characters use the special invisible character (configured in
// BurningParams.Logo.WallpaperInvisibleChar, e.g. unicode's thin space) as the
// lowest density, so any wallpaper rendered will (unnoticingly to the user)
// carry the meaning that it's part of the wallpaper.
//
// NOTE: Turn on DebugWallpaperBounds in [BurningParams] to view the wallpaper
// bounds and top left and bottom right corner.
func (s *burning) hasExpectedWallpaper(in [][]term.Cell) bool {
	w := float(len(in[0]))
	h := float(len(in))
	mid := vec2(float(w)/2.0, float(h)/2.0)
	invisibleCharContiguous := 0
	for p := mid; 0 < p.x && p.x < w && 0 < p.y && p.y < h; p = p.add(
		vec2(1.0, 1.0/asciiart.HeightToWidthCellAspectRatio),
	) {
		char := in[int(p.y)][int(p.x)].Ch
		if char != s.Logo.WallpaperInvisibleChar {
			invisibleCharContiguous = 0
			continue
		}
		invisibleCharContiguous += 1
		if invisibleCharContiguous > 2 {
			return true
		}
	}
	return false
}

func (s *burning) findWallpaperBounds(in [][]term.Cell) (
	found bool, bounds wallpaperBounds,
) {
	w := len(in[0])
	h := len(in)

	var foundTopLeftX, foundTopLeftY, foundBottomRightX, foundBottomRightY int
	found = false

FirstCornerLoop:
	for y := range h {
		for x := range w {
			if in[y][x].Ch == s.Logo.WallpaperInvisibleChar {
				found = true
				foundTopLeftX = x
				foundTopLeftY = y
				break FirstCornerLoop
			}
		}
	}

	if !found {
		return
	}

	found = false
SecondCornerLoop:
	for y := h - 1; y > 0; y-- {
		for x := w - 1; x > 0; x-- {
			if in[y][x].Ch == s.Logo.WallpaperInvisibleChar {
				found = true
				foundBottomRightX = x
				foundBottomRightY = y
				break SecondCornerLoop
			}
		}
	}

	// What we found by scanning row-first is the following points:
	// >0
	// >1
	// >2
	// >3  █░░░░░  topLeft █     (x,y) = (2,3)
	// >4  ░░░░░░  bottomRight █ (x,y) = (7,5)
	// >5  ░░░░░█  dimensions    (WxH) = (5x2) WRONG
	// >6
	// >7
	// >8
	// >9
	// > 012345689
	//
	//     WxH of 7x4 is WRONG: use your cursor to count ░
	//
	// To achieve the correct dimensions we need to move the bottom right one
	// step away in each dimension:
	// >0
	// >1
	// >2
	// >3  █░░░░░  topLeft █     (x,y) = (2,3)
	// >4  ░░░░░░  bottomRight █ (x,y) = (8,6)
	// >5  ░░░░░░  dimensions    (WxH) = (6x3) CORRECT
	// >6        █
	// >7
	// >8
	// > 012345689
	//
	//     WxH of 6x3 is CORRECT: use your cursor to count ░
	bounds.topLeftX = foundTopLeftX
	bounds.topLeftY = foundTopLeftY
	bounds.bottomRightX = foundBottomRightX + 1
	bounds.bottomRightY = foundBottomRightY + 1
	return
}

func (s *burning) isWithinWallpaperBounds(x, y int) bool {
	return s.wallpaperBounds.topLeftX < x &&
		x < s.wallpaperBounds.bottomRightX &&
		s.wallpaperBounds.topLeftY < y &&
		y < s.wallpaperBounds.bottomRightY
}

func (s *burning) ensureHeatRecordsCap(in [][]term.Cell) {
	if len(s.heatRecords) < len(in) {
		s.heatRecords = append(
			s.heatRecords,
			make([][]heatRecord, len(in)-len(s.heatRecords))...,
		)
	}
	for i, row := range in {
		if len(s.heatRecords[i]) < len(row) {
			s.heatRecords[i] = append(
				s.heatRecords[i], make([]heatRecord,
					len(row)-len(s.heatRecords[i]))...,
			)
		}
	}
}

// heatRecord stores the highest temperature (heat) at a point in time.
type heatRecord struct {
	when        int   // frame
	temperature float // heat 0..1
}

type wallpaperBounds struct {
	topLeftX, topLeftY         int
	bottomRightX, bottomRightY int
}
