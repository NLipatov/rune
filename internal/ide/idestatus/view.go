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

package idestatus

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/unstablebuild/rune-go-sdk/api/workspaceapi"
	"github.com/unstablebuild/rune-go-sdk/component"
	"github.com/unstablebuild/rune-go-sdk/term"
	"github.com/unstablebuild/rune-go-sdk/term/graphemecluster"
	"github.com/unstablebuild/rune-go-sdk/tui"
	"unstable.build/rune/internal/browser"
	"unstable.build/rune/internal/debug"
	"unstable.build/rune/internal/term/programstatus"
)

// InboxStyle styles the chrome of the status inbox. Colors left at the
// default keep those of what they are drawn over.
type InboxStyle struct {
	// SelectedAttr styles the selected item, and HoverAttr the one under
	// the mouse, all of it: their colors win over those of its text, and
	// SelectedAttr's over HoverAttr's on an item both style. Marker is
	// drawn down the left of the selected item in MarkerAttr, over them.
	Marker                              rune
	MarkerAttr, SelectedAttr, HoverAttr term.Attributes
	// WorkspaceAttr styles the name of the workspace above the name of
	// an item, and WorkspaceHighlightAttr that of the item selected or
	// under the mouse. Their colors win over the item's.
	WorkspaceAttr, WorkspaceHighlightAttr term.Attributes
	// ProgressBar draws progress bars, their ends included, over
	// ProgressAttr, which keeps its colors over the item's: in
	// ProgressDoneAttr as far as they are done and in
	// ProgressRemainingAttr past that, or in the selection in
	// ProgressRemainingSelectedAttr, which ProgressRemainingAttr would
	// blend into. ProgressLabelAttr styles the percentage on their right
	// as the item's text.
	ProgressBar                                           component.ProgressBarCharSet
	ProgressAttr, ProgressDoneAttr, ProgressRemainingAttr term.Attributes
	ProgressRemainingSelectedAttr, ProgressLabelAttr      term.Attributes
	// MoreBelow is drawn in MoreBelowAttr in the row below the items when
	// there are more out of sight below.
	MoreBelow     rune
	MoreBelowAttr term.Attributes
	// Frame draws the line between the programs and the preview in
	// DividerAttr, and PlaceholderAttr the text that stands in for them
	// when there is nothing to show.
	Frame                        component.FrameUnionCharSet
	DividerAttr, PlaceholderAttr term.Attributes
}

// DefaultInboxStyle returns the style of an unconfigured inbox.
func DefaultInboxStyle() InboxStyle {
	return InboxStyle{
		Marker:                 '▌',
		MarkerAttr:             browser.DefaultConfig().FocusTabHighlightAttr,
		SelectedAttr:           term.Attributes{Bg: term.ColorGray},
		HoverAttr:              term.Attributes{Attrs: term.AttrBold},
		WorkspaceAttr:          statusMutedAttr,
		WorkspaceHighlightAttr: term.Attributes{Fg: term.ColorSilver},
		ProgressBar: component.ProgressBarCharSet{
			Left: statusProgressGlyph, Filled: statusProgressGlyph,
			Empty: statusProgressGlyph, Right: statusProgressGlyph,
		},
		ProgressDoneAttr:              term.Attributes{Fg: term.ColorGreen},
		ProgressRemainingAttr:         statusMutedAttr,
		ProgressRemainingSelectedAttr: term.Attributes{Fg: term.ColorSilver},
		MoreBelow:                     statusMoreBelow,
		Frame:                         component.DefaultFrameUnionCharSet(),
		DividerAttr:                   statusMutedAttr,
		PlaceholderAttr:               statusMutedAttr,
	}
}

// Preview is what the inbox shows of a program: Content, drawn at Width
// by Height. Fit, if not nil, sizes Content when it was never sized, as
// it would draw nothing; content shown elsewhere must have none, as
// resizing it would reflow it there. Hidden content, shown nowhere, is
// fit to the preview and drawn whole.
type Preview struct {
	Content       tui.Component
	Width, Height int
	Fit           func(width, height int)
	Hidden        bool
}

// View is the status inbox: a sidebar listing every program that
// reported a status, beside a live preview of the selected one, kept up
// to date until it is closed. The preview draws its content at the
// content's own size, as it looks in its window, cutting the top rows
// and right columns that do not fit, and never sends it input. Use
// Store.View to create one.
type View struct {
	store   *Store
	entries []statusEntry
	sel     statusRef
	// selIdx is where sel was last found, for the selection to stay in
	// place when its program goes away.
	selIdx int
	top    int
	// scrolled stops the sidebar from keeping the selection in sight
	// once the wheel scrolls it, until the selection moves.
	scrolled bool
	rows     []int
	hover    int
	// drawing stops a preview that draws the view, as what the view
	// floats over does, from recursing.
	drawing bool
	// ticker wakes the host while the view is open, for the spinners to
	// turn and the preview to follow a workspace out of focus.
	ticker *statusTicker
	// fitted is the size hidden content was last fit to, for it to be
	// resized only as the preview changes size.
	fitted        statusFit
	minWidth      func() int
	width, height int
}

// Select selects uri in the workspace at ws, and reports whether the
// view lists it. It leaves the selection alone when it does not.
func (v *View) Select(ws, uri workspaceapi.URI) bool {
	ref := statusRef{ws: ws.String(), uri: uri.String()}
	i := slices.IndexFunc(v.entries, func(e statusEntry) bool { return e.ref() == ref })
	v.selectAt(i)
	return i >= 0
}

// Resize sizes the view, and fits the previewed content to the preview
// when that content was never sized or is hidden.
func (v *View) Resize(width, height int) {
	v.width, v.height = width, height
	v.sizePreview()
}

// Draw draws the view. Called again from within its own Draw, as by a
// preview of what the view floats over, it draws nothing.
func (v *View) Draw(w term.Writer) {
	if v.drawing {
		return
	}
	v.drawing = true
	defer func() { v.drawing = false }()
	sideW := v.sidebarWidth()
	v.drawSidebar(&component.VirtualWriter{Writer: w, Width: sideW, Height: v.height}, sideW)
	if sideW >= v.width {
		return
	}
	divider := term.NewCell(v.store.style.Frame.VerticalLeft, 1, v.store.style.DividerAttr)
	for y := range v.height {
		w.SetCell(term.Coordinates{X: sideW, Y: y}, divider)
	}
	x := sideW + statusDividerWidth
	if x >= v.width {
		return
	}
	v.drawPreview(&component.VirtualWriter{
		Writer: w, Offset: term.Coordinates{X: x}, Width: v.width - x, Height: v.height,
	}, v.width-x, v.height)
}

// Handle moves the selection, wrapping around, with the arrows, j and
// k, or ctrl with j, k, n and p; enter brings the selected program in
// sight and exits; delete and backspace close it, asking first while it
// is working or blocked; esc, q and ctrl-c exit. A click selects the
// program under it, and the wheel scrolls the sidebar without moving
// the selection.
func (v *View) Handle(ev term.Event) (exit, handled bool) {
	if ev.Type == term.EventMouse {
		return false, v.handleMouse(ev)
	}
	if ev.Type != term.EventKey {
		return false, false
	}
	switch ev.Key {
	case term.KeyEsc:
		return true, true
	case term.KeyEnter:
		v.choose()
		return true, true
	case term.KeyArrowUp:
		v.move(-1)
		return false, true
	case term.KeyArrowDown:
		v.move(1)
		return false, true
	case term.KeyDelete, term.KeyBackspace:
		v.closeSelected()
		return false, true
	}
	if ev.Mod == term.ModCtrl {
		switch ev.Ch {
		case 'j', 'n':
			v.move(1)
			return false, true
		case 'k', 'p':
			v.move(-1)
			return false, true
		case 'c':
			return true, true
		}
	}
	switch ev.Ch {
	case 'j':
		v.move(1)
		return false, true
	case 'k':
		v.move(-1)
		return false, true
	case 'q':
		return true, true
	}
	return false, false
}

// Cursor reports no cursor.
func (v *View) Cursor() (term.Coordinates, term.CursorStyle, bool) {
	return term.Coordinates{}, term.CursorStyleDefault, false
}

// Selection returns the name of the selected program, or false when
// nothing is listed.
func (v *View) Selection() (string, bool) {
	i := v.resolve()
	if i < 0 {
		return "", false
	}
	return v.entries[i].name, true
}

// Close stops updating the view and turning its spinners. It returns
// nil.
func (v *View) Close() error {
	delete(v.store.views, v)
	v.setTicking(false)
	return nil
}

// Dimensions returns the size the view asks for: the sidebar beside the
// previewed content at its own size, at least as wide as minWidth asks
// and 40 rows high.
func (v *View) Dimensions() (int, int) {
	sideW, sideH := v.sidebarSize()
	w, h := v.previewSize()
	width := sideW + statusDividerWidth + w
	if v.minWidth != nil {
		width = max(width, v.minWidth())
	}
	return width, max(sideH, h, statusMinHeight)
}

const (
	statusSubSeparator  = " · "
	statusViewEmpty     = "No program reported a status"
	statusPreviewNone   = "No preview"
	statusPreviewWidth  = 80
	statusPreviewHeight = 21
	statusSidebarMin    = 24
	statusSidebarMax    = 40
	statusDividerWidth  = 1
	// statusMinHeight keeps the inbox from changing size as programs
	// come and go.
	statusMinHeight = 40
	statusPadTop    = 1
	statusPadLeft   = 1
	// statusPadRight is twice statusPadTop, as cells are about twice as
	// tall as wide, for the selection to look as far from the preview as
	// from the top.
	statusPadRight     = 2
	statusSelectionPad = 1
	statusIconX        = statusPadLeft + 2
	statusTextX        = statusIconX + 2
	statusGap          = 1
	// statusProgressGlyph, BLOCK SEXTANT-34 (🬋), fills the middle third
	// of a cell edge to edge: a run of them draws one thick bar centered
	// on the text, which colors alone split.
	statusProgressGlyph = '\U0001FB0B'
	// statusMoreBelow is nf-fa-arrow_down, as tutorials point at the rest
	// of a step with.
	statusMoreBelow = '\uf063'
)

var statusMutedAttr = term.Attributes{Fg: term.ColorGray}

func (v *View) set(entries []statusEntry) {
	v.entries = entries
	v.resolve()
	v.setTicking(len(entries) > 0)
	v.sizePreview()
}

func (v *View) resolve() int {
	if len(v.entries) == 0 {
		return -1
	}
	if i := slices.IndexFunc(v.entries, func(e statusEntry) bool { return e.ref() == v.sel }); i >= 0 {
		v.selIdx = i
		return i
	}
	v.selIdx = min(max(v.selIdx, 0), len(v.entries)-1)
	v.sel = v.entries[v.selIdx].ref()
	return v.selIdx
}

func (v *View) selectAt(i int) {
	if i < 0 || i >= len(v.entries) {
		return
	}
	v.sel, v.selIdx = v.entries[i].ref(), i
	v.scrolled = false
	v.fitted = statusFit{}
	v.sizePreview()
}

func (v *View) move(delta int) {
	n := len(v.entries)
	if n == 0 {
		return
	}
	v.selectAt(((v.resolve()+delta)%n + n) % n)
}

func (v *View) setTicking(ticking bool) {
	if ticking == (v.ticker != nil) {
		return
	}
	if !ticking {
		v.ticker.cancel()
		v.ticker = nil
		return
	}
	if v.store.interrupter == nil {
		return
	}
	fps := v.store.cfg.SpinnerFPS
	if fps <= 0 {
		fps = browser.DefaultTabStatusConfig().SpinnerFPS
	}
	v.ticker = newStatusTicker(v.store.interrupter, fps)
}

func (v *View) spinnerFrame() (string, bool) {
	frames := v.store.cfg.Spinner
	if v.ticker == nil || len(frames) == 0 {
		return "", false
	}
	return string(frames[int(v.ticker.epoch.Load()%int64(len(frames)))]), true
}

func (v *View) entryHeights() []int {
	ret := make([]int, len(v.entries))
	for i, e := range v.entries {
		ret[i] = e.height()
	}
	return ret
}

func (v *View) sidebarSize() (int, int) {
	if len(v.entries) == 0 {
		w := statusIconX + graphemecluster.StringWidth(statusViewEmpty) + statusPadRight
		return min(max(w, statusSidebarMin), statusSidebarMax), statusPadTop + 1
	}
	w := 0
	for _, e := range v.entries {
		for _, s := range []string{e.wsName, e.name, e.sub} {
			w = max(w, statusTextX+graphemecluster.StringWidth(s))
		}
	}
	return min(max(w+statusSelectionPad+statusPadRight, statusSidebarMin), statusSidebarMax),
		statusPadTop + sectionHeight(v.entryHeights())
}

func (v *View) sidebarWidth() int {
	w, _ := v.sidebarSize()
	return min(w, v.width)
}

func (v *View) preview() (Preview, bool) {
	i := v.resolve()
	if i < 0 {
		return Preview{}, false
	}
	return v.store.host.Preview(v.entries[i].ws, v.entries[i].uri)
}

func (v *View) previewSize() (int, int) {
	p, ok := v.preview()
	switch {
	case !ok:
		return graphemecluster.StringWidth(statusPreviewNone) + 4, 0
	case p.Hidden, p.Width == 0 && p.Height == 0:
		return statusPreviewWidth, statusPreviewHeight
	}
	return p.Width, p.Height
}

// sizePreview must run whenever the selection or the size changes.
func (v *View) sizePreview() {
	p, ok := v.preview()
	if !ok || p.Fit == nil {
		return
	}
	width := v.width - v.sidebarWidth() - statusDividerWidth
	if width <= 0 || v.height <= 0 {
		return
	}
	switch fit := (statusFit{ref: v.sel, width: width, height: v.height}); {
	case p.Hidden:
		if v.fitted != fit {
			v.fitted = fit
			p.Fit(width, v.height)
		}
	case p.Width == 0 && p.Height == 0:
		p.Fit(width, v.height)
	}
}

func (v *View) drawSidebar(w term.Writer, width int) {
	v.rows = slices.Repeat([]int{-1}, v.height)
	style := v.store.style
	if len(v.entries) == 0 {
		statusText(w, statusIconX, statusPadTop, statusViewEmpty, style.PlaceholderAttr, width-statusPadRight)
		return
	}
	sel := v.resolve()
	heights := v.entryHeights()
	rows, arrow := v.listRows(heights)
	follow := sel
	if v.scrolled {
		follow = -1
	}
	v.top = statusScroll(v.top, follow, heights, rows)
	selEnd := width - statusPadRight
	spans := v.layout(heights)
	// A program cut short stops above the arrow's row.
	list := &component.VirtualWriter{Writer: w, Width: width, Height: statusPadTop + rows}
	for _, s := range spans {
		var base term.Attributes
		if s.i == v.hover {
			base = style.HoverAttr
		}
		if s.i == sel {
			base = term.AttributesUnion(base, style.SelectedAttr)
		}
		if s.i == v.hover || s.i == sel {
			blank := term.NewCell(' ', 1, base)
			for dy := range s.n {
				for x := statusPadLeft; x < selEnd; x++ {
					list.SetCell(term.Coordinates{X: x, Y: s.y + dy}, blank)
				}
			}
		}
		v.drawEntry(list, s.i, s.y, width, base, s.i == sel)
		if s.i != sel {
			continue
		}
		for dy := range s.n {
			statusText(list, statusPadLeft, s.y+dy, string(style.Marker),
				term.AttributesUnion(base, style.MarkerAttr), statusIconX)
		}
	}
	if !arrow {
		return
	}
	if last := spans[len(spans)-1]; last.i < len(heights)-1 || last.n < heights[last.i] {
		statusText(w, statusPadLeft+(selEnd-statusPadLeft-1)/2, statusPadTop+rows,
			string(style.MoreBelow), style.MoreBelowAttr, selEnd)
	}
}

func (v *View) listRows(heights []int) (int, bool) {
	rows := v.height - statusPadTop
	// The arrow's row is kept while the programs do not all fit, however
	// far the sidebar is scrolled, for them not to shift as the arrow
	// comes and goes.
	if rows > 1 && sectionHeight(heights) > rows {
		return rows - 1, true
	}
	return rows, false
}

func (v *View) layout(heights []int) []statusSpan {
	rows, _ := v.listRows(heights)
	spans := statusLayout(v.top, heights, statusPadTop+rows)
	v.rows = slices.Repeat([]int{-1}, v.height)
	for _, s := range spans {
		for dy := range s.n {
			v.rows[s.y+dy] = s.i
		}
	}
	return spans
}

func (v *View) scroll(down bool) bool {
	heights := v.entryHeights()
	rows, _ := v.listRows(heights)
	top := min(v.top, max(len(heights)-1, 0))
	switch {
	case down && top < len(heights)-1 && sectionHeight(heights[top:]) > rows:
		top++
	case !down && top > 0:
		top--
	default:
		return false
	}
	v.top, v.scrolled = top, true
	v.layout(heights)
	return true
}

func (v *View) drawEntry(w term.Writer, i, y, width int, base term.Attributes, selected bool) {
	e := v.entries[i]
	style := v.store.style
	// Short of room, text keeps clear of the padding, as it does when it
	// fits, and never splits a wide character there.
	textEnd := width - statusPadRight - statusSelectionPad
	if e.wsName != "" {
		ws := style.WorkspaceAttr
		if selected || i == v.hover {
			ws = style.WorkspaceHighlightAttr
		}
		statusText(w, statusTextX, y, e.wsName, term.AttributesUnion(base, ws), textEnd)
		y++
	}
	icon := e.icon
	if e.state == programstatus.Working {
		if frame, ok := v.spinnerFrame(); ok {
			icon = frame
		}
	}
	statusText(w, statusIconX, y, icon, term.AttributesUnion(e.iconAttr, base), statusTextX)
	statusText(w, statusTextX, y, e.name, term.AttributesUnion(e.nameAttr, base), textEnd)
	statusText(w, statusTextX, y+1, e.sub, term.AttributesUnion(e.subAttr, base), textEnd)
	if e.progress < 0 {
		return
	}
	bar := term.AttributesUnion(base, style.ProgressAttr)
	remaining := style.ProgressRemainingAttr
	if selected {
		remaining = style.ProgressRemainingSelectedAttr
	}
	statusProgress(w, statusTextX, y+2, textEnd-statusTextX, e.progress, style.ProgressBar,
		term.AttributesUnion(bar, style.ProgressDoneAttr), term.AttributesUnion(bar, remaining),
		term.AttributesUnion(style.ProgressLabelAttr, base))
}

func (v *View) drawPreview(w term.Writer, width, height int) {
	p, ok := v.preview()
	if ok && p.Hidden {
		p.Content.Draw(&component.VirtualWriter{Writer: w, Width: width, Height: height})
		return
	}
	if !ok || p.Width == 0 || p.Height == 0 {
		s := component.NewStringWithConfig(statusPreviewNone, component.StringConfig{
			Alignment:  component.AlignmentCentered,
			Attributes: v.store.style.PlaceholderAttr,
		})
		s.Resize(width, height)
		s.Draw(w)
		return
	}
	// The bottom of a program, such as its prompt, matters the most: the
	// top rows are the ones cut.
	p.Content.Draw(&component.VirtualWriter{
		Writer: w,
		Offset: term.Coordinates{Y: -max(p.Height-height, 0)},
		Width:  p.Width,
		Height: p.Height,
	})
}

func (v *View) handleMouse(ev term.Event) bool {
	inSidebar := ev.MouseX >= 0 && ev.MouseX < v.sidebarWidth() && ev.MouseY >= 0
	handled := true
	switch ev.Key {
	case 0, term.MouseLeft:
	case term.MouseWheelDown, term.MouseWheelUp:
		handled = inSidebar && v.scroll(ev.Key == term.MouseWheelDown)
	default:
		return false
	}
	v.hover = -1
	if inSidebar && ev.MouseY < len(v.rows) {
		v.hover = v.rows[ev.MouseY]
	}
	if ev.Key == term.MouseLeft {
		v.selectAt(v.hover)
	}
	return handled
}

func (v *View) choose() {
	i := v.resolve()
	if i < 0 {
		return
	}
	e := v.entries[i]
	// The window of the view closes once Handle returns: show it on the
	// next tick, for the window to close in the workspace that opened it.
	_ = v.store.schedule(func() { v.store.host.Show(e.ws, e.uri) })
}

func (v *View) closeSelected() {
	i := v.resolve()
	if i < 0 {
		return
	}
	e := v.entries[i]
	host := v.store.host
	closeIt := func() { host.Close(e.ws, e.uri) }
	// On the next tick, as closing may close windows of the browser that
	// is dispatching to the view.
	switch e.state {
	case programstatus.Working, programstatus.Blocked:
		// Closing would interrupt it.
		_ = v.store.schedule(func() { host.Confirm(statusCloseMessage(e), closeIt) })
	default:
		_ = v.store.schedule(closeIt)
	}
}

type statusEntry struct {
	ws, uri workspaceapi.URI
	wsName  string
	state   programstatus.State
	// status is everything reported, for the user to know what closing
	// it would interrupt.
	status                      programstatus.Summary
	icon                        string
	name                        string
	sub                         string
	progress                    int
	nameAttr, iconAttr, subAttr term.Attributes
}

func (e statusEntry) height() int {
	ret := 2
	if e.wsName != "" {
		ret++
	}
	if e.progress >= 0 {
		ret++
	}
	return ret
}

func (e statusEntry) ref() statusRef {
	return statusRef{ws: e.ws.String(), uri: e.uri.String()}
}

type statusInbox struct {
	cfg browser.TabStatusConfig
}

func (in statusInbox) entries(items []Item) []statusEntry {
	items = slices.Clone(items)
	slices.SortStableFunc(items, func(a, b Item) int {
		return cmp.Or(
			programstatus.Precedence(b.Status.State)-programstatus.Precedence(a.Status.State),
			a.Slot-b.Slot,
			strings.Compare(a.Name, b.Name))
	})
	ret := make([]statusEntry, 0, len(items))
	for _, it := range items {
		ret = append(ret, in.entry(it))
	}
	return ret
}

func (in statusInbox) entry(it Item) statusEntry {
	st := it.Status
	app := cmp.Or(st.App, it.App)
	name := it.Name
	if (name == "" || name == detachedStatusLabel) && app != "" {
		name = app
	}
	// The title and the message would bury the name: the preview shows
	// them.
	sub := st.State.String()
	if app != "" && app != name {
		sub += statusSubSeparator + app
	}
	nameAttr, iconAttr := in.cfg.ContentTab.BarAttrs(st)
	subAttr := nameAttr
	if subAttr == (term.Attributes{}) {
		subAttr = iconAttr
	}
	progress := -1
	if st.State == programstatus.Working && st.Progress >= 0 {
		progress = min(st.Progress, 100)
	}
	return statusEntry{
		ws:       it.WS,
		uri:      it.URI,
		wsName:   it.WorkspaceName,
		state:    st.State,
		status:   st,
		icon:     in.glyph(st.State, st.Kind, it.Icon),
		name:     name,
		sub:      sub,
		progress: progress,
		nameAttr: nameAttr,
		iconAttr: iconAttr,
		subAttr:  subAttr,
	}
}

func (in statusInbox) glyph(state programstatus.State, kind programstatus.Kind, fallback rune) string {
	var icon rune
	switch state {
	case programstatus.Working:
		if len(in.cfg.Spinner) > 0 {
			icon = in.cfg.Spinner[0]
		}
	case programstatus.Blocked:
		icon = in.cfg.BlockedIcons[kind]
		if icon == 0 {
			icon = in.cfg.BlockedIcons[programstatus.KindNone]
		}
	case programstatus.Done:
		icon = in.cfg.DoneIcon
	case programstatus.Error:
		icon = in.cfg.ErrorIcon
	}
	if icon == 0 {
		icon = fallback
	}
	// No icon leaves the cell empty: the state is spelled out beneath the
	// name, and no word fits the one cell of an icon.
	if icon == 0 {
		return ""
	}
	return string(icon)
}

type statusFit struct {
	ref           statusRef
	width, height int
}

type statusRef struct {
	ws, uri string
}

type statusSpan struct{ i, y, n int }

type statusTicker struct {
	epoch  atomic.Int64
	cancel context.CancelFunc
}

func newStatusTicker(interrupter term.Interrupter, fps int) *statusTicker {
	ctx, cancel := context.WithCancel(context.Background())
	t := &statusTicker{cancel: cancel}
	go debug.CapturePanicReport(func() {
		ticker := time.NewTicker(time.Second / time.Duration(fps))
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.epoch.Add(1)
				_ = interrupter.Interrupt(ctx)
			case <-ctx.Done():
				return
			}
		}
	})
	return t
}

func sectionHeight(heights []int) int {
	ret := 0
	for i, h := range heights {
		if i > 0 {
			ret += statusGap
		}
		ret += h
	}
	return ret
}

func statusLayout(top int, heights []int, height int) []statusSpan {
	var ret []statusSpan
	y := statusPadTop
	for i := top; i < len(heights) && y < height; i++ {
		ret = append(ret, statusSpan{i: i, y: y, n: min(heights[i], height-y)})
		y += heights[i] + statusGap
	}
	return ret
}

func statusScroll(top, sel int, heights []int, rows int) int {
	top = min(top, max(len(heights)-1, 0))
	if sel >= 0 && sel < len(heights) {
		top = min(top, sel)
		for top < sel && sectionHeight(heights[top:sel+1]) > rows {
			top++
		}
	}
	// With room below the last item, scroll back up rather than leave
	// rows empty while items above are hidden.
	for top > 0 && sectionHeight(heights[top-1:]) <= rows {
		top--
	}
	return top
}

func statusText(w term.Writer, x, y int, s string, attr term.Attributes, end int) int {
	lines := term.StringToCells(s)
	if len(lines) == 0 {
		return x
	}
	for _, c := range lines[0] {
		cw := max(int(c.Width), 1)
		if x+cw > end {
			break
		}
		c.SetAttributes(attr)
		w.SetCell(term.Coordinates{X: x, Y: y}, c)
		x += cw
	}
	return x
}

func statusProgress(
	w term.Writer, x, y, width, pct int, chars component.ProgressBarCharSet, done, remaining, label term.Attributes,
) {
	// Padded, the bar keeps its length as the percentage grows.
	text := fmt.Sprintf("%3d%%", pct)
	barW := width - 1 - len(text)
	if barW < 2 {
		statusText(w, x, y, strconv.Itoa(pct)+"%", label, x+width)
		return
	}
	// Rounding down keeps the bar from looking full before it is.
	filled := pct * barW / 100
	for i := range barW {
		r, attr := chars.Empty, remaining
		if i < filled {
			r, attr = chars.Filled, done
		}
		// The ends keep the colors of the cells they stand in, for a bar
		// of one glyph all along to read as one.
		switch i {
		case 0:
			r = chars.Left
		case barW - 1:
			r = chars.Right
		}
		w.SetCell(term.Coordinates{X: x + i, Y: y}, term.NewCell(r, 1, attr))
	}
	statusText(w, x+barW+1, y, text, label, x+width)
}

func statusCloseMessage(e statusEntry) string {
	var b strings.Builder
	b.WriteString("Are you sure you want to close `")
	b.WriteString(e.name)
	b.WriteString("`")
	if e.wsName != "" {
		b.WriteString(" in `")
		b.WriteString(e.wsName)
		b.WriteString("`")
	}
	b.WriteString("? It is ")
	b.WriteString(e.state.String())
	b.WriteString(".\n")
	for _, rec := range e.status.Records {
		b.WriteString("\n- ")
		b.WriteString(rec.State.String())
		if rec.App != "" {
			b.WriteString(statusSubSeparator)
			b.WriteString(rec.App)
		}
		if rec.ID != "" {
			b.WriteString(" (")
			b.WriteString(rec.ID)
			b.WriteString(")")
		}
		var text []string
		if rec.Title != "" {
			text = append(text, rec.Title)
		}
		if rec.Msg != "" {
			text = append(text, rec.Msg)
		}
		if rec.Progress >= 0 {
			text = append(text, strconv.Itoa(min(rec.Progress, 100))+"%")
		}
		if len(text) > 0 {
			b.WriteString(": ")
			b.WriteString(strings.Join(text, " — "))
		}
	}
	return b.String()
}
