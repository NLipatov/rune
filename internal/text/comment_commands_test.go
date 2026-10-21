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

package text

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/unstablebuild/rune-go-sdk/api/textapi"
	"github.com/unstablebuild/rune-go-sdk/api/workspaceapi"
	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/handler/handlertest"
)

func TestSubscribeCommentCommands(t *testing.T) {
	cwd, err := workspaceapi.ParseURI("memory:///")
	require.NoError(t, err)
	uri := workspaceapi.Join(cwd, "comment.go")

	registry := newIndentTestWorkspaceRegistry()
	fileRegistry := NewFileCommandRegistry(cwd, registry)
	h := &indentTestHandler{uri: uri}
	c := setupCursorContent(t, 80, 10, "a\nb", false)
	attachCommentTestView(c, CommentSpec{
		Line:  []string{"//"},
		Block: []CommentBlock{{Start: "/*", End: "*/"}},
	})

	wrapped, err := SubscribeCommentCommands(uri, fileRegistry, c, h)
	require.NoError(t, err)
	commands := registry.sub[cwd.String()]
	require.Contains(t, commands, CommandToggleLineComment)
	require.Contains(t, commands, CommandToggleBlockComment)

	err = commands[CommandToggleLineComment].HandleCommand(context.Background(), textapi.Command{
		URI:  uri,
		Name: CommandToggleLineComment,
	})
	require.NoError(t, err)
	assert.Equal(t, "// a\nb", c.buffer().String())

	require.True(t, c.SelectRange(term.Coordinates{Y: 1, X: 0}, term.Coordinates{Y: 1, X: 1}))
	err = commands[CommandToggleBlockComment].HandleCommand(context.Background(), textapi.Command{
		URI:  uri,
		Name: CommandToggleBlockComment,
	})
	require.NoError(t, err)
	assert.Equal(t, "// a\n/*b*/", c.buffer().String())

	require.NoError(t, wrapped.Close())
	assert.NotContains(t, registry.sub[cwd.String()], CommandToggleLineComment)
	assert.NotContains(t, registry.sub[cwd.String()], CommandToggleBlockComment)
}

func TestCommentCommandLineWithoutSpecReturnsError(t *testing.T) {
	cwd, err := workspaceapi.ParseURI("memory:///")
	require.NoError(t, err)
	uri := workspaceapi.Join(cwd, "comment.go")

	registry := newIndentTestWorkspaceRegistry()
	fileRegistry := NewFileCommandRegistry(cwd, registry)
	h := &indentTestHandler{uri: uri}
	c := setupCursorContent(t, 80, 10, "a", false)

	wrapped, err := SubscribeCommentCommands(uri, fileRegistry, c, h)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, wrapped.Close()) })

	err = registry.sub[cwd.String()][CommandToggleLineComment].HandleCommand(
		context.Background(), textapi.Command{URI: uri, Name: CommandToggleLineComment})
	require.EqualError(t, err, "line comment not configured for this file")
}

func TestCommentCommandBlockWithoutSpecReturnsError(t *testing.T) {
	cwd, err := workspaceapi.ParseURI("memory:///")
	require.NoError(t, err)
	uri := workspaceapi.Join(cwd, "comment.go")

	registry := newIndentTestWorkspaceRegistry()
	fileRegistry := NewFileCommandRegistry(cwd, registry)
	h := &indentTestHandler{uri: uri}
	c := setupCursorContent(t, 80, 10, "a", false)

	wrapped, err := SubscribeCommentCommands(uri, fileRegistry, c, h)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, wrapped.Close()) })

	err = registry.sub[cwd.String()][CommandToggleBlockComment].HandleCommand(
		context.Background(), textapi.Command{URI: uri, Name: CommandToggleBlockComment})
	require.EqualError(t, err, "block comment not available at the current position")
}

func TestCommentCommandHandlerPointerLeaves(t *testing.T) {
	t.Parallel()
	cwd, err := workspaceapi.ParseURI("memory:///")
	require.NoError(t, err)
	uri := workspaceapi.Join(cwd, "comment.go")
	wrap := func() Handler {
		fileRegistry := NewFileCommandRegistry(cwd, newIndentTestWorkspaceRegistry())
		ed := newPointerEditor(t, uri)
		h, err := SubscribeCommentCommands(uri, fileRegistry, ed.cursor, ed)
		require.NoError(t, err)
		return h
	}
	for _, size := range []struct{ width, height int }{{40, 10}, {1, 1}} {
		handlertest.RunPointerLeaves(t, wrap(), size.width, size.height)
	}
	requirePointerLeavesKeepState(t, wrap(), 40, 10,
		term.Coordinates{X: 3, Y: 2}, term.Coordinates{X: 12, Y: 4})
}
