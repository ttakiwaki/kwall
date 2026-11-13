package colors

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kwall/internal/config"
)

func writeTestPNG(t *testing.T, path string, bright bool) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 120, 120))
	bands := []color.RGBA{
		{20, 24, 40, 255},
		{200, 70, 90, 255},
		{60, 180, 120, 255},
		{70, 130, 220, 255},
		{210, 160, 50, 255},
		{160, 90, 200, 255},
	}
	if bright {
		bands[0] = color.RGBA{235, 240, 248, 255}
	}
	for y := 0; y < 120; y++ {
		c := bands[(y/20)%len(bands)]
		for x := 0; x < 120; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestExtractModesAndContrast(t *testing.T) {
	dir := t.TempDir()
	darkImg := filepath.Join(dir, "dark.png")
	lightImg := filepath.Join(dir, "light.png")
	writeTestPNG(t, darkImg, false)
	writeTestPNG(t, lightImg, true)

	pDark, err := Extract(darkImg, "/walls/dark.png", Options{Mode: "dark", Saturation: 1.0, Contrast: 1.0, Alpha: 85})
	if err != nil {
		t.Fatal(err)
	}
	if pDark.Wallpaper != "/walls/dark.png" || pDark.Alpha != 85 {
		t.Fatalf("metadata mismatch: %+v", pDark)
	}
	if relLuminance(pDark.Background) >= relLuminance(pDark.Foreground) {
		t.Fatalf("dark mode bg (%s) should be darker than fg (%s)", pDark.Background.Hex(), pDark.Foreground.Hex())
	}
	if contrastRatio(pDark.Foreground, pDark.Background) < 4.5 {
		t.Fatalf("insufficient fg/bg contrast in dark mode")
	}

	pLight, err := Extract(lightImg, "", Options{Mode: "light", Saturation: 1.1, Contrast: 1.0, Alpha: 100})
	if err != nil {
		t.Fatal(err)
	}
	if relLuminance(pLight.Background) <= relLuminance(pLight.Foreground) {
		t.Fatalf("light mode bg (%s) should be brighter than fg (%s)", pLight.Background.Hex(), pLight.Foreground.Hex())
	}
	if contrastRatio(pLight.Foreground, pLight.Background) < 4.5 {
		t.Fatalf("insufficient fg/bg contrast in light mode")
	}

	pAuto, err := Extract(darkImg, "", Options{Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if pAuto.Alpha != 100 {
		t.Fatalf("default alpha = %d, want 100", pAuto.Alpha)
	}
}

func TestReplacerModifiersAndRenderAll(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, `wall "1".png`)
	writeTestPNG(t, imgPath, false)

	walCfg := filepath.Join(dir, "config", "wal")
	walCache := filepath.Join(dir, "cache", "wal")
	userTplDir := filepath.Join(walCfg, "templates")
	if err := os.MkdirAll(userTplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customTpl := "hex={color1}\nstrip={color1.strip}\nrgb={color1.rgb}\nrgba={color1.rgba}\nxrgba={color1.xrgba}\nwp={wallpaper}\n"
	if err := os.WriteFile(filepath.Join(userTplDir, "custom.conf"), []byte(customTpl), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		WalConfigDir: walCfg,
		WalCacheDir:  walCache,
		Colors: config.ColorsConfig{
			Enabled:    true,
			Mode:       "dark",
			Saturation: 1.0,
			Contrast:   1.0,
			Alpha:      85,
		},
		Templates: config.TemplatesConfig{
			Enabled: []string{"kitty", "waybar", "rofi", "hyprland", "sway", "polybar", "dunst", "mako", "alacritty", "foot", "fuzzel"},
		},
	}

	p, err := Extract(imgPath, imgPath, Options{
		Mode:       cfg.Colors.Mode,
		Saturation: cfg.Colors.Saturation,
		Contrast:   cfg.Colors.Contrast,
		Alpha:      cfg.Colors.Alpha,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := RenderAll(cfg, p); err != nil {
		t.Fatal(err)
	}

	expectedFiles := []string{
		"colors.sh",
		"colors.json",
		"colors-kitty.conf",
		"sequences",
		"colors-waybar.css",
		"colors-rofi-dark.rasi",
		"colors-rofi-light.rasi",
		"colors-hyprland.conf",
		"colors-sway",
		"colors-polybar",
		"dunstrc",
		"colors-mako",
		"colors-alacritty.toml",
		"colors-alacritty.yml",
		"colors-foot-dark.ini",
		"colors-foot-light.ini",
		"colors-fuzzel.ini",
		"custom.conf",
	}
	for _, name := range expectedFiles {
		data, err := os.ReadFile(filepath.Join(walCache, name))
		if err != nil {
			t.Fatalf("missing rendered file %s: %v", name, err)
		}
		if name != "sequences" && strings.Contains(string(data), "{color") {
			t.Errorf("unreplaced placeholder in %s", name)
		}
	}

	// Verify colors.json is valid JSON even with quotes in wallpaper path.
	jsonBytes, err := os.ReadFile(filepath.Join(walCache, "colors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		t.Fatalf("invalid colors.json: %v\n%s", err, jsonBytes)
	}
	if parsed["wallpaper"] != imgPath {
		t.Errorf("colors.json wallpaper = %v, want %q", parsed["wallpaper"], imgPath)
	}

	// Verify custom template modifiers.
	customOut, err := os.ReadFile(filepath.Join(walCache, "custom.conf"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(customOut)
	if !strings.Contains(s, "hex=#") || !strings.Contains(s, "rgba=") || !strings.Contains(s, ",0.85") || !strings.Contains(s, "/d9") {
		t.Errorf("unexpected custom template output:\n%s", s)
	}
}
