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
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/unstablebuild/blue/iterator"
	"github.com/unstablebuild/rune-go-sdk/api/textapi"
	"github.com/unstablebuild/rune-go-sdk/component"
	"github.com/unstablebuild/rune-go-sdk/term"
	"github.com/unstablebuild/rune-go-sdk/tui"
	"unstable.build/rune/internal/cell"
	"unstable.build/rune/internal/component/shader"
	"unstable.build/rune/internal/component/shader/glslshader"
	"unstable.build/rune/internal/component/shader/timeshader"
)

const (
	defaultShaderFPS             = 30
	defaultShaderDuration        = 1 * time.Second
	defaultLoadingShaderDuration = 10 * time.Second
	defaultOpenShaderDuration    = 1 * time.Second

	// introShaderName plays the intro of the wallpaper from :shaderrun,
	// even when animations.intro keeps it from playing at startup.
	introShaderName = "intro"
)

type initShaderConfig struct {
	shader   func(term.Attributes, component.FrameCharSet) shader.Shader
	fps      int
	duration time.Duration
}

type shutdownShaderConfig struct {
	shader   func(term.Attributes) shader.Shader
	fps      int
	duration time.Duration
}

func nopShutdownShaderConfig() shutdownShaderConfig {
	return shutdownShaderConfig{
		shader:   func(_ term.Attributes) shader.Shader { return shader.Nop() },
		fps:      defaultShaderFPS,
		duration: 100 * time.Millisecond,
	}
}

type loadingShaderConfig struct {
	shader   func(term.Attributes) shader.Shader
	fps      int
	duration time.Duration
}

type openShaderConfig struct {
	shader   func(term.Attributes) shader.Shader
	fps      int
	duration time.Duration
}

// used as the root tui.Handler to dynamically run shaders
type shaderRunner struct {
	tui.Handler
	interrupter       term.Interrupter
	shader            *shader.Component
	defAttr           term.Attributes
	fc                component.FrameCharSet
	width, height     int
	initShaderCfg     initShaderConfig
	shutdownShaderCfg shutdownShaderConfig
	loadingShaderCfg  loadingShaderConfig
	openShaderCfg     openShaderConfig
	openShaderCells   [][]term.Cell
	openShaderImages  []term.Image
	loadingShaderInst shader.Shader
	// pixelSize is the cell size of the writer last drawn into, which the
	// screen needs to draw its pictures.
	pixelSize cell.PixelSize
}

func (r *shaderRunner) HandleCommand(ctx context.Context, cmd textapi.Command) (
	err error,
) {
	if len(cmd.Args) == 0 {
		err = errors.New("expected at least one argument with the shader name")
		return
	}

	if cmd.Args[0] == introShaderName {
		if !r.runInitShader() {
			err = errors.New("the wallpaper has no intro: " +
				"only the built-in logos of workspace.wallpaper_logo have one")
		}
		return
	}

	d := defaultShaderDuration
	if len(cmd.Args) > 1 {
		d, err = time.ParseDuration(cmd.Args[1])
		if err != nil {
			err = fmt.Errorf("parse duration: %v", err)
			return
		}
	}

	fps := defaultShaderFPS
	if len(cmd.Args) > 2 {
		fps, err = strconv.Atoi(cmd.Args[2])
		if err != nil {
			err = fmt.Errorf("parse fps: %v", err)
			return
		}
	}

	s, ok := buildNamedShader(cmd.Args[0], r.defAttr, fps, d, r.fc)
	if !ok {
		err = errors.New("expected one of the available shaders")
		return
	}

	r.runShader(s, fps, d)
	return
}

func (r *shaderRunner) Complete(ctx context.Context, cmd textapi.Command) (
	iterator.Iterator[string], string, error,
) {
	if len(cmd.Args) <= 1 {
		names := namedShaderNames()
		if r.initShaderCfg.shader != nil {
			names = append([]string{introShaderName}, names...)
		}
		return iterator.FromSlice(names), "", nil
	}
	return iterator.Empty[string](), "", nil
}

// namedShaderNames returns the names accepted by buildNamedShader, in
// the order shown by the :shaderrun completer. The order is preserved
// for showroom UI stability.
func namedShaderNames() []string {
	return []string{
		// TODO: Review and leave only useful shaders. This at the moment is a review showroom!
		"blaze",
		"blazeBlue",
		"bomb",
		"burn",
		"embers",
		"fade",
		"incendium",
		"incendiumReversed",
		"incendiumPingPong",
		"flames",
		"flamesA",
		"flamesV",
		"inferno",
		"infernoBlue",
		"burningOnlyRedFlamesLightsOn",
		"burningOnlyRedFlamesLightsOff",
		"burningOnlyBlueFlamesLightsOn",
		"burningOnlyBlueFlamesLightsOff",
		"noise",
		"nop",
		"pulse",
		"risingChars",
		"shine",
		"shineFrame",
		"radarFrame",
		"grayFade",
		"trippy",
	}
}

// buildNamedShader returns the shader registered under name with the
// default parameters used by the :shaderrun showroom, wrapped in the
// same cross-fade transitions when applicable. fps and duration are
// only consumed by shaders that need them (GLSL animated shaders and
// the burning preset family). Returns ok=false when name does not
// match any known shader.
func buildNamedShader(
	name string, defAttr term.Attributes,
	fps int, duration time.Duration, fc component.FrameCharSet,
) (shader.Shader, bool) {
	const (
		fadeInPerc  = 0.1
		fadeOutPerc = 0.1
	)
	switch name {
	case "blaze":
		return wrapShaderCrossFadeInOut(
			glslshader.Blaze(glslshader.DefaultBlazeParams(), float64(fps)),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "blazeBlue":
		params := glslshader.DefaultBlazeParams()
		params.SwapRedBlue = true
		return wrapShaderCrossFadeInOut(
			glslshader.Blaze(params, float64(fps)),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "bomb":
		return shader.Bomb(shader.DefaultBombParams(), defAttr), true
	case "burn":
		return shader.Burn(shader.DefaultBurnParams(), defAttr), true
	case "embers":
		return wrapShaderCrossFadeInOut(
			glslshader.Embers(glslshader.DefaultEmbersParams(), defAttr, float64(fps)),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "fade":
		return shader.Fade(defAttr), true
	case "incendium":
		return wrapShaderCrossFadeInOut(
			glslshader.Incendium(glslshader.DefaultIncendiumParams(), defAttr, float64(fps)),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "incendiumReversed":
		return wrapShaderCrossFadeInOut(
			timeshader.Reverse(glslshader.Incendium(glslshader.DefaultIncendiumParams(), defAttr, float64(fps))),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "incendiumPingPong":
		return wrapShaderCrossFadeInOut(
			timeshader.PingPong(glslshader.Incendium(
				glslshader.DefaultIncendiumParams(), defAttr, float64(fps))),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "flames":
		return wrapShaderCrossFadeInOut(
			glslshader.Flames(glslshader.DefaultFlamesParams(), defAttr, float64(fps)),
			0.0, fadeOutPerc,
			defAttr,
		), true
	case "flamesA":
		return wrapShaderCrossFadeInOut(
			glslshader.Flames(glslshader.FlamesPresetAShape(), defAttr, float64(fps)),
			0.0, fadeOutPerc,
			defAttr,
		), true
	case "flamesV":
		return wrapShaderCrossFadeInOut(
			glslshader.Flames(glslshader.FlamesPresetVShape(), defAttr, float64(fps)),
			0.0, fadeOutPerc,
			defAttr,
		), true
	case "inferno":
		return wrapShaderCrossFadeInOut(
			glslshader.Inferno(glslshader.DefaultInfernoParams(), float64(fps)),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "infernoBlue":
		params := glslshader.DefaultInfernoParams()
		params.SwapRedBlue = true
		return wrapShaderCrossFadeInOut(
			glslshader.Inferno(params, float64(fps)),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "burningOnlyRedFlamesLightsOn":
		params := glslshader.BurningPresetGentleOnlyFlames(false, fc)
		return glslshader.Burning(params, defAttr, duration, float64(fps)), true
	case "burningOnlyRedFlamesLightsOff":
		params := glslshader.BurningPresetGentleOnlyFlames(true, fc)
		return glslshader.Burning(params, defAttr, duration, float64(fps)), true
	case "burningOnlyBlueFlamesLightsOn":
		params := glslshader.BurningPresetGentleOnlyFlames(false, fc)
		params.Colors.SwapRedBlue = true
		return glslshader.Burning(params, defAttr, duration, float64(fps)), true
	case "burningOnlyBlueFlamesLightsOff":
		params := glslshader.BurningPresetGentleOnlyFlames(true, fc)
		params.Colors.SwapRedBlue = true
		return glslshader.Burning(params, defAttr, duration, float64(fps)), true
	case "noise":
		return wrapShaderCrossFadeInOut(
			glslshader.Noise(glslshader.DefaultNoiseParams(), float64(fps)),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "nop":
		return shader.Nop(), true
	case "pulse":
		return shader.Pulse(shader.DefaultPulseParams(), defAttr), true
	case "risingChars":
		return wrapShaderCrossFadeInOut(
			glslshader.RisingChars(glslshader.DefaultRisingCharsParams(), defAttr),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "shine":
		return wrapShaderCrossFadeInOut(
			glslshader.Shine(glslshader.DefaultShineParams(), defAttr),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "shineFrame":
		return wrapShaderCrossFadeInOut(
			glslshader.ShineFrame(glslshader.DefaultShineFrameParams(fc), defAttr),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "pulseFrame":
		return wrapShaderCrossFadeInOut(
			glslshader.PulseFrame(glslshader.DefaultPulseFrameParams(fc), defAttr),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "radarFrame":
		return wrapShaderCrossFadeInOut(
			glslshader.RadarFrame(glslshader.DefaultRadarFrameParams(fc), defAttr),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	case "grayFade":
		return shader.GrayFade(shader.DefaultGrayFadeParams(), defAttr), true
	case "trippy":
		return wrapShaderCrossFadeInOut(
			glslshader.Trippy(glslshader.DefaultTrippyParams(), float64(fps)),
			fadeInPerc, fadeOutPerc,
			defAttr,
		), true
	}
	return nil, false
}

func (r *shaderRunner) init(
	root tui.Handler,
	interrupter term.Interrupter,
	defAttr term.Attributes,
	shutdownShaderCfg shutdownShaderConfig,
	loadingShaderCfg loadingShaderConfig,
	openShaderCfg openShaderConfig,
	fc component.FrameCharSet,
) {
	r.Handler = root
	r.interrupter = interrupter
	r.defAttr = defAttr
	r.shutdownShaderCfg = shutdownShaderCfg
	r.loadingShaderCfg = loadingShaderCfg
	r.openShaderCfg = openShaderCfg
	r.fc = fc

	// Initialize zero shader so we can treat field always as non-nil.
	//
	// Some duration > 0 is passed so the r.shader.done stays true,
	// that's something that happens after rendering any shader and
	// allows us to only use one variable (c.done) to determine if a
	// shader is running or not.
	r.shader = shader.New(r.Handler, shader.Nop(), r.interrupter, defaultShaderFPS, 100*time.Millisecond)
}

func (r *shaderRunner) runShader(s shader.Shader, fps int, duration time.Duration) {
	_ = r.shader.Close()
	r.shader = shader.New(r.Handler, s, r.interrupter, fps, duration)
	r.shader.Resize(r.width, r.height)
}

func (r *shaderRunner) captureOpenShaderCells() {
	if r.width <= 0 || r.height <= 0 || r.Handler == nil {
		r.openShaderCells = nil
		r.openShaderImages = nil
		return
	}
	// Drawn as on screen, so that it draws its pictures rather than what
	// it draws for writers without images.
	ctx := cell.ContextWithPixelSize(context.Background(), r.pixelSize)
	buf := cell.NewBufferWriter(ctx, r.width, r.height)
	_ = buf.Clear(term.Attributes{})
	r.Handler.Draw(buf)
	r.openShaderCells = term.CloneCells(buf.RawCells())
	r.openShaderImages = buf.Images()
}

func (r *shaderRunner) cancel() {
	r.runShader(shader.Nop(), defaultShaderFPS, 100*time.Millisecond)
}

// runInitShader runs the init shader, reporting false when there is none.
func (r *shaderRunner) runInitShader() bool {
	if r.initShaderCfg.shader == nil {
		return false
	}
	r.runShader(r.initShaderCfg.shader(r.defAttr, r.fc), r.initShaderCfg.fps, r.initShaderCfg.duration)
	return true
}

func (r *shaderRunner) runShutdownShader() {
	r.runShader(
		r.shutdownShaderCfg.shader(r.defAttr),
		r.shutdownShaderCfg.fps,
		r.shutdownShaderCfg.duration)
}

func (r *shaderRunner) startLoading() {
	r.openShaderCells = nil
	r.openShaderImages = nil
	r.loadingShaderInst = nil
	if r.loadingShaderCfg.shader == nil {
		return
	}
	loading := r.loadingShaderCfg.shader(r.defAttr)
	r.loadingShaderInst = loading
	duration := r.loadingShaderCfg.duration
	if duration <= 0 {
		duration = defaultLoadingShaderDuration
	}
	r.runShader(loading, r.loadingShaderCfg.fps, duration)
}

func (r *shaderRunner) stopLoading() {
	openShaderCells := r.openShaderCells
	openShaderImages := r.openShaderImages
	r.openShaderCells = nil
	r.openShaderImages = nil
	loadingShaderInst := r.loadingShaderInst
	r.loadingShaderInst = nil
	if r.loadingShaderCfg.shader == nil {
		return
	}
	if r.openShaderCfg.shader == nil {
		r.cancel()
		return
	}
	openShader := r.openShaderCfg.shader(r.defAttr)
	if sh, ok := openShader.(shader.Reveal); ok {
		if len(openShaderCells) > 0 {
			sh.SetInitialCells(openShaderCells)
			sh.SetInitialImages(openShaderImages)
		}
		if gf, ok := loadingShaderInst.(*shader.GrayFadeShader); ok {
			sh.SetInitialDesaturation(gf.Progress())
		}
	}
	duration := r.openShaderCfg.duration
	if duration <= 0 {
		duration = defaultOpenShaderDuration
	}
	r.runShader(openShader, r.openShaderCfg.fps, duration)
}

func (r *shaderRunner) Draw(w term.Writer) {
	r.pixelSize, _ = cell.PixelSizeFromContext(w.Context())
	r.shader.Draw(w)
}

func (r *shaderRunner) Resize(width, height int) {
	r.width = width
	r.height = height
	r.shader.Resize(width, height)
}

func (r *shaderRunner) Close() error {
	return r.shader.Close()
}

func wrapShaderCrossFadeInOut(
	sh shader.Shader, fadeInPerc, fadeOutPerc float64,
	defaultAttr term.Attributes,
) shader.Shader {
	return shader.TransitionCrossFade(
		shader.TransitionCrossFadeParams{
			ChangeAtPerc: fadeInPerc,
			OverlapPerc:  fadeInPerc,
		},
		defaultAttr,
		shader.Nop(),
		shader.TransitionCrossFade(
			shader.TransitionCrossFadeParams{
				ChangeAtPerc: 1.0 - fadeOutPerc,
				OverlapPerc:  fadeOutPerc,
			},
			defaultAttr,
			sh,
			shader.Nop(),
		),
	)
}
