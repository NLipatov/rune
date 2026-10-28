---
sidebar_position: 48
sidebar_label: Sounds
---

# Sounds

Rune plays a short sound for events you might not be looking at, such as a
program in a terminal ringing the bell. Sounds have names: Rune ships four
built-in ones, and you and the packages you install can add more. Settings
that make a sound, such as `terminal.bell`, refer to a sound by its name.

## Built-in sounds

| Name | Plays when |
| --- | --- |
| `error` | something fails or is refused, such as a vi-mode edit above a terminal's prompt |
| `warning` | something needs caution; by default, a program in a terminal ringing the bell |
| `info` | something informational happens |
| `success` | something you were waiting on succeeded |

Rune currently plays `error` and `warning` for the events above. You can
configure `info` and `success` now, and they apply as more events start to
use them.

## Defining sounds

The `sounds` section maps names to sounds. Each value is one of:

- the path of a WAV, MP3 or Ogg Vorbis file;
- the name of another sound;
- an empty string, which silences the sound.

```yaml tab
sounds:
  ding: "$HOME/sounds/ding.wav"
  warning: ding
  info: ""
```

```python tab
config["sounds"] = {
    "ding": "$HOME/sounds/ding.wav",
    "warning": "ding",
    "info": "",
}
```

This defines a new sound, `ding`, makes the built-in `warning` sound play
it, and silences `info`.

- Use absolute paths or `file://` URIs. A path can reference environment
  variables such as `$HOME`.
- Rune plays up to the first 10 seconds of a file.
- If a file is missing or cannot be played, Rune logs a warning. A built-in
  sound pointed at it plays its built-in version instead.
- A name cannot contain `/`, since a value with `/` is read as a path.
- Rune reports a name that refers to an unknown sound, or to itself through
  other names, as a config error and leaves that entry out.

Sounds come from your config file and the packages you install; a
workspace's `.rune/config.yaml` cannot define them. Changes apply after
`workspacereload`, and sounds added by a package install are available right
away. To share sounds, publish them in a
[package](../develop/packages.md#registering-sounds).

To hear a sound, run `guisound` with its name:

```
guisound ding
```

## Terminal bell

`terminal.bell` names the sound a program in a terminal plays when it rings
the bell. It is `warning` by default. Set it to an empty string to keep
programs from making a sound; the tab of an unfocused terminal is still
marked when its program rings the bell.

```yaml tab
terminal:
  bell: ding
```

```python tab
config["terminal"]["bell"] = "ding"
```

Like other `terminal.*` settings, a change to `terminal.bell` applies after
`workspacereload`.

## Volume

`gui.sound_volume` sets the volume of every sound, from `0` (mute) to `1`
(the default):

```yaml tab
gui:
  sound_volume: 0.5
```

```python tab
config["gui"]["sound_volume"] = 0.5
```

To change the volume for the current session, use `guivolume`:

| Command | Effect |
| --- | --- |
| `guivolume increase` | raises the volume by 10% and unmutes |
| `guivolume decrease` | lowers the volume by 10% and unmutes |
| `guivolume mute` | mutes, or unmutes at the previous volume |

Sounds play in the Rune window. With `rune --tui`, every sound rings the
bell of the terminal Rune runs in instead, and that terminal's settings
control how it sounds.
