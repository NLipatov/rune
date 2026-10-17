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
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/unstablebuild/rune-go-sdk/api/browserapi"
	"github.com/unstablebuild/rune-go-sdk/api/workspaceapi"
	"github.com/unstablebuild/rune-go-sdk/component"
	"github.com/unstablebuild/rune-go-sdk/term"
	"github.com/unstablebuild/rune-go-sdk/term/graphemecluster"
	"github.com/unstablebuild/rune-go-sdk/tui"
	"unstable.build/rune/internal/browser"
	"unstable.build/rune/internal/debug"
	"unstable.build/rune/internal/ide/idehistory"
	"unstable.build/rune/internal/term/programstatus"
)

const cmdProgramStatus = "programstatus"

// detachedStatusLabel names a terminal that is no tab and whose program
// did not name itself.
const detachedStatusLabel = "terminal"

// statusHost is what a statusStore needs from the workspaces whose
// statuses it accounts for. It is only called on the host event loop.
type statusHost interface {
	// statusWorkspace returns the slot of the open workspace at ws and
	// the name it is shown under, or false when no workspace at ws
	// holds a slot.
	statusWorkspace(ws workspaceapi.URI) (slot int, name string, ok bool)
	// statusTabName returns the name of the tab at uri in the
	// workspace at ws, or false when there is no such tab.
	statusTabName(ws, uri workspaceapi.URI) (string, bool)
	// statusPreview returns what previews what is at uri in the
	// workspace at ws: its tab, or the window showing it. It returns
	// false when there is nothing to show.
	statusPreview(ws, uri workspaceapi.URI) (statusPreview, bool)
	// statusTabIcon returns the icon the tab at uri in the workspace at
	// ws was opened with, or false when there is no such tab.
	statusTabIcon(ws, uri workspaceapi.URI) (rune, bool)
	// statusInSight reports whether the user sees what is at uri in
	// the workspace at ws: the tab in focus, or any terminal that is
	// no tab, as it floats over its workspace, while the workspace is
	// in focus.
	statusInSight(ws, uri workspaceapi.URI) bool
	// notifyStatus tells the user msg at level in the workspace at ws,
	// if it is still open.
	notifyStatus(ws workspaceapi.URI, level browserapi.NotificationLevel, msg string)
	// statusChanged tells that the status reported somewhere in the
	// workspace at ws changed.
	statusChanged(ws workspaceapi.URI)
	// showStatus brings what is at uri in the workspace at ws in
	// sight, or tells the user why it cannot.
	showStatus(ws, uri workspaceapi.URI)
	// closeStatus closes what reported at uri in the workspace at ws, as
	// tabclose closes what a window shows, or tells the user why it
	// cannot. Its statuses go with it.
	closeStatus(ws, uri workspaceapi.URI)
	// confirmStatus asks the user message, a yes or no question, and
	// calls yes on a later tick if they answer yes.
	confirmStatus(message string, yes func())
}

// tabStatusNotifyConfig says which states a source entering raises a
// notification for while out of sight, the minimum time between two
// such notifications for the same source, and whether the messages
// programs report are notified at all.
type tabStatusNotifyConfig struct {
	states   map[programstatus.State]bool
	interval time.Duration
	messages bool
}

func defaultTabStatusNotifyConfig() tabStatusNotifyConfig {
	return tabStatusNotifyConfig{
		states: map[programstatus.State]bool{
			programstatus.Blocked: true,
			programstatus.Error:   true,
			programstatus.Done:    true,
		},
		interval: 2 * time.Second,
		messages: true,
	}
}

// tabStatusConfigFor returns the tab status styling cfg asks for, or
// no styling at all when terminal.status is disabled.
func tabStatusConfigFor(cfg ideConfig) browser.TabStatusConfig {
	if !cfg.tabStatusEnabled() {
		return browser.TabStatusConfig{}
	}
	return cfg.tabStatusConfig()
}

// statusKey identifies a status source: what is at uri in the
// workspace at ws.
type statusKey struct {
	ws, uri string
}

// statusSource is what reported a status: a tab, or a terminal that is
// no tab, such as a floating "!" command or a task.
type statusSource struct {
	ws, uri workspaceapi.URI
	// records holds what a terminal that is no tab reported. It is nil
	// for a tab, whose records its browser holds.
	records *programstatus.Store
	status  programstatus.Summary
	// app is the app src last reported, which a tab keeps showing once
	// it holds no record.
	app string
}

func (src *statusSource) key() statusKey {
	return statusKey{ws: src.ws.String(), uri: src.uri.String()}
}

// statusStore accounts for every program status reported across the
// workspaces, whether by the program behind a tab or by a terminal that
// is no tab. It rolls them up per workspace, tells the user about the
// transitions worth knowing, and lists them, live, in the status
// inbox. A tab that reported is listed until it is removed, even once
// the user saw what it reported; a terminal that is no tab is forgotten
// once it holds no record. Both are forgotten with their workspace. It
// is not safe for concurrent use: every method must be called on the
// host event loop. Use newStatusStore to create one.
type statusStore struct {
	host   statusHost
	cfg    browser.TabStatusConfig
	notify tabStatusNotifyConfig
	style  statusInboxStyle
	// interrupter wakes the host while an inbox is open, so its spinner
	// turns and its preview stays live. Without one, inboxes stand still.
	interrupter term.Interrupter
	schedule    func(func()) bool
	sources     map[statusKey]*statusSource
	// lastNotified and pendingNotify rate limit the notifications of
	// each source: a pending entry holds the sequence number of the
	// deferred notification that may still deliver it.
	lastNotified  map[statusKey]time.Time
	pendingNotify map[statusKey]uint64
	notifySeq     uint64
	views         map[*statusView]struct{}
}

// newStatusStore returns an empty store that styles statuses after cfg
// and its inboxes after style, notifies them as notify says, and whose
// inboxes interrupter keeps live. schedule runs a function on the next
// tick of the host event loop.
func newStatusStore(
	host statusHost, cfg browser.TabStatusConfig, notify tabStatusNotifyConfig,
	style statusInboxStyle, schedule func(func()) bool, interrupter term.Interrupter,
) *statusStore {
	return &statusStore{
		host:          host,
		cfg:           cfg,
		notify:        notify,
		style:         style,
		interrupter:   interrupter,
		schedule:      schedule,
		sources:       make(map[statusKey]*statusSource),
		lastNotified:  make(map[statusKey]time.Time),
		pendingNotify: make(map[statusKey]uint64),
		views:         make(map[*statusView]struct{}),
	}
}

// setTabStatus applies report, made by the program behind uri in the
// workspace at ws, to the tab at uri among tabs, the workspace's tabs,
// or, when uri is no tab, to the records the store keeps for it.
func (s *statusStore) setTabStatus(
	ws workspaceapi.URI, tabs browser.TabManager, uri workspaceapi.URI,
	report programstatus.Report,
) error {
	if _, ok := s.host.statusTabName(ws, uri); ok {
		return tabs.SetTabStatus(uri, report)
	}
	s.updateDetached(ws, uri, func(r *programstatus.Store) { r.Apply(report) })
	return nil
}

// endTabStatus tells that the program behind uri in the workspace at ws
// ended, as setTabStatus applies a report.
func (s *statusStore) endTabStatus(
	ws workspaceapi.URI, tabs browser.TabManager, uri workspaceapi.URI,
) error {
	if _, ok := s.host.statusTabName(ws, uri); ok {
		return tabs.EndTabStatus(uri)
	}
	s.updateDetached(ws, uri, func(r *programstatus.Store) { r.DropTransient() })
	return nil
}

// tabStatusChanged accounts for the status of the tab at uri in the
// workspace at ws becoming status. Every change of a tab's status must
// be reported here, as it is the only way the store learns about tabs.
func (s *statusStore) tabStatusChanged(ws, uri workspaceapi.URI, status programstatus.Summary) {
	src, ok := s.sources[statusKey{ws: ws.String(), uri: uri.String()}]
	if !ok {
		src = &statusSource{ws: ws, uri: uri, status: programstatus.Summary{Progress: -1}}
	}
	wasDetached := src.records != nil
	src.records = nil
	s.update(src, status)
	if wasDetached {
		s.refreshViews()
	}
}

// tabRemoved forgets the tab at uri in the workspace at ws. It must be
// called once the tab is removed, as nothing else tells the store about
// a tab whose program no longer holds a record, and before the loss of
// the tab's status is reported, for its row to go from where it is.
func (s *statusStore) tabRemoved(ws, uri workspaceapi.URI) {
	key := statusKey{ws: ws.String(), uri: uri.String()}
	src, ok := s.sources[key]
	if !ok || src.records != nil {
		return
	}
	delete(s.sources, key)
	if len(src.status.Records) > 0 {
		s.host.statusChanged(ws)
	}
	s.refreshViews()
}

// programs returns the tabs of the workspace at ws that are listed, for
// a later session of the workspace to list them again.
func (s *statusStore) programs(ws workspaceapi.URI) []idehistory.Program {
	var ret []idehistory.Program
	for _, src := range s.sorted() {
		if src.records == nil && src.ws.String() == ws.String() {
			ret = append(ret, idehistory.Program{URI: src.uri, App: src.app})
		}
	}
	return ret
}

// restore lists again the programs of a previous session of the
// workspace at ws, as having reported nothing yet, but only those whose
// tab came back with the session: the rest are gone. It leaves alone
// what reported since.
func (s *statusStore) restore(ws workspaceapi.URI, programs []idehistory.Program) {
	restored := false
	for _, p := range programs {
		key := statusKey{ws: ws.String(), uri: p.URI.String()}
		if _, ok := s.sources[key]; ok {
			continue
		}
		if _, ok := s.host.statusTabName(ws, p.URI); !ok {
			continue
		}
		s.sources[key] = &statusSource{
			ws: ws, uri: p.URI, status: programstatus.Summary{Progress: -1}, app: p.App,
		}
		restored = true
	}
	if restored {
		s.refreshViews()
	}
}

// dropWorkspace forgets every status reported in the workspace at ws.
func (s *statusStore) dropWorkspace(ws workspaceapi.URI) {
	dropped := false
	for key := range s.sources {
		if key.ws == ws.String() {
			delete(s.sources, key)
			dropped = true
		}
	}
	for key := range s.lastNotified {
		if key.ws == ws.String() {
			delete(s.lastNotified, key)
		}
	}
	for key := range s.pendingNotify {
		if key.ws == ws.String() {
			delete(s.pendingNotify, key)
		}
	}
	if dropped {
		s.host.statusChanged(ws)
		s.refreshViews()
	}
}

// status returns the status last reported at uri in the workspace at
// ws, and false when nothing there holds a record.
func (s *statusStore) status(ws, uri workspaceapi.URI) (programstatus.Summary, bool) {
	src, ok := s.sources[statusKey{ws: ws.String(), uri: uri.String()}]
	if !ok {
		return programstatus.Summary{}, false
	}
	return src.status, true
}

// rollup returns the most pressing status reported in the workspace at
// ws (blocked > error > done > working > reported idle) and the uri it
// was reported at, the first in uri order on a tie. When nothing was
// reported it returns an empty idle status and a zero uri.
func (s *statusStore) rollup(ws workspaceapi.URI) (programstatus.Summary, workspaceapi.URI) {
	sum, uri := programstatus.Summary{Progress: -1}, workspaceapi.URI{}
	for _, src := range s.sorted() {
		if src.ws.String() == ws.String() && src.status.Outranks(sum) {
			sum, uri = src.status, src.uri
		}
	}
	return sum, uri
}

// items lists every source of a workspace that holds a slot, sorted by
// workspace and uri.
func (s *statusStore) items() []statusItem {
	var ret []statusItem
	for _, src := range s.sorted() {
		slot, wsName, ok := s.host.statusWorkspace(src.ws)
		if !ok {
			continue
		}
		ret = append(ret, statusItem{
			slot: slot, ws: src.ws, wsName: wsName,
			uri: src.uri, name: s.name(src), app: src.app, status: src.status,
		})
		if src.records == nil {
			ret[len(ret)-1].icon, _ = s.host.statusTabIcon(src.ws, src.uri)
		}
	}
	return ret
}

// view returns a new live status inbox over the store.
func (s *statusStore) view() *statusView {
	v := &statusView{store: s, hover: -1}
	v.set(statusInbox{cfg: s.cfg}.entries(s.items()))
	s.views[v] = struct{}{}
	return v
}

func (s *statusStore) sorted() []*statusSource {
	ret := slices.Collect(maps.Values(s.sources))
	slices.SortFunc(ret, func(a, b *statusSource) int {
		return cmp.Or(
			strings.Compare(a.ws.String(), b.ws.String()),
			strings.Compare(a.uri.String(), b.uri.String()))
	})
	return ret
}

// name returns the name of the tab src is, or detachedStatusLabel when
// it is no tab.
func (s *statusStore) name(src *statusSource) string {
	if src.records != nil {
		return detachedStatusLabel
	}
	name, _ := s.host.statusTabName(src.ws, src.uri)
	return name
}

// updateDetached applies fn to the records kept for uri in the
// workspace at ws, which is no tab.
func (s *statusStore) updateDetached(
	ws, uri workspaceapi.URI, fn func(*programstatus.Store),
) {
	src, ok := s.sources[statusKey{ws: ws.String(), uri: uri.String()}]
	if !ok || src.records == nil {
		src = &statusSource{
			ws: ws, uri: uri, records: programstatus.New(),
			status: programstatus.Summary{Progress: -1},
		}
	}
	fn(src.records)
	s.update(src, src.records.Summary())
}

// update sets the status of src, keeping a tab until it is removed and
// a terminal that is no tab while it holds a record, and reacts to the
// change.
func (s *statusStore) update(src *statusSource, status programstatus.Summary) {
	if src.status.Equal(status) {
		return
	}
	prev := src.status
	src.status = status
	if status.App != "" {
		src.app = status.App
	}
	if src.records != nil && len(status.Records) == 0 {
		delete(s.sources, src.key())
	} else {
		s.sources[src.key()] = src
	}
	s.host.statusChanged(src.ws)
	s.announce(src, prev)
	s.refreshViews()
}

// announce notifies what changed in src since prev:
//   - every new message a record carries, at once and whether or not
//     src is in sight, since nothing else shows it;
//   - src entering a state worth telling the user about while out of
//     sight, unless a message in that state already did, at most once
//     per source every notify interval with the latest state winning.
func (s *statusStore) announce(src *statusSource, prev programstatus.Summary) {
	key, status := src.key(), src.status
	announced := false
	if s.notify.messages {
		for _, rec := range newStatusMessages(prev, status) {
			s.host.notifyStatus(src.ws, statusNotificationLevel(rec.State),
				statusSourceLabel(s.name(src), rec.App)+": "+rec.Msg)
			announced = announced || rec.State == status.State
		}
	}
	if announced {
		delete(s.pendingNotify, key)
	}
	if prev.State == status.State || !s.notify.states[status.State] {
		return
	}
	if announced {
		s.lastNotified[key] = time.Now()
		return
	}
	if _, pending := s.pendingNotify[key]; pending {
		return
	}
	wait := s.notify.interval - time.Since(s.lastNotified[key])
	if wait <= 0 {
		s.notifyState(key)
		return
	}
	s.notifySeq++
	seq := s.notifySeq
	s.pendingNotify[key] = seq
	time.AfterFunc(wait, func() {
		debug.CapturePanicReport(func() {
			_ = s.schedule(func() {
				if pending, ok := s.pendingNotify[key]; !ok || pending != seq {
					return
				}
				delete(s.pendingNotify, key)
				s.notifyState(key)
			})
		})
	})
}

// notifyState notifies the current state of the source at key, unless
// it is gone, has since left the states worth notifying, or is in
// sight. Messages are notified on their own, so this names the state
// only.
func (s *statusStore) notifyState(key statusKey) {
	src, ok := s.sources[key]
	if !ok || !s.notify.states[src.status.State] || s.host.statusInSight(src.ws, src.uri) {
		return
	}
	s.lastNotified[key] = time.Now()
	status := src.status
	text := status.State.String()
	if status.State == programstatus.Blocked && status.Kind != programstatus.KindNone {
		text += " (" + status.Kind.String() + ")"
	}
	if status.Title != "" {
		text = status.Title + " — " + text
	}
	s.host.notifyStatus(src.ws, statusNotificationLevel(status.State),
		statusSourceLabel(s.name(src), status.App)+": "+text)
}

func (s *statusStore) refreshViews() {
	if len(s.views) == 0 {
		return
	}
	entries := statusInbox{cfg: s.cfg}.entries(s.items())
	for v := range s.views {
		v.set(entries)
	}
}

// newStatusMessages returns the records of status whose message is
// new: absent from the record with the same id in prev, or different
// there.
func newStatusMessages(prev, status programstatus.Summary) []programstatus.Record {
	var ret []programstatus.Record
	for _, rec := range status.Records {
		if rec.Msg == "" {
			continue
		}
		i := slices.IndexFunc(prev.Records, func(p programstatus.Record) bool {
			return p.ID == rec.ID
		})
		if i < 0 || prev.Records[i].Msg != rec.Msg {
			ret = append(ret, rec)
		}
	}
	return ret
}

func statusNotificationLevel(state programstatus.State) browserapi.NotificationLevel {
	switch state {
	case programstatus.Blocked:
		return browserapi.LevelWarn
	case programstatus.Error:
		return browserapi.LevelError
	case programstatus.Done:
		return browserapi.LevelSuccess
	}
	return browserapi.LevelInfo
}

// statusSourceLabel names the source of a status for the user: the
// tab's name followed by the app that reported it when that adds to
// the name, or the app alone for a terminal that is no tab.
func statusSourceLabel(name, app string) string {
	switch {
	case app == "" || app == name:
		return name
	case name == "" || name == detachedStatusLabel:
		return app
	}
	return name + " (" + app + ")"
}

// statusItem is one source as the status inbox lists it.
type statusItem struct {
	slot int
	ws   workspaceapi.URI
	// wsName is the workspace's display name.
	wsName string
	uri    workspaceapi.URI
	// name is the tab's name, or detachedStatusLabel.
	name string
	// app is the app the source last reported, which stands in for the
	// app of status once it reports none.
	app string
	// icon is the icon the tab was opened with, or zero.
	icon   rune
	status programstatus.Summary
}

// statusEntry is one program of the status inbox: a source, read as
// its name, its state and app beneath, and its progress, if any, and
// styled as the tab bar styles a tab in the same state.
type statusEntry struct {
	ws, uri workspaceapi.URI
	// wsName is the name of the workspace on the workspace bar.
	wsName string
	state  programstatus.State
	// status is everything reported, for the user to know what closing
	// it would interrupt.
	status programstatus.Summary
	// icon is the glyph of state. A working entry turns the spinner in
	// its place while the inbox is open.
	icon string
	name string
	sub  string
	// progress is the percentage the progress bar shows, or -1 for no
	// bar.
	progress int
	// nameAttr and iconAttr style the name and the icon as the name and
	// the icon of a tab in state are styled; subAttr colors the state.
	nameAttr, iconAttr, subAttr term.Attributes
}

// height returns the rows the entry takes in the sidebar.
func (e statusEntry) height() int {
	if e.progress >= 0 {
		return 3
	}
	return 2
}

// statusInbox renders status items as inbox entries, styled after the
// tab status configuration.
type statusInbox struct {
	cfg browser.TabStatusConfig
}

// statusSubSeparator sits between the state and the app of an entry.
const statusSubSeparator = " · "

// entries returns one entry per item, most pressing first (blocked >
// error > done > working > idle), then by slot and name. An item
// holding several records is listed once, in its most pressing state.
func (in statusInbox) entries(items []statusItem) []statusEntry {
	items = slices.Clone(items)
	slices.SortStableFunc(items, func(a, b statusItem) int {
		return cmp.Or(
			programstatus.Precedence(b.status.State)-programstatus.Precedence(a.status.State),
			a.slot-b.slot,
			strings.Compare(a.name, b.name))
	})
	ret := make([]statusEntry, 0, len(items))
	for _, it := range items {
		ret = append(ret, in.entry(it))
	}
	return ret
}

// entry renders it: its name, or the app of a terminal that is no tab,
// its state followed by its app, or the app it last reported, when
// that adds to the name, and the progress of a working item. The title
// and the message are left to the preview, as they would bury the name.
func (in statusInbox) entry(it statusItem) statusEntry {
	st := it.status
	app := cmp.Or(st.App, it.app)
	name := it.name
	if (name == "" || name == detachedStatusLabel) && app != "" {
		name = app
	}
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
		ws:       it.ws,
		uri:      it.uri,
		wsName:   it.wsName,
		state:    st.State,
		status:   st,
		icon:     in.glyph(st.State, st.Kind, it.icon),
		name:     name,
		sub:      sub,
		progress: progress,
		nameAttr: nameAttr,
		iconAttr: iconAttr,
		subAttr:  subAttr,
	}
}

// glyph returns the icon configured for state, the first spinner frame
// for working, or, when none is configured, fallback, or nothing: the
// state is spelled out beneath the name, and a word would not fit the
// single cell an icon takes.
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
	if icon == 0 {
		return ""
	}
	return string(icon)
}

const (
	// statusViewEmpty stands in the programs when none reported a
	// status.
	statusViewEmpty = "No program reported a status"
	// statusPreviewNone stands in the preview of what there is nothing
	// of to show, such as a terminal whose window closed.
	statusPreviewNone = "No preview"
	// statusPreviewWidth and statusPreviewHeight size the preview of
	// what has no size of its own: a tab never shown, or nothing.
	statusPreviewWidth  = 80
	statusPreviewHeight = 21
	statusSidebarMin    = 24
	statusSidebarMax    = 40
	statusDividerWidth  = 1
	// statusTextX is the column names start at in the sidebar, past the
	// marker, the icon and a space.
	statusTextX = 3
	// statusGap is the empty rows between two programs.
	statusGap = 1
)

var (
	statusMutedAttr = term.Attributes{Fg: term.ColorGray}
	statusBoldAttr  = term.Attributes{Attrs: term.AttrBold}
)

// statusInboxStyle styles the chrome of the status inbox.
type statusInboxStyle struct {
	// marker is drawn down the left of the selected item, in markerAttr
	// over the background of selectedAttr, which styles the rest of it.
	marker                   rune
	markerAttr, selectedAttr term.Attributes
	// frame draws the line between the programs and the preview.
	frame component.FrameUnionCharSet
}

func defaultStatusInboxStyle() statusInboxStyle {
	return statusInboxStyle{
		marker:       '▌',
		markerAttr:   browser.DefaultConfig().FocusTabHighlightAttr,
		selectedAttr: term.Attributes{Bg: term.ColorGray},
		frame:        component.DefaultFrameUnionCharSet(),
	}
}

// statusPreview is what the inbox previews, drawn as it draws itself at
// width by height cells. fit, when set, sizes what was never sized,
// which would otherwise draw nothing; what is shown elsewhere has none,
// as resizing it would reflow it there. Hidden content, shown nowhere,
// such as a minimized task or a hidden terminal, has no size of its
// own worth showing: the inbox fits it to the preview and draws it
// whole.
type statusPreview struct {
	content       tui.Component
	width, height int
	fit           func(width, height int)
	hidden        bool
}

// statusFit is the size the inbox last fit hidden content to.
type statusFit struct {
	ref           statusRef
	width, height int
}

// statusRef identifies a program of a workspace.
type statusRef struct {
	ws, uri string
}

// statusView is the status inbox: a sidebar listing every program that
// reported a status, kept up to date until it is closed, beside a live
// preview of the tab or terminal of the selected one. The preview draws
// it at its own size, so it looks as it does in its window, and the
// view asks to be large enough for it; given less, the preview loses
// the top rows and right columns. The selection wraps around. Choosing
// a program brings it in sight and closes the view. The previewed
// content never receives input, and only a tab never sized is resized.
type statusView struct {
	store   *statusStore
	entries []statusEntry
	sel     statusRef
	// selIdx is where sel was last found, for the selection to stay in
	// place when its program goes away.
	selIdx int
	// top is the first program the sidebar shows.
	top int
	// rows holds the program drawn on each row of the sidebar, as an
	// index in entries, or -1.
	rows []int
	// hover is the index in entries of the program under the mouse, or
	// -1.
	hover int
	// drawing is set while the view draws: a preview that draws the
	// view, as what the view floats over would, must not recurse.
	drawing bool
	// ticker wakes the host while the view is open, so the spinners turn
	// and the preview follows its content even when its workspace is out
	// of focus.
	ticker *statusTicker
	// fitted is the size the hidden content previewed was last fit to,
	// so it is only resized as the preview changes size.
	fitted statusFit
	// minWidth, when set, returns the least width the view asks for.
	minWidth      func() int
	width, height int
}

// set replaces the programs with entries, keeping the selection on the
// same program while it is listed, or in place when it is gone.
func (v *statusView) set(entries []statusEntry) {
	v.entries = entries
	v.resolve()
	v.setTicking(len(entries) > 0)
	v.sizePreview()
}

func (e statusEntry) ref() statusRef {
	return statusRef{ws: e.ws.String(), uri: e.uri.String()}
}

// resolve returns the index of the selected program, moving the
// selection to the program now at its place when it is gone, or -1
// when nothing is listed.
func (v *statusView) resolve() int {
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

// selectAt selects the program at index i.
func (v *statusView) selectAt(i int) {
	if i < 0 || i >= len(v.entries) {
		return
	}
	v.sel, v.selIdx = v.entries[i].ref(), i
	v.fitted = statusFit{}
	v.sizePreview()
}

// move moves the selection by delta programs, wrapping around.
func (v *statusView) move(delta int) {
	n := len(v.entries)
	if n == 0 {
		return
	}
	v.selectAt(((v.resolve()+delta)%n + n) % n)
}

// setTicking starts or stops the ticker. Starting is a no-op without
// an interrupter.
func (v *statusView) setTicking(ticking bool) {
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

// spinnerFrame returns the frame a working item shows in place of its
// icon, or false while the inbox stands still.
func (v *statusView) spinnerFrame() (string, bool) {
	frames := v.store.cfg.Spinner
	if v.ticker == nil || len(frames) == 0 {
		return "", false
	}
	return string(frames[int(v.ticker.epoch.Load()%int64(len(frames)))]), true
}

// sectionHeight returns the rows items of heights take, with the gaps
// between them.
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

func (v *statusView) entryHeights() []int {
	ret := make([]int, len(v.entries))
	for i, e := range v.entries {
		ret[i] = e.height()
	}
	return ret
}

// sidebarSize returns the size the sidebar asks for to show every
// program whole.
func (v *statusView) sidebarSize() (int, int) {
	if len(v.entries) == 0 {
		return min(max(2+graphemecluster.StringWidth(statusViewEmpty), statusSidebarMin), statusSidebarMax), 1
	}
	w := 0
	for _, e := range v.entries {
		w = max(w, statusTextX+graphemecluster.StringWidth(e.name), statusTextX+graphemecluster.StringWidth(e.sub))
	}
	return min(max(w+1, statusSidebarMin), statusSidebarMax), sectionHeight(v.entryHeights())
}

// sidebarWidth returns the width the sidebar takes of the view.
func (v *statusView) sidebarWidth() int {
	w, _ := v.sidebarSize()
	return min(w, v.width)
}

// preview returns what the selected item previews, or false when there
// is nothing of it to show.
func (v *statusView) preview() (statusPreview, bool) {
	i := v.resolve()
	if i < 0 {
		return statusPreview{}, false
	}
	return v.store.host.statusPreview(v.entries[i].ws, v.entries[i].uri)
}

// previewSize returns the size the preview asks for: that of what it
// shows, statusPreviewWidth by statusPreviewHeight for a tab never
// sized, or room for statusPreviewNone beside the sidebar when there
// is nothing to show.
func (v *statusView) previewSize() (int, int) {
	p, ok := v.preview()
	switch {
	case !ok:
		return graphemecluster.StringWidth(statusPreviewNone) + 4, 0
	case p.hidden, p.width == 0 && p.height == 0:
		return statusPreviewWidth, statusPreviewHeight
	}
	return p.width, p.height
}

// sizePreview fits what the selected item previews to the preview when
// it was never sized. It must be called whenever the selection or the
// size changes.
func (v *statusView) sizePreview() {
	p, ok := v.preview()
	if !ok || p.fit == nil {
		return
	}
	width := v.width - v.sidebarWidth() - statusDividerWidth
	if width <= 0 || v.height <= 0 {
		return
	}
	switch fit := (statusFit{ref: v.sel, width: width, height: v.height}); {
	case p.hidden:
		if v.fitted != fit {
			v.fitted = fit
			p.fit(width, v.height)
		}
	case p.width == 0 && p.height == 0:
		p.fit(width, v.height)
	}
}

func (v *statusView) Resize(width, height int) {
	v.width, v.height = width, height
	v.sizePreview()
}

func (v *statusView) Draw(w term.Writer) {
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
	divider := term.NewCell(v.store.style.frame.VerticalLeft, 1, statusMutedAttr)
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

// drawSidebar draws the programs into w, which is width cells wide,
// scrolled to keep the selected one in sight.
func (v *statusView) drawSidebar(w term.Writer, width int) {
	v.rows = slices.Repeat([]int{-1}, v.height)
	if len(v.entries) == 0 {
		statusText(w, 1, 0, statusViewEmpty, statusMutedAttr, width-1)
		return
	}
	sel := v.resolve()
	style := v.store.style
	heights := v.entryHeights()
	v.top = statusScroll(v.top, sel, heights, v.height)
	y := 0
	for i := v.top; i < len(heights); i++ {
		if i > v.top {
			y += statusGap
		}
		if y >= v.height {
			break
		}
		rows := min(heights[i], v.height-y)
		if i == sel {
			blank := term.NewCell(' ', 1, style.selectedAttr)
			for dy := range rows {
				for x := range width {
					w.SetCell(term.Coordinates{X: x, Y: y + dy}, blank)
				}
			}
		}
		v.drawEntry(w, i, y, width)
		for dy := range rows {
			v.rows[y+dy] = i
			if i != sel {
				continue
			}
			for x := range width {
				w.UnionAttributes(term.Coordinates{X: x, Y: y + dy}, style.selectedAttr)
			}
			statusText(w, 0, y+dy, string(style.marker),
				term.AttributesUnion(style.markerAttr, term.Attributes{Bg: style.selectedAttr.Bg}), 1)
		}
		y += heights[i]
	}
}

// drawEntry draws the program at index i from row y of w, which is
// width cells wide: its icon, spinning while working, its name, bold
// while hovered, its state and app, and its progress bar, if any.
func (v *statusView) drawEntry(w term.Writer, i, y, width int) {
	e := v.entries[i]
	icon := e.icon
	if e.state == programstatus.Working {
		if frame, ok := v.spinnerFrame(); ok {
			icon = frame
		}
	}
	statusText(w, 1, y, icon, e.iconAttr, statusTextX)
	nameAttr := e.nameAttr
	if i == v.hover {
		nameAttr = term.AttributesUnion(statusBoldAttr, nameAttr)
	}
	// Short of room, text stops a cell before the divider, as it does
	// when it fits, and never splits a wide character there.
	statusText(w, statusTextX, y, e.name, nameAttr, width-1)
	statusText(w, statusTextX, y+1, e.sub, e.subAttr, width-1)
	if e.progress >= 0 {
		statusProgress(w, statusTextX, y+2, width-statusTextX-1, e.progress)
	}
}

// statusScroll returns the first of the items of heights to show in
// rows from top on, moved so the item at sel, if any, fits, or starts
// them when it does not.
func statusScroll(top, sel int, heights []int, rows int) int {
	top = min(top, max(len(heights)-1, 0))
	if sel < 0 || sel >= len(heights) {
		return top
	}
	top = min(top, sel)
	for top < sel && sectionHeight(heights[top:sel+1]) > rows {
		top++
	}
	// With room below the last item, scroll back up rather than leave
	// rows empty while items above are hidden.
	for top > 0 && sectionHeight(heights[top-1:]) <= rows {
		top--
	}
	return top
}

// statusText writes the first line of s at x, y in attr, up to column
// end, leaving out the cells that would reach it, and returns the
// column past what it wrote.
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

// statusProgress draws a progress bar of pct percent at x, y, width
// cells wide, with the percentage on its right, as the console draws
// the progress of a command.
func statusProgress(w term.Writer, x, y, width, pct int) {
	label := strconv.Itoa(pct) + "%"
	barW := width - 1 - len(label)
	if barW < 2 {
		statusText(w, x, y, label, term.Attributes{}, x+width)
		return
	}
	chars := component.DefaultProgressBarCharSet()
	inner := barW - 2
	filled := pct * inner / 100
	bar := make([]rune, 0, barW)
	bar = append(bar, chars.Left)
	for i := range inner {
		if i < filled {
			bar = append(bar, chars.Filled)
		} else {
			bar = append(bar, chars.Empty)
		}
	}
	bar = append(bar, chars.Right)
	statusText(w, x, y, string(bar)+" "+label, term.Attributes{}, x+width)
}

// drawPreview draws what the selected item previews, as it draws itself
// at its own size, into w, which is width by height cells, leaving out
// the top rows and right columns that do not fit, as the bottom of a
// program, such as its prompt, matters the most.
func (v *statusView) drawPreview(w term.Writer, width, height int) {
	p, ok := v.preview()
	if ok && p.hidden {
		p.content.Draw(&component.VirtualWriter{Writer: w, Width: width, Height: height})
		return
	}
	if !ok || p.width == 0 || p.height == 0 {
		s := component.NewStringWithConfig(statusPreviewNone, component.StringConfig{
			Alignment:  component.AlignmentCentered,
			Attributes: statusMutedAttr,
		})
		s.Resize(width, height)
		s.Draw(w)
		return
	}
	p.content.Draw(&component.VirtualWriter{
		Writer: w,
		Offset: term.Coordinates{Y: -max(p.height-height, 0)},
		Width:  p.width,
		Height: p.height,
	})
}

func (v *statusView) Handle(ev term.Event) (exit, handled bool) {
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

// handleMouse selects the program clicked in the sidebar, and emboldens
// the one the mouse moves over.
func (v *statusView) handleMouse(ev term.Event) bool {
	if ev.Key != 0 && ev.Key != term.MouseLeft {
		return false
	}
	v.hover = -1
	if ev.MouseX < v.sidebarWidth() && ev.MouseY >= 0 && ev.MouseY < len(v.rows) {
		v.hover = v.rows[ev.MouseY]
	}
	if ev.Key == term.MouseLeft {
		v.selectAt(v.hover)
	}
	return true
}

// choose brings the selected program in sight. The view's window closes
// once Handle returns, so it is shown on the next tick, to leave the
// window with the workspace that opened it.
func (v *statusView) choose() {
	i := v.resolve()
	if i < 0 {
		return
	}
	e := v.entries[i]
	_ = v.store.schedule(func() { v.store.host.showStatus(e.ws, e.uri) })
}

// closeSelected closes the selected program, asking first while it is
// still at work or waiting on the user, as closing it would interrupt
// that. The view stays open, and the program's row goes with its
// statuses, leaving the selection on the next one. Like choose, it acts
// on the next tick, as closing may close windows of the browser that
// is dispatching to the view.
func (v *statusView) closeSelected() {
	i := v.resolve()
	if i < 0 {
		return
	}
	e := v.entries[i]
	host := v.store.host
	closeIt := func() { host.closeStatus(e.ws, e.uri) }
	switch e.state {
	case programstatus.Working, programstatus.Blocked:
		_ = v.store.schedule(func() { host.confirmStatus(statusCloseMessage(e), closeIt) })
	default:
		_ = v.store.schedule(closeIt)
	}
}

// statusCloseMessage asks whether to close the program of e, telling
// everything it reported: where it runs, and each record's state, app,
// title, message and progress.
func statusCloseMessage(e statusEntry) string {
	var b strings.Builder
	b.WriteString("Are you sure you want to close `" + e.name + "`")
	if e.wsName != "" {
		b.WriteString(" in `" + e.wsName + "`")
	}
	b.WriteString("? It is " + e.state.String() + ".\n")
	for _, rec := range e.status.Records {
		b.WriteString("\n- " + rec.State.String())
		if rec.App != "" {
			b.WriteString(statusSubSeparator + rec.App)
		}
		if rec.ID != "" {
			b.WriteString(" (" + rec.ID + ")")
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
			b.WriteString(": " + strings.Join(text, " — "))
		}
	}
	return b.String()
}

func (v *statusView) Cursor() (term.Coordinates, term.CursorStyle, bool) {
	return term.Coordinates{}, term.CursorStyleDefault, false
}

func (v *statusView) Selection() (string, bool) {
	i := v.resolve()
	if i < 0 {
		return "", false
	}
	return v.entries[i].name, true
}

// Close stops following the store and moving.
func (v *statusView) Close() error {
	delete(v.store.views, v)
	v.setTicking(false)
	return nil
}

// Dimensions returns the size of the sidebar beside that of the
// preview, which follows the size of what it shows as it changes, and
// is at least minWidth wide.
func (v *statusView) Dimensions() (int, int) {
	sideW, sideH := v.sidebarSize()
	w, h := v.previewSize()
	width := sideW + statusDividerWidth + w
	if v.minWidth != nil {
		width = max(width, v.minWidth())
	}
	return width, max(sideH, h)
}

// statusTicker wakes the host at a fixed cadence and counts the frames
// it asked for.
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
