---
sidebar_position: 46
---

# Wallpaper

Rune shows a wallpaper in every [window](./layout-management.md#windows) that
has no tab to show. By default it is one of Rune's built-in logos, which plays
an intro when Rune starts. You can replace it with ASCII or Unicode text that
Rune renders directly, or give Rune a PNG image to ASCII encode it.

Wallpaper settings live under `workspace`, so you can set one globally in
`~/.rune/config.yaml` or `~/.rune/config.star`. A project can also provide its
own wallpaper in `.rune/config.yaml` at the workspace root.

Here, Rune converts a transparent Python logo into colored ASCII art. The
source image stays recognizable, while the empty area around the logo blends
into the workspace:

<img
  className="docs-figure wallpaper-figure"
  src="https://assets.rune.build/images/docs-rune/wallpaper/python-logo-example-v2.png"
  alt="Rune displaying a Python logo converted from a PNG into colored ASCII art, with the wallpaper configuration open beside it"
/>

## Choose the built-in logo

Without a wallpaper of your own, Rune draws the built-in logo that
`workspace.wallpaper_logo` names:

| Logo | Wallpaper | Intro |
| --- | --- | --- |
| `rune` (default) | The Rune logo, drawn as a full-resolution picture. | Braille dots fly in and assemble the logo, then bloom into its picture. |
| `unstable_build` | The Unstable Build logo, drawn as colored ASCII art. | Flames sweep across the window and burn the logo in. |

```yaml tab
workspace:
  wallpaper_logo: unstable_build
```

```python tab
config["workspace"]["wallpaper_logo"] = "unstable_build"
```

To leave empty windows blank, set `wallpaper_logo` to an empty string, with no
other wallpaper set.

The Rune logo is drawn for the fonts and pictures of the GUI. In a terminal,
with `rune --tui`, Rune shows the Unstable Build logo whichever logo
`wallpaper_logo` names.

### The intro

The intro is the animation that plays over the logo when Rune starts. Set
`animations.intro` to `false` to start on the logo directly, and run
`:shaderrun intro` to play the intro at any time:

```yaml tab
animations:
  intro: false
```

```python tab
config["animations"]["intro"] = False
```

Only the built-in logos have an intro. A wallpaper of your own, ASCII art or
image, replaces the logo together with its intro, so Rune starts on your
wallpaper without one.

The intro plays once, at startup, so it belongs to the wallpaper that your
config file in `~/.rune` sets. A project's `.rune/config.yaml` can change the
wallpaper of its windows, but not the intro. A change to `animations.intro`,
or to the intro that `wallpaper_logo` picks, takes effect the next time Rune
starts.

## Use ASCII art directly

Choose `workspace.wallpaper_ascii` when you already have ASCII art and want
exact control over every character:

```yaml tab
workspace:
  wallpaper_ascii: |
    ┌─────────────┐
    │   R U N E   │
    └─────────────┘
  wallpaper_attr:
    fg: "#D34728"
    bg: default
  wallpaper_background_attr:
    bg: default
```

```python tab
config["workspace"]["wallpaper_ascii"] = """┌─────────────┐
│   R U N E   │
└─────────────┘"""
config["workspace"]["wallpaper_attr"] = attr(
    fg = "#D34728",
    bg = "default",
)
config["workspace"]["wallpaper_background_attr"] = attr(bg = "default")
```

Rune draws the art exactly as written. Both plain ASCII and Unicode drawing
characters work. Keep in mind that:

- Rune centers the art as one block, and the lines keep their alignment
  within it: a short line sits at the block's left edge, not in the middle.
  Pad it with leading spaces to center it.
- Tabs are not expanded. Each one takes a single cell, so indent with spaces.
- Art larger than the window is not scaled down. Rune cuts off whatever does
  not fit at the right and bottom edges.
- An empty string turns the ASCII wallpaper off, and Rune shows the built-in
  logo instead.

### Colors

`wallpaper_attr` styles the characters of the art: its `fg`, `bg`, and
`flags` such as `bold`. `wallpaper_background_attr` fills the rest of the
window, and its `flags` apply only to those surrounding cells.

As soon as `wallpaper_background_attr` sets anything other than defaults, its
`bg` also replaces the `bg` of `wallpaper_attr`, so the whole window shares
one background. To put the art on a background of its own, leave
`wallpaper_background_attr` at its defaults.

Without a `wallpaper_attr`, ASCII art is drawn in your theme's blue in the
GUI. In a terminal it is drawn in `#D34728` on a `#1e1e1e` background.

## Encode an image to ASCII

Choose `workspace.wallpaper_image` to have Rune turn an image into colored
characters as it renders:

```yaml tab
workspace:
  wallpaper_image: ~/Pictures/my-logo.png
  wallpaper_background_attr:
    bg: default
```

```python tab
config["workspace"]["wallpaper_image"] = "~/Pictures/my-logo.png"
config["workspace"]["wallpaper_background_attr"] = attr(bg = "default")
```

The image must currently be a **PNG**. `~` in the path expands to your home
directory, and `$VARIABLES` expand to their values. A relative path is
resolved against the directory Rune was started from, not against the config
file, so a project's `.rune/config.yaml` should use an absolute path, `~`, or
a variable.

Rune reads the image when it loads your configuration. After replacing the
file, run `:workspacereload` to see the new image.

`wallpaper_attr` has no effect on an image wallpaper: every character takes
its color from the image. `wallpaper_background_attr` sets the background and
flags of every cell in the window, the image's characters included.

For the best results, start with an image that has:

- a **transparent background**, rather than a solid rectangle;
- a simple silhouette and clear contrast;
- large features that remain recognizable at terminal-cell resolution;
- a tight crop, without a large amount of unused canvas around the subject; and
- enough pixels to fill the window, since Rune enlarges small images without
  smoothing and they come out blocky.

Fully transparent pixels count as black, so they take the first character of
the ramp, a space by default. This lets your theme or transparent GUI
background show around the subject and avoids the box-shaped background
common with JPEG-like source art. Partial transparency is ignored: a
semi-transparent pixel is drawn in its full color, so soft anti-aliased edges
come out as a hard outline. Fully opaque photographs work, but tend to look
busier than logos and illustrations.

:::tip

Export the source as a PNG with alpha transparency. If your editor shows a
checkerboard around the subject rather than a white or black rectangle, the
background is probably transparent.

:::

## Choose the conversion characters

For image wallpapers, `wallpaper_density_characters` controls the characters
Rune uses for different brightness levels. Write them from the least visible
to the most dense:

```yaml tab
workspace:
  wallpaper_image: ~/Pictures/my-logo.png
  wallpaper_density_characters: " ░▒▓█"
```

```python tab
config["workspace"]["wallpaper_image"] = "~/Pictures/my-logo.png"
config["workspace"]["wallpaper_density_characters"] = " ░▒▓█"
```

The first character should normally be a space so transparent and darkest
parts of the image disappear. A short ramp produces a bold, graphic result;
a longer ramp can preserve more detail.

Rune measures a pixel's brightness as the plain average of its red, green,
and blue values, and splits that range into equal bands, one per character.
The last character is the exception: only pure white gets it. With
`" ░▒▓█"`, a pixel darker than a quarter of full brightness becomes a space,
the next three quarters become `░`, `▒`, and `▓`, and only pure white becomes
`█`. To give the densest character a band of its own, repeat it at the end,
as in `" ░▒▓██"`.

Because brightness is a plain average, saturated colors read as dim: pure
red, green, or blue is a third as bright as white and lands low on the ramp.

Rune's default ramp, `" ▓▓▓▓"`, draws every pixel at least a quarter as
bright as white as a solid `▓` in that pixel's color, and drops everything
darker, black outlines included. Rune also uses it in place of an empty ramp,
which it reports as a configuration error.

## Available settings

| Setting | What it does |
| --- | --- |
| `workspace.wallpaper_image` | Path to a PNG that Rune encodes as colored ASCII art. Relative paths start from the directory Rune was started in. Defaults to `""`, no image. |
| `workspace.wallpaper_ascii` | ASCII art drawn as written and centered as a block. Defaults to `""`, no art. |
| `workspace.wallpaper_logo` | Built-in logo shown when no other wallpaper is set: `rune` or `unstable_build`. Defaults to `rune`; `""` shows no logo. |
| `workspace.wallpaper_density_characters` | Brightness ramp used for image conversion, ordered from least to most dense. Defaults to `" ▓▓▓▓"`. |
| `workspace.wallpaper_attr` | Foreground, background, and flags of the characters of `wallpaper_ascii`. Ignored by image wallpapers, whose colors come from the image. |
| `workspace.wallpaper_background_attr` | Background and flags of the window behind any wallpaper, including the built-in logo. Once set, its `bg` also replaces the `bg` of `wallpaper_attr`. |
| `animations.intro` | Plays the intro of the built-in logo when Rune starts. Defaults to `true`. |

Rune shows the first of these wallpapers that is not empty, in this order:
`wallpaper_image`, then `wallpaper_ascii`, then the built-in logo of
`wallpaper_logo`. When all three are empty, the window stays blank. If Rune
cannot open or decode the image, it reports a configuration error and moves on
to `wallpaper_ascii`. A `wallpaper_ascii` that is not a string is reported as
a configuration error too, and Rune moves on to the built-in logo. A
`wallpaper_logo` that names no built-in logo is reported as a configuration
error, and the window stays blank. In a terminal, it shows the Unstable Build
logo instead.

## What happens under the hood

A wallpaper is not drawn behind your code. Rune creates it as the content of
each window that has no tab to show: a window you open without one, or a
window whose tab you close while every other tab is already shown in a
window. Showing a tab in the window replaces it. This is why every empty
window in a split layout can show one independently.

The art of `wallpaper_ascii` is centered and drawn without conversion. For a
PNG, Rune:

1. resizes the image, preserving its proportions, to fit the central 60% of
   the window's width and 80% of its height;
2. compensates for terminal cells being taller than they are wide, assuming
   a cell is 2.3 times as tall as it is wide;
3. measures each output pixel's brightness as the average of its red, green,
   and blue values and picks a character from the density ramp; and
4. uses the source pixel's color as that character's foreground color.

The result is generated for the current window size, so the same source image
adapts to different layouts and terminal dimensions.

## See also

- [Config](../config.md): config file locations and YAML versus Starlark
- [Themes](./themes.md): change the colors used by Rune and its terminal
