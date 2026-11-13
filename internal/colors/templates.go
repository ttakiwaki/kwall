package colors

import (
	"embed"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"kwall/internal/config"
)

//go:embed templates/*
var embeddedTemplates embed.FS

var tmpSeq atomic.Uint64

var coreTemplates = []string{
	"colors.sh",
	"colors.json",
	"colors-kitty.conf",
}

var namedTemplates = map[string][]string{
	"kitty":     {"colors-kitty.conf"},
	"waybar":    {"colors-waybar.css"},
	"rofi":      {"colors-rofi-dark.rasi", "colors-rofi-light.rasi"},
	"hyprland":  {"colors-hyprland.conf"},
	"sway":      {"colors-sway"},
	"polybar":   {"colors-polybar"},
	"dunst":     {"dunstrc"},
	"mako":      {"colors-mako"},
	"alacritty": {"colors-alacritty.toml", "colors-alacritty.yml"},
	"foot":      {"colors-foot-dark.ini", "colors-foot-light.ini"},
	"fuzzel":    {"colors-fuzzel.ini"},
}

// NewReplacer builds a strings.Replacer for all pywal-style placeholders.
func NewReplacer(p *Palette) *strings.Replacer {
	alphaDec := formatAlphaDec(p.Alpha)
	alphaByte := uint8(math.Round(float64(p.Alpha) * 255.0 / 100.0))
	alphaHex := fmt.Sprintf("%02x", alphaByte)
	wpQuoted := strconv.Quote(p.Wallpaper)
	wpJSON := wpQuoted[1 : len(wpQuoted)-1]

	pairs := make([]string, 0, 19*14+12)
	pairs = append(pairs,
		"{wallpaper}", p.Wallpaper,
		"{wallpaper_json}", wpJSON,
		"{checksum}", p.Checksum,
		"{alpha}", strconv.Itoa(p.Alpha),
		"{alpha_dec}", alphaDec,
		"{alpha_hex}", alphaHex,
	)

	addColor := func(name string, c RGB) {
		rgb := fmt.Sprintf("%d,%d,%d", c.R, c.G, c.B)
		rgba := fmt.Sprintf("%d,%d,%d,%s", c.R, c.G, c.B, alphaDec)
		xrgba := fmt.Sprintf("%02x/%02x/%02x/%s", c.R, c.G, c.B, alphaHex)
		pairs = append(pairs,
			"{"+name+".strip}", c.Strip(),
			"{"+name+".rgb}", rgb,
			"{"+name+".rgba}", rgba,
			"{"+name+".xrgba}", xrgba,
			"{"+name+".alpha_dec}", alphaDec,
			"{"+name+".alpha_hex}", alphaHex,
			"{"+name+"}", c.Hex(),
		)
	}

	addColor("background", p.Background)
	addColor("foreground", p.Foreground)
	addColor("cursor", p.Cursor)
	for i := 0; i < 16; i++ {
		addColor(fmt.Sprintf("color%d", i), p.Colors[i])
	}
	return strings.NewReplacer(pairs...)
}

func formatAlphaDec(alpha int) string {
	if alpha >= 100 {
		return "1.0"
	}
	if alpha <= 0 {
		return "0.0"
	}
	s := fmt.Sprintf("%.2f", float64(alpha)/100.0)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// buildSequences generates pywal-compatible terminal OSC escape sequences.
func buildSequences(p *Palette) []byte {
	var b strings.Builder
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&b, "\x1b]P%x%s\x1b\\", i, p.Colors[i].Strip())
	}
	// Special terminal indices matching pywal sequences.
	fmt.Fprintf(&b, "\x1b]Pg%s\x1b\\", p.Foreground.Strip())
	fmt.Fprintf(&b, "\x1b]Ph%s\x1b\\", p.Background.Strip())
	fmt.Fprintf(&b, "\x1b]Pl%s\x1b\\", p.Cursor.Strip())
	fmt.Fprintf(&b, "\x1b]Pj%s\x1b\\", p.Foreground.Strip())
	fmt.Fprintf(&b, "\x1b]Pk%s\x1b\\", p.Foreground.Strip())
	fmt.Fprintf(&b, "\x1b]Pm%s\x1b\\", p.Background.Strip())
	fmt.Fprintf(&b, "\x1b]4;232;%s\x1b\\", p.Background.Hex())
	fmt.Fprintf(&b, "\x1b]4;256;%s\x1b\\", p.Foreground.Hex())
	fmt.Fprintf(&b, "\x1b]4;257;%s\x1b\\", p.Background.Hex())
	fmt.Fprintf(&b, "\x1b]708;[%d]%s\x1b\\", p.Alpha, p.Background.Hex())
	fmt.Fprintf(&b, "\x1b]6;1;bg;red;brightness;%d\x07", p.Background.R)
	fmt.Fprintf(&b, "\x1b]6;1;bg;green;brightness;%d\x07", p.Background.G)
	fmt.Fprintf(&b, "\x1b]6;1;bg;blue;brightness;%d\x07", p.Background.B)
	return []byte(b.String())
}

// RenderAll writes core files, enabled embedded templates, and any custom
// user templates from ~/.config/wal/templates/ into cfg.WalCacheDir (~/.cache/wal/).
func RenderAll(cfg *config.Config, p *Palette) error {
	if err := os.MkdirAll(cfg.WalCacheDir, 0o755); err != nil {
		return fmt.Errorf("create wal cache dir: %w", err)
	}

	repl := NewReplacer(p)
	rendered := make(map[string]bool)

	renderEmbedded := func(name string) error {
		if rendered[name] {
			return nil
		}
		raw, err := embeddedTemplates.ReadFile("templates/" + name)
		if err != nil {
			return fmt.Errorf("read embedded template %s: %w", name, err)
		}
		out := repl.Replace(string(raw))
		if name == "colors.json" {
			// Ensure JSON-escaped wallpaper path if wallpaper had quotes/backslashes.
			wpQuoted := strconv.Quote(p.Wallpaper)
			wpJSON := wpQuoted[1 : len(wpQuoted)-1]
			if wpJSON != p.Wallpaper {
				out = strings.Replace(out, `"wallpaper": "`+p.Wallpaper+`"`, `"wallpaper": "`+wpJSON+`"`, 1)
			}
		}
		if err := writeAtomic(filepath.Join(cfg.WalCacheDir, name), []byte(out)); err != nil {
			return err
		}
		rendered[name] = true
		return nil
	}

	for _, name := range coreTemplates {
		if err := renderEmbedded(name); err != nil {
			return err
		}
	}
	if err := writeAtomic(filepath.Join(cfg.WalCacheDir, "sequences"), buildSequences(p)); err != nil {
		return err
	}

	for _, key := range cfg.Templates.Enabled {
		files, ok := namedTemplates[strings.ToLower(strings.TrimSpace(key))]
		if !ok {
			continue
		}
		for _, f := range files {
			if err := renderEmbedded(f); err != nil {
				return err
			}
		}
	}

	userDir := filepath.Join(cfg.WalConfigDir, "templates")
	entries, err := os.ReadDir(userDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read user templates %s: %w", userDir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || entry.IsDir() {
			continue
		}
		srcPath := filepath.Join(userDir, name)
		fi, err := os.Stat(srcPath)
		if err != nil || fi.Mode().IsDir() || fi.Mode()&fs.ModeType != 0 {
			continue
		}
		raw, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("read user template %s: %w", srcPath, err)
		}
		out := repl.Replace(string(raw))
		if err := writeAtomic(filepath.Join(cfg.WalCacheDir, name), []byte(out)); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(dest string, data []byte) error {
	tmp := fmt.Sprintf("%s.tmp-%d-%d", dest, os.Getpid(), tmpSeq.Add(1))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
