# kwall

Kitty Wallpaper Switcher: pick a wallpaper from the terminal with `fzf` and a
live, high-resolution image preview rendered by `kitten icat`. Works on
**macOS** and on **Linux (Wayland / Hyprland)**, with optional **pywal**
integration so your terminal colours follow the wallpaper.

## Requirements

Both platforms: [Kitty](https://sw.kovidgoyal.net/kitty/), `fzf`, Go 1.22+ to build.

| | macOS | Linux (Hyprland / Wayland) |
|---|---|---|
| Sets the wallpaper with | `osascript` (built in) | [`awww`](https://codeberg.org/LGFae/awww) (formerly `swww`) |
| Thumbnails with | `sips` (built in) | ImageMagick (`magick` / `convert`) or `ffmpeg` |
| Install | `brew install fzf` | e.g. `pacman -S awww fzf kitty imagemagick` |

On Linux kwall starts `awww-daemon` for you if it isn't running. To have it up
from login, add `exec-once = awww-daemon` to `hyprland.conf`. An older `swww`
install is used automatically if `awww` isn't found.

Without a thumbnail tool kwall still works, but previews use the full-size
originals, so scrolling through big images is slower.

## Install

```sh
make install          # builds and copies kwall to ~/.local/bin
```

Cross-build with `make build-mac` or `make build-linux` (outputs to `dist/`).

## First-time setup

**1. Put `~/.local/bin` on your PATH.** If `kwall` gives "command not found"
after installing, that folder isn't on your PATH yet. In zsh:

```zsh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

On Hyprland, also put that `export` line in `~/.zprofile` so programs launched
outside a shell see it too.

**2. Point kwall at your wallpapers.** The default is `~/Pictures/Wallpapers`.
For another folder, set `WALLPAPER_DIR` (example path):

```zsh
echo 'export WALLPAPER_DIR="$HOME/Downloads/Wallpapers"' >> ~/.zshrc
source ~/.zshrc
```

On Hyprland, add this to `~/.config/hypr/hyprland.conf` and reload Hyprland (or
log out and in), so keybinds and other launchers get it too:

```
env = WALLPAPER_DIR,/home/you/Downloads/Wallpapers
```

**3. Check the tools are installed:**

```sh
which awww fzf kitten magick      # Linux
which fzf kitten                  # macOS
```

Then run `kwall cache` once, and `kwall` to pick a wallpaper.

## Usage

| Command | What it does |
|---|---|
| `kwall` | fzf picker with a 65% right-hand image preview; Enter applies the wallpaper |
| `kwall set <path>` | set a specific image (absolute, relative to cwd, or relative to the wallpaper dir) |
| `kwall random` | set a random wallpaper (avoids the current one when possible) |
| `kwall current` | print the current wallpaper path, with an inline preview in Kitty |
| `kwall cache` | pre-generate thumbnails for every image using 8 workers |
| `kwall preview <rel-path>` | internal, called by fzf for the preview pane |

Supported formats: `.jpg .jpeg .png .webp .heic`. Hidden files and folders are skipped; subfolders are included.
(HEIC on Linux needs an ImageMagick build with libheif.)

## Configuration

| Setting | Default |
|---|---|
| `WALLPAPER_DIR` | `~/Pictures/Wallpapers` |
| Thumbnail cache | `~/.cache/kwall/thumbs/` |
| Last wallpaper | `~/.local/state/kwall/current` |
| Post-hook | `~/.config/kwall/post-hook.sh` |

`XDG_CONFIG_HOME`, `XDG_CACHE_HOME` and `XDG_STATE_HOME` are honoured if set.

| Environment variable | Effect |
|---|---|
| `KWALL_BACKEND` | `osascript` (macOS default), `awww` (Linux default) or `wallpaper` (the macOS `wallpaper` CLI) |
| `KWALL_AWWW_ARGS` | extra flags for `awww img`, e.g. `--resize fit --outputs DP-1` |
| `KWALL_PYWAL=1` | run pywal after every change |
| `KWALL_PYWAL_ARGS` | extra flags for `wal`, e.g. `-a 85 --backend colorz` |
| `KWALL_UNICODE_PLACEHOLDER=0` | disable Kitty's Unicode placeholder mode in previews if they misrender |

awww also reads its own variables, so transitions can be set the usual way:
`AWWW_TRANSITION=fade`, `AWWW_TRANSITION_DURATION=1`, `AWWW_TRANSITION_FPS=60`.

## pywal

Install pywal or its maintained fork pywal16 (both provide the `wal` command),
for example `pipx install pywal16`, plus ImageMagick (pywal's default colour backend).

Turn it on:

```sh
export KWALL_PYWAL=1                # zsh/bash: put it in ~/.zshrc or ~/.bashrc
# Hyprland (so it applies everywhere): env = KWALL_PYWAL,1   in hyprland.conf
```

After each wallpaper change kwall runs `wal -n -q -i <thumbnail>` (it passes
the small cached thumbnail, which is much faster than a 4K original). On macOS
it also adds `-e`, which skips reloading Linux-only desktop pieces. If you
launch kwall inside Kitty with `allow_remote_control yes`, the colours are also
pushed into every open Kitty window.

For new Kitty windows to start with the wallpaper colours, add this **below**
your other colour/theme includes in `kitty.conf`:

```
include ~/.cache/wal/colors-kitty.conf
```

Pywal's Kitty file includes its own `background_opacity`, so keep your own
opacity afterwards, or pass it to wal: `KWALL_PYWAL_ARGS="-a 85"`.

If you used to call `wal` from your post-hook, remove that line when you
enable `KWALL_PYWAL`, otherwise it runs twice.

## Post-hook

If an executable `post-hook.sh` exists in the config dir, it runs after each
change (after pywal, if enabled) with the wallpaper path as `$1` (also in
`$KWALL_WALLPAPER`) and a small cached JPEG thumbnail as `$2` (also in
`$KWALL_THUMBNAIL`). Handy for reloading a bar or notifying other tools:

```sh
#!/bin/sh
# ~/.config/kwall/post-hook.sh  (chmod +x)
pkill -SIGUSR2 waybar      # Hyprland example: reload waybar with the new colours
```

## Hyprland window border colours (pywal)

Needs `KWALL_PYWAL=1` (see above). pywal writes its colours to
`~/.cache/wal/colors.sh`; the post-hook below reads them and gives them to
Hyprland. Not tested on every setup, so check the notes at the end if the
borders don't change.

**1. Create the hook** (merge it into your existing hook if you have one):

```sh
#!/bin/sh
# ~/.config/kwall/post-hook.sh  (chmod +x)
. "$HOME/.cache/wal/colors.sh"

a1=${color4#\#}        # active border, first colour
a2=${color5#\#}        # active border, second colour
inactive=${color0#\#}

# Saved so the colours survive a Hyprland restart (read by your Hyprland config)
cat > "$HOME/.cache/wal/kwall-hypr.lua" <<EOT
return {
  active = { "rgba(${a1}ee)", "rgba(${a2}ee)" },
  inactive = "rgba(${inactive}aa)",
}
EOT

# Applies them to the running session immediately (Lua config: use eval, not keyword)
hyprctl eval "hl.config({ general = { col = { active_border = { colors = { \"rgba(${a1}ee)\", \"rgba(${a2}ee)\" }, angle = 45 }, inactive_border = \"rgba(${inactive}aa)\" } } })" >/dev/null
```

**2. Load the saved colours in your Hyprland config.**

*Lua config (`hyprland.lua`).* Put this above your `general` settings:

```lua
-- Colours from kwall/pywal; falls back to these if the file doesn't exist yet
local ok, wal = pcall(dofile, os.getenv("HOME") .. "/.cache/wal/kwall-hypr.lua")
local active   = ok and wal.active   or { "rgba(157, 206, 223, 1)", "rgba(35, 95, 168, 1)" }
local inactive = ok and wal.inactive or "rgba(595959aa)"
```

then use them in the `general` block:

```lua
col = {
    active_border   = { colors = active, angle = 45 },
    inactive_border = inactive,
},
```

*Classic hyprlang config (`hyprland.conf`).* `hyprctl eval` only exists for Lua
configs, so replace the last command in the hook with these two lines, make the
hook write `general { col.active_border = ... col.inactive_border = ... }` to
`~/.cache/wal/kwall-hypr.conf` instead of the `.lua` file, and `source` it
after your `general` block:

```sh
hyprctl keyword general:col.active_border "rgba(${a1}ee) rgba(${a2}ee) 45deg" >/dev/null
hyprctl keyword general:col.inactive_border "rgba(${inactive}aa)" >/dev/null
```

```
source = ~/.cache/wal/kwall-hypr.conf
```

Run `kwall` once before reloading Hyprland so the file exists.

**Notes**

- `color4` / `color5` are two of pywal's 16 colours (`color0`..`color15`); change
  the numbers to taste.
- If `dofile` is unavailable in Hyprland's Lua environment, the `pcall` falls
  back to the default colours: live changes still work, but reset when
  Hyprland restarts.
- To test the live part by hand:
  `hyprctl eval 'hl.config({ general = { col = { inactive_border = "rgba(ff0000ff)" } } })'`
  should give you a red inactive border.

## Platform notes

- **macOS:** the first time you set a wallpaper, macOS asks whether your
  terminal may control System Events. Allow it (System Settings > Privacy &
  Security > Automation). macOS only applies scripted changes to the active
  Space; there is no scriptable way to set every Space.
- **Linux:** `awww` applies the wallpaper to all monitors by default. Use
  `KWALL_AWWW_ARGS="--outputs DP-1"` to target one.
- Run `kwall cache` once so the first scroll through the picker is instant.

## Layout

```
main.go                     CLI dispatch
internal/config/            env vars, XDG paths, image discovery
internal/fzf/               spawns fzf with --preview="kwall preview {}"
internal/preview/           kitten icat + thumbnail cache (sips / ImageMagick / ffmpeg)
internal/wallpaper/         backends (osascript, awww), state file, post-hook
internal/pywal/             pywal integration
```
