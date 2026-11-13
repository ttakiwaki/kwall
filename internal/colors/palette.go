// Package colors extracts 16-color terminal palettes from wallpaper images
// in pure Go and renders pywal-compatible templates to ~/.cache/wal/.
package colors

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"sort"
)

// RGB represents an 8-bit sRGB color.
type RGB struct {
	R, G, B uint8
}

// Hex returns the lowercase #rrggbb representation.
func (c RGB) Hex() string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// Strip returns rrggbb without a leading #.
func (c RGB) Strip() string {
	return fmt.Sprintf("%02x%02x%02x", c.R, c.G, c.B)
}

// Options configures palette extraction.
type Options struct {
	Mode       string  // "dark" | "light" | "auto"
	Saturation float64 // multiplier (1.0 = default)
	Contrast   float64 // multiplier (1.0 = default)
	Alpha      int     // 0..100 (100 = opaque)
}

// Palette holds a 16-color terminal scheme plus special colors.
type Palette struct {
	Wallpaper  string
	Checksum   string
	Alpha      int
	Background RGB
	Foreground RGB
	Cursor     RGB
	Colors     [16]RGB
}

// Extract decodes imgPath, builds a 16-color palette, and records wallpaperPath
// (or imgPath if empty) as the wallpaper metadata.
func Extract(imgPath, wallpaperPath string, opts Options) (*Palette, error) {
	if wallpaperPath == "" {
		wallpaperPath = imgPath
	}
	f, err := os.Open(imgPath)
	if err != nil {
		return nil, fmt.Errorf("open image: %w", err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	samples, avgLum := samplePixels(img, 96)
	if len(samples) == 0 {
		return nil, fmt.Errorf("image %s has no pixels", imgPath)
	}

	mode := opts.Mode
	if mode == "" || mode == "auto" {
		if avgLum > 0.52 {
			mode = "light"
		} else {
			mode = "dark"
		}
	}
	sat := opts.Saturation
	if sat <= 0 {
		sat = 1.0
	}
	con := opts.Contrast
	if con <= 0 {
		con = 1.0
	}
	alpha := opts.Alpha
	if alpha <= 0 && opts.Alpha == 0 {
		alpha = 100
	}
	if alpha < 0 {
		alpha = 0
	} else if alpha > 100 {
		alpha = 100
	}

	clusters := medianCut(samples, 32)
	bg, fg, accents := buildScheme(clusters, mode, sat, con)

	var p Palette
	p.Wallpaper = wallpaperPath
	p.Alpha = alpha
	p.Background = bg
	p.Foreground = fg
	p.Cursor = fg
	p.Colors[0] = bg
	p.Colors[7] = fg
	p.Colors[8] = deriveColor8(bg, fg, mode)
	p.Colors[15] = deriveColor15(bg, fg, mode)

	for i := 0; i < 6; i++ {
		c := accents[i]
		p.Colors[i+1] = c
		p.Colors[i+9] = deriveBright(c, bg, mode)
	}

	sum := md5.Sum([]byte(fmt.Sprintf("%s|%s|%v", wallpaperPath, mode, p.Colors)))
	p.Checksum = hex.EncodeToString(sum[:])
	return &p, nil
}

type colorCluster struct {
	color RGB
	count int
}

// samplePixels downsamples img to roughly maxDim x maxDim pixels and returns
// the sampled pixels alongside mean relative luminance.
func samplePixels(img image.Image, maxDim int) ([]RGB, float64) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, 0
	}
	stepX := max(1, w/maxDim)
	stepY := max(1, h/maxDim)

	samples := make([]RGB, 0, (w/stepX+1)*(h/stepY+1))
	var lumSum float64
	for y := b.Min.Y; y < b.Max.Y; y += stepY {
		for x := b.Min.X; x < b.Max.X; x += stepX {
			r32, g32, b32, a32 := img.At(x, y).RGBA()
			if a32 < 0x1000 {
				continue
			}
			c := RGB{uint8(r32 >> 8), uint8(g32 >> 8), uint8(b32 >> 8)}
			samples = append(samples, c)
			lumSum += relLuminance(c)
		}
	}
	if len(samples) == 0 {
		return nil, 0
	}
	return samples, lumSum / float64(len(samples))
}

func medianCut(pixels []RGB, maxBuckets int) []colorCluster {
	if len(pixels) == 0 {
		return nil
	}
	buckets := [][]RGB{pixels}
	for len(buckets) < maxBuckets {
		bestIdx := -1
		bestSpan := -1
		bestChan := 0

		for i, bkt := range buckets {
			if len(bkt) < 2 {
				continue
			}
			minR, maxR := uint8(255), uint8(0)
			minG, maxG := uint8(255), uint8(0)
			minB, maxB := uint8(255), uint8(0)
			for _, p := range bkt {
				if p.R < minR {
					minR = p.R
				}
				if p.R > maxR {
					maxR = p.R
				}
				if p.G < minG {
					minG = p.G
				}
				if p.G > maxG {
					maxG = p.G
				}
				if p.B < minB {
					minB = p.B
				}
				if p.B > maxB {
					maxB = p.B
				}
			}
			spanR := int(maxR - minR)
			spanG := int(maxG - minG)
			spanB := int(maxB - minB)
			span := spanR
			ch := 0
			if spanG > span {
				span = spanG
				ch = 1
			}
			if spanB > span {
				span = spanB
				ch = 2
			}
			if span > bestSpan {
				bestSpan = span
				bestIdx = i
				bestChan = ch
			}
		}
		if bestIdx < 0 || bestSpan <= 4 {
			break
		}

		bkt := buckets[bestIdx]
		sort.Slice(bkt, func(i, j int) bool {
			switch bestChan {
			case 0:
				return bkt[i].R < bkt[j].R
			case 1:
				return bkt[i].G < bkt[j].G
			default:
				return bkt[i].B < bkt[j].B
			}
		})
		mid := len(bkt) / 2
		buckets[bestIdx] = bkt[:mid]
		buckets = append(buckets, bkt[mid:])
	}

	out := make([]colorCluster, 0, len(buckets))
	for _, bkt := range buckets {
		if len(bkt) == 0 {
			continue
		}
		var sumR, sumG, sumB int
		for _, p := range bkt {
			sumR += int(p.R)
			sumG += int(p.G)
			sumB += int(p.B)
		}
		n := len(bkt)
		out = append(out, colorCluster{
			color: RGB{uint8(sumR / n), uint8(sumG / n), uint8(sumB / n)},
			count: n,
		})
	}
	return out
}

func buildScheme(clusters []colorCluster, mode string, satMult, conMult float64) (bg, fg RGB, accents [6]RGB) {
	// Pick dominant color for background tinting.
	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].count > clusters[j].count
	})
	dom := clusters[0].color
	domH, domS, _ := rgbToHSL(dom)

	if mode == "light" {
		bg = hslToRGB(domH, minFloat(domS, 0.22), 0.94)
		fg = hslToRGB(domH, minFloat(domS, 0.14), 0.14)
	} else {
		bg = hslToRGB(domH, minFloat(domS, 0.35), 0.08)
		fg = hslToRGB(domH, minFloat(domS, 0.12), 0.80)
	}

	// Score clusters for vibrant, well-represented accents.
	type scored struct {
		color RGB
		score float64
	}
	ranked := make([]scored, len(clusters))
	total := 0
	for _, c := range clusters {
		total += c.count
	}
	for i, c := range clusters {
		_, s, l := rgbToHSL(c.color)
		weight := float64(c.count) / float64(max(1, total))
		midBias := 1.0 - math.Abs(l-0.55)*1.4
		if midBias < 0.15 {
			midBias = 0.15
		}
		score := (0.35 + 1.8*s) * midBias * math.Sqrt(weight+0.01)
		ranked[i] = scored{color: c.color, score: score}
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	var picked []RGB
	for _, minDist := range []float64{55, 32, 16, 0} {
		for _, cand := range ranked {
			if len(picked) >= 6 {
				break
			}
			if tooClose(cand.color, picked, minDist) {
				continue
			}
			picked = append(picked, cand.color)
		}
		if len(picked) >= 6 {
			break
		}
	}
	for len(picked) < 6 {
		idx := len(picked)
		base := picked[0]
		h, s, l := rgbToHSL(base)
		h = math.Mod(h+float64(idx)*37, 360)
		s = clampFloat(s+0.12, 0.25, 0.75)
		l = clampFloat(l+float64(idx-2)*0.06, 0.35, 0.72)
		picked = append(picked, hslToRGB(h, s, l))
	}

	// Sort accents by luminance (soft gradient order like pywal).
	sort.Slice(picked, func(i, j int) bool {
		return relLuminance(picked[i]) < relLuminance(picked[j])
	})

	minContrast := 3.2 * conMult
	if minContrast < 1.8 {
		minContrast = 1.8
	} else if minContrast > 7.0 {
		minContrast = 7.0
	}

	for i := 0; i < 6; i++ {
		h, s, l := rgbToHSL(picked[i])
		s = clampFloat(s*satMult, 0.0, 0.95)
		if mode == "light" {
			l = clampFloat(l, 0.22, 0.48)
		} else {
			l = clampFloat(l, 0.44, 0.76)
		}
		c := ensureContrast(hslToRGB(h, s, l), bg, mode, minContrast)
		accents[i] = c
	}

	fg = ensureContrast(fg, bg, mode, 7.0)
	return bg, fg, accents
}

func tooClose(c RGB, chosen []RGB, minDist float64) bool {
	for _, x := range chosen {
		dr := float64(int(c.R) - int(x.R))
		dg := float64(int(c.G) - int(x.G))
		db := float64(int(c.B) - int(x.B))
		if math.Sqrt(dr*dr+dg*dg+db*db) < minDist {
			return true
		}
	}
	return false
}

func deriveColor8(bg, fg RGB, mode string) RGB {
	if mode == "light" {
		return mixRGB(bg, fg, 0.40)
	}
	return mixRGB(bg, fg, 0.35)
}

func deriveColor15(bg, fg RGB, mode string) RGB {
	if mode == "light" {
		return mixRGB(fg, RGB{0, 0, 0}, 0.20)
	}
	return mixRGB(fg, RGB{255, 255, 255}, 0.18)
}

func deriveBright(c, bg RGB, mode string) RGB {
	h, s, l := rgbToHSL(c)
	if mode == "light" {
		l = clampFloat(l-0.08, 0.16, 0.45)
	} else {
		l = clampFloat(l+0.07, 0.48, 0.84)
	}
	s = clampFloat(s*1.06, 0, 0.96)
	return ensureContrast(hslToRGB(h, s, l), bg, mode, 3.6)
}

func ensureContrast(c, bg RGB, mode string, minRatio float64) RGB {
	if contrastRatio(c, bg) >= minRatio {
		return c
	}
	h, s, l := rgbToHSL(c)
	step := 0.03
	if mode == "light" {
		step = -0.03
	}
	for iter := 0; iter < 30; iter++ {
		l = clampFloat(l+step, 0.04, 0.96)
		cand := hslToRGB(h, s, l)
		if contrastRatio(cand, bg) >= minRatio {
			return cand
		}
		if l <= 0.04 || l >= 0.96 {
			break
		}
	}
	return hslToRGB(h, s, l)
}

func mixRGB(a, b RGB, t float64) RGB {
	return RGB{
		R: uint8(clampFloat(float64(a.R)*(1-t)+float64(b.R)*t, 0, 255)),
		G: uint8(clampFloat(float64(a.G)*(1-t)+float64(b.G)*t, 0, 255)),
		B: uint8(clampFloat(float64(a.B)*(1-t)+float64(b.B)*t, 0, 255)),
	}
}

func relLuminance(c RGB) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255.0
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrastRatio(a, b RGB) float64 {
	la := relLuminance(a)
	lb := relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func rgbToHSL(c RGB) (h, s, l float64) {
	r := float64(c.R) / 255.0
	g := float64(c.G) / 255.0
	b := float64(c.B) / 255.0
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2.0
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2.0 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	case b:
		h = (r-g)/d + 4
	}
	h *= 60
	return h, s, l
}

func hslToRGB(h, s, l float64) RGB {
	if s == 0 {
		v := uint8(math.Round(clampFloat(l*255, 0, 255)))
		return RGB{v, v, v}
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	hk := h / 360.0
	hue2rgb := func(t float64) float64 {
		t = math.Mod(t+1, 1)
		switch {
		case t < 1.0/6.0:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3.0:
			return p + (q-p)*(2.0/3.0-t)*6
		default:
			return p
		}
	}
	return RGB{
		R: uint8(math.Round(clampFloat(hue2rgb(hk+1.0/3.0)*255, 0, 255))),
		G: uint8(math.Round(clampFloat(hue2rgb(hk)*255, 0, 255))),
		B: uint8(math.Round(clampFloat(hue2rgb(hk-1.0/3.0)*255, 0, 255))),
	}
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
