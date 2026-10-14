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

package ide

import (
	"context"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/unstablebuild/blue/iterator"
	"github.com/unstablebuild/rune-go-sdk/api/textapi"
	"github.com/unstablebuild/rune-go-sdk/component"
	"github.com/unstablebuild/rune-go-sdk/handler"
	"github.com/unstablebuild/rune-go-sdk/term"
	"github.com/unstablebuild/rune-go-sdk/tui"
	"unstable.build/rune/internal/cell"
	"unstable.build/rune/internal/component/braille"
	"unstable.build/rune/internal/component/shader"
	"unstable.build/rune/internal/component/shader/glslshader"
)

// fakeLoadingShader returns a deterministic, cheap shader factory
// usable in tests. We use Shine because it's pure CPU and avoids
// the heavier GL paths.
func fakeLoadingShader() func(term.Attributes) shader.Shader {
	return func(defaultAttr term.Attributes) shader.Shader {
		params := glslshader.DefaultShineParams()
		params.Color = term.NewRGBColor(255, 255, 255)
		return glslshader.Shine(params, defaultAttr)
	}
}

func newTestShaderRunner(loading, open loadingShaderConfig) *shaderRunner {
	r := new(shaderRunner)
	openCfg := openShaderConfig{
		shader:   open.shader,
		fps:      open.fps,
		duration: open.duration,
	}
	r.init(handler.Nop(), term.NopInterrupter(), term.Attributes{},
		nopShutdownShaderConfig(), loading, openCfg,
		component.FrameCharSetDefault())
	return r
}

func TestShaderRunnerLoadingNoOpWithoutConfig(t *testing.T) {
	r := newTestShaderRunner(loadingShaderConfig{}, loadingShaderConfig{})
	initial := r.shader

	r.startLoading()
	r.startLoading()
	assert.Same(t, initial, r.shader,
		"startLoading must not swap the shader when no loading config is set")

	r.stopLoading()
	r.stopLoading()
	assert.Same(t, initial, r.shader,
		"stopLoading must not swap the shader when no loading config is set")
}

func TestShaderRunnerStartLoadingRestarts(t *testing.T) {
	loading := loadingShaderConfig{shader: fakeLoadingShader(), fps: 30}
	r := newTestShaderRunner(loading, loadingShaderConfig{})
	initial := r.shader

	r.startLoading()
	first := r.shader
	assert.NotSame(t, initial, first,
		"first startLoading must swap in a loading shader")

	r.startLoading()
	second := r.shader
	assert.NotSame(t, first, second,
		"second startLoading must replace the previous loading shader")
}

func TestShaderRunnerStopLoadingFiresOpenShader(t *testing.T) {
	loading := loadingShaderConfig{shader: fakeLoadingShader(), fps: 30}
	open := loadingShaderConfig{shader: fakeLoadingShader(), fps: 30}
	r := newTestShaderRunner(loading, open)

	r.startLoading()
	loadingComp := r.shader

	r.stopLoading()
	assert.NotSame(t, loadingComp, r.shader,
		"stopLoading must swap in the open shader")
}

func TestShaderRunnerStopLoadingFallsBackToCancelWithoutOpen(t *testing.T) {
	loading := loadingShaderConfig{shader: fakeLoadingShader(), fps: 30}
	r := newTestShaderRunner(loading, loadingShaderConfig{})

	r.startLoading()
	loadingComp := r.shader

	r.stopLoading()
	assert.NotSame(t, loadingComp, r.shader,
		"stopLoading without an open shader must cancel the active one")
}

func TestShaderRunnerStartLoadingUsesConfiguredDuration(t *testing.T) {
	const (
		fps    = 30
		dur    = 4 * time.Second
		expect = int(dur) / (int(time.Second) / fps) // shader.Component.total
	)
	loading := loadingShaderConfig{
		shader:   fakeLoadingShader(),
		fps:      fps,
		duration: dur,
	}
	r := newTestShaderRunner(loading, loadingShaderConfig{})

	r.startLoading()
	assert.Equal(t, expect, r.shader.Total(),
		"startLoading must use the configured duration")
}

func TestShaderRunnerStopLoadingUsesConfiguredOpenDuration(t *testing.T) {
	const (
		fps    = 30
		dur    = 2500 * time.Millisecond
		expect = int(dur) / (int(time.Second) / fps)
	)
	loading := loadingShaderConfig{shader: fakeLoadingShader(), fps: fps}
	open := loadingShaderConfig{
		shader:   fakeLoadingShader(),
		fps:      fps,
		duration: dur,
	}
	r := newTestShaderRunner(loading, open)

	r.startLoading()
	r.stopLoading()
	assert.Equal(t, expect, r.shader.Total(),
		"stopLoading must use the configured open-shader duration")
}

func TestBuildNamedShaderRegistersEveryListedName(t *testing.T) {
	defAttr := term.Attributes{}
	fc := component.FrameCharSetDefault()
	for _, name := range namedShaderNames() {
		sh, ok := buildNamedShader(name, defAttr, 30, time.Second, fc)
		assert.True(t, ok,
			"buildNamedShader must register %q (listed by namedShaderNames)",
			name)
		assert.NotNil(t, sh,
			"buildNamedShader(%q) must return a non-nil Shader", name)
	}
}

func TestShaderRunnerReplaysTheIntro(t *testing.T) {
	ctx := context.Background()
	r := newTestShaderRunner(loadingShaderConfig{}, loadingShaderConfig{})
	completions := func() []string {
		it, _, err := r.Complete(ctx, textapi.Command{Name: "shaderrun"})
		require.NoError(t, err)
		names, err := iterator.ToSlice(ctx, it)
		require.NoError(t, err)
		return names
	}
	intro := textapi.Command{Name: "shaderrun", Args: []string{"intro", "5s", "60"}}

	initial := r.shader
	assert.Error(t, r.HandleCommand(ctx, intro), "without an intro")
	assert.Same(t, initial, r.shader)
	assert.NotContains(t, completions(), "intro")

	var built []term.Attributes
	r.defAttr = term.Attributes{Fg: term.ColorWhite}
	r.initShaderCfg = initShaderConfig{
		shader: func(attr term.Attributes, _ component.FrameCharSet) shader.Shader {
			built = append(built, attr)
			return shader.Nop()
		},
		fps:      30,
		duration: 2 * time.Second,
	}
	require.NoError(t, r.HandleCommand(ctx, intro))
	assert.Equal(t, []term.Attributes{{Fg: term.ColorWhite}}, built)
	assert.Equal(t, 60, r.shader.Total(), "the intro keeps its own length")
	assert.Equal(t, "intro", completions()[0])
}

func TestShaderRunnerOpenShaderStartsOnThePicturesOnScreen(t *testing.T) {
	const cols, rows = 40, 12
	ctx := cell.ContextWithPixelSize(context.Background(), cell.PixelSize{Width: 10, Height: 20})
	wallpaper := func(w, h int, c color.NRGBA) tui.Component {
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
		}
		return braille.NewComponent(img, func() (int, int) { return 10, 20 }, braille.Blocks).
			WithPicture(func() image.Image { return img })
	}
	screen := &swappable{Component: wallpaper(10, 20, color.NRGBA{R: 255, A: 255})}
	r := new(shaderRunner)
	r.init(handler.NopFromComponent(screen), term.NopInterrupter(), term.Attributes{},
		nopShutdownShaderConfig(),
		loadingShaderConfig{
			shader: func(attr term.Attributes) shader.Shader {
				return shader.GrayFade(shader.DefaultGrayFadeParams(), attr)
			},
			fps: 1, duration: 10 * time.Second,
		},
		openShaderConfig{
			shader: func(attr term.Attributes) shader.Shader {
				return shader.Burn(shader.DefaultBurnParams(), attr)
			},
			fps: 1, duration: 10 * time.Second,
		},
		component.FrameCharSetDefault())
	t.Cleanup(func() { _ = r.shader.Close() })
	r.Resize(cols, rows)
	draw := func() *cell.BufferWriter {
		buf := cell.NewBufferWriter(ctx, cols, rows)
		r.Draw(buf)
		return buf
	}

	shown := draw()
	require.Len(t, shown.Images(), 1)
	r.startLoading()
	draw()
	r.captureOpenShaderCells()
	screen.swap(wallpaper(40, 10, color.NRGBA{B: 255, A: 255}))
	r.stopLoading()
	first := draw()

	for y, row := range first.RawCells() {
		for x, c := range row {
			require.NotEqual(t, braille.Block, c.Ch, "cell %d,%d shows the fallback for writers without images", x, y)
		}
	}
	require.Len(t, first.Images(), 1, "only the picture that was on screen shows")
	want, got := shown.Images()[0], first.Images()[0]
	assert.Equal(t, want.Visible(), got.Visible())
	assert.Equal(t, want.Src.(*image.RGBA).Pix, got.Src.(*image.RGBA).Pix)
}

// swappable is a component whose content can be replaced, like the
// screen when a workspace opens.
type swappable struct {
	tui.Component
	width, height int
}

func (s *swappable) Resize(width, height int) {
	s.width, s.height = width, height
	s.Component.Resize(width, height)
}

func (s *swappable) swap(c tui.Component) {
	c.Resize(s.width, s.height)
	s.Component = c
}
