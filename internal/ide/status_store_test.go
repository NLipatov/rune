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
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/unstablebuild/rune-go-sdk/api/browserapi"
	"github.com/unstablebuild/rune-go-sdk/api/workspaceapi"
	"github.com/unstablebuild/rune-go-sdk/component"
	"github.com/unstablebuild/rune-go-sdk/handler"
	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/browser"
	"unstable.build/rune/internal/cell"
	"unstable.build/rune/internal/handler/handlertest"
	"unstable.build/rune/internal/ide/idehistory"
	"unstable.build/rune/internal/term/programstatus"
)

func TestStatusInboxEntries(t *testing.T) {
	parse := func(s string) workspaceapi.URI {
		uri, err := workspaceapi.ParseURI(s)
		require.NoError(t, err)
		return uri
	}
	worker := parse("rune-agent://model/worker")
	build := parse("rune-agent://model/build")
	agent := parse("rune-agent://model/agent")
	idle := parse("rune-agent://model/idle")
	waiting := parse("rune-agent://model/waiting")
	pty := parse("file:///dev/ttys042")
	in := statusInbox{cfg: browser.TabStatusConfig{
		ContentTab: browser.TabStatusStyle{
			BlockedAttr:     term.Attributes{Fg: term.ColorYellow},
			BlockedIconAttr: term.Attributes{Fg: term.ColorRed},
			ErrorAttr:       term.Attributes{Fg: term.ColorRed},
			DoneIconAttr:    term.Attributes{Fg: term.ColorSilver},
			IdleIconAttr:    term.Attributes{Fg: term.ColorBlue},
			WorkingIconAttr: term.Attributes{Fg: term.ColorBlue},
		},
		WorkspaceTab: browser.TabStatusStyle{
			BlockedAttr: term.Attributes{Fg: term.ColorPurple},
		},
	}}

	assert.Empty(t, in.entries(nil))

	reported := func(state programstatus.State) []programstatus.Record {
		return []programstatus.Record{{Report: programstatus.Report{State: state, Progress: -1}}}
	}
	sources := []statusItem{
		{slot: 0, wsName: "1 home", uri: worker, name: "worker", icon: 'w', status: programstatus.Summary{
			State: programstatus.Working, Progress: 40, Title: "cargo build",
		}},
		{slot: 1, wsName: "2 proj", uri: build, name: "build", status: programstatus.Summary{
			State: programstatus.Error, Progress: -1, Msg: "exit status 2", App: "build",
		}},
		{slot: 1, wsName: "2 proj", uri: agent, name: "agent", icon: 'a', status: programstatus.Summary{
			State: programstatus.Blocked, Kind: programstatus.KindPermission, Progress: -1,
			App: "claude", Msg: "run make test?",
			Records: []programstatus.Record{
				{Report: programstatus.Report{State: programstatus.Working, Progress: -1, App: "claude"}},
				{Report: programstatus.Report{
					ID: "tool/bash", State: programstatus.Blocked,
					Kind: programstatus.KindPermission, Progress: -1, App: "claude", Msg: "run make test?",
				}},
			},
		}},
		{slot: 1, wsName: "2 proj", uri: idle, name: "idle", app: "rune-agent", status: programstatus.Summary{Progress: -1}},
		{slot: 0, wsName: "1 home", uri: waiting, name: "waiting", icon: 'z', status: programstatus.Summary{
			Progress: -1, App: "opencode", Records: reported(programstatus.Idle),
		}},
		{slot: 1, wsName: "2 proj", uri: pty, name: detachedStatusLabel, status: programstatus.Summary{
			State: programstatus.Done, Progress: -1, Msg: "ok", App: "make",
		}},
	}
	entries := in.entries(sources)
	type row struct {
		uri                         workspaceapi.URI
		icon, name, sub             string
		progress                    int
		nameAttr, iconAttr, subAttr term.Attributes
	}
	var rows []row
	for _, e := range entries {
		rows = append(rows, row{e.uri, e.icon, e.name, e.sub, e.progress, e.nameAttr, e.iconAttr, e.subAttr})
	}
	assert.Equal(t, []row{
		{
			uri: agent, icon: "a", name: "agent", sub: "blocked · claude", progress: -1,
			nameAttr: term.Attributes{Fg: term.ColorYellow}, iconAttr: term.Attributes{Fg: term.ColorRed},
			subAttr: term.Attributes{Fg: term.ColorYellow},
		},
		{
			uri: build, name: "build", sub: "error", progress: -1,
			nameAttr: term.Attributes{Fg: term.ColorRed}, subAttr: term.Attributes{Fg: term.ColorRed},
		},
		{
			uri: pty, name: "make", sub: "done", progress: -1,
			iconAttr: term.Attributes{Fg: term.ColorSilver}, subAttr: term.Attributes{Fg: term.ColorSilver},
		},
		{
			uri: worker, icon: "w", name: "worker", sub: "working", progress: 40,
			iconAttr: term.Attributes{Fg: term.ColorBlue}, subAttr: term.Attributes{Fg: term.ColorBlue},
		},
		{
			uri: waiting, icon: "z", name: "waiting", sub: "idle · opencode", progress: -1,
			iconAttr: term.Attributes{Fg: term.ColorBlue}, subAttr: term.Attributes{Fg: term.ColorBlue},
		},
		{uri: idle, name: "idle", sub: "idle · rune-agent", progress: -1},
	}, rows, "a source is listed once, with its state and app, or the app it last reported")

	t.Run("sources are left untouched", func(t *testing.T) {
		assert.Len(t, sources, 6)
		assert.Equal(t, worker, sources[0].uri)
	})

	t.Run("ties sort by slot then name", func(t *testing.T) {
		working := programstatus.Summary{State: programstatus.Working, Progress: -1}
		got := in.entries([]statusItem{
			{slot: 1, uri: build, name: "zeta", status: working},
			{slot: 1, uri: agent, name: "alpha", status: working},
			{slot: 0, uri: worker, name: "omega", status: working},
		})
		var names []string
		for _, e := range got {
			names = append(names, e.name)
		}
		assert.Equal(t, []string{"omega", "alpha", "zeta"}, names)
	})
}

func TestStatusInboxGlyph(t *testing.T) {
	in := statusInbox{cfg: browser.TabStatusConfig{
		Spinner:   []rune("⠋⠙"),
		DoneIcon:  '✓',
		ErrorIcon: '✗',
		BlockedIcons: map[programstatus.Kind]rune{
			programstatus.KindNone:       '!',
			programstatus.KindPermission: '?',
		},
	}}
	for _, tc := range []struct {
		name     string
		in       statusInbox
		state    programstatus.State
		kind     programstatus.Kind
		fallback rune
		want     string
	}{
		{name: "working spins", in: in, state: programstatus.Working, fallback: 'x', want: "⠋"},
		{name: "done", in: in, state: programstatus.Done, fallback: 'x', want: "✓"},
		{name: "error", in: in, state: programstatus.Error, want: "✗"},
		{name: "blocked kind", in: in, state: programstatus.Blocked, kind: programstatus.KindPermission, want: "?"},
		{name: "blocked unknown kind falls back", in: in, state: programstatus.Blocked, kind: programstatus.KindAuth, want: "!"},
		{name: "idle keeps the own icon", in: in, state: programstatus.Idle, fallback: 'x', want: "x"},
		{name: "unconfigured keeps the own icon", state: programstatus.Done, fallback: 'x', want: "x"},
		{name: "nothing configured leaves no icon", state: programstatus.Blocked, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.in.glyph(tc.state, tc.kind, tc.fallback))
		})
	}
}

// statusViewTestConfig gives every state an icon one cell wide.
func statusViewTestConfig() browser.TabStatusConfig {
	return browser.TabStatusConfig{
		Spinner:      []rune("⠋⠙"),
		DoneIcon:     '✓',
		ErrorIcon:    '✗',
		BlockedIcons: map[programstatus.Kind]rune{programstatus.KindNone: '!'},
		ContentTab: browser.TabStatusStyle{
			Progress:        true,
			WorkingIconAttr: term.Attributes{Fg: term.ColorYellow},
			WorkingDoneAttr: term.Attributes{Fg: term.ColorGreen},
			BlockedAttr:     term.Attributes{Fg: term.ColorYellow},
			BlockedIconAttr: term.Attributes{Fg: term.ColorRed},
			DoneAttr:        term.Attributes{Fg: term.ColorGreen},
			DoneIconAttr:    term.Attributes{Fg: term.ColorSilver},
		},
	}
}

// statusViewFixture is a store whose programs report in the workspace
// at ws, as detached terminals or as tabs of b, with its inbox v.
type statusViewFixture struct {
	t     *testing.T
	host  *statusStoreTestHost
	store *statusStore
	ws    workspaceapi.URI
	b     *browser.Component
	v     *statusView
}

func newStatusViewFixture(t *testing.T) *statusViewFixture {
	host, store, ws := newStatusStoreTestWith(t, statusViewTestConfig(), nil)
	host.workspaces[ws.String()] = "1 home"
	cfg := browser.DefaultConfig()
	cfg.OnTabStatus = func(uri workspaceapi.URI, _, next programstatus.Summary) {
		store.tabStatusChanged(ws, uri, next)
	}
	cfg.OnTabRemoved = func(uri workspaceapi.URI) { store.tabRemoved(ws, uri) }
	b := browser.NewComponent(cfg)
	t.Cleanup(func() { require.NoError(t, b.Close()) })
	b.Resize(120, 60)
	f := &statusViewFixture{t: t, host: host, store: store, ws: ws, b: b}
	// Closed, a tab or a terminal takes its statuses with it.
	host.onClose = func(key statusKey) {
		if tab, ok := host.tabs[key]; ok {
			require.True(t, b.RemoveTab(tab))
			return
		}
		f.store.updateDetached(f.ws, statusStoreTestURI(t, key.uri), func(s *programstatus.Store) {
			s.Apply(programstatus.Report{State: programstatus.Clear})
		})
	}
	return f
}

// report applies r to the terminal at file:///path, which is no tab.
func (f *statusViewFixture) report(path string, r programstatus.Report) {
	f.store.updateDetached(f.ws, f.uri(path), func(s *programstatus.Store) { s.Apply(r) })
}

func (f *statusViewFixture) clear(path string) {
	f.report(path, programstatus.Report{State: programstatus.Clear})
}

// tab opens a tab named name, never shown, that reports r.
func (f *statusViewFixture) tab(name string, r programstatus.Report) {
	uri := f.uri(name)
	h := &statusPreviewTestHandler{name: name}
	tab := f.b.NewTab(uri, 'x', name, h, h)
	key := statusKey{f.ws.String(), uri.String()}
	f.host.names[key], f.host.tabs[key] = name, tab
	require.NoError(f.t, f.store.setTabStatus(f.ws, statusStoreTestTabs{component: f.b}, uri, r))
}

// preview makes the terminal at file:///path previewed as p.
func (f *statusViewFixture) preview(path string, p statusPreview) {
	f.host.previews[statusKey{f.ws.String(), f.uri(path).String()}] = p
}

func (f *statusViewFixture) uri(path string) workspaceapi.URI {
	return statusStoreTestURI(f.t, "file:///"+path)
}

func statusTestReport(state programstatus.State, app string, progress int) programstatus.Report {
	return programstatus.Report{State: state, App: app, Progress: progress}
}

func statusTestClick(x, y int) term.Event {
	return term.Event{Type: term.EventMouse, Key: term.MouseLeft, MouseX: x, MouseY: y}
}

func statusTestWheel(key term.Key, x, y int) term.Event {
	return term.Event{Type: term.EventMouse, Key: key, MouseX: x, MouseY: y}
}

// statusViewStep acts on the inbox then draws it, at width by height
// when set, or at the size of the previous step.
type statusViewStep struct {
	keys   string
	events []term.Event
	do     func(*statusViewFixture)
	width  int
	height int
	want   string
	// wantSize, when set, is what the inbox asks to be sized to.
	wantSize []int
	// wantShown, when set, are the uris chosen so far.
	wantShown []string
	// wantClosed, when set, are the uris closed so far.
	wantClosed []string
	// wantConfirms, when set, are the questions asked so far.
	wantConfirms []string
}

func TestStatusView(t *testing.T) {
	const (
		blocked = programstatus.Blocked
		working = programstatus.Working
		done    = programstatus.Done
		failed  = programstatus.Error
		idle    = programstatus.Idle
	)
	threeDone := func(f *statusViewFixture) {
		for _, name := range []string{"a", "b", "c"} {
			f.report(name, statusTestReport(done, name, -1))
		}
	}
	for _, tc := range []struct {
		name  string
		setup func(*statusViewFixture)
		steps []statusViewStep
	}{
		{
			name: "nothing reported",
			steps: []statusViewStep{
				{width: 45, height: 2, wantSize: []int{48, statusMinHeight}, want: `
                                 │No preview 
   No program reported a status  │           `[1:]},
				{keys: "jk<down><up>", want: `
                                 │No preview 
   No program reported a status  │           `[1:]},
				{keys: "<enter>", wantShown: []string{}, want: `
                                 │No preview 
   No program reported a status  │           `[1:]},
				{width: 20, height: 3, want: `
                    
   No program repo  
                    `[1:]},
			},
		},
		{
			name: "programs, most pressing first, a row apart",
			setup: func(f *statusViewFixture) {
				f.report("idle", statusTestReport(idle, "vim", -1))
				f.report("done", statusTestReport(done, "make", 60))
				f.tab("agent", programstatus.Report{
					State: working, App: "rune-agent", Progress: 40,
					Title: "Reasoning", Msg: "reading the code",
				})
				f.report("failed", statusTestReport(failed, "", -1))
				f.report("blocked", programstatus.Report{
					State: blocked, App: "claude", Progress: -1,
					Title: "Allow edit?", Msg: "edit main.go",
				})
			},
			steps: []statusViewStep{
				{width: 40, height: 21, wantSize: []int{
					// The widest line, "working · rune-agent", takes 20 cells.
					statusTextX + 20 + statusSelectionPad + statusPadRight + statusDividerWidth + len(statusPreviewNone) + 4,
					statusMinHeight,
				}, want: `
                            │           
 ▌   1 home                 │           
 ▌ ! claude                 │           
 ▌   blocked                │           
                            │           
     1 home                 │           
   ✗ terminal               │           
     error                  │           
                            │           
     1 home                 │           
   ✓ make                   │No preview 
     done                   │           
                            │           
     1 home                 │           
   ⠋ agent                  │           
     working · rune-agent   │           
     🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │           
                            │           
     1 home                 │           
     vim                    │           
     idle                   │           `[1:]},
				// A tab never shown is fit to the preview.
				{keys: "jjj", want: `
                            │agent067890
     1 home                 │agent167890
   ! claude                 │agent267890
     blocked                │agent367890
                            │agent467890
     1 home                 │agent567890
   ✗ terminal               │agent667890
     error                  │agent767890
                            │agent867890
     1 home                 │agent967890
   ✓ make                   │agent107890
     done                   │agent117890
                            │agent127890
 ▌   1 home                 │agent137890
 ▌ ⠋ agent                  │agent147890
 ▌   working · rune-agent   │agent157890
 ▌   🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │agent167890
                            │agent177890
     1 home                 │agent187890
     vim                    │agent197890
     idle                   │agent207890`[1:]},
				{keys: "<enter>", wantShown: []string{"file:///agent"}, want: `
                            │agent067890
     1 home                 │agent167890
   ! claude                 │agent267890
     blocked                │agent367890
                            │agent467890
     1 home                 │agent567890
   ✗ terminal               │agent667890
     error                  │agent767890
                            │agent867890
     1 home                 │agent967890
   ✓ make                   │agent107890
     done                   │agent117890
                            │agent127890
 ▌   1 home                 │agent137890
 ▌ ⠋ agent                  │agent147890
 ▌   working · rune-agent   │agent157890
 ▌   🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │agent167890
                            │agent177890
     1 home                 │agent187890
     vim                    │agent197890
     idle                   │agent207890`[1:]},
			},
		},
		{
			name: "each program beneath the name of its workspace, if any",
			setup: func(f *statusViewFixture) {
				f.report("a", statusTestReport(blocked, "a", -1))
				for _, ws := range []struct{ uri, name, path string }{
					{uri: "file:///other", name: "2 other", path: "b"},
					{uri: "file:///unnamed", path: "c"},
				} {
					uri := statusStoreTestURI(f.t, ws.uri)
					f.host.addWorkspace(uri, ws.name)
					f.store.updateDetached(uri, f.uri(ws.path), func(s *programstatus.Store) {
						s.Apply(statusTestReport(done, ws.path, -1))
					})
				}
			},
			steps: []statusViewStep{{width: 40, height: 11, want: `
                        │               
 ▌   1 home             │               
 ▌ ! a                  │               
 ▌   blocked            │               
                        │               
     2 other            │  No preview   
   ✓ b                  │               
     done               │               
                        │               
   ✓ c                  │               
     done               │               `[1:]}},
		},
		{
			name: "progress",
			setup: func(f *statusViewFixture) {
				f.report("a", statusTestReport(working, "none", -1))
				f.report("b", statusTestReport(working, "zero", 0))
				f.report("c", statusTestReport(working, "full", 100))
				f.report("d", statusTestReport(working, "over", 150))
				f.report("e", statusTestReport(done, "done", 70))
			},
			steps: []statusViewStep{{width: 40, height: 23, want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ done               │               
 ▌   done               │               
                        │               
     1 home             │               
   ⠋ none               │               
     working            │               
                        │               
     1 home             │               
   ⠋ zero               │               
     working            │  No preview   
     🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋   0%   │               
                        │               
     1 home             │               
   ⠋ full               │               
     working            │               
     🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋 100%   │               
                        │               
     1 home             │               
   ⠋ over               │               
     working            │               
     🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋 100%   │               `[1:]}},
		},
		{
			name:  "navigation wraps around both ways",
			setup: threeDone,
			steps: []statusViewStep{
				{width: 40, height: 12, want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ a                  │               
 ▌   done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               `[1:]},
				{keys: "k", want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
 ▌   1 home             │               
 ▌ ✓ c                  │               
 ▌   done               │               `[1:]},
				{keys: "<down>", want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ a                  │               
 ▌   done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               `[1:]},
				{keys: "<c-j>", want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
 ▌   1 home             │  No preview   
 ▌ ✓ b                  │               
 ▌   done               │               
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               `[1:]},
				{keys: "<c-n>", want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
 ▌   1 home             │               
 ▌ ✓ c                  │               
 ▌   done               │               `[1:]},
				{keys: "<c-p><up>", want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ a                  │               
 ▌   done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               `[1:]},
				{keys: "<c-k>", want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
 ▌   1 home             │               
 ▌ ✓ c                  │               
 ▌   done               │               `[1:]},
				{keys: "j", want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ a                  │               
 ▌   done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               `[1:]},
				{keys: "x<left><right>", want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ a                  │               
 ▌   done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               `[1:]},
				{keys: "kk<enter>", wantShown: []string{"file:///b"}, want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
 ▌   1 home             │  No preview   
 ▌ ✓ b                  │               
 ▌   done               │               
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               `[1:]},
				{keys: "<esc>q<c-c>", wantShown: []string{"file:///b"}, want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
 ▌   1 home             │  No preview   
 ▌ ✓ b                  │               
 ▌   done               │               
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               `[1:]},
			},
		},
		{
			name: "short of rows, the selection stays in sight",
			setup: func(f *statusViewFixture) {
				for _, name := range []string{"a", "b", "c", "d", "e"} {
					f.report(name, statusTestReport(done, name, -1))
				}
			},
			steps: []statusViewStep{
				{width: 40, height: 9, want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ a                  │               
 ▌   done               │               
                        │  No preview   
     1 home             │               
   ✓ b                  │               
     done               │               
           ↓            │               `[1:]},
				{keys: "<down><c-j>", want: `
                        │               
     1 home             │               
   ✓ b                  │               
     done               │               
                        │  No preview   
 ▌   1 home             │               
 ▌ ✓ c                  │               
 ▌   done               │               
           ↓            │               `[1:]},
				{keys: "<down><down>", want: `
                        │               
     1 home             │               
   ✓ d                  │               
     done               │               
                        │  No preview   
 ▌   1 home             │               
 ▌ ✓ e                  │               
 ▌   done               │               
                        │               `[1:]},
				{keys: "<up>", want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ d                  │               
 ▌   done               │               
                        │  No preview   
     1 home             │               
   ✓ e                  │               
     done               │               
                        │               `[1:]},
				{keys: "<c-j>", want: `
                        │               
     1 home             │               
   ✓ d                  │               
     done               │               
                        │  No preview   
 ▌   1 home             │               
 ▌ ✓ e                  │               
 ▌   done               │               
                        │               `[1:]},
				{keys: "<down>", want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ a                  │               
 ▌   done               │               
                        │  No preview   
     1 home             │               
   ✓ b                  │               
     done               │               
           ↓            │               `[1:]},
				{keys: "<c-k>", want: `
                        │               
     1 home             │               
   ✓ d                  │               
     done               │               
                        │  No preview   
 ▌   1 home             │               
 ▌ ✓ e                  │               
 ▌   done               │               
                        │               `[1:]},
			},
		},
		{
			name: "the wheel scrolls the sidebar, leaving the selection where it is",
			setup: func(f *statusViewFixture) {
				for _, name := range []string{"a", "b", "c", "d", "e"} {
					f.report(name, statusTestReport(done, name, -1))
				}
			},
			steps: []statusViewStep{
				{width: 40, height: 9, want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ a                  │               
 ▌   done               │               
                        │  No preview   
     1 home             │               
   ✓ b                  │               
     done               │               
           ↓            │               `[1:]},
				{events: []term.Event{statusTestWheel(term.MouseWheelDown, 5, 2)}, want: `
                        │               
     1 home             │               
   ✓ b                  │               
     done               │               
                        │  No preview   
     1 home             │               
   ✓ c                  │               
     done               │               
           ↓            │               `[1:]},
				// No further than the last program.
				{events: []term.Event{
					statusTestWheel(term.MouseWheelDown, 5, 2),
					statusTestWheel(term.MouseWheelDown, 5, 2),
					statusTestWheel(term.MouseWheelDown, 5, 2),
				}, want: `
                        │               
     1 home             │               
   ✓ d                  │               
     done               │               
                        │  No preview   
     1 home             │               
   ✓ e                  │               
     done               │               
                        │               `[1:]},
				{events: []term.Event{statusTestWheel(term.MouseWheelUp, 5, 2)}, want: `
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               
                        │  No preview   
     1 home             │               
   ✓ d                  │               
     done               │               
           ↓            │               `[1:]},
				// Over the preview, it scrolls nothing.
				{events: []term.Event{statusTestWheel(term.MouseWheelDown, 30, 2)}, want: `
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               
                        │  No preview   
     1 home             │               
   ✓ d                  │               
     done               │               
           ↓            │               `[1:]},
				// Moved, the selection comes back in sight.
				{keys: "<down>", want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ b                  │               
 ▌   done               │               
                        │  No preview   
     1 home             │               
   ✓ c                  │               
     done               │               
           ↓            │               `[1:]},
				// No further than the first program.
				{events: []term.Event{
					statusTestWheel(term.MouseWheelUp, 5, 2),
					statusTestWheel(term.MouseWheelUp, 5, 2),
				}, want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │  No preview   
 ▌   1 home             │               
 ▌ ✓ b                  │               
 ▌   done               │               
           ↓            │               `[1:]},
			},
		},
		{
			name: "a program taller than the view",
			setup: func(f *statusViewFixture) {
				f.report("a", statusTestReport(done, "a", -1))
				f.report("b", statusTestReport(working, "b", 50))
			},
			steps: []statusViewStep{
				{width: 40, height: 5, want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ a                  │  No preview   
 ▌   done               │               
           ↓            │               `[1:]},
				{keys: "j", want: `
                        │               
 ▌   1 home             │               
 ▌ ⠋ b                  │  No preview   
 ▌   working            │               
           ↓            │               `[1:]},
				{width: 40, height: 4, want: `
                        │               
 ▌   1 home             │  No preview   
 ▌ ⠋ b                  │               
           ↓            │               `[1:]},
				{keys: "k", want: `
                        │               
 ▌   1 home             │  No preview   
 ▌ ✓ a                  │               
           ↓            │               `[1:]},
				// Too short to keep a row for the arrow.
				{width: 40, height: 2, want: `
                        │  No preview   
 ▌   1 home             │               `[1:]},
			},
		},
		{
			name: "long and wide names",
			setup: func(f *statusViewFixture) {
				f.report("a", statusTestReport(done, strings.Repeat("a", 50), -1))
				f.report("b", statusTestReport(done, strings.Repeat("界", 25), -1))
			},
			steps: []statusViewStep{
				{width: 60, height: 8, wantSize: []int{statusSidebarMax + statusDividerWidth + len(statusPreviewNone) + 4, statusMinHeight}, want: `
                                        │                   
 ▌   1 home                             │                   
 ▌ ✓ aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa   │                   
 ▌   done                               │    No preview     
                                        │                   
     1 home                             │                   
   ✓ 界 界 界 界 界 界 界 界 界 界 界 界 界 界 界 界    │                   
     done                               │                   `[1:]},
			},
		},
		{
			name:  "narrower than the sidebar and the preview",
			setup: threeDone,
			steps: []statusViewStep{
				{width: 10, height: 5, want: `
          
 ▌   1    
 ▌ ✓ a    
 ▌   do   
    ↓     `[1:]},
				{width: statusSidebarMin, height: 5, want: `
                        
 ▌   1 home             
 ▌ ✓ a                  
 ▌   done               
           ↓            `[1:]},
				{width: statusSidebarMin + 1, height: 5, want: `
                        │
 ▌   1 home             │
 ▌ ✓ a                  │
 ▌   done               │
           ↓            │`[1:]},
				{width: statusSidebarMin + 2, height: 5, want: `
                        │ 
 ▌   1 home             │ 
 ▌ ✓ a                  │N
 ▌   done               │ 
           ↓            │ `[1:]},
			},
		},
		{
			name:  "no room at all",
			setup: threeDone,
			steps: []statusViewStep{
				{width: 0, height: 0, keys: "j", want: ""},
				{width: 30, height: 5, want: `
                        │     
 ▌   1 home             │     
 ▌ ✓ b                  │No pr
 ▌   done               │     
           ↓            │     `[1:]},
			},
		},
		{
			name: "programs come and go while it is open",
			setup: func(f *statusViewFixture) {
				f.report("a", statusTestReport(blocked, "a", -1))
				f.report("b", statusTestReport(done, "b", -1))
			},
			steps: []statusViewStep{
				{width: 40, height: 12, keys: "j", want: `
                        │               
     1 home             │               
   ! a                  │               
     blocked            │               
                        │               
 ▌   1 home             │  No preview   
 ▌ ✓ b                  │               
 ▌   done               │               
                        │               
                        │               
                        │               
                        │               `[1:]},
				{do: func(f *statusViewFixture) { f.report("c", statusTestReport(failed, "c", -1)) }, want: `
                        │               
     1 home             │               
   ! a                  │               
     blocked            │               
                        │               
     1 home             │  No preview   
   ✗ c                  │               
     error              │               
                        │               
 ▌   1 home             │               
 ▌ ✓ b                  │               
 ▌   done               │               `[1:]},
				{do: func(f *statusViewFixture) { f.report("b", statusTestReport(working, "b", 10)) }, want: `
                        │               
     1 home             │               
   ✗ c                  │               
     error              │               
                        │               
 ▌   1 home             │  No preview   
 ▌ ⠋ b                  │               
 ▌   working            │               
 ▌   🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  10%   │               
                        │               
                        │               
                        │               `[1:]},
				{do: func(f *statusViewFixture) { f.clear("b") }, want: `
                        │               
     1 home             │               
   ! a                  │               
     blocked            │               
                        │               
 ▌   1 home             │  No preview   
 ▌ ✗ c                  │               
 ▌   error              │               
                        │               
                        │               
                        │               
                        │               `[1:]},
				{do: func(f *statusViewFixture) { f.clear("a"); f.clear("c") }, want: `
                                 │      
   No program reported a status  │      
                                 │      
                                 │      
                                 │      
                                 │No pre
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      `[1:]},
				{do: func(f *statusViewFixture) { f.report("d", statusTestReport(done, "d", -1)) }, want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ d                  │               
 ▌   done               │               
                        │               
                        │  No preview   
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               `[1:]},
			},
		},
		{
			name:  "the mouse selects what it clicks in the sidebar",
			setup: threeDone,
			steps: []statusViewStep{
				{width: 40, height: 12, want: `
                        │               
 ▌   1 home             │               
 ▌ ✓ a                  │               
 ▌   done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               `[1:]},
				// Any row of a program, at any column of the sidebar.
				{events: []term.Event{statusTestClick(5, 7)}, want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
 ▌   1 home             │  No preview   
 ▌ ✓ b                  │               
 ▌   done               │               
                        │               
     1 home             │               
   ✓ c                  │               
     done               │               `[1:]},
				{events: []term.Event{statusTestClick(0, 9)}, want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
 ▌   1 home             │               
 ▌ ✓ c                  │               
 ▌   done               │               `[1:]},
				// A gap, the padding, the preview and other buttons select
				// nothing.
				{events: []term.Event{statusTestClick(5, 8)}, want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
 ▌   1 home             │               
 ▌ ✓ c                  │               
 ▌   done               │               `[1:]},
				{events: []term.Event{statusTestClick(5, 0), statusTestClick(30, 1), statusTestClick(24, 1)}, want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
 ▌   1 home             │               
 ▌ ✓ c                  │               
 ▌   done               │               `[1:]},
				{events: []term.Event{
					{Type: term.EventMouse, Key: term.MouseWheelUp, MouseX: 1, MouseY: 1},
					{Type: term.EventMouse, Key: term.MouseRight, MouseX: 1, MouseY: 1},
				}, want: `
                        │               
     1 home             │               
   ✓ a                  │               
     done               │               
                        │               
     1 home             │  No preview   
   ✓ b                  │               
     done               │               
                        │               
 ▌   1 home             │               
 ▌ ✓ c                  │               
 ▌   done               │               `[1:]},
			},
		},
		{
			name: "previews",
			setup: func(f *statusViewFixture) {
				// Larger than the preview: it loses its top rows and right
				// columns.
				f.report("a", statusTestReport(blocked, "large", -1))
				f.preview("a", statusPreview{
					content: &statusPreviewTestHandler{name: "L", width: 20, height: 6}, width: 20, height: 6,
				})
				// Smaller: drawn whole at its own size.
				f.report("b", statusTestReport(failed, "small", -1))
				f.preview("b", statusPreview{
					content: &statusPreviewTestHandler{name: "S", width: 4, height: 2}, width: 4, height: 2,
				})
				// Shown nowhere: fit to the preview.
				f.report("c", statusTestReport(done, "hidden", -1))
				hidden := &statusPreviewTestHandler{name: "H"}
				f.preview("c", statusPreview{content: hidden, fit: hidden.Resize, hidden: true})
				// Its window closed.
				f.report("d", statusTestReport(working, "gone", -1))
			},
			steps: []statusViewStep{
				{width: 40, height: 5, wantSize: []int{statusSidebarMin + statusDividerWidth + 20, statusMinHeight}, want: `
                        │L12345678901234
 ▌   1 home             │L22345678901234
 ▌ ! large              │L32345678901234
 ▌   blocked            │L42345678901234
           ↓            │L52345678901234`[1:]},
				{keys: "j", wantSize: []int{statusSidebarMin + statusDividerWidth + 4, statusMinHeight}, want: `
                        │S023           
 ▌   1 home             │S123           
 ▌ ✗ small              │               
 ▌   error              │               
           ↓            │               `[1:]},
				{keys: "j", wantSize: []int{statusSidebarMin + statusDividerWidth + statusPreviewWidth, max(statusPreviewHeight, statusMinHeight)}, want: `
                        │H02345678901234
 ▌   1 home             │H12345678901234
 ▌ ✓ hidden             │H22345678901234
 ▌   done               │H32345678901234
           ↓            │H42345678901234`[1:]},
				{keys: "j", want: `
                        │               
 ▌   1 home             │               
 ▌ ⠋ gone               │  No preview   
 ▌   working            │               
                        │               `[1:]},
				// A preview drawing the inbox, as previewing the workspace it
				// floats over would, leaves the nested inbox blank.
				{do: func(f *statusViewFixture) {
					f.preview("d", statusPreview{content: f.v, width: 15, height: 4})
				}, want: `
                        │               
 ▌   1 home             │               
 ▌ ⠋ gone               │               
 ▌   working            │               
                        │               `[1:]},
			},
		},
		{
			name: "delete and backspace close the selected program",
			setup: func(f *statusViewFixture) {
				f.tab("agent", programstatus.Report{
					ID: "tool/edit", State: blocked, Kind: programstatus.KindPermission,
					App: "claude", Progress: -1, Title: "Allow edit?", Msg: "edit main.go",
				})
				f.report("test", statusTestReport(failed, "test", -1))
				f.report("make", statusTestReport(done, "make", -1))
				f.report("build", programstatus.Report{
					State: working, App: "build", Progress: 40, Title: "Compiling",
				})
				f.report("vim", statusTestReport(idle, "vim", -1))
			},
			steps: []statusViewStep{
				{width: 40, height: 21, want: `
                        │agent0678901234
 ▌   1 home             │agent1678901234
 ▌ ! agent              │agent2678901234
 ▌   blocked · claude   │agent3678901234
                        │agent4678901234
     1 home             │agent5678901234
   ✗ test               │agent6678901234
     error              │agent7678901234
                        │agent8678901234
     1 home             │agent9678901234
   ✓ make               │agent1078901234
     done               │agent1178901234
                        │agent1278901234
     1 home             │agent1378901234
   ⠋ build              │agent1478901234
     working            │agent1578901234
     🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │agent1678901234
                        │agent1778901234
     1 home             │agent1878901234
     vim                │agent1978901234
     idle               │agent2078901234`[1:]},
				// Done with, it closes at once and the next program is
				// selected.
				{keys: "j<delete>", wantClosed: []string{"file:///test"}, wantConfirms: []string{}, want: `
                        │               
     1 home             │               
   ! agent              │               
     blocked · claude   │               
                        │               
 ▌   1 home             │               
 ▌ ✓ make               │               
 ▌   done               │               
                        │               
     1 home             │               
   ⠋ build              │  No preview   
     working            │               
     🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │               
                        │               
     1 home             │               
     vim                │               
     idle               │               
                        │               
                        │               
                        │               
                        │               `[1:]},
				{keys: "<backspace>", wantClosed: []string{"file:///test", "file:///make"}, want: `
                        │               
     1 home             │               
   ! agent              │               
     blocked · claude   │               
                        │               
 ▌   1 home             │               
 ▌ ⠋ build              │               
 ▌   working            │               
 ▌   🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │               
                        │               
     1 home             │  No preview   
     vim                │               
     idle               │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               `[1:]},
				// At work, it is only closed once the user says so.
				{keys: "<delete>", wantClosed: []string{"file:///test", "file:///make"}, wantConfirms: []string{
					"Are you sure you want to close `build` in `1 home`? It is working.\n\n" +
						"- working · build: Compiling — 40%",
				}, want: `
                        │               
     1 home             │               
   ! agent              │               
     blocked · claude   │               
                        │               
 ▌   1 home             │               
 ▌ ⠋ build              │               
 ▌   working            │               
 ▌   🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │               
                        │               
     1 home             │  No preview   
     vim                │               
     idle               │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               `[1:]},
				// Waiting on the user, likewise.
				{keys: "k<backspace>", wantClosed: []string{"file:///test", "file:///make"}, wantConfirms: []string{
					"Are you sure you want to close `build` in `1 home`? It is working.\n\n" +
						"- working · build: Compiling — 40%",
					"Are you sure you want to close `agent` in `1 home`? It is blocked.\n\n" +
						"- blocked · claude (tool/edit): Allow edit? — edit main.go",
				}, want: `
                        │agent0678901234
 ▌   1 home             │agent1678901234
 ▌ ! agent              │agent2678901234
 ▌   blocked · claude   │agent3678901234
                        │agent4678901234
     1 home             │agent5678901234
   ⠋ build              │agent6678901234
     working            │agent7678901234
     🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │agent8678901234
                        │agent9678901234
     1 home             │agent1078901234
     vim                │agent1178901234
     idle               │agent1278901234
                        │agent1378901234
                        │agent1478901234
                        │agent1578901234
                        │agent1678901234
                        │agent1778901234
                        │agent1878901234
                        │agent1978901234
                        │agent2078901234`[1:]},
				{do: func(f *statusViewFixture) { f.host.yes() }, wantClosed: []string{
					"file:///test", "file:///make", "file:///agent",
				}, want: `
                        │               
 ▌   1 home             │               
 ▌ ⠋ build              │               
 ▌   working            │               
 ▌   🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │               
                        │               
     1 home             │               
     vim                │               
     idle               │               
                        │               
                        │  No preview   
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               `[1:]},
				// Idle, it closes at once.
				{keys: "j<delete>", wantClosed: []string{
					"file:///test", "file:///make", "file:///agent", "file:///vim",
				}, want: `
                        │               
 ▌   1 home             │               
 ▌ ⠋ build              │               
 ▌   working            │               
 ▌   🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │  No preview   
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               `[1:]},
				{keys: "<delete>", wantConfirms: []string{
					"Are you sure you want to close `build` in `1 home`? It is working.\n\n" +
						"- working · build: Compiling — 40%",
					"Are you sure you want to close `agent` in `1 home`? It is blocked.\n\n" +
						"- blocked · claude (tool/edit): Allow edit? — edit main.go",
					"Are you sure you want to close `build` in `1 home`? It is working.\n\n" +
						"- working · build: Compiling — 40%",
				}, want: `
                        │               
 ▌   1 home             │               
 ▌ ⠋ build              │               
 ▌   working            │               
 ▌   🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  40%   │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │  No preview   
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               
                        │               `[1:]},
				{do: func(f *statusViewFixture) { f.host.yes() }, want: `
                                 │      
   No program reported a status  │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │No pre
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      `[1:]},
				{keys: "<delete><backspace>", wantClosed: []string{
					"file:///test", "file:///make", "file:///agent", "file:///vim", "file:///build",
				}, want: `
                                 │      
   No program reported a status  │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │No pre
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      
                                 │      `[1:]},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStatusViewFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			f.v = f.store.view()
			t.Cleanup(func() { require.NoError(t, f.v.Close()) })
			width, height := 0, 0
			for i, step := range tc.steps {
				if step.width != 0 || step.height != 0 || i == 0 {
					width, height = step.width, step.height
					f.v.Resize(width, height)
				}
				if step.do != nil {
					step.do(f)
				}
				keys, err := term.ParseKeys(step.keys)
				require.NoError(t, err)
				for _, k := range keys {
					f.v.Handle(term.Event{Type: term.EventKey, Key: k.Key, Ch: k.Ch, Mod: k.Mod})
				}
				for _, ev := range step.events {
					f.v.Handle(ev)
				}
				if step.wantShown == nil {
					f.host.drain()
				}
				var got string
				require.NotPanics(t, func() { got = handlertest.DrawHandler(f.v, width, height) }, "step %d", i)
				assert.Equal(t, step.want, got, "step %d", i)
				if step.wantSize != nil {
					w, h := f.v.Dimensions()
					assert.Equal(t, step.wantSize, []int{w, h}, "step %d", i)
				}
				if step.wantShown != nil {
					f.host.drain()
					shown := []string{}
					for _, k := range f.host.shown {
						assert.Equal(t, f.ws.String(), k.ws)
						shown = append(shown, k.uri)
					}
					assert.Equal(t, step.wantShown, shown, "step %d", i)
				}
				if step.wantClosed != nil {
					closed := []string{}
					for _, k := range f.host.closed {
						assert.Equal(t, f.ws.String(), k.ws)
						closed = append(closed, k.uri)
					}
					assert.Equal(t, step.wantClosed, closed, "step %d", i)
				}
				if step.wantConfirms != nil {
					assert.Equal(t, step.wantConfirms, append([]string{}, f.host.confirms...), "step %d", i)
				}
			}
		})
	}
}

func TestStatusViewMouse(t *testing.T) {
	_, store, ws := newStatusStoreTestWith(t, statusViewTestConfig(), nil)
	for _, path := range []string{"a", "b"} {
		store.updateDetached(ws, statusStoreTestURI(t, "file:///"+path), func(r *programstatus.Store) {
			r.Apply(detachedTestReport(programstatus.Done, ""))
		})
	}
	v := store.view()
	t.Cleanup(func() { require.NoError(t, v.Close()) })
	// 0 padding, 1-3 a, 4 gap, 5-7 b, 8 below them.
	v.Resize(40, 9)
	handlertest.DrawHandler(v, 40, 9)
	click := func(x, y int) bool {
		_, handled := v.Handle(term.Event{Type: term.EventMouse, Key: term.MouseLeft, MouseX: x, MouseY: y})
		return handled
	}
	a, b := statusRef{ws: ws.String(), uri: "file:///a"}, statusRef{ws: ws.String(), uri: "file:///b"}
	require.Equal(t, a, v.sel)
	for _, tc := range []struct {
		desc string
		x, y int
		want statusRef
	}{
		{desc: "a program's last row", x: 5, y: 7, want: b},
		{desc: "a gap", x: 5, y: 4, want: b},
		{desc: "below the programs", x: 5, y: 8, want: b},
		{desc: "the padding above the programs", x: 5, y: 0, want: b},
		{desc: "a program's workspace", x: 1, y: 1, want: a},
		{desc: "the preview", x: 30, y: 6, want: a},
	} {
		assert.True(t, click(tc.x, tc.y), tc.desc)
		assert.Equal(t, tc.want, v.sel, tc.desc)
	}
	_, handled := v.Handle(statusTestWheel(term.MouseWheelDown, 1, 1))
	assert.False(t, handled, "with every program in sight, the wheel has nothing to scroll")
	assert.Equal(t, a, v.sel, "the wheel selects nothing")

	nameAttrs := func(y int) term.Attributes {
		w := cell.NewBufferWriter(context.Background(), 40, 9)
		v.Draw(w)
		return w.RawCells()[y][statusTextX].Attributes()
	}
	require.Zero(t, nameAttrs(6).Attrs&term.AttrBold, "names are not bold")
	_, handled = v.Handle(term.Event{Type: term.EventMouse, MouseX: 5, MouseY: 7})
	assert.True(t, handled)
	assert.NotZero(t, nameAttrs(6).Attrs&term.AttrBold, "the name under the mouse is bold")
	assert.Zero(t, nameAttrs(2).Attrs&term.AttrBold)
	assert.Equal(t, a, v.sel, "hovering selects nothing")
	_, _ = v.Handle(term.Event{Type: term.EventMouse, MouseX: 30, MouseY: 6})
	assert.Zero(t, nameAttrs(6).Attrs&term.AttrBold, "off the sidebar, nothing is bold")

	store.updateDetached(ws, statusStoreTestURI(t, "file:///c"), func(r *programstatus.Store) {
		r.Apply(detachedTestReport(programstatus.Done, ""))
	})
	// 0 padding, 1-3 a, 4 gap, 5-7 b, 8 the arrow, and c out of sight.
	require.Zero(t, nameAttrs(6).Attrs&term.AttrBold)
	assert.True(t, click(11, 8))
	assert.Equal(t, a, v.sel, "the arrow selects nothing")
	_, _ = v.Handle(term.Event{Type: term.EventMouse, MouseX: 5, MouseY: 6})
	require.NotZero(t, nameAttrs(6).Attrs&term.AttrBold)
	_, handled = v.Handle(statusTestWheel(term.MouseWheelDown, 5, 6))
	assert.True(t, handled, "with a program out of sight, the wheel scrolls")
	// 0 padding, 1-3 b, 4 gap, 5-7 c.
	assert.NotZero(t, nameAttrs(6).Attrs&term.AttrBold, "scrolled, the program now under the mouse is styled")
	assert.Zero(t, nameAttrs(2).Attrs&term.AttrBold, "and the one that scrolled away from it is not")
	assert.Equal(t, a, v.sel, "the wheel selects nothing")
}

func TestStatusViewSidebarWidth(t *testing.T) {
	_, store, ws := newStatusStoreTest(t)
	v := store.view()
	defer v.Close()
	for _, tc := range []struct {
		name, app, wsName string
		want              int
	}{
		{name: "short", want: statusSidebarMin},
		{name: strings.Repeat("a", 25), want: statusTextX + 25 + statusSelectionPad + statusPadRight},
		{name: strings.Repeat("界", 30), want: statusSidebarMax},
		{name: "short", app: strings.Repeat("b", 20),
			want: statusTextX + len([]rune("idle · ")) + 20 + statusSelectionPad + statusPadRight},
		{name: "short", wsName: strings.Repeat("c", 25), want: statusTextX + 25 + statusSelectionPad + statusPadRight},
	} {
		v.set(statusInbox{}.entries([]statusItem{{ws: ws, wsName: tc.wsName, name: tc.name, status: programstatus.Summary{
			Progress: -1, App: tc.app,
			Records: []programstatus.Record{{Report: programstatus.Report{Progress: -1}}},
		}}}))
		w, _ := v.sidebarSize()
		assert.Equal(t, tc.want, w, "%q %q %q", tc.name, tc.app, tc.wsName)
	}
}

func TestStatusViewPreview(t *testing.T) {
	host, store, ws := newStatusStoreTest(t)
	cfg := browser.DefaultConfig()
	cfg.OnTabStatus = func(uri workspaceapi.URI, _, next programstatus.Summary) { store.tabStatusChanged(ws, uri, next) }
	b := browser.NewComponent(cfg)
	t.Cleanup(func() { require.NoError(t, b.Close()) })
	b.Resize(120, 60)
	tabs := statusStoreTestTabs{component: b}
	open := func(name string, state programstatus.State) (*browser.Tab, *statusPreviewTestHandler) {
		uri := statusStoreTestURI(t, "rune-agent://model/"+name)
		h := &statusPreviewTestHandler{name: name}
		tab := b.NewTab(uri, 'x', name, h, h)
		key := statusKey{ws.String(), uri.String()}
		host.names[key], host.tabs[key] = name, tab
		require.NoError(t, store.setTabStatus(ws, tabs, uri, programstatus.Report{State: state, Progress: -1}))
		return tab, h
	}
	freeTab, free := open("f", programstatus.Blocked)
	boundTab, bound := open("b", programstatus.Working)
	require.True(t, b.SetContentToTab(b.Focus(), 1))
	_, ok := boundTab.Window()
	require.True(t, ok)
	boundW, boundH := boundTab.ContentSize()
	require.GreaterOrEqual(t, boundW, 51)
	require.GreaterOrEqual(t, boundH, 33)
	// A terminal outside of a tab, in a window of its own.
	pty := statusStoreTestURI(t, "file:///dev/ttys001")
	term1 := &statusPreviewTestHandler{name: "t", width: 20, height: 5}
	host.previews[statusKey{ws.String(), pty.String()}] = statusPreview{content: term1, width: 20, height: 5}
	store.updateDetached(ws, pty, func(r *programstatus.Store) {
		r.Apply(detachedTestReport(programstatus.Done, "ok"))
	})
	gone := statusStoreTestURI(t, "file:///dev/ttys002")
	store.updateDetached(ws, gone, func(r *programstatus.Store) {
		r.Apply(detachedTestReport(programstatus.Done, "ok"))
	})

	v := store.view()
	t.Cleanup(func() { require.NoError(t, v.Close()) })
	const sideW = statusSidebarMin + statusDividerWidth
	w, h := v.Dimensions()
	assert.Equal(t, []int{sideW + statusPreviewWidth, max(statusPreviewHeight, statusMinHeight)}, []int{w, h},
		"a tab never sized asks for the default size, and the view for no less than its least height")

	const width, height = sideW + 30, 12
	draw := func() []string {
		var ret []string
		for line := range strings.SplitSeq(handlertest.DrawHandler(v, width, height), "\n") {
			ret = append(ret, string([]rune(line)[sideW:]))
		}
		return ret
	}
	preview := func(name string, y0, rows, cols int) []string {
		var ret []string
		for y := range height {
			if y >= rows {
				ret = append(ret, strings.Repeat(" ", width-sideW))
				continue
			}
			ret = append(ret, statusPreviewTestRow(name, y0+y, cols)+strings.Repeat(" ", width-sideW-cols))
		}
		return ret
	}
	v.Resize(width, height)
	assert.Equal(t, preview("f", 0, height, width-sideW), draw())
	w, h = freeTab.ContentSize()
	assert.Equal(t, []int{width - sideW, height}, []int{w, h}, "a tab never sized is fit to the preview")

	// Sorted by state then name: f (blocked), the two terminals (done),
	// then b (working).
	v.move(1)
	assert.Equal(t, preview("t", 0, 5, 20), draw(), "a terminal outside of a tab is previewed in its window")
	w, h = v.Dimensions()
	assert.Equal(t, []int{sideW + 20, statusMinHeight}, []int{w, h}, "the view keeps its least height over a short terminal")
	v.move(1)
	assert.Contains(t, strings.Join(draw(), "\n"), statusPreviewNone, "a terminal whose window is gone")
	// Given less than the tab, the preview leaves out its top rows and
	// right columns.
	v.move(1)
	assert.Equal(t, preview("b", boundH-height, height, width-sideW), draw())
	w, h = boundTab.ContentSize()
	assert.Equal(t, []int{boundW, boundH}, []int{w, h}, "a previewed tab keeps its size")

	w, h = v.Dimensions()
	require.Equal(t, []int{sideW + boundW, boundH}, []int{w, h}, "the view asks for the size of the selected tab")
	v.Resize(w, h)
	lines := strings.Split(handlertest.DrawHandler(v, w, h), "\n")
	for y := range boundH {
		assert.Equal(t, statusPreviewTestRow("b", y, boundW), string([]rune(lines[y])[sideW:]),
			"given its size, the tab is drawn whole")
	}

	b.Resize(100, 40)
	resizedW, resizedH := boundTab.ContentSize()
	require.NotEqual(t, []int{boundW, boundH}, []int{resizedW, resizedH})
	w, h = v.Dimensions()
	assert.Equal(t, []int{sideW + resizedW, max(resizedH, statusMinHeight)}, []int{w, h},
		"the view follows the size of the tab")

	assert.Zero(t, free.events, "the previewed tab receives no input")
	assert.Zero(t, bound.events, "the previewed tab receives no input")
	assert.Zero(t, term1.events, "the previewed terminal receives no input")
}

func TestStatusViewHiddenPreview(t *testing.T) {
	host, store, ws := newStatusStoreTest(t)
	hidden, shown := statusStoreTestURI(t, "file:///hidden"), statusStoreTestURI(t, "file:///shown")
	program := &statusPreviewTestHandler{name: "h", width: 3, height: 1}
	fits := 0
	host.previews[statusKey{ws.String(), hidden.String()}] = statusPreview{
		content: program, hidden: true,
		fit: func(w, h int) { fits++; program.Resize(w, h) },
	}
	other := &statusPreviewTestHandler{name: "s", width: 5, height: 2}
	host.previews[statusKey{ws.String(), shown.String()}] = statusPreview{content: other, width: 5, height: 2}
	for _, uri := range []workspaceapi.URI{hidden, shown} {
		store.updateDetached(ws, uri, func(r *programstatus.Store) {
			r.Apply(detachedTestReport(programstatus.Done, ""))
		})
	}
	v := store.view()
	t.Cleanup(func() { require.NoError(t, v.Close()) })
	require.Equal(t, hidden.String(), v.sel.uri)

	const sideW = statusSidebarMin + statusDividerWidth
	w, h := v.Dimensions()
	assert.Equal(t, []int{sideW + statusPreviewWidth, max(statusPreviewHeight, statusMinHeight)}, []int{w, h},
		"what is shown nowhere asks for the default size")
	const width, height = sideW + 20, 6
	v.Resize(width, height)
	assert.Equal(t, []int{20, height}, []int{program.width, program.height}, "it is fit to the preview")
	lines := strings.Split(handlertest.DrawHandler(v, width, height), "\n")
	for y := range height {
		assert.Equal(t, statusPreviewTestRow("h", y, 20), string([]rune(lines[y])[sideW:]), "it is drawn whole")
	}
	v.Resize(width, height)
	assert.Equal(t, 1, fits, "it is only resized as the preview changes size")

	program.Resize(3, 1)
	v.move(1)
	v.move(-1)
	assert.Equal(t, 2, fits, "selected again, it is fit again")
	assert.Equal(t, []int{20, height}, []int{program.width, program.height})
	assert.Equal(t, []int{5, 2}, []int{other.width, other.height}, "what is shown elsewhere is never resized")
}

func TestStatusViewStyle(t *testing.T) {
	_, store, ws := newStatusStoreTestWith(t, statusViewTestConfig(), nil)
	for path, report := range map[string]programstatus.Report{
		"a": detachedTestReport(programstatus.Blocked, ""),
		"b": detachedTestReport(programstatus.Done, ""),
		"c": {State: programstatus.Working, Progress: 50, Title: "abcd", Msg: "efgh"},
	} {
		store.updateDetached(ws, statusStoreTestURI(t, "file:///"+path), func(r *programstatus.Store) { r.Apply(report) })
	}
	v := store.view()
	t.Cleanup(func() { require.NoError(t, v.Close()) })
	const width = statusSidebarMin + 10
	v.Resize(width, 14)
	w := cell.NewBufferWriter(context.Background(), width, 14)
	v.Draw(w)
	cells := w.RawCells()
	text := func(y int) string {
		var b strings.Builder
		for _, c := range cells[y][:statusSidebarMin] {
			if c.Ch == 0 {
				c.Ch = ' '
			}
			b.WriteRune(c.Ch)
		}
		return strings.TrimRight(b.String(), " ")
	}
	var got []string
	for y := range 13 {
		got = append(got, text(y))
	}
	require.Equal(t, []string{
		"",
		" ▌   1 home",
		" ▌ ! terminal",
		" ▌   blocked",
		"",
		"     1 home",
		"   ✓ terminal",
		"     done",
		"",
		"     1 home",
		"   ⠋ terminal",
		"     working",
		"     🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋🬋  50%",
	}, got, "each program is beneath its workspace, and the title and the message are left to the preview, "+
		"past a row and a column of padding")

	attrs := func(y, from, to int) []term.Attributes {
		var ret []term.Attributes
		for _, c := range cells[y][from:to] {
			ret = append(ret, c.Attributes())
		}
		return ret
	}
	repeat := func(attr term.Attributes, n int) []term.Attributes {
		return slices.Repeat([]term.Attributes{attr}, n)
	}
	selected := func(attr term.Attributes) term.Attributes {
		return term.AttributesUnion(attr, statusTestStyle().selectedAttr)
	}
	for _, tc := range []struct {
		name               string
		y                  int
		icon, label, state term.Attributes
	}{
		{
			name: "blocked", y: 2, icon: selected(term.Attributes{Fg: term.ColorRed}),
			label: selected(term.Attributes{Fg: term.ColorYellow}), state: selected(term.Attributes{Fg: term.ColorYellow}),
		},
		{name: "done", y: 6, icon: term.Attributes{Fg: term.ColorSilver}, label: term.Attributes{Fg: term.ColorGreen}, state: term.Attributes{Fg: term.ColorGreen}},
		{name: "working", y: 10, icon: term.Attributes{Fg: term.ColorYellow}, state: term.Attributes{Fg: term.ColorYellow}},
	} {
		assert.Equal(t, tc.icon, cells[tc.y][statusIconX].Attributes(), "%s icon", tc.name)
		assert.Equal(t, repeat(tc.label, len("terminal")), attrs(tc.y, statusTextX, statusTextX+len("terminal")), "%s name", tc.name)
		assert.Equal(t, repeat(tc.state, len(tc.name)), attrs(tc.y+1, statusTextX, statusTextX+len(tc.name)), "%s state", tc.name)
	}
	style := statusTestStyle()
	wsName := func(y int) []term.Attributes { return attrs(y, statusTextX, statusTextX+len("1 home")) }
	assert.Equal(t, repeat(term.AttributesUnion(style.selectedAttr, style.workspaceHighlightAttr), len("1 home")), wsName(1),
		"the workspace of the selected program stands out from the selection")
	for _, y := range []int{5, 9} {
		assert.Equal(t, repeat(style.workspaceAttr, len("1 home")), wsName(y), "the workspace at %d", y)
	}
	const barW, doneW = 11, 5
	bar := func() []term.Attributes { return attrs(12, statusTextX, statusTextX+barW) }
	label := func() []term.Attributes { return attrs(12, statusTextX+barW+1, statusTextX+barW+5) }
	onBar := func(base, attr term.Attributes) term.Attributes {
		return term.AttributesUnion(term.AttributesUnion(base, style.progressAttr), attr)
	}
	assert.Equal(t, append(repeat(onBar(term.Attributes{}, style.progressDoneAttr), doneW),
		repeat(onBar(term.Attributes{}, style.progressRemainingAttr), barW-doneW)...), bar(),
		"half of the bar is done, over the bar's own background")
	assert.Equal(t, repeat(style.progressLabelAttr, 4), label(), "the percentage")
	marker := term.Attributes{Fg: style.markerAttr.Fg, Bg: style.selectedAttr.Bg}
	selEnd := statusSidebarMin - 2*statusPadTop
	for _, y := range []int{1, 2, 3} {
		assert.Equal(t, marker, cells[y][statusPadLeft].Attributes(),
			"the marker at %d takes the background of the selection it leaves unset", y)
		assert.Equal(t, ' ', cells[y][statusPadLeft+1].Ch,
			"a column of row %d stands between the marker and the icon", y)
		assert.Equal(t, style.selectedAttr, cells[y][statusPadLeft+1].Attributes(),
			"the column between the marker and the icon of row %d is selected", y)
		assert.Equal(t, repeat(style.selectedAttr, 5), attrs(y, selEnd-5, selEnd),
			"the selection paints row %d past its text, into the padding", y)
		assert.Equal(t, repeat(term.Attributes{}, statusPadLeft), attrs(y, 0, statusPadLeft),
			"the left padding of row %d is not selected", y)
		assert.Equal(t, repeat(term.Attributes{}, 2*statusPadTop), attrs(y, selEnd, statusSidebarMin),
			"cells being about twice as tall as wide, the selection leaves twice as many columns of row %d "+
				"empty on its right as rows above it, for the padding to look even", y)
		assert.Equal(t, style.frame.VerticalLeft, cells[y][statusSidebarMin].Ch,
			"a line stands between the sidebar and the preview")
		assert.Equal(t, style.dividerAttr, cells[y][statusSidebarMin].Attributes())
	}
	assert.Equal(t, repeat(term.Attributes{}, statusSidebarMin), attrs(0, 0, statusSidebarMin), "the padding is not selected")
	assert.Equal(t, repeat(term.Attributes{}, statusSidebarMin), attrs(4, 0, statusSidebarMin), "the gap is not selected")

	redraw := func() {
		w = cell.NewBufferWriter(context.Background(), width, 14)
		v.Draw(w)
		cells = w.RawCells()
	}
	store.style.markerAttr.Bg = term.ColorRed
	redraw()
	assert.Equal(t, term.Attributes{Fg: term.ColorPurple, Bg: term.ColorRed}, cells[1][statusPadLeft].Attributes(),
		"the marker keeps a background of its own over the selection")
	store.style.markerAttr.Bg = term.ColorDefault

	hover := func(y int) {
		_, handled := v.Handle(term.Event{Type: term.EventMouse, MouseX: statusTextX, MouseY: y})
		require.True(t, handled)
		redraw()
	}
	hover(7)
	for _, y := range []int{5, 6, 7} {
		assert.Equal(t, repeat(style.hoverAttr, 5), attrs(y, selEnd-5, selEnd),
			"the hover paints row %d of the program under the mouse, as the selection does", y)
		assert.Equal(t, repeat(term.Attributes{}, statusPadLeft), attrs(y, 0, statusPadLeft))
	}
	assert.Equal(t, repeat(term.AttributesUnion(term.Attributes{Fg: term.ColorGreen}, style.hoverAttr), len("terminal")),
		attrs(6, statusTextX, statusTextX+len("terminal")), "the name takes the colors the hover sets")
	assert.Equal(t, repeat(term.AttributesUnion(style.hoverAttr, style.workspaceHighlightAttr), len("1 home")), wsName(5),
		"the workspace of the program under the mouse stands out as the selected one's does")
	assert.Equal(t, repeat(style.selectedAttr, 5), attrs(1, selEnd-5, selEnd), "the hover leaves the selection alone")
	hover(2)
	assert.Equal(t, repeat(term.AttributesUnion(style.hoverAttr, style.selectedAttr), 5), attrs(1, selEnd-5, selEnd),
		"the selection's colors win over the hover's")
	assert.Equal(t, term.AttributesUnion(term.AttributesUnion(style.hoverAttr, style.selectedAttr), style.markerAttr),
		cells[1][statusPadLeft].Attributes(), "the marker is drawn over both")
	assert.Equal(t, repeat(term.Attributes{}, 5), attrs(6, selEnd-5, selEnd), "the hover moves off")
	assert.Equal(t, repeat(style.workspaceAttr, len("1 home")), wsName(5), "and so does the workspace's highlight")
	hover(0)

	v.move(2)
	redraw()
	assert.Equal(t, append(repeat(onBar(style.selectedAttr, style.progressDoneAttr), doneW),
		repeat(onBar(style.selectedAttr, style.progressRemainingSelectedAttr), barW-doneW)...), bar(),
		"selected, the bar keeps its own background and what is not done stands out from the selection")
	assert.Equal(t, repeat(selected(style.progressLabelAttr), 4), label(), "the percentage is selected as text is")
	assert.Equal(t, '%', cells[12][selEnd-statusSelectionPad-1].Ch, "the widest text stops short of the selection's edge")
	assert.Equal(t, repeat(style.selectedAttr, statusSelectionPad), attrs(12, selEnd-statusSelectionPad, selEnd))
	store.style.progressAttr = term.Attributes{}
	redraw()
	assert.Equal(t, slices.Repeat([]term.Color{style.selectedAttr.Bg}, barW), func() []term.Color {
		var ret []term.Color
		for _, a := range bar() {
			ret = append(ret, a.Bg)
		}
		return ret
	}(), "a bar without a background of its own shows the selection through")

	// 0 padding, 1-3 a, 4 gap, 5-7 b, 8 the arrow, and c out of sight.
	v.move(1)
	v.Resize(width, 9)
	redraw()
	arrowX := statusPadLeft + (selEnd-statusPadLeft-1)/2
	assert.Equal(t, style.moreBelow, cells[8][arrowX].Ch, "an arrow points at the program out of sight")
	assert.Equal(t, style.moreBelowAttr, cells[8][arrowX].Attributes())

	_, empty, _ := newStatusStoreTestWith(t, statusViewTestConfig(), nil)
	ev := empty.view()
	t.Cleanup(func() { require.NoError(t, ev.Close()) })
	const emptyW, emptyH = 60, 5
	ev.Resize(emptyW, emptyH)
	w = cell.NewBufferWriter(context.Background(), emptyW, emptyH)
	ev.Draw(w)
	var screen strings.Builder
	for _, row := range w.RawCells() {
		for x, c := range row {
			if c.Ch == 0 || c.Ch == ' ' || x == ev.sidebarWidth() {
				continue
			}
			screen.WriteRune(c.Ch)
			assert.Equal(t, style.placeholderAttr, c.Attributes(), "%q stands in for what there is not", c.Ch)
		}
	}
	assert.Equal(t, strings.ReplaceAll(statusViewEmpty+statusPreviewNone, " ", ""), screen.String())
}

func TestStatusViewSpinnerTurns(t *testing.T) {
	interrupts := &statusTestInterrupter{}
	cfg := browser.TabStatusConfig{Spinner: []rune("ab"), SpinnerFPS: 100}
	_, store, ws := newStatusStoreTestWith(t, cfg, interrupts)
	store.updateDetached(ws, statusStoreTestURI(t, "file:///w"), func(r *programstatus.Store) {
		r.Apply(detachedTestReport(programstatus.Working, ""))
	})
	v := store.view()
	v.Resize(statusSidebarMin, 6)
	draw := func() [][]term.Cell {
		w := cell.NewBufferWriter(context.Background(), statusSidebarMin, 6)
		v.Draw(w)
		return w.RawCells()
	}
	// The program spins where its icon is, beside its name beneath its
	// workspace, below the padding; nothing else moves.
	spinning := []term.Coordinates{{X: statusIconX, Y: statusPadTop + 1}}
	still := func(cells [][]term.Cell) [][]term.Cell {
		for _, at := range spinning {
			cells[at.Y][at.X] = term.Cell{}
		}
		return cells
	}
	rest := still(draw())
	seen := map[string]bool{}
	assert.Eventually(t, func() bool {
		cells := draw()
		for _, at := range spinning {
			seen[string(cells[at.Y][at.X].Ch)] = true
		}
		require.Equal(t, rest, still(cells), "only the icons of working rows move")
		return seen["a"] && seen["b"]
	}, 5*time.Second, time.Millisecond, "the spinner turns while the inbox is open")

	require.NoError(t, v.Close())
	n := interrupts.count.Load()
	time.Sleep(100 * time.Millisecond)
	assert.LessOrEqual(t, interrupts.count.Load(), n+1, "a closed inbox stops waking the host")
}

func TestStatusScroll(t *testing.T) {
	heights := []int{2, 3, 2, 2}
	for _, tc := range []struct {
		desc           string
		top, sel, rows int
		want           int
	}{
		{desc: "in sight", top: 0, sel: 1, rows: 6, want: 0},
		{desc: "below", top: 0, sel: 2, rows: 6, want: 1},
		{desc: "above", top: 2, sel: 0, rows: 6, want: 0},
		{desc: "taller than the rows", top: 0, sel: 1, rows: 2, want: 1},
		{desc: "no selection keeps the top", top: 2, sel: -1, rows: 2, want: 2},
		{desc: "shrunk list", top: 9, sel: -1, rows: 2, want: 3},
		{desc: "no selection, room below scrolls back up", top: 3, sel: -1, rows: 6, want: 2},
		{desc: "room below scrolls back up", top: 3, sel: 3, rows: 6, want: 2},
		{desc: "everything fits", top: 2, sel: 3, rows: 20, want: 0},
	} {
		assert.Equal(t, tc.want, statusScroll(tc.top, tc.sel, heights, tc.rows), tc.desc)
	}
}

func TestStatusProgress(t *testing.T) {
	done := term.Attributes{Fg: term.ColorGreen}
	remaining := term.Attributes{Fg: term.ColorGray}
	labelAttr := term.Attributes{Fg: term.ColorAqua}
	glyph := string(statusProgressGlyph)
	console := component.DefaultProgressBarCharSet()
	for _, tc := range []struct {
		desc       string
		chars      component.ProgressBarCharSet
		width, pct int
		bar, label string
		wantDone   int
	}{
		{desc: "nothing done", width: 12, pct: 0, bar: strings.Repeat(glyph, 7), label: "   0%"},
		{desc: "half done", width: 12, pct: 50, bar: strings.Repeat(glyph, 7), label: "  50%", wantDone: 3},
		{desc: "not quite done", width: 12, pct: 99, bar: strings.Repeat(glyph, 7), label: "  99%", wantDone: 6},
		{desc: "done", width: 12, pct: 100, bar: strings.Repeat(glyph, 7), label: " 100%", wantDone: 7},
		{desc: "too narrow for a bar", width: 4, pct: 50, label: "50%"},
		{desc: "too narrow for both ends", width: 6, pct: 50, label: "50%"},
		{desc: "the ends of the console's", chars: console, width: 12, pct: 50, bar: "╢░░___╟", label: "  50%", wantDone: 3},
		{desc: "the console's, done", chars: console, width: 12, pct: 100, bar: "╢░░░░░╟", label: " 100%", wantDone: 7},
	} {
		chars := tc.chars
		if chars == (component.ProgressBarCharSet{}) {
			chars = defaultStatusInboxStyle().progressBar
		}
		w := cell.NewBufferWriter(context.Background(), tc.width, 1)
		statusProgress(w, 0, 0, tc.width, tc.pct, chars, done, remaining, labelAttr)
		var got strings.Builder
		n := len([]rune(tc.bar))
		gotAttrs := []term.Attributes{}
		var gotLabelAttrs []term.Attributes
		for x, c := range w.RawCells()[0] {
			if c.Ch == 0 {
				c.Ch = ' '
			}
			got.WriteRune(c.Ch)
			switch {
			case x < n:
				gotAttrs = append(gotAttrs, c.Attributes())
			case c.Ch != ' ':
				gotLabelAttrs = append(gotLabelAttrs, c.Attributes())
			}
		}
		assert.Equal(t, tc.bar+tc.label, strings.TrimRight(got.String(), " "),
			"%s: the bar keeps its length as the percentage grows", tc.desc)
		wantAttrs := append(slices.Repeat([]term.Attributes{done}, tc.wantDone),
			slices.Repeat([]term.Attributes{remaining}, n-tc.wantDone)...)
		assert.Equal(t, wantAttrs, gotAttrs, "%s: what is done, then what is not", tc.desc)
		assert.Equal(t, slices.Repeat([]term.Attributes{labelAttr}, len(strings.TrimSpace(tc.label))), gotLabelAttrs,
			"%s: the percentage", tc.desc)
	}
}

func detachedTestReport(state programstatus.State, msg string) programstatus.Report {
	return programstatus.Report{State: state, Progress: -1, Msg: msg}
}

func statusTestStyle() statusInboxStyle {
	style := defaultStatusInboxStyle()
	style.markerAttr = term.Attributes{Fg: term.ColorPurple}
	style.selectedAttr = term.Attributes{Bg: term.ColorNavy}
	style.hoverAttr = term.Attributes{Bg: term.ColorTeal, Attrs: term.AttrBold}
	style.workspaceAttr = term.Attributes{Fg: term.ColorBlue}
	style.workspaceHighlightAttr = term.Attributes{Fg: term.ColorBlack}
	style.progressAttr = term.Attributes{Bg: term.ColorMaroon}
	style.progressDoneAttr = term.Attributes{Fg: term.ColorLime}
	style.progressRemainingAttr = term.Attributes{Fg: term.ColorSilver}
	style.progressRemainingSelectedAttr = term.Attributes{Fg: term.ColorWhite}
	style.progressLabelAttr = term.Attributes{Fg: term.ColorAqua}
	// Unlike the default, it shows in snapshots without a Nerd Font.
	style.moreBelow = '↓'
	style.moreBelowAttr = term.Attributes{Fg: term.ColorYellow}
	style.dividerAttr = term.Attributes{Fg: term.ColorOlive}
	style.placeholderAttr = term.Attributes{Fg: term.ColorFuchsia}
	return style
}

type statusTestInterrupter struct{ count atomic.Int64 }

func (i *statusTestInterrupter) Interrupt(context.Context) error {
	i.count.Add(1)
	return nil
}

// statusPreviewTestHandler fills what it is sized to with
// statusPreviewTestRow rows, and counts the events it receives.
type statusPreviewTestHandler struct {
	name          string
	width, height int
	events        int
}

func (h *statusPreviewTestHandler) Resize(width, height int) { h.width, h.height = width, height }
func (h *statusPreviewTestHandler) Draw(w term.Writer) {
	for y := range h.height {
		for x, ch := range statusPreviewTestRow(h.name, y, h.width) {
			w.SetCell(term.Coordinates{X: x, Y: y}, term.NewCell(ch, 1, term.Attributes{}))
		}
	}
}
func (h *statusPreviewTestHandler) Handle(term.Event) (bool, bool) { h.events++; return false, true }
func (h *statusPreviewTestHandler) Cursor() (term.Coordinates, term.CursorStyle, bool) {
	return term.Coordinates{}, term.CursorStyleDefault, false
}
func (h *statusPreviewTestHandler) Selection() (string, bool) { return "", false }
func (h *statusPreviewTestHandler) Close() error              { return nil }

// statusPreviewTestRow returns the first width cells of row y of the
// handler named name: its name and the row, then the digit of each
// column.
func statusPreviewTestRow(name string, y, width int) string {
	label := name + strconv.Itoa(y)
	var b strings.Builder
	for x := range width {
		if x < len(label) {
			b.WriteByte(label[x])
		} else {
			b.WriteByte(byte('0' + x%10))
		}
	}
	return b.String()
}

type statusStoreTestHost struct {
	workspaces map[string]string
	order      []workspaceapi.URI
	names      map[statusKey]string
	tabs       map[statusKey]*browser.Tab
	previews   map[statusKey]statusPreview
	changed    []workspaceapi.URI
	shown      []statusKey
	closed     []statusKey
	// onClose, when set, closes what closeStatus is asked to.
	onClose func(statusKey)
	// confirms holds the questions confirmStatus asked, and yes what
	// answering the last one yes does.
	confirms []string
	yes      func()
	mu       sync.Mutex
	queued   []func()
}

func (h *statusStoreTestHost) addWorkspace(ws workspaceapi.URI, name string) {
	h.workspaces[ws.String()] = name
	h.order = append(h.order, ws)
}
func (h *statusStoreTestHost) statusWorkspace(ws workspaceapi.URI) (int, string, bool) {
	name, ok := h.workspaces[ws.String()]
	return slices.IndexFunc(h.order, ws.Equal), name, ok
}
func (h *statusStoreTestHost) statusTabName(ws, uri workspaceapi.URI) (string, bool) {
	name, ok := h.names[statusKey{ws.String(), uri.String()}]
	return name, ok
}
func (h *statusStoreTestHost) statusPreview(ws, uri workspaceapi.URI) (statusPreview, bool) {
	key := statusKey{ws.String(), uri.String()}
	if tab, ok := h.tabs[key]; ok {
		w, hh := tab.ContentSize()
		return statusPreview{content: tab, width: w, height: hh, fit: tab.Resize}, true
	}
	p, ok := h.previews[key]
	return p, ok
}
func (h *statusStoreTestHost) statusTabIcon(ws, uri workspaceapi.URI) (rune, bool) {
	if _, ok := h.tabs[statusKey{ws.String(), uri.String()}]; !ok {
		return 0, false
	}
	return 'x', true
}
func (h *statusStoreTestHost) statusInSight(workspaceapi.URI, workspaceapi.URI) bool               { return false }
func (h *statusStoreTestHost) notifyStatus(workspaceapi.URI, browserapi.NotificationLevel, string) {}
func (h *statusStoreTestHost) statusChanged(ws workspaceapi.URI)                                   { h.changed = append(h.changed, ws) }
func (h *statusStoreTestHost) showStatus(ws, uri workspaceapi.URI) {
	h.shown = append(h.shown, statusKey{ws.String(), uri.String()})
}
func (h *statusStoreTestHost) closeStatus(ws, uri workspaceapi.URI) {
	key := statusKey{ws.String(), uri.String()}
	h.closed = append(h.closed, key)
	if h.onClose != nil {
		h.onClose(key)
	}
}
func (h *statusStoreTestHost) confirmStatus(message string, yes func()) {
	h.confirms = append(h.confirms, message)
	h.yes = yes
}
func (h *statusStoreTestHost) schedule(fn func()) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queued = append(h.queued, fn)
	return true
}
func (h *statusStoreTestHost) drain() {
	h.mu.Lock()
	queued := h.queued
	h.queued = nil
	h.mu.Unlock()
	for _, fn := range queued {
		fn()
	}
}

func statusStoreTestURI(t *testing.T, raw string) workspaceapi.URI {
	t.Helper()
	uri, err := workspaceapi.ParseURI(raw)
	require.NoError(t, err)
	return uri
}

func newStatusStoreTest(t *testing.T) (*statusStoreTestHost, *statusStore, workspaceapi.URI) {
	t.Helper()
	return newStatusStoreTestWith(t, browser.TabStatusConfig{}, nil)
}

func newStatusStoreTestWith(
	t *testing.T, cfg browser.TabStatusConfig, interrupter term.Interrupter,
) (*statusStoreTestHost, *statusStore, workspaceapi.URI) {
	t.Helper()
	ws := statusStoreTestURI(t, "file:///workspace")
	host := &statusStoreTestHost{
		workspaces: make(map[string]string),
		names:      make(map[statusKey]string),
		tabs:       make(map[statusKey]*browser.Tab),
		previews:   make(map[statusKey]statusPreview),
	}
	host.addWorkspace(ws, "1 home")
	return host, newStatusStore(host, cfg, tabStatusNotifyConfig{}, statusTestStyle(), host.schedule, interrupter), ws
}

type statusStoreDetachedTest struct {
	store *statusStore
	ws    workspaceapi.URI
}

func newStatusStoreDetachedTest(t *testing.T) statusStoreDetachedTest {
	_, store, ws := newStatusStoreTest(t)
	return statusStoreDetachedTest{store, ws}
}
func (d *statusStoreDetachedTest) get(uri workspaceapi.URI) (programstatus.Summary, bool) {
	if d.store == nil {
		return programstatus.Summary{}, false
	}
	return d.store.status(d.ws, uri)
}
func (d *statusStoreDetachedTest) list() []statusItem {
	if d.store == nil {
		return nil
	}
	return d.store.items()
}
func (d *statusStoreDetachedTest) rollup() (programstatus.Summary, workspaceapi.URI) {
	if d.store == nil {
		return programstatus.Summary{Progress: -1}, workspaceapi.URI{}
	}
	return d.store.rollup(d.ws)
}
func (d *statusStoreDetachedTest) update(uri workspaceapi.URI, fn func(*programstatus.Store)) (programstatus.Summary, programstatus.Summary, bool) {
	prev, ok := d.get(uri)
	if !ok {
		prev = programstatus.Summary{Progress: -1}
	}
	host := d.store.host.(*statusStoreTestHost)
	n := len(host.changed)
	d.store.updateDetached(d.ws, uri, fn)
	next, ok := d.get(uri)
	if !ok {
		next = programstatus.Summary{Progress: -1}
	}
	return prev, next, len(host.changed) != n
}

func TestStatusStoreLiveViews(t *testing.T) {
	host, store, ws := newStatusStoreTest(t)
	a, b := statusStoreTestURI(t, "file:///a"), statusStoreTestURI(t, "file:///b")
	v, other := store.view(), store.view()
	t.Cleanup(func() { require.NoError(t, v.Close()); require.NoError(t, other.Close()) })
	v.Resize(60, 14)
	apply := func(uri workspaceapi.URI, id string, state programstatus.State) {
		store.updateDetached(ws, uri, func(r *programstatus.Store) { r.Apply(programstatus.Report{ID: id, State: state, Progress: -1}) })
	}
	program := func(uri workspaceapi.URI) statusRef { return statusRef{ws: ws.String(), uri: uri.String()} }
	_, ok := v.Selection()
	require.False(t, ok, "with no program, nothing is selected")
	apply(a, "", programstatus.Working)
	assert.Equal(t, program(a), v.sel, "the first program is selected as it comes")
	apply(b, "", programstatus.Done)
	require.Len(t, v.entries, 2)
	assert.Equal(t, program(a), v.sel, "the selection stays as programs come")
	assert.Equal(t, program(a), other.sel)
	v.Handle(term.Event{Type: term.EventKey, Ch: 'j'})
	require.Equal(t, program(b), v.sel)
	apply(a, "", programstatus.Blocked)
	assert.Equal(t, program(b), v.sel, "the selection follows a reordered source")
	assert.Equal(t, program(a), other.sel)
	apply(a, "tool/edit", programstatus.Working)
	require.Len(t, v.entries, 2, "a record of a listed source adds no entry")
	apply(a, "", programstatus.Working)
	assert.Equal(t, program(b), v.sel, "the selection follows a reordered source")
	w := term.NewStringWriter(60, 14)
	v.Draw(w)
	require.NoError(t, w.Flush())
	assert.Contains(t, w.String(), detachedStatusLabel, "rebuilt entries retain the open view's size")
	require.NoError(t, other.Close())
	frozen := append([]statusEntry(nil), other.entries...)
	apply(a, "tool/edit", programstatus.Done)
	assert.Equal(t, frozen, other.entries)
	assert.Len(t, store.views, 1)
	exit, handled := v.Handle(term.Event{Type: term.EventKey, Key: term.KeyEnter})
	assert.True(t, exit)
	assert.True(t, handled)
	assert.Empty(t, host.shown)
	host.drain()
	assert.Equal(t, []statusKey{{ws.String(), b.String()}}, host.shown)
	apply(a, "", programstatus.Clear)
	require.Len(t, v.entries, 1)
	assert.Equal(t, program(b), v.sel)
	apply(b, "", programstatus.Clear)
	assert.Empty(t, v.entries)
	_, ok = v.Selection()
	assert.False(t, ok, "with no program left, nothing is selected")
	w = term.NewStringWriter(60, 14)
	v.Draw(w)
	require.NoError(t, w.Flush())
	assert.Contains(t, w.String(), statusViewEmpty)
}

func TestStatusStoreWorkspaceCleanup(t *testing.T) {
	host, store, ws := newStatusStoreTest(t)
	other := statusStoreTestURI(t, "file:///other")
	uri := statusStoreTestURI(t, "file:///terminal")
	host.addWorkspace(other, "2 other")
	for _, workspace := range []workspaceapi.URI{ws, other} {
		store.updateDetached(workspace, uri, func(r *programstatus.Store) { r.Apply(detachedTestReport(programstatus.Error, "boom")) })
		key := statusKey{workspace.String(), uri.String()}
		store.lastNotified[key] = time.Now()
		store.pendingNotify[key] = 1
	}
	v := store.view()
	defer v.Close()
	require.Len(t, v.entries, 2)
	store.dropWorkspace(ws)
	require.Len(t, v.entries, 1)
	assert.Equal(t, other, v.entries[0].ws)
	_, ok := store.status(ws, uri)
	assert.False(t, ok)
	assert.Len(t, store.lastNotified, 1)
	assert.Len(t, store.pendingNotify, 1)
	n := len(host.changed)
	store.dropWorkspace(ws)
	assert.Len(t, host.changed, n)
	delete(host.workspaces, other.String())
	assert.Empty(t, store.items(), "workspaces without a slot are hidden")
	store.dropWorkspace(other)
	assert.Empty(t, store.sources)
	assert.Empty(t, store.lastNotified)
	assert.Empty(t, store.pendingNotify)
	assert.Empty(t, v.entries)
}

func TestStatusStoreSourceBecomesTab(t *testing.T) {
	host, store, ws := newStatusStoreTest(t)
	uri := statusStoreTestURI(t, "file:///terminal")
	store.updateDetached(ws, uri, func(r *programstatus.Store) {
		r.Apply(detachedTestReport(programstatus.Working, ""))
	})
	v := store.view()
	defer v.Close()
	host.names[statusKey{ws.String(), uri.String()}] = "build"
	records := programstatus.New()
	records.Apply(detachedTestReport(programstatus.Working, ""))
	store.tabStatusChanged(ws, uri, records.Summary())
	require.Nil(t, store.sources[statusKey{ws.String(), uri.String()}].records)
	require.Len(t, v.entries, 1)
	assert.Equal(t, "build", v.entries[0].name)
}

type statusStoreTestTabs struct {
	browser.TabManager
	component *browser.Component
}

type statusStoreTestHandler struct{ handler.TestHandler }

func (*statusStoreTestHandler) Close() error { return nil }
func (b statusStoreTestTabs) SetTabStatus(uri workspaceapi.URI, r programstatus.Report) error {
	if !b.component.SetTabStatus(uri, r) {
		return errors.New("missing tab")
	}
	return nil
}
func (b statusStoreTestTabs) EndTabStatus(uri workspaceapi.URI) error {
	if !b.component.EndTabStatus(uri) {
		return errors.New("missing tab")
	}
	return nil
}

func TestStatusStoreTabLifecycle(t *testing.T) {
	host, store, ws := newStatusStoreTest(t)
	cfg := browser.DefaultConfig()
	cfg.OnTabStatus = func(uri workspaceapi.URI, _, next programstatus.Summary) { store.tabStatusChanged(ws, uri, next) }
	cfg.OnTabRemoved = func(uri workspaceapi.URI) { store.tabRemoved(ws, uri) }
	b := browser.NewComponent(cfg)
	t.Cleanup(func() { require.NoError(t, b.Close()) })
	uri := statusStoreTestURI(t, "file:///tab")
	key := statusKey{ws.String(), uri.String()}
	host.names[key] = "build"
	h := &statusStoreTestHandler{}
	tab := b.NewTab(uri, 'b', "build", h, h)
	tabs := statusStoreTestTabs{component: b}
	v := store.view()
	defer v.Close()
	for _, state := range []programstatus.State{programstatus.Done, programstatus.Error} {
		store.setTabStatus(ws, tabs, uri, programstatus.Report{State: state, Progress: -1, App: "make", Msg: "result"})
		require.Len(t, v.entries, 1)
		assert.Equal(t, "build", v.entries[0].name)
		assert.Equal(t, state, v.entries[0].state)
		require.Nil(t, store.sources[key].records)
		changed := len(host.changed)
		tab.AckStatus()
		require.Len(t, v.entries, 1, "seen, the tab stays listed until it is removed")
		assert.Equal(t, programstatus.Idle, v.entries[0].state)
		assert.Equal(t, "idle · make", v.entries[0].sub, "it keeps the app it last reported")
		sum, ok := store.status(ws, uri)
		require.True(t, ok)
		assert.Empty(t, sum.Records)
		assert.Len(t, host.changed, changed+1, "the workspace learns its status went")
		rollup, _ := store.rollup(ws)
		assert.Empty(t, rollup.Records, "nothing it no longer reports weighs on the workspace")
	}
	store.setTabStatus(ws, tabs, uri, programstatus.Report{ID: "working", State: programstatus.Working, Progress: -1})
	store.setTabStatus(ws, tabs, uri, programstatus.Report{ID: "done", State: programstatus.Done, Progress: -1})
	store.endTabStatus(ws, tabs, uri)
	sum, ok := store.status(ws, uri)
	require.True(t, ok)
	require.Len(t, sum.Records, 1)
	assert.Equal(t, programstatus.Done, sum.State)
	changed := len(host.changed)
	require.True(t, b.RemoveTab(tab))
	assert.Empty(t, v.entries)
	_, ok = store.status(ws, uri)
	assert.False(t, ok)
	assert.Len(t, host.changed, changed+1, "removed with a record, the workspace learns its status went once")

	// Seen before it is removed, the tab goes all the same.
	tab = b.NewTab(uri, 'b', "build", h, h)
	store.setTabStatus(ws, tabs, uri, detachedTestReport(programstatus.Done, ""))
	tab.AckStatus()
	require.Len(t, v.entries, 1)
	require.True(t, b.RemoveTab(tab))
	assert.Empty(t, v.entries)
	assert.Empty(t, store.sources)

	// A tab that never reported leaves nothing behind.
	silent := statusStoreTestURI(t, "file:///silent")
	require.True(t, b.RemoveTab(b.NewTab(silent, 's', "silent", h, h)))
	assert.Empty(t, store.sources)

	tab = b.NewTab(uri, 'b', "build", h, h)
	require.True(t, b.RemoveTab(tab))
	require.Error(t, store.setTabStatus(ws, tabs, uri, detachedTestReport(programstatus.Working, "")))
	require.Error(t, store.endTabStatus(ws, tabs, uri))
	assert.Empty(t, store.sources, "a tab error must not create a detached source")
	delete(host.names, key)
	store.setTabStatus(ws, tabs, uri, detachedTestReport(programstatus.Working, ""))
	require.Len(t, v.entries, 1)
	assert.Equal(t, detachedStatusLabel, v.entries[0].name)
	store.endTabStatus(ws, tabs, uri)
	assert.Empty(t, v.entries)
}

func TestStatusStoreRestore(t *testing.T) {
	parse := func(raw string) workspaceapi.URI { return statusStoreTestURI(t, raw) }
	other, closed := parse("file:///other"), parse("file:///closed")
	// The tabs back, silent and live came back with the workspace, and
	// elsewhere with other; gone did not, and pty is a terminal that is
	// no tab.
	back, silent, live := parse("rune-agent://model/back"), parse("rune-agent://model/silent"),
		parse("rune-agent://model/live")
	gone, pty, elsewhere := parse("rune-agent://model/gone"), parse("file:///dev/ttys001"),
		parse("rune-agent://model/elsewhere")
	setup := func(t *testing.T) (*statusStoreTestHost, *statusStore, workspaceapi.URI, *statusView) {
		host, store, ws := newStatusStoreTest(t)
		host.addWorkspace(other, "2 other")
		for _, uri := range []workspaceapi.URI{back, silent, live} {
			host.names[statusKey{ws.String(), uri.String()}] = uri.Name()
		}
		host.names[statusKey{other.String(), elsewhere.String()}] = elsewhere.Name()
		v := store.view()
		t.Cleanup(func() { _ = v.Close() })
		return host, store, ws, v
	}
	working := func(app string) programstatus.Summary {
		records := programstatus.New()
		records.Apply(programstatus.Report{State: programstatus.Working, Progress: -1, App: app})
		return records.Summary()
	}
	type row struct{ uri, sub string }
	rows := func(v *statusView) []row {
		var ret []row
		for _, e := range v.entries {
			ret = append(ret, row{e.uri.String(), e.sub})
		}
		return ret
	}

	suite := []struct {
		name string
		// reported is where a program reported working with the app
		// fresh before the restore, as a tab or as a terminal that is
		// no tab, whichever it is.
		reported []workspaceapi.URI
		// into is the workspace restored: the one setup opens when it
		// is zero.
		into     workspaceapi.URI
		programs []idehistory.Program
		want     []row
	}{
		{
			name: "with no program saved, no tab that came back is listed",
		},
		{
			name:     "a program whose tab came back is listed idle, with the app it last reported",
			programs: []idehistory.Program{{URI: back, App: "rune-agent"}},
			want:     []row{{back.String(), "idle · rune-agent"}},
		},
		{
			name:     "a program that reported no app is listed idle",
			programs: []idehistory.Program{{URI: back}},
			want:     []row{{back.String(), "idle"}},
		},
		{
			name:     "a program whose tab did not come back is not listed",
			programs: []idehistory.Program{{URI: gone, App: "rune-agent"}},
		},
		{
			name: "the programs whose tab came back are listed, those whose tab did not are not",
			programs: []idehistory.Program{
				{URI: gone, App: "a"}, {URI: live, App: "b"}, {URI: pty, App: "c"}, {URI: back, App: "d"},
			},
			want: []row{{back.String(), "idle · d"}, {live.String(), "idle · b"}},
		},
		{
			name:     "a program saved twice is listed once, with the app saved first",
			programs: []idehistory.Program{{URI: back, App: "first"}, {URI: back, App: "second"}},
			want:     []row{{back.String(), "idle · first"}},
		},
		{
			name:     "a program that reported since keeps what it reported",
			reported: []workspaceapi.URI{live},
			programs: []idehistory.Program{{URI: live, App: "stale"}},
			want:     []row{{live.String(), "working · fresh"}},
		},
		{
			name:     "a program saved for a terminal that is no tab is not listed",
			programs: []idehistory.Program{{URI: pty, App: "make"}},
		},
		{
			name:     "a terminal that is no tab and reported since keeps what it reported",
			reported: []workspaceapi.URI{pty},
			programs: []idehistory.Program{{URI: pty, App: "stale"}},
			want:     []row{{pty.String(), "working"}},
		},
		{
			name:     "a program whose tab came back in another workspace is not listed in this one",
			programs: []idehistory.Program{{URI: elsewhere, App: "rune-agent"}},
		},
		{
			name:     "a program is listed in the workspace restored only",
			into:     other,
			programs: []idehistory.Program{{URI: elsewhere, App: "a"}, {URI: back, App: "b"}},
			want:     []row{{elsewhere.String(), "idle · a"}},
		},
		{
			name:     "a program restored into a workspace that is not open is not listed",
			into:     closed,
			programs: []idehistory.Program{{URI: back, App: "rune-agent"}},
		},
	}
	for _, tcase := range suite {
		t.Run(tcase.name, func(t *testing.T) {
			host, store, ws, v := setup(t)
			for _, uri := range tcase.reported {
				if _, ok := host.statusTabName(ws, uri); ok {
					store.tabStatusChanged(ws, uri, working("fresh"))
					continue
				}
				store.updateDetached(ws, uri, func(r *programstatus.Store) {
					r.Apply(programstatus.Report{State: programstatus.Working, Progress: -1, App: "fresh"})
				})
			}
			into := cmp.Or(tcase.into, ws)
			changed := len(host.changed)
			sources := len(store.sources)

			store.restore(into, tcase.programs)
			assert.Equal(t, tcase.want, rows(v))
			assert.Len(t, store.sources, sources+len(tcase.want)-len(tcase.reported),
				"what is not listed is not kept either")
			assert.Len(t, host.changed, changed, "what was restored weighs nothing on any workspace")
		})
	}

	t.Run("restoring twice lists a program once", func(t *testing.T) {
		_, store, ws, v := setup(t)
		programs := []idehistory.Program{{URI: back, App: "rune-agent"}}
		store.restore(ws, programs)
		store.restore(ws, programs)
		assert.Equal(t, []row{{back.String(), "idle · rune-agent"}}, rows(v))
	})

	t.Run("a restored program is saved as it was, for the next session to list it again", func(t *testing.T) {
		_, store, ws, _ := setup(t)
		programs := []idehistory.Program{{URI: back, App: "rune-agent"}, {URI: live}}
		store.restore(ws, programs)
		assert.Equal(t, programs, store.programs(ws))
		assert.Empty(t, store.programs(other))

		_, next, _, v := setup(t)
		next.restore(ws, store.programs(ws))
		assert.Equal(t, []row{{back.String(), "idle · rune-agent"}, {live.String(), "idle"}}, rows(v))
	})

	t.Run("a restored program lists what it reports next, and keeps the app it reports", func(t *testing.T) {
		host, store, ws, v := setup(t)
		store.restore(ws, []idehistory.Program{{URI: back, App: "stale"}})
		store.tabStatusChanged(ws, back, working("fresh"))
		assert.Equal(t, []row{{back.String(), "working · fresh"}}, rows(v))
		assert.Len(t, host.changed, 1, "what it reports weighs on its workspace")
		store.tabStatusChanged(ws, back, programstatus.Summary{Progress: -1})
		assert.Equal(t, []row{{back.String(), "idle · fresh"}}, rows(v))
		assert.Equal(t, []idehistory.Program{{URI: back, App: "fresh"}}, store.programs(ws))
	})

	t.Run("a restored program goes with its tab", func(t *testing.T) {
		host, store, ws, v := setup(t)
		store.restore(ws, []idehistory.Program{{URI: back, App: "rune-agent"}, {URI: live}})
		store.tabRemoved(ws, back)
		assert.Equal(t, []row{{live.String(), "idle"}}, rows(v))
		assert.Empty(t, host.changed, "as it reported nothing, no workspace learns it went")
		assert.Equal(t, []idehistory.Program{{URI: live}}, store.programs(ws))
	})

	t.Run("a restored program goes with its workspace", func(t *testing.T) {
		_, store, ws, v := setup(t)
		store.restore(ws, []idehistory.Program{{URI: back, App: "rune-agent"}})
		store.dropWorkspace(ws)
		assert.Empty(t, rows(v))
		assert.Empty(t, store.programs(ws))
	})
}

func TestStatusSourceLabel(t *testing.T) {
	for _, tc := range []struct {
		name, app, want string
	}{
		{"rolling-fox", "", "rolling-fox"},
		{"rolling-fox", "rune-agent", "rolling-fox (rune-agent)"},
		{"cargo", "cargo", "cargo"},
		{detachedStatusLabel, "", detachedStatusLabel},
		{detachedStatusLabel, "make", "make"},
		{"", "make", "make"},
		{"", "", ""},
	} {
		assert.Equal(t, tc.want, statusSourceLabel(tc.name, tc.app), "%q, %q", tc.name, tc.app)
	}
}

func TestStatusStoreDetached(t *testing.T) {
	idle := programstatus.Summary{Progress: -1}
	parse := func(s string) workspaceapi.URI {
		uri, err := workspaceapi.ParseURI(s)
		require.NoError(t, err)
		return uri
	}
	ttyA := parse("file:///dev/ttys001")
	ttyB := parse("file:///dev/ttys002")
	clearAll := programstatus.Report{State: programstatus.Clear}
	apply := func(d *statusStoreDetachedTest, uri workspaceapi.URI, r programstatus.Report) (
		prev, status programstatus.Summary, changed bool,
	) {
		return d.update(uri, func(s *programstatus.Store) { s.Apply(r) })
	}

	t.Run("zero value reads as empty", func(t *testing.T) {
		var d statusStoreDetachedTest
		_, ok := d.get(ttyA)
		assert.False(t, ok)
		assert.Empty(t, d.list())
		sum, uri := d.rollup()
		assert.Equal(t, idle, sum)
		assert.Equal(t, workspaceapi.URI{}, uri)
	})

	t.Run("update reports both statuses and whether they differ", func(t *testing.T) {
		d := newStatusStoreDetachedTest(t)
		prev, status, changed := apply(&d, ttyA, detachedTestReport(programstatus.Working, ""))
		assert.True(t, changed)
		assert.Equal(t, idle, prev, "an unknown uri was idle")
		assert.Equal(t, programstatus.Working, status.State)

		working := status
		prev, status, changed = apply(&d, ttyA, detachedTestReport(programstatus.Working, ""))
		assert.False(t, changed, "the same report again is not a change")
		assert.True(t, working.Equal(prev))
		assert.True(t, working.Equal(status))

		prev, status, changed = apply(&d, ttyA, detachedTestReport(programstatus.Done, "ok"))
		assert.True(t, changed)
		assert.True(t, working.Equal(prev))
		got, ok := d.get(ttyA)
		require.True(t, ok)
		assert.True(t, status.Equal(got))
		assert.Equal(t, "ok", got.Msg)
	})

	t.Run("ending keeps done and error", func(t *testing.T) {
		d := newStatusStoreDetachedTest(t)
		_, _, _ = apply(&d, ttyA, programstatus.Report{ID: "w", State: programstatus.Working, Progress: -1})
		_, _, _ = apply(&d, ttyA, programstatus.Report{ID: "d", State: programstatus.Done, Progress: -1})
		_, status, changed := d.update(ttyA, func(s *programstatus.Store) { s.DropTransient() })
		assert.True(t, changed)
		assert.Equal(t, programstatus.Done, status.State)
		require.Len(t, status.Records, 1)
	})

	t.Run("no record left forgets the uri", func(t *testing.T) {
		d := newStatusStoreDetachedTest(t)
		_, done, _ := apply(&d, ttyA, detachedTestReport(programstatus.Done, "ok"))

		prev, status, changed := apply(&d, ttyA, clearAll)
		assert.True(t, changed)
		assert.True(t, done.Equal(prev))
		assert.Equal(t, idle, status)
		_, ok := d.get(ttyA)
		assert.False(t, ok)
		assert.Empty(t, d.list())

		_, _, changed = apply(&d, ttyA, clearAll)
		assert.False(t, changed, "clearing an unknown uri is not a change")
		assert.Empty(t, d.list())
	})

	t.Run("a reported idle is kept apart from silence", func(t *testing.T) {
		d := newStatusStoreDetachedTest(t)
		_, reportedIdle, changed := apply(&d, ttyA, detachedTestReport(programstatus.Idle, ""))
		assert.True(t, changed)
		require.Len(t, reportedIdle.Records, 1)
		got, ok := d.get(ttyA)
		require.True(t, ok)
		assert.True(t, reportedIdle.Equal(got))
		sum, uri := d.rollup()
		assert.True(t, reportedIdle.Equal(sum))
		assert.Equal(t, ttyA, uri)
	})

	t.Run("list is sorted by uri", func(t *testing.T) {
		d := newStatusStoreDetachedTest(t)
		_, _, _ = apply(&d, ttyB, detachedTestReport(programstatus.Working, ""))
		_, _, _ = apply(&d, ttyA, detachedTestReport(programstatus.Done, ""))
		got := d.list()
		require.Len(t, got, 2)
		assert.Equal(t, ttyA, got[0].uri)
		assert.Equal(t, programstatus.Done, got[0].status.State)
		assert.Equal(t, ttyB, got[1].uri)
		assert.Equal(t, programstatus.Working, got[1].status.State)
	})

	t.Run("rollup picks the most pressing status", func(t *testing.T) {
		d := newStatusStoreDetachedTest(t)
		_, _, _ = apply(&d, ttyA, detachedTestReport(programstatus.Working, ""))
		sum, uri := d.rollup()
		assert.Equal(t, programstatus.Working, sum.State)
		assert.Equal(t, ttyA, uri)

		_, _, _ = apply(&d, ttyB, detachedTestReport(programstatus.Error, "boom"))
		sum, uri = d.rollup()
		assert.Equal(t, programstatus.Error, sum.State)
		assert.Equal(t, "boom", sum.Msg)
		assert.Equal(t, ttyB, uri)

		_, _, _ = apply(&d, ttyA, detachedTestReport(programstatus.Blocked, "approve?"))
		sum, uri = d.rollup()
		assert.Equal(t, programstatus.Blocked, sum.State)
		assert.Equal(t, ttyA, uri)

		_, _, _ = apply(&d, ttyA, clearAll)
		_, _, _ = apply(&d, ttyB, clearAll)
		sum, uri = d.rollup()
		assert.Equal(t, idle, sum)
		assert.Equal(t, workspaceapi.URI{}, uri)
	})

	t.Run("rollup breaks ties by uri order", func(t *testing.T) {
		d := newStatusStoreDetachedTest(t)
		_, _, _ = apply(&d, ttyB, detachedTestReport(programstatus.Error, "b"))
		_, _, _ = apply(&d, ttyA, detachedTestReport(programstatus.Error, "a"))
		sum, uri := d.rollup()
		assert.Equal(t, "a", sum.Msg)
		assert.Equal(t, ttyA, uri)
	})
}
