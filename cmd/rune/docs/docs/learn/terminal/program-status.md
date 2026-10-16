---
sidebar_position: 2
description: How Rune shows what programs report through the Program Status Protocol (OSC 7501) on tabs, the workspace bar, notifications, and the status inbox.
---

# Program Status Protocol

The Program Status Protocol (OSC 7501) is a [novel terminal escape sequence
protocol](https://mitchellh.com/writing/program-status-osc7501) that lets a
TUI/CLI program tell the host terminal what it is doing: idle, working, waiting
on you, finished, or failed, and why. Rune understands it in every terminal, so a
build, a deploy, or a coding agent you left running in another tab or
workspace can tell you when it needs you, without you watching it.

The protocol is a generic terminal protocol, published in the [Rex
documentation](https://www.superlogical.com/rex/docs/build/program-status).
It describes state only and leaves presentation to the terminal. This guide
covers what Rune does with each report, and the choices Rune makes where the
specification leaves them to the terminal.

## In Rune

When a program running in a Rune terminal reports its status, Rune shows it
in four places:

- **On the tab.** A working tab's icon spins and an animation plays over its
  label. Blocked, finished, and failed tabs take an icon and color for that
  state. See [Tabs](#tabs).
- **On the workspace bar.** Each workspace tab shows the most pressing status
  among its terminals, so you can tell which workspace needs you without
  switching to it. See [The workspace bar](#the-workspace-bar).
- **As a notification.** Every new message comes through as a notification,
  and a tab out of sight that becomes blocked, fails, or finishes raises one
  too. See [Notifications](#notifications).
- **In the status inbox.** `:inbox` lists every reported status across your
  workspaces, most pressing first, and jumps to the one you pick. See [The
  status inbox](#the-status-inbox).

Rune's own features report through the same model, and extensions can
[report the status of their tabs](#report-from-an-extension) through the
SDK.

## Try it

With [`runectl`](../../develop/runectl.md#program-status) installed (`pkg install
runectl` in the [console](../console.md)), play one of the examples from
the specification in a Rune terminal and watch the tab and the workspace bar:

```
runectl status demo --scenario deploy
```

To give any command a status without changing it, wrap it. The tab spins while
it runs, then turns green or red, and Rune notifies you if you have moved on:

```
runectl status wrap -- make test
```

Nothing about the protocol needs `runectl`. This shell function, from the
specification, writes the sequence directly:

```sh
status() {
  printf '\e]7501;state=%s:msg=%s\e\\' "$1" "$(printf '%s' "$2" | base64 | tr -d '\n')"
}
status working "Syncing photos"
rsync -a ~/Photos backup:/photos && status done "Photos synced" || status error "rsync failed"
```

Reports travel in the terminal's output, so they reach Rune from [SSH
workspaces](../ssh.md), from `rune://` workspaces on the
[network](../network.md), from inside containers, and through an `ssh`
session you start in a Rune terminal.

## What reports status

| Source | What it reports |
| --- | --- |
| Any program that writes OSC 7501 | Whatever it reports. |
| [Claude Code](../../agent/claude-code.md#see-claudes-status-automatically) v2.1.295 or later | Working, waiting for your input, or done, with no setup. |
| [`runectl status`](../../develop/runectl.md#program-status) | Any state from a script, a hook, or your shell, and `runectl status wrap` for any command. |
| Rune Agent chats | Working while the agent thinks and runs tools, with what it is doing as the title. Blocked when it asks you a question or for permission. Done with the first line of its reply, or failed with the reason. Sub-agents and skills are records beneath the chat. |
| [Rune console](../console.md) commands | Each command is a record: working while it runs, with a percentage when it reports progress (package and model downloads do), then done or failed. Interrupting a command drops its record. |
| Rune's permission prompts | Blocked on permission, as app `rune`, until you answer the prompt. |
| [Extensions](#report-from-an-extension) | The status of the tabs they own, through the Go SDK. |

## How Rune shows it

### Tabs

A tab's status comes from the terminal or extension behind it. With the
shipped configuration:

| State | Tab icon | Tab name |
| --- | --- | --- |
| `working` | A spinner, in yellow | The `animations.active_content_tab` effect plays over it |
| `blocked` | `?` for `permission` and `question`, `@` for `auth`, in yellow | Unchanged |
| `done` | A check mark, in green | Unchanged |
| `error` | `x`, in red | Unchanged |
| `idle` | The tab's own icon, in yellow | Unchanged |

A program that reported `idle` is at rest and waiting on you, which is why its
icon is colored until you see the tab; a tab whose program never reported
anything is left alone. A blocked report without a `kind` takes the
`permission` icon.

`progress` is kept with the status and shown in the [inbox](#the-status-inbox).
Set `terminal.status.content_tab.progress` to `true` to also split working tab
labels at the reported percentage, painting the completed part with
`working_done_attr`. Every icon, color, and effect is configurable; see
[Configuration](#configuration).

### The workspace bar

Each workspace tab on the bar shows the most pressing status in its workspace,
drawn from every tab and from terminals that are not tabs, such as floating
`!` commands and minimized [tasks](./tasks.md). The order is:

1. `blocked`, because the program cannot continue without you.
2. `error`.
3. `done`.
4. `working`.
5. A reported `idle`, which outranks a terminal that reported nothing.

With the shipped configuration the workspace's name turns yellow when
blocked, red on error, and green once done. While the workspace is working,
the `animations.active_workspace_tab` effect plays over its name, and a
reported percentage fills the name in green from the left, so the tab reads as
turning green as the work completes. The effect stops as soon as anything in
the workspace is blocked, failed, or finished, so it never hides a workspace
that needs you.

### Notifications

Rune raises a notification, in the workspace the status came from, in two
cases:

- **A new message.** Every time a record's `msg` is new or changes, Rune
  notifies it right away, whether or not you are looking at the tab, since
  nothing else shows the message. Report a message once per event, not with
  every report.
- **A state worth knowing about.** When a terminal you are not looking at
  enters one of the states in `terminal.status.notify` (`blocked`, `error`, and
  `done` by default), Rune names the state, the `kind` of a blocked status,
  and the record's `title`. These notifications are rate limited to one per
  tab every `terminal.status.notify_interval` (`2s` by default), and the latest
  state wins. A message already announced in that state is not repeated.

The notification level follows the state: warning for `blocked`, error for
`error`, success for `done`, and info otherwise. Each notification starts with
the name of the tab it came from, followed by the reporting `app` when that
adds anything. A terminal that is not a tab is named after its `app`, or
`terminal` when it reported none.

A notification in a workspace you are not looking at stays open until you
switch to it and adds to the badge on its workspace tab. See [Knowing when a
worktree needs you](../worktrees.md#knowing-when-a-worktree-needs-you).

### The status inbox

`statuslist` (shipped with the alias `inbox`) opens a floating list of every
status reported across your workspaces, most pressing first, then by
workspace and tab. Each row shows the state's icon, the workspace, the tab and
`app`, the `title`, the percentage of a working record, and the `msg`. The
list updates live while it is open. Rows are styled like the tabs they stand
for: working rows turn the spinner, run the `animations.active_content_tab`
effect, and split their label at the reported percentage when
`terminal.status.content_tab.progress` is `true`.

Above the list, the inbox previews the tab of the selected row live, as it
draws itself, without sending it any input. A tab that no window shows is fit
to the preview. A tab that a window shows keeps the window's size and is
cropped around its cursor, or to its bottom rows when it shows no cursor.

| Key | Action |
| --- | --- |
| `j` / `<down>` / `<ctrl-j>` / `<ctrl-n>` | Next row |
| `k` / `<up>` / `<ctrl-k>` / `<ctrl-p>` | Previous row |
| `<enter>` | Switch to the row's workspace and show its tab |
| `<esc>` / `q` / `<ctrl-c>` | Close the inbox |

Tabs at rest are not listed, and the inbox is not bound to a key by default.
Bind it like any other command under `command.key_bindings`.

### Several records

A program can report several records at once, one per `id`, to describe a
tree of tasks. This is the `deploy` example from the specification:

```
OSC 7501 ; state=working:app=deploy:msg=RGVwbG95aW5nIHYyLjQuMQ== ST                                               # Deploying v2.4.1
OSC 7501 ; state=working:id=us-east:title=VVMgRWFzdA==:progress=40:msg=UHVzaGluZyBpbWFnZQ== ST                    # US East / Pushing image
OSC 7501 ; state=blocked:kind=permission:id=eu-west:title=RVUgV2VzdA==:msg=QXBwcm92ZSBkZXBsb3kgdG8gZXUtd2VzdCAocHJvZHVjdGlvbik/ ST  # EU West / Approve deploy to eu-west (production)?
```

The tab shows the most pressing record, in the same order as the workspace
bar, so it shows `blocked` while `eu-west` waits for approval even though the
root record still says `working`. When two records share the top state, the
most recently updated one wins. The inbox lists the tab, then one indented row
per record beneath it, so you see both. A record without an `app` takes it from
its nearest ancestor that has one, which is how the region rows above show
`deploy`.

## When a status goes away

Records live with the terminal, not with a screen. Rune ties their lifetime to
events it already sees, and never asks a program to repeat a report to keep it
alive.

| State | Rune drops it when |
| --- | --- |
| `working`, `blocked` | The terminal's process exits, or the shell starts a new prompt and marks it with OSC 133 `A`. |
| `idle` | Either of the above, or you see it, as for `done` and `error`. |
| `done`, `error` | You see it: you focus its tab, press a key in it, or it is reported on the tab you are already looking at. A terminal that is not a tab, such as a floating `!` command or a minimized task, keeps it until the terminal closes. |

In every state a record also goes away when the program reports another one
with the same `id`, when the program reports `clear` for it or one of its
ancestors, and when its terminal closes. A full reset (`ESC c`) removes every
record. Switching between the primary and alternate screen, and a soft reset,
leave records alone.

A `done` or `error` record survives the process exit and the next prompt, so
a program that exits as soon as it finishes should report one of them right
before exiting, and leave it for you to find.

:::tip
Rune does not install shell integration. If your shell does not mark its
prompts with OSC 133, a program that dies without reporting again leaves its
`working` or `blocked` record until the terminal closes. Run
`runectl status clear` to remove it, or start the program under
`runectl status wrap`, which always reports how the command ended.
:::

## Protocol support

Rune implements the whole specification. Where it leaves the choice to the
terminal, Rune's behavior is the one described here.

### Reports

```
OSC 7501 ; pairs ST        report
OSC 7501 ; ? ST            feature detection
```

The body is a list of `key=value` pairs separated by `:`. Rune accepts both
terminators, `ESC \` and BEL. It follows the parsing rules exactly:

- Whitespace around keys and values is removed.
- A malformed pair (no `=`, an empty key, or a byte outside the value set) is
  skipped, and the rest of the report still applies.
- Unknown keys are ignored, and a repeated key keeps its last value.
- A report without `state`, with a `state` Rune does not know, or with a
  malformed `id` is ignored.
- `msg` and `title` are standard base64 of UTF-8 text, with optional padding.

| Key | Values | Rune's handling |
| --- | --- | --- |
| `state` | `idle`, `working`, `done`, `blocked`, `error`, `clear` | Required. `clear` removes the addressed record and every record beneath it, or every record when there is no `id`. |
| `id` | `/`-separated segments of `[A-Za-z0-9_.+-]` | Absent addresses the root record. Each report replaces its record completely. |
| `kind` | `permission`, `question`, `auth` | Read with `blocked` only. Picks the tab icon and is named in notifications. An unknown value is treated as absent. |
| `progress` | `0` to `100` | Read with `working` and `blocked` only. Absent or invalid means indeterminate. |
| `app` | `[A-Za-z0-9_.+-]`, up to 32 bytes | Names the program in notifications and the inbox. Inherited from the nearest ancestor. An invalid value is treated as absent. |
| `title` | base64 text | The record's label in notifications and the inbox. |
| `msg` | base64 text | Notified whenever it changes and shown in the inbox. |

### Feature detection

Rune answers the query `OSC 7501 ; ? ST` with `OSC 7501 ; ? ST`, using the
terminator the query used. The reply lists no `states` or `kinds`, which
means exactly the five states and three kinds of the specification. Programs
may also report without asking first.

`runectl status query` sends the query, followed by a primary device
attributes request so it never waits for long, and exits with status 0 when the
terminal supports the protocol and 1 when it does not.

### Limits

Rune enforces every limit of the specification. A report that breaks one is
discarded whole, before any record changes.

| Item | Limit |
| --- | --- |
| Whole sequence, OSC through ST | 4096 bytes |
| Key length | 16 bytes |
| `msg` | 2732 bytes encoded, 2048 decoded |
| `title` | 256 bytes encoded, 192 decoded |
| `app` | 32 bytes |
| `id` | 128 bytes in all, 32 per segment, 8 levels deep |
| Records per terminal | 256. A report that would create one more removes the least recently updated record. |

### Security

Everything in a report is untrusted input, so Rune:

- Discards a report whose `msg` or `title` is not valid UTF-8 or contains a
  control character, including newline and tab.
- Strips zero-width and bidirectional formatting characters from `msg` and
  `title` before showing them anywhere, so a program cannot disguise what a
  notification or the inbox says.
- Never treats `msg` or `title` as markup.
- Never writes anything from a report back to the terminal. The feature
  detection reply is fixed, and there is no way to read records.
- Raises each notification in the workspace the status came from, prefixed
  with the tab's name, and lists every status in the inbox under its workspace
  and tab.
- Rate limits the notifications a state change raises, per tab.

## Relationship to other sequences

| Sequence | In Rune |
| --- | --- |
| OSC 9;4 (progress) | Not read, and not mapped to a status. A program may send both without either affecting the other. |
| OSC 9, OSC 99, OSC 777 (desktop notifications) | Not shown. Report a `msg` instead, which Rune notifies, or use `runectl notify`. |
| OSC 133 (shell integration) | Rune reads `A`, the start of a prompt, to drop `working`, `blocked`, and `idle` records. The other marks are ignored. |
| OSC 0 and OSC 2 (window title) | Set the tab name while `terminal.dynamic_tab_name` is on, which it is by default. Independent of the status. |
| BEL | Highlights the tab of an unfocused window with `terminal.needs_attention_attr`. Independent of the status. |

## Report from an extension

Extensions report the status of the tabs they own through the [Go
SDK](../../develop/sdk/go.md), without writing escape sequences. The model is the
same: `WindowManager.SetTabStatus` replaces the record a `browserapi.TabStatus`
addresses on the tab identified by its URI.

```go
err := wm.SetTabStatus(uri, browserapi.TabStatus{
	ID:       "index",
	State:    browserapi.TabWorking,
	Progress: 40,
	Title:    "Indexing",
})
```

- `State` is one of `TabIdle`, `TabWorking`, `TabDone`, `TabBlocked`,
  `TabError`, or `TabClear`, and `Kind` one of `TabKindNone`,
  `TabKindPermission`, `TabKindQuestion`, or `TabKindAuth`.
- Set `Progress` to `-1` when there is no percentage: its zero value means 0%.
- Rune fills in `app` with the extension's id.
- `Title` and `Message` follow the protocol's limits, and a report that breaks
  one, or carries a control character, fails with an `InvalidArgument` error.
- `TabDone`, `TabError` and `TabIdle` clear once you see the tab, and every
  record goes away when the tab closes.

Rune Agent reports its chats this way.

## Configuration

Program status is configured under `terminal.status`. The activity effects
that play over working tabs are configured separately, under
`animations.active_content_tab` and `animations.active_workspace_tab`; see
[Tab animations](../../config.md#tab-animations).

| Key | Default | Effect |
| --- | --- | --- |
| `enabled` | `true` | `false` drops the status icons and colors from tabs and the workspace bar, and raises no status notifications. The activity effects have their own `enabled` switch under `animations`. |
| `icons.permission`, `icons.question`, `icons.auth` | `?`, `?`, `@` | The icon of a blocked tab by `kind`. |
| `icons.done`, `icons.error` | a check mark, `x` | The icon of a finished or failed tab. |
| `spinner` | `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏` | The frames a working tab's icon cycles through. |
| `spinner_fps` | `10` | Spinner frames per second. |
| `content_tab` | see below | How content tabs (files, terminals, agent chats) show their status. |
| `workspace_tab` | see below | How workspace tabs on the bar show the status of their workspace. |
| `notify` | `[blocked, error, done]` | The states that raise a notification when a tab out of sight enters them. `working` is also accepted. |
| `notify_interval` | `2s` | The minimum time between two state notifications for the same tab. |

`content_tab` and `workspace_tab` take the same keys. Colors left at `default`
keep the tab's own:

| Key | `content_tab` | `workspace_tab` | Effect |
| --- | --- | --- | --- |
| `progress` | `false` | `true` | Split a working label at the reported percentage. |
| `working_done_attr` | default | green | The completed part of a split label. |
| `blocked_attr`, `done_attr`, `error_attr` | default | yellow, green, red | The name in that state. |
| `idle_icon_attr`, `working_icon_attr` | yellow | default | The icon of a tab that reported `idle`, or the spinner. |
| `blocked_icon_attr`, `done_icon_attr`, `error_icon_attr` | yellow, green, red | default | The icon in that state. |

For example, to be notified only when a program needs you or fails, and to
color the names of finished and failed content tabs:

```yaml tab
terminal:
  status:
    notify: [blocked, error]
    content_tab:
      done_attr: {fg: green}
      error_attr: {fg: red}
```

```python tab
config["terminal"]["status"]["notify"] = ["blocked", "error"]
config["terminal"]["status"]["content_tab"]["done_attr"] = attr(fg = "green")
config["terminal"]["status"]["content_tab"]["error_attr"] = attr(fg = "red")
```

## Troubleshooting

**Nothing shows when a program reports.** Run `runectl status query` in the
same terminal. If it reports the protocol unsupported while you are inside
tmux, screen, or another multiplexer, the multiplexer is answering instead of
Rune and does not forward the reports: run the program directly in a Rune
terminal. Otherwise, check that `terminal.status.enabled` is not `false`.

**A status never goes away.** A `working` or `blocked` record whose program
died needs the shell's next prompt to be marked with OSC 133, or a `clear`;
see [When a status goes away](#when-a-status-goes-away). A `done` or `error`
record on a terminal that is not a tab stays until that terminal closes.

**Too many notifications.** Remove states from `terminal.status.notify`, or
raise `terminal.status.notify_interval`. Messages are notified once each, so a
program that changes its `msg` on every report raises one notification per
report.
