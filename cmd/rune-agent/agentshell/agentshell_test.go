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

package agentshell

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/unstablebuild/rune-go-sdk/api/browserapi"
	"github.com/unstablebuild/rune-go-sdk/api/llmapi"
	"github.com/unstablebuild/rune-go-sdk/api/storageapi/storagestub"
	"github.com/unstablebuild/rune-go-sdk/component"
	"github.com/unstablebuild/rune-go-sdk/iterator"
	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/cmd/rune-agent/agent/audit"
	"unstable.build/rune/cmd/rune-agent/agent/skills"
	"unstable.build/rune/cmd/rune-agent/dialogue/dialoguemanager"
	"unstable.build/rune/internal/handler/handlertest"
)

func TestShellFloatCloseLeavesItsWindow(t *testing.T) {
	t.Parallel()
	const id = "rolling-fox"
	storage := storagestub.NewInMemoryService()
	auditStore := audit.NewStore(storage)
	auditStore.Append(context.Background(), id, audit.Entry{
		StartedAt: time.Now(),
		Messages:  []llmapi.Message{{Role: llmapi.RoleUser, Content: "question"}},
	})
	for _, tc := range []struct {
		name string
		show func(s *shell) (iterator.Iterator[component.Responsive], error)
	}{
		{"history", func(s *shell) (iterator.Iterator[component.Responsive], error) {
			return s.showHistory(context.Background(), id)
		}},
		{"audit", func(s *shell) (iterator.Iterator[component.Responsive], error) {
			return s.showAudit(context.Background(), id)
		}},
		{"skill", func(s *shell) (iterator.Iterator[component.Responsive], error) {
			return s.showSkill("explore")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wm := &floatingWindowManager{}
			s := &shell{
				wm:         wm,
				auditStore: auditStore,
				store: &compactDialogueStore{dialogue: dialoguemanager.Dialogue{
					ID: id,
					Messages: []llmapi.Message{
						{Role: llmapi.RoleUser, Content: "question"},
					},
				}},
				skillRegistry: skills.NewRegistry(osFS{}, dirURI(t.TempDir()), nil, nil),
			}
			_, err := tc.show(s)
			require.NoError(t, err)
			require.NotNil(t, wm.floated)

			require.NoError(t, wm.floated.Close())

			assert.Zero(t, wm.closeWindowCalls,
				"the window may already show other content when the view is released")
		})
	}
}

// floatingWindowManager records the last floating window opened and
// every CloseWindow call.
type floatingWindowManager struct {
	stubWindowManager
	floated          browserapi.Floating
	closeWindowCalls int
}

func (m *floatingWindowManager) Floating(
	f browserapi.Floating, _ browserapi.FloatingConfig,
) (browserapi.Window, error) {
	m.floated = f
	return nil, nil
}

func (m *floatingWindowManager) CloseWindow(browserapi.Window) error {
	m.closeWindowCalls++
	return nil
}

type forkPickerCalls struct{ selected, closed int }

func newPointerLeaveForkPicker(calls *forkPickerCalls) *forkPickerHandler {
	candidates := []forkCandidate{
		{messageIndex: 0, label: "user: Fix the off-by-one in alpha.go", preview: "Fix the off-by-one in **alpha.go**"},
		{messageIndex: 1, label: "assistant: The index is off by one", preview: "The index is off by one:\n\n" +
			"```go\nfunc Last(s []int) int {\n\treturn s[len(s)-1]\n}\n```"},
		{messageIndex: 3, label: "user: Also add a test for the empty slice case", preview: "Also add a test"},
		{messageIndex: 4, label: "assistant: Added TestLastEmpty", preview: "**Tool Call:** apply_patch\n```\n{}\n```"},
	}
	h := newForkPickerHandler(candidates,
		func(forkCandidate) error { calls.selected++; return nil },
		func() error { calls.closed++; return nil })
	h.Handle(term.Event{Type: term.EventKey, Key: term.KeyArrowDown})
	return h
}

func TestForkPickerHandlerPointerLeaves(t *testing.T) {
	t.Parallel()
	for _, size := range []struct{ width, height int }{{60, 15}, {80, 30}, {1, 1}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			t.Parallel()
			calls := &forkPickerCalls{}
			handlertest.RunPointerLeaves(t, newPointerLeaveForkPicker(calls), size.width, size.height)
			assert.Equal(t, forkPickerCalls{}, *calls)
		})
	}
}

func TestForkPickerHandlerPointerLeaveKeepsFocus(t *testing.T) {
	t.Parallel()
	const width, height = 80, 30
	mouse := func(key term.Key, x, y int) term.Event {
		return term.Event{
			Type: term.EventMouse, Key: key, MouseX: x, MouseY: y,
			Context: term.ContextWithPointer(context.Background(), new(term.Pointer)),
		}
	}
	for _, tc := range []struct {
		name    string
		gesture []term.Event
	}{
		{name: "nothing"},
		{name: "hover", gesture: []term.Event{mouse(0, 5, 17)}},
		{name: "click", gesture: []term.Event{mouse(term.MouseLeft, 5, 18), mouse(term.MouseRelease, 5, 18)}},
		{name: "press", gesture: []term.Event{mouse(term.MouseLeft, 5, 19)}},
		{name: "wheel", gesture: []term.Event{mouse(term.MouseWheelDown, 5, 17)}},
	} {
		for _, leave := range []term.Coordinates{
			{X: -1, Y: 17}, {X: width, Y: 17}, {X: 5, Y: -1}, {X: 5, Y: height}, {X: 500, Y: 500},
		} {
			t.Run(fmt.Sprintf("%s then %v", tc.name, leave), func(t *testing.T) {
				t.Parallel()
				calls := &forkPickerCalls{}
				h := newPointerLeaveForkPicker(calls)
				h.Resize(width, height)
				for _, ev := range tc.gesture {
					h.Handle(ev)
				}
				before, _ := h.Selection()

				exit, _ := h.Handle(term.Event{Type: term.EventMouse, MouseX: leave.X, MouseY: leave.Y})
				h.Draw(term.NewStringWriter(width, height))

				after, ok := h.Selection()
				assert.False(t, exit)
				assert.True(t, ok)
				assert.Equal(t, before, after)
				assert.Equal(t, "assistant: The index is off by one", after)
				assert.Equal(t, forkPickerCalls{}, *calls)
			})
		}
	}
}
