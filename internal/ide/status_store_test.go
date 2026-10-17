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
	"github.com/unstablebuild/rune-go-sdk/handler"
	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/browser"
	"unstable.build/rune/internal/cell"
	"unstable.build/rune/internal/handler/handlertest"
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
		{slot: 1, wsName: "2 proj", uri: idle, name: "idle", status: programstatus.Summary{Progress: -1}},
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
	}, rows, "a source is listed once, with its state and app; one that reported nothing is not")

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
				{width: 45, height: 1, wantSize: []int{45, 1}, want: `
 No program reported a status │  No preview  `[1:]},
				{keys: "jk<down><up>", want: `
 No program reported a status │  No preview  `[1:]},
				{keys: "<enter>", wantShown: []string{}, want: `
 No program reported a status │  No preview  `[1:]},
				{width: 20, height: 3, want: `
 No program reporte 
                    
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
				{width: 40, height: 16, wantSize: []int{
					statusSidebarMin + statusDividerWidth + len(statusPreviewNone) + 4, 15,
				}, want: `
▌! claude               │               
▌  blocked              │               
                        │               
 ✗ terminal             │               
   error                │               
                        │               
 ✓ make                 │               
   done                 │  No preview   
                        │               
 ⠋ agent                │               
   working · rune-agent │               
   ╢░░░░░_________╟ 40% │               
                        │               
   vim                  │               
   idle                 │               
                        │               `[1:]},
				// A tab never shown is fit to the preview.
				{keys: "jjj", want: `
 ! claude               │agent0678901234
   blocked              │agent1678901234
                        │agent2678901234
 ✗ terminal             │agent3678901234
   error                │agent4678901234
                        │agent5678901234
 ✓ make                 │agent6678901234
   done                 │agent7678901234
                        │agent8678901234
▌⠋ agent                │agent9678901234
▌  working · rune-agent │agent1078901234
▌  ╢░░░░░_________╟ 40% │agent1178901234
                        │agent1278901234
   vim                  │agent1378901234
   idle                 │agent1478901234
                        │agent1578901234`[1:]},
				{keys: "<enter>", wantShown: []string{"file:///agent"}, want: `
 ! claude               │agent0678901234
   blocked              │agent1678901234
                        │agent2678901234
 ✗ terminal             │agent3678901234
   error                │agent4678901234
                        │agent5678901234
 ✓ make                 │agent6678901234
   done                 │agent7678901234
                        │agent8678901234
▌⠋ agent                │agent9678901234
▌  working · rune-agent │agent1078901234
▌  ╢░░░░░_________╟ 40% │agent1178901234
                        │agent1278901234
   vim                  │agent1378901234
   idle                 │agent1478901234
                        │agent1578901234`[1:]},
			},
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
			steps: []statusViewStep{{width: 40, height: 19, want: `
▌✓ done                 │               
▌  done                 │               
                        │               
 ⠋ none                 │               
   working              │               
                        │               
 ⠋ zero                 │               
   working              │               
   ╢_______________╟ 0% │               
                        │  No preview   
 ⠋ full                 │               
   working              │               
   ╢░░░░░░░░░░░░░╟ 100% │               
                        │               
 ⠋ over                 │               
   working              │               
   ╢░░░░░░░░░░░░░╟ 100% │               
                        │               
                        │               `[1:]}},
		},
		{
			name:  "navigation wraps around both ways",
			setup: threeDone,
			steps: []statusViewStep{
				{width: 40, height: 8, want: `
▌✓ a                    │               
▌  done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
 ✓ c                    │               
   done                 │               `[1:]},
				{keys: "k", want: `
 ✓ a                    │               
   done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
▌✓ c                    │               
▌  done                 │               `[1:]},
				{keys: "<down>", want: `
▌✓ a                    │               
▌  done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
 ✓ c                    │               
   done                 │               `[1:]},
				{keys: "<c-j>", want: `
 ✓ a                    │               
   done                 │               
                        │               
▌✓ b                    │  No preview   
▌  done                 │               
                        │               
 ✓ c                    │               
   done                 │               `[1:]},
				{keys: "<c-n>", want: `
 ✓ a                    │               
   done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
▌✓ c                    │               
▌  done                 │               `[1:]},
				{keys: "<c-p><up>", want: `
▌✓ a                    │               
▌  done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
 ✓ c                    │               
   done                 │               `[1:]},
				{keys: "<c-k>", want: `
 ✓ a                    │               
   done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
▌✓ c                    │               
▌  done                 │               `[1:]},
				{keys: "j", want: `
▌✓ a                    │               
▌  done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
 ✓ c                    │               
   done                 │               `[1:]},
				{keys: "x<left><right>", want: `
▌✓ a                    │               
▌  done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
 ✓ c                    │               
   done                 │               `[1:]},
				{keys: "kk<enter>", wantShown: []string{"file:///b"}, want: `
 ✓ a                    │               
   done                 │               
                        │               
▌✓ b                    │  No preview   
▌  done                 │               
                        │               
 ✓ c                    │               
   done                 │               `[1:]},
				{keys: "<esc>q<c-c>", wantShown: []string{"file:///b"}, want: `
 ✓ a                    │               
   done                 │               
                        │               
▌✓ b                    │  No preview   
▌  done                 │               
                        │               
 ✓ c                    │               
   done                 │               `[1:]},
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
				{width: 40, height: 5, want: `
▌✓ a                    │               
▌  done                 │               
                        │  No preview   
 ✓ b                    │               
   done                 │               `[1:]},
				{keys: "jj", want: `
 ✓ b                    │               
   done                 │               
                        │  No preview   
▌✓ c                    │               
▌  done                 │               `[1:]},
				{keys: "jj", want: `
 ✓ d                    │               
   done                 │               
                        │  No preview   
▌✓ e                    │               
▌  done                 │               `[1:]},
				{keys: "k", want: `
▌✓ d                    │               
▌  done                 │               
                        │  No preview   
 ✓ e                    │               
   done                 │               `[1:]},
				{keys: "j", want: `
 ✓ d                    │               
   done                 │               
                        │  No preview   
▌✓ e                    │               
▌  done                 │               `[1:]},
				{keys: "j", want: `
▌✓ a                    │               
▌  done                 │               
                        │  No preview   
 ✓ b                    │               
   done                 │               `[1:]},
				{keys: "k", want: `
 ✓ d                    │               
   done                 │               
                        │  No preview   
▌✓ e                    │               
▌  done                 │               `[1:]},
			},
		},
		{
			name: "a program taller than the view",
			setup: func(f *statusViewFixture) {
				f.report("a", statusTestReport(done, "a", -1))
				f.report("b", statusTestReport(working, "b", 50))
			},
			steps: []statusViewStep{
				{width: 40, height: 2, want: `
▌✓ a                    │  No preview   
▌  done                 │               `[1:]},
				{keys: "j", want: `
▌⠋ b                    │  No preview   
▌  working              │               `[1:]},
				{width: 40, height: 1, want: `
▌⠋ b                    │  No preview   `[1:]},
				{keys: "k", want: `
▌✓ a                    │  No preview   `[1:]},
			},
		},
		{
			name: "long and wide names",
			setup: func(f *statusViewFixture) {
				f.report("a", statusTestReport(done, strings.Repeat("a", 50), -1))
				f.report("b", statusTestReport(done, strings.Repeat("界", 25), -1))
			},
			steps: []statusViewStep{
				{width: 60, height: 5, wantSize: []int{statusSidebarMax + 1 + len(statusPreviewNone) + 4, 5}, want: `
▌✓ aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa │                   
▌  done                                 │                   
                                        │    No preview     
 ✓ 界 界 界 界 界 界 界 界 界 界 界 界 界 界 界 界 界 界  │                   
   done                                 │                   `[1:]},
			},
		},
		{
			name:  "narrower than the sidebar and the preview",
			setup: threeDone,
			steps: []statusViewStep{
				{width: 10, height: 2, want: `
▌✓ a      
▌  done   `[1:]},
				{width: statusSidebarMin, height: 2, want: `
▌✓ a                    
▌  done                 `[1:]},
				{width: statusSidebarMin + 1, height: 2, want: `
▌✓ a                    │
▌  done                 │`[1:]},
				{width: statusSidebarMin + 2, height: 2, want: `
▌✓ a                    │N
▌  done                 │ `[1:]},
			},
		},
		{
			name:  "no room at all",
			setup: threeDone,
			steps: []statusViewStep{
				{width: 0, height: 0, keys: "j", want: ""},
				{width: 30, height: 2, want: `
▌✓ b                    │No pr
▌  done                 │     `[1:]},
			},
		},
		{
			name: "programs come and go while it is open",
			setup: func(f *statusViewFixture) {
				f.report("a", statusTestReport(blocked, "a", -1))
				f.report("b", statusTestReport(done, "b", -1))
			},
			steps: []statusViewStep{
				{width: 40, height: 8, keys: "j", want: `
 ! a                    │               
   blocked              │               
                        │               
▌✓ b                    │  No preview   
▌  done                 │               
                        │               
                        │               
                        │               `[1:]},
				{do: func(f *statusViewFixture) { f.report("c", statusTestReport(failed, "c", -1)) }, want: `
 ! a                    │               
   blocked              │               
                        │               
 ✗ c                    │  No preview   
   error                │               
                        │               
▌✓ b                    │               
▌  done                 │               `[1:]},
				{do: func(f *statusViewFixture) { f.report("b", statusTestReport(working, "b", 10)) }, want: `
 ✗ c                    │               
   error                │               
                        │               
▌⠋ b                    │  No preview   
▌  working              │               
▌  ╢░_____________╟ 10% │               
                        │               
                        │               `[1:]},
				{do: func(f *statusViewFixture) { f.clear("b") }, want: `
 ! a                    │               
   blocked              │               
                        │               
▌✗ c                    │  No preview   
▌  error                │               
                        │               
                        │               
                        │               `[1:]},
				{do: func(f *statusViewFixture) { f.clear("a"); f.clear("c") }, want: `
 No program reported a status │         
                              │         
                              │         
                              │No previe
                              │         
                              │         
                              │         
                              │         `[1:]},
				{do: func(f *statusViewFixture) { f.report("d", statusTestReport(done, "d", -1)) }, want: `
▌✓ d                    │               
▌  done                 │               
                        │               
                        │  No preview   
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
				{width: 40, height: 8, want: `
▌✓ a                    │               
▌  done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
 ✓ c                    │               
   done                 │               `[1:]},
				// Either row of a program, at any column of the sidebar.
				{events: []term.Event{statusTestClick(5, 4)}, want: `
 ✓ a                    │               
   done                 │               
                        │               
▌✓ b                    │  No preview   
▌  done                 │               
                        │               
 ✓ c                    │               
   done                 │               `[1:]},
				{events: []term.Event{statusTestClick(0, 6)}, want: `
 ✓ a                    │               
   done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
▌✓ c                    │               
▌  done                 │               `[1:]},
				// A gap, the preview, the divider and other buttons select
				// nothing.
				{events: []term.Event{statusTestClick(5, 5)}, want: `
 ✓ a                    │               
   done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
▌✓ c                    │               
▌  done                 │               `[1:]},
				{events: []term.Event{statusTestClick(30, 0), statusTestClick(24, 0)}, want: `
 ✓ a                    │               
   done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
▌✓ c                    │               
▌  done                 │               `[1:]},
				{events: []term.Event{
					{Type: term.EventMouse, Key: term.MouseWheelUp, MouseX: 1, MouseY: 0},
					{Type: term.EventMouse, Key: term.MouseRight, MouseX: 1, MouseY: 0},
				}, want: `
 ✓ a                    │               
   done                 │               
                        │               
 ✓ b                    │  No preview   
   done                 │               
                        │               
▌✓ c                    │               
▌  done                 │               `[1:]},
			},
		},
		{
			name: "previews",
			setup: func(f *statusViewFixture) {
				// Larger than the preview: it loses its top rows and right
				// columns.
				f.report("a", statusTestReport(blocked, "large", -1))
				f.preview("a", statusPreview{
					content: &statusPreviewTestHandler{name: "L", width: 20, height: 5}, width: 20, height: 5,
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
				{width: 40, height: 4, wantSize: []int{statusSidebarMin + 1 + 20, 11}, want: `
▌! large                │L12345678901234
▌  blocked              │L22345678901234
                        │L32345678901234
 ✗ small                │L42345678901234`[1:]},
				{keys: "j", wantSize: []int{statusSidebarMin + 1 + 4, 11}, want: `
▌✗ small                │S023           
▌  error                │S123           
                        │               
 ✓ hidden               │               `[1:]},
				{keys: "j", wantSize: []int{statusSidebarMin + 1 + statusPreviewWidth, statusPreviewHeight}, want: `
▌✓ hidden               │H02345678901234
▌  done                 │H12345678901234
                        │H22345678901234
 ⠋ gone                 │H32345678901234`[1:]},
				{keys: "j", want: `
▌⠋ gone                 │               
▌  working              │  No preview   
                        │               
                        │               `[1:]},
				// A preview drawing the inbox, as previewing the workspace it
				// floats over would, leaves the nested inbox blank.
				{do: func(f *statusViewFixture) {
					f.preview("d", statusPreview{content: f.v, width: 15, height: 4})
				}, want: `
▌⠋ gone                 │               
▌  working              │               
                        │               
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
				{width: 40, height: 14, want: `
▌! agent                │agent0678901234
▌  blocked · claude     │agent1678901234
                        │agent2678901234
 ✗ test                 │agent3678901234
   error                │agent4678901234
                        │agent5678901234
 ✓ make                 │agent6678901234
   done                 │agent7678901234
                        │agent8678901234
 ⠋ build                │agent9678901234
   working              │agent1078901234
   ╢░░░░░_________╟ 40% │agent1178901234
                        │agent1278901234
   vim                  │agent1378901234`[1:]},
				// Done with, it closes at once and the next program is
				// selected.
				{keys: "j<delete>", wantClosed: []string{"file:///test"}, wantConfirms: []string{}, want: `
 ! agent                │               
   blocked · claude     │               
                        │               
▌✓ make                 │               
▌  done                 │               
                        │               
 ⠋ build                │  No preview   
   working              │               
   ╢░░░░░_________╟ 40% │               
                        │               
   vim                  │               
   idle                 │               
                        │               
                        │               `[1:]},
				{keys: "<backspace>", wantClosed: []string{"file:///test", "file:///make"}, want: `
 ! agent                │               
   blocked · claude     │               
                        │               
▌⠋ build                │               
▌  working              │               
▌  ╢░░░░░_________╟ 40% │               
                        │  No preview   
   vim                  │               
   idle                 │               
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
 ! agent                │               
   blocked · claude     │               
                        │               
▌⠋ build                │               
▌  working              │               
▌  ╢░░░░░_________╟ 40% │               
                        │  No preview   
   vim                  │               
   idle                 │               
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
▌! agent                │agent0678901234
▌  blocked · claude     │agent1678901234
                        │agent2678901234
 ⠋ build                │agent3678901234
   working              │agent4678901234
   ╢░░░░░_________╟ 40% │agent5678901234
                        │agent6678901234
   vim                  │agent7678901234
   idle                 │agent8678901234
                        │agent9678901234
                        │agent1078901234
                        │agent1178901234
                        │agent1278901234
                        │agent1378901234`[1:]},
				{do: func(f *statusViewFixture) { f.host.yes() }, wantClosed: []string{
					"file:///test", "file:///make", "file:///agent",
				}, want: `
▌⠋ build                │               
▌  working              │               
▌  ╢░░░░░_________╟ 40% │               
                        │               
   vim                  │               
   idle                 │               
                        │  No preview   
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
▌⠋ build                │               
▌  working              │               
▌  ╢░░░░░_________╟ 40% │               
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
                        │               `[1:]},
				{keys: "<delete>", wantConfirms: []string{
					"Are you sure you want to close `build` in `1 home`? It is working.\n\n" +
						"- working · build: Compiling — 40%",
					"Are you sure you want to close `agent` in `1 home`? It is blocked.\n\n" +
						"- blocked · claude (tool/edit): Allow edit? — edit main.go",
					"Are you sure you want to close `build` in `1 home`? It is working.\n\n" +
						"- working · build: Compiling — 40%",
				}, want: `
▌⠋ build                │               
▌  working              │               
▌  ╢░░░░░_________╟ 40% │               
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
                        │               `[1:]},
				{do: func(f *statusViewFixture) { f.host.yes() }, want: `
 No program reported a status │         
                              │         
                              │         
                              │         
                              │         
                              │         
                              │No previe
                              │         
                              │         
                              │         
                              │         
                              │         
                              │         
                              │         `[1:]},
				{keys: "<delete><backspace>", wantClosed: []string{
					"file:///test", "file:///make", "file:///agent", "file:///vim", "file:///build",
				}, want: `
 No program reported a status │         
                              │         
                              │         
                              │         
                              │         
                              │         
                              │No previe
                              │         
                              │         
                              │         
                              │         
                              │         
                              │         
                              │         `[1:]},
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
	// 0-1 a, 2 gap, 3-4 b.
	v.Resize(40, 6)
	handlertest.DrawHandler(v, 40, 6)
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
		{desc: "a program's second row", x: 5, y: 4, want: b},
		{desc: "a gap", x: 5, y: 2, want: b},
		{desc: "below the programs", x: 5, y: 5, want: b},
		{desc: "a program's first row", x: 1, y: 0, want: a},
		{desc: "the preview", x: 30, y: 4, want: a},
	} {
		assert.True(t, click(tc.x, tc.y), tc.desc)
		assert.Equal(t, tc.want, v.sel, tc.desc)
	}
	_, handled := v.Handle(term.Event{Type: term.EventMouse, Key: term.MouseWheelDown, MouseX: 1, MouseY: 1})
	assert.False(t, handled, "only clicks select")

	nameAttrs := func(y int) term.Attributes {
		w := cell.NewBufferWriter(context.Background(), 40, 6)
		v.Draw(w)
		return w.RawCells()[y][statusTextX].Attributes()
	}
	require.Zero(t, nameAttrs(3).Attrs&term.AttrBold, "names are not bold")
	_, handled = v.Handle(term.Event{Type: term.EventMouse, MouseX: 5, MouseY: 4})
	assert.True(t, handled)
	assert.NotZero(t, nameAttrs(3).Attrs&term.AttrBold, "the name under the mouse is bold")
	assert.Zero(t, nameAttrs(0).Attrs&term.AttrBold)
	assert.Equal(t, a, v.sel, "hovering selects nothing")
	_, _ = v.Handle(term.Event{Type: term.EventMouse, MouseX: 30, MouseY: 4})
	assert.Zero(t, nameAttrs(3).Attrs&term.AttrBold, "off the sidebar, nothing is bold")
}

func TestStatusViewSidebarWidth(t *testing.T) {
	_, store, ws := newStatusStoreTest(t)
	v := store.view()
	defer v.Close()
	for _, tc := range []struct {
		name, app string
		want      int
	}{
		{name: "short", want: statusSidebarMin},
		{name: strings.Repeat("a", 25), want: statusTextX + 25 + 1},
		{name: strings.Repeat("界", 30), want: statusSidebarMax},
		{name: "short", app: strings.Repeat("b", 20), want: statusTextX + len([]rune("idle · ")) + 20 + 1},
	} {
		v.set(statusInbox{}.entries([]statusItem{{ws: ws, name: tc.name, status: programstatus.Summary{
			Progress: -1, App: tc.app,
			Records: []programstatus.Record{{Report: programstatus.Report{Progress: -1}}},
		}}}))
		w, _ := v.sidebarSize()
		assert.Equal(t, tc.want, w, "%q %q", tc.name, tc.app)
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
	assert.Equal(t, []int{sideW + statusPreviewWidth, statusPreviewHeight}, []int{w, h},
		"a tab never sized asks for the default size")

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
	assert.Equal(t, []int{sideW + 20, 11}, []int{w, h}, "the sidebar is taller than the terminal")
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
	assert.Equal(t, []int{sideW + resizedW, resizedH}, []int{w, h}, "the view follows the size of the tab")

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
	assert.Equal(t, []int{sideW + statusPreviewWidth, statusPreviewHeight}, []int{w, h},
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
	const width = statusSidebarMin + 1
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
	for y := range 9 {
		got = append(got, text(y))
	}
	require.Equal(t, []string{
		"▌! terminal",
		"▌  blocked",
		"",
		" ✓ terminal",
		"   done",
		"",
		" ⠋ terminal",
		"   working",
		"   ╢░░░░░░░_______╟ 50%",
	}, got, "the title and the message are left to the preview")

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
			name: "blocked", y: 0, icon: selected(term.Attributes{Fg: term.ColorRed}),
			label: selected(term.Attributes{Fg: term.ColorYellow}), state: selected(term.Attributes{Fg: term.ColorYellow}),
		},
		{name: "done", y: 3, icon: term.Attributes{Fg: term.ColorSilver}, label: term.Attributes{Fg: term.ColorGreen}, state: term.Attributes{Fg: term.ColorGreen}},
		{name: "working", y: 6, icon: term.Attributes{Fg: term.ColorYellow}, state: term.Attributes{Fg: term.ColorYellow}},
	} {
		assert.Equal(t, tc.icon, cells[tc.y][1].Attributes(), "%s icon", tc.name)
		assert.Equal(t, repeat(tc.label, len("terminal")), attrs(tc.y, statusTextX, statusTextX+len("terminal")), "%s name", tc.name)
		assert.Equal(t, repeat(tc.state, len(tc.name)), attrs(tc.y+1, statusTextX, statusTextX+len(tc.name)), "%s state", tc.name)
	}
	marker := term.Attributes{Fg: statusTestStyle().markerAttr.Fg, Bg: statusTestStyle().selectedAttr.Bg}
	for _, y := range []int{0, 1} {
		assert.Equal(t, marker, cells[y][0].Attributes(),
			"the marker at %d is styled as the marker of the focused tab, over the selection", y)
		assert.Equal(t, repeat(statusTestStyle().selectedAttr, 5), attrs(y, statusSidebarMin-5, statusSidebarMin),
			"the selection paints the whole width of row %d", y)
	}
	assert.Equal(t, repeat(term.Attributes{}, statusSidebarMin), attrs(2, 0, statusSidebarMin), "the gap is not selected")
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
	// The program spins in the second column of its first row; nothing
	// else moves.
	spinning := []term.Coordinates{{X: 1, Y: 0}}
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
		{desc: "room below scrolls back up", top: 3, sel: 3, rows: 6, want: 2},
		{desc: "everything fits", top: 2, sel: 3, rows: 20, want: 0},
	} {
		assert.Equal(t, tc.want, statusScroll(tc.top, tc.sel, heights, tc.rows), tc.desc)
	}
}

func TestStatusProgress(t *testing.T) {
	for _, tc := range []struct {
		width, pct int
		want       string
	}{
		{width: 12, pct: 0, want: "╢_______╟ 0%"},
		{width: 12, pct: 50, want: "╢░░░___╟ 50%"},
		{width: 13, pct: 100, want: "╢░░░░░░╟ 100%"},
		{width: 4, pct: 50, want: "50%"},
	} {
		w := term.NewStringWriter(tc.width, 1)
		statusProgress(w, 0, 0, tc.width, tc.pct)
		require.NoError(t, w.Flush())
		assert.Equal(t, tc.want, strings.TrimRight(w.String(), " "), "%d%% in %d", tc.pct, tc.width)
	}
}

func detachedTestReport(state programstatus.State, msg string) programstatus.Report {
	return programstatus.Report{State: state, Progress: -1, Msg: msg}
}

func statusTestStyle() statusInboxStyle {
	style := defaultStatusInboxStyle()
	style.markerAttr = term.Attributes{Fg: term.ColorPurple}
	style.selectedAttr = term.Attributes{Bg: term.ColorNavy}
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
	b := browser.NewComponent(cfg)
	t.Cleanup(func() { require.NoError(t, b.Close()) })
	uri := statusStoreTestURI(t, "file:///tab")
	host.names[statusKey{ws.String(), uri.String()}] = "build"
	h := &statusStoreTestHandler{}
	tab := b.NewTab(uri, 'b', "build", h, h)
	tabs := statusStoreTestTabs{component: b}
	v := store.view()
	defer v.Close()
	for _, state := range []programstatus.State{programstatus.Done, programstatus.Error} {
		store.setTabStatus(ws, tabs, uri, detachedTestReport(state, "result"))
		require.Len(t, v.entries, 1)
		assert.Equal(t, "build", v.entries[0].name)
		require.Nil(t, store.sources[statusKey{ws.String(), uri.String()}].records)
		tab.AckStatus()
		assert.Empty(t, v.entries)
		_, ok := store.status(ws, uri)
		assert.False(t, ok)
	}
	store.setTabStatus(ws, tabs, uri, programstatus.Report{ID: "working", State: programstatus.Working, Progress: -1})
	store.setTabStatus(ws, tabs, uri, programstatus.Report{ID: "done", State: programstatus.Done, Progress: -1})
	store.endTabStatus(ws, tabs, uri)
	sum, ok := store.status(ws, uri)
	require.True(t, ok)
	require.Len(t, sum.Records, 1)
	assert.Equal(t, programstatus.Done, sum.State)
	require.True(t, b.RemoveTab(tab))
	assert.Empty(t, v.entries)
	_, ok = store.status(ws, uri)
	assert.False(t, ok)
	require.Error(t, store.setTabStatus(ws, tabs, uri, detachedTestReport(programstatus.Working, "")))
	require.Error(t, store.endTabStatus(ws, tabs, uri))
	assert.Empty(t, store.sources, "a tab error must not create a detached source")
	delete(host.names, statusKey{ws.String(), uri.String()})
	store.setTabStatus(ws, tabs, uri, detachedTestReport(programstatus.Working, ""))
	require.Len(t, v.entries, 1)
	assert.Equal(t, detachedStatusLabel, v.entries[0].name)
	store.endTabStatus(ws, tabs, uri)
	assert.Empty(t, v.entries)
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
