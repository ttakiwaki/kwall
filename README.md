# kwall

Kitty Wallpaper Switcher: pick a macOS wallpaper from the terminal with `fzf`
and a live, high-resolution image preview rendered by `kitten icat`.

## Requirements

- macOS (uses `sips`, `plutil`, and `osascript` on macOS 13 and earlier)
- [Kitty](https://sw.kovidgoyal.net/kitty/) terminal (for previews)
- `fzf` (`brew install fzf`)
- Go 1.22+ to build

## Install

```sh
make install          # builds and copies kwall to ~/.local/bin
# make sure ~/.local/bin is on your PATH
```

Or cross-build from another machine: `make build-mac` (outputs to `dist/`).

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

## Configuration

| Setting | Default |
|---|---|
| `WALLPAPER_DIR` | `~/Pictures/Wallpapers` |
| Thumbnail cache | `~/.cache/kwall/thumbs/` |
| Last wallpaper | `~/.local/state/kwall/current` |
| Post-hook | `~/.config/kwall/post-hook.sh` |

`XDG_CONFIG_HOME`, `XDG_CACHE_HOME` and `XDG_STATE_HOME` are honoured if set.

**Post-hook:** if an executable `post-hook.sh` exists in the config dir, it runs after each change with the wallpaper path as `$1` (also in `$KWALL_WALLPAPER`) and a small cached JPEG thumbnail as `$2` (also in `$KWALL_THUMBNAIL`). Use the thumbnail for anything that analyses the image; it is far faster than a full-size original. Example with pywal:

```sh
#!/bin/sh
# ~/.config/kwall/post-hook.sh  (chmod +x)
wal -n -q -e -a 85 -i "${2:-$1}"
```

Other environment variables:

- `KWALL_BACKEND` controls how wallpapers are applied:
  - `plist` (default on macOS 14+): updates `~/Library/Application Support/com.apple.wallpaper/Store/Index.plist` and restarts `WallpaperAgent` so the wallpaper changes across **every macOS Space** and display (falls back to `osascript` on macOS 13 and earlier).
  - `osascript`: uses System Events AppleScript (active Space only).
  - `wallpaper`: uses the [`wallpaper` CLI](https://github.com/sindresorhus/macos-wallpaper) (active Space only).
- `KWALL_UNICODE_PLACEHOLDER=0` turns off Kitty's Unicode placeholder mode in the preview pane (falls back to direct placement) if previews misrender in your fzf/Kitty combination.

## First run

On macOS 14+, the default `plist` backend needs no special Automation permission. If you use `KWALL_BACKEND=osascript` (or macOS 13 and earlier), macOS will ask whether your terminal may control **System Events** — allow it in System Settings > Privacy & Security > Automation. Run `kwall cache` once so the first scroll through the picker is instant.

## Layout

```
main.go                     CLI dispatch
internal/config/            env vars, XDG paths, image discovery
internal/fzf/               spawns fzf with --preview="kwall preview {}"
internal/preview/           kitten icat + sips thumbnail cache
internal/wallpaper/         wallpaper store/osascript setter, state file, post-hook
```
