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
	"testing"

	"github.com/stretchr/testify/require"
	"unstable.build/rune/internal/handler/handlertest"
	"unstable.build/rune/internal/text"
	"unstable.build/rune/internal/text/texttest"
)

func TestPromptStitcherPointerLeaves(t *testing.T) {
	t.Parallel()
	b := newExForTesting(t, texttest.NopEditor(), text.WithCommandKey(testCommandKey))
	t.Cleanup(func() { _ = b.Close() })
	unlock := b.lock()
	defer unlock()
	b.ex.openCommandPrompt()
	require.NotNil(t, b.ex.cmd)
	b.ex.config.WindowManagerConfig.Frame = true
	h := newCommandPromptHandler(b.ex.cmd, b.ex, func() error { return nil })
	handlertest.RunPointerLeaves(t, h, 60, 12)
	handlertest.RunPointerLeaves(t, h, 2, 2)
}
