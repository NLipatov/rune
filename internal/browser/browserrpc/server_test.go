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

package browserrpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/unstablebuild/rune-go-sdk/api/browserapi"
	"github.com/unstablebuild/rune-go-sdk/api/browserapi/browserrpc"
	"github.com/unstablebuild/rune-go-sdk/api/workspaceapi"
	"github.com/unstablebuild/rune-go-sdk/term"
	"github.com/unstablebuild/rune-go-sdk/term/termrpc"
	gomock "go.uber.org/mock/gomock"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	"unstable.build/rune/internal/browser/browsertest"
	"unstable.build/rune/internal/debug"
	"unstable.build/rune/internal/handler/handlerrpc"
	"unstable.build/rune/internal/term/programstatus"
	"unstable.build/rune/internal/workspace/processctx"
)

const asyncResultsSleepDuration = 300 * time.Millisecond

func newServerWithNoBroker(ctrl *gomock.Controller) (
	*Server, *browsertest.MockBrowser,
) {
	mock := browsertest.NewMockBrowser(ctrl)
	s := NewServer(mock, new(sync.Mutex), handlerrpc.GraphicsConfig{})
	s.SetSyncMode()
	return s, mock
}

func newTestServer(ctrl *gomock.Controller, mu *sync.Mutex) (
	*Server, *browsertest.MockBrowser,
) {
	mockBrowser := browsertest.NewMockBrowser(ctrl)
	s := NewServer(mockBrowser, mu, handlerrpc.GraphicsConfig{})
	s.SetSyncMode()

	return s, mockBrowser
}

func TestServerNotify(t *testing.T) {
	ctx := context.Background()

	t.Run("delegates Notify to underlying Browser", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, mock := newServerWithNoBroker(ctrl)

		mock.EXPECT().
			Notify(gomock.Eq(browserapi.LevelSuccess), gomock.Eq("%s"), gomock.Eq("blah")).
			Return("1234", nil)

		req := browserrpc.NotifyRequest{Level: uint32(browserapi.LevelSuccess), Msg: "blah"}
		res, err := s.Notify(ctx, &req)
		require.NoError(t, err)
		assert.NotNil(t, res)
		assert.Equal(t, "1234", res.GetId())
	})

	t.Run("keeps line breaks and strips NUL", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, mock := newServerWithNoBroker(ctrl)

		mock.EXPECT().
			Notify(gomock.Eq(browserapi.LevelWarn), gomock.Eq("%s"), gomock.Eq("failed:\n× cause\nhint: fix")).
			Return("1", nil)

		req := browserrpc.NotifyRequest{
			Level: uint32(browserapi.LevelWarn),
			Msg:   "failed:\r\n× ca\x00use\nhint: fix",
		}
		_, err := s.Notify(ctx, &req)
		require.NoError(t, err)
	})

	t.Run("bubbles up Notify Browser error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, mock := newServerWithNoBroker(ctrl)

		mock.EXPECT().Notify(gomock.Any(), gomock.Any(), gomock.Any()).Return("", errors.New("oopsie daisy"))

		_, err := s.Notify(ctx, new(browserrpc.NotifyRequest))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "oopsie")
	})
}

func TestServerOpen(t *testing.T) {
	ctx := context.Background()

	t.Run("delegates Open to underlying Browser", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, mock := newTestServer(ctrl, new(sync.Mutex))
		uri, err := workspaceapi.ParseURI("file:///tmp/coronavirus.sql")
		require.NoError(t, err)

		h := browsertest.NewTestHandler()
		mock.EXPECT().Open(gomock.Eq(uri)).Return(h, nil)

		req := browserrpc.OpenResourceRequest{Resource: "file:///tmp/coronavirus.sql"}
		res, err := s.Open(ctx, &req)
		require.NoError(t, err)
		assert.NotNil(t, res)
	})

	t.Run("bubbles up Open Browser error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, mock := newTestServer(ctrl, new(sync.Mutex))

		mock.EXPECT().Open(gomock.Any()).Return(nil, errors.New("oopsie daisy"))

		req := browserrpc.OpenResourceRequest{Resource: "file:///a"}
		_, err := s.Open(ctx, &req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "oopsie")
	})
}

func TestServerSetTabStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("delegates to underlying Browser under the lock", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mu := new(sync.Mutex)
		s, mock := newTestServer(ctrl, mu)
		uri, err := workspaceapi.ParseURI("rune-agent://model/rolling-fox")
		require.NoError(t, err)

		want := programstatus.Report{
			ID: "build", State: programstatus.Working, Progress: 25,
			Title: "link", Msg: "building",
		}
		mock.EXPECT().SetTabStatus(gomock.Eq(uri), want).
			DoAndReturn(func(workspaceapi.URI, programstatus.Report) error {
				assert.False(t, mu.TryLock(), "must be called under the UI lock")
				return nil
			})

		res, err := s.SetTabStatus(ctx, &browserrpc.SetTabStatusRequest{
			ResourceId: uri.String(), Id: "build", State: uint32(browserapi.TabWorking),
			Progress: 25, Title: "link", Message: "building",
		})
		require.NoError(t, err)
		assert.NotNil(t, res)
	})

	t.Run("attributes the report to the calling extension", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, mock := newTestServer(ctrl, new(sync.Mutex))
		uri, err := workspaceapi.ParseURI("rune-agent://model/rolling-fox")
		require.NoError(t, err)

		mock.EXPECT().SetTabStatus(gomock.Eq(uri), programstatus.Report{
			State: programstatus.Done, Progress: -1, App: "rune-agent", Msg: "ok",
		}).Return(nil)

		_, err = s.SetTabStatus(processctx.ContextWithExtensionID(ctx, "rune-agent"),
			&browserrpc.SetTabStatusRequest{
				ResourceId: uri.String(), State: uint32(browserapi.TabDone), Message: "ok",
			})
		require.NoError(t, err)
	})

	t.Run("bubbles up Browser error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, mock := newTestServer(ctrl, new(sync.Mutex))

		mock.EXPECT().SetTabStatus(gomock.Any(), programstatus.Report{Progress: -1}).
			Return(errors.New("unknown tab"))

		_, err := s.SetTabStatus(ctx, &browserrpc.SetTabStatusRequest{
			ResourceId: "file:///a",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown tab")
	})

	t.Run("rejects a malformed resource id", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, _ := newTestServer(ctrl, new(sync.Mutex))

		_, err := s.SetTabStatus(ctx, &browserrpc.SetTabStatusRequest{
			ResourceId: "%zz", State: uint32(browserapi.TabWorking),
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("rejects unknown state and kind", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, _ := newTestServer(ctrl, new(sync.Mutex))

		_, err := s.SetTabStatus(ctx, &browserrpc.SetTabStatusRequest{
			ResourceId: "file:///a", State: 42,
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		_, err = s.SetTabStatus(ctx, &browserrpc.SetTabStatusRequest{
			ResourceId: "file:///a", State: uint32(browserapi.TabBlocked), Kind: 42,
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("rejects a report outside the protocol limits", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, _ := newTestServer(ctrl, new(sync.Mutex))

		_, err := s.SetTabStatus(ctx, &browserrpc.SetTabStatusRequest{
			ResourceId: "file:///a", State: uint32(browserapi.TabWorking), Id: "/a",
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		_, err = s.SetTabStatus(ctx, &browserrpc.SetTabStatusRequest{
			ResourceId: "file:///a", State: uint32(browserapi.TabDone), Message: "a\x1bb",
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestServerShow(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		showErr  error
		wantURI  string
		wantCode codes.Code
		wantErr  string
	}{
		{
			name:     "file",
			resource: "file:///tmp/main.go",
			wantURI:  "file:///tmp/main.go",
		},
		{
			name:     "https",
			resource: "https://example.com/a?b=c#d",
			wantURI:  "https://example.com/a?b=c#d",
		},
		{
			name:     "bubbles up show error",
			resource: "https://example.com/docs",
			showErr:  errors.New("no system browser configured"),
			wantURI:  "https://example.com/docs",
			wantErr:  "no system browser configured",
		},
		{
			name:     "malformed resource",
			resource: "%zz",
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "relative resource",
			resource: "#section",
			wantCode: codes.InvalidArgument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mu := new(sync.Mutex)
			s, mock := newTestServer(ctrl, mu)
			var shown []string
			mock.EXPECT().Show(gomock.Any()).AnyTimes().DoAndReturn(
				func(uri workspaceapi.URI) error {
					assert.False(t, mu.TryLock(), "must be called under the UI lock")
					shown = append(shown, uri.String())
					return tt.showErr
				})

			res, err := s.Show(context.Background(),
				&browserrpc.ShowResourceRequest{Resource: tt.resource})

			switch {
			case tt.wantCode != codes.OK:
				assert.Equal(t, tt.wantCode, status.Code(err))
				assert.Empty(t, shown)
			case tt.wantErr != "":
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Equal(t, []string{tt.wantURI}, shown)
			default:
				require.NoError(t, err)
				assert.NotNil(t, res)
				assert.Equal(t, []string{tt.wantURI}, shown)
			}
		})
	}
}

func TestServerPublish(t *testing.T) {
	ctx := context.Background()

	t.Run("handles interrupt event by calling interrupt handler", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		var mu sync.Mutex
		s, mock := newTestServer(ctrl, &mu)
		req := browserrpc.PublishRequest{Ev: &termrpc.Event{Type: termrpc.Event_TypeInterrupt}}

		mock.EXPECT().PublishEvent(gomock.Eq(term.Event{Type: term.EventInterrupt})).Times(1)

		res, err := s.Publish(ctx, &req)
		require.NoError(t, err)
		assert.NotNil(t, res)
	})

	t.Run("handles EventNone event by calling interrupt handler", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		var mu sync.Mutex
		s, mock := newTestServer(ctrl, &mu)
		req := browserrpc.PublishRequest{Ev: &termrpc.Event{Type: termrpc.Event_TypeNone}}

		mock.EXPECT().PublishEvent(gomock.Eq(term.Event{Type: term.EventNone})).Times(1)

		res, err := s.Publish(ctx, &req)
		require.NoError(t, err)
		assert.NotNil(t, res)
	})

	t.Run("handles interrupt handler error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		var mu sync.Mutex
		s, mock := newTestServer(ctrl, &mu)
		req := browserrpc.PublishRequest{Ev: &termrpc.Event{Type: termrpc.Event_TypeInterrupt}}

		mock.EXPECT().PublishEvent(gomock.Any()).Return(errors.New("uRock"))

		res, err := s.Publish(ctx, &req)
		require.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("handles event none handler error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		var mu sync.Mutex
		s, mock := newTestServer(ctrl, &mu)
		req := browserrpc.PublishRequest{Ev: &termrpc.Event{Type: termrpc.Event_TypeNone}}

		mock.EXPECT().PublishEvent(gomock.Any()).Return(errors.New("uRock"))

		res, err := s.Publish(ctx, &req)
		require.Error(t, err)
		assert.Nil(t, res)
	})
}

func TestServerSetContent(t *testing.T) {
	/* tested via ex integration tests */
}

func TestServerPublishDoesNotWaitOnUILock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	var mu sync.Mutex
	s, mock := newTestServer(ctrl, &mu)
	mock.EXPECT().PublishEvent(gomock.Any()).Times(1)

	// Stand in for gui.Update holding the lock across a whole tick.
	mu.Lock()
	defer mu.Unlock()

	done := make(chan error, 1)
	go debug.CapturePanicReport(func() {
		_, err := s.Publish(context.Background(), &browserrpc.PublishRequest{
			Ev: &termrpc.Event{Type: termrpc.Event_TypeInterrupt},
		})
		done <- err
	})

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on the UI lock held by the render loop")
	}
}

// resizeRecorder stands in for the handler on the far end of a tab's
// stream, recording the dimensions the server forwards to it.
type resizeRecorder struct {
	*browsertest.TestHandler
	mu      sync.Mutex
	resizes [][2]int
}

func (r *resizeRecorder) Resize(width, height int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resizes = append(r.resizes, [2]int{width, height})
}

func (r *resizeRecorder) last() [2]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.resizes) == 0 {
		return [2]int{}
	}
	return r.resizes[len(r.resizes)-1]
}

// resizeOnUnlockLocker runs fn as the browser lock is released. Resizes
// reach a streamHandler under that lock, so this places one exactly where
// another goroutine's would land while the tab's stream finishes setting up.
type resizeOnUnlockLocker struct {
	sync.Mutex
	once sync.Once
	fn   func()
}

func (l *resizeOnUnlockLocker) Unlock() {
	l.once.Do(func() {
		if l.fn != nil {
			l.fn()
		}
	})
	l.Mutex.Unlock()
}

func TestStreamHandlerResizeDuringStreamSetup(t *testing.T) {
	rec := &resizeRecorder{TestHandler: browsertest.NewTestHandler()}
	lock := new(resizeOnUnlockLocker)
	h := &streamHandler{Handler: rec, mu: lock}
	lock.fn = func() { h.Resize(20, 6) }

	h.doneSetup()

	assert.Equal(t, [2]int{20, 6}, rec.last(),
		"the window's size must reach the handler behind the stream")
}
