package wallpaper

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestBplistRoundTripAndUpdate(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil is only available on macOS")
	}

	// Build a realistic XML plist (including unicode strings, dates, data, ints, bools)
	// and convert it to binary1 using plutil so we test against Apple's encoder.
	xmlIn := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>AllSpacesAndDisplays</key>
	<dict>
		<key>Desktop</key>
		<dict>
			<key>Content</key>
			<dict>
				<key>Choices</key>
				<array>
					<dict>
						<key>Configuration</key>
						<data>AAAA</data>
						<key>Files</key>
						<array/>
						<key>Provider</key>
						<string>com.apple.wallpaper.choice.image</string>
					</dict>
				</array>
			</dict>
			<key>LastSet</key>
			<date>2026-01-01T00:00:00Z</date>
			<key>LastUse</key>
			<date>2026-01-01T00:00:00Z</date>
		</dict>
		<key>Idle</key>
		<dict>
			<key>BigBlob</key>
			<data>AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=</data>
			<key>Count</key>
			<integer>42</integer>
			<key>Scale</key>
			<real>1.5</real>
			<key>Active</key>
			<true/>
			<key>Hidden</key>
			<false/>
			<key>Label</key>
			<string>ünïcödé-壁</string>
		</dict>
	</dict>
</dict>
</plist>
`
	binIn, err := plutilToBinary([]byte(xmlIn), "-")
	if err != nil {
		t.Fatalf("plutilToBinary: %v", err)
	}
	if !isBplist(binIn) {
		t.Fatalf("expected bplist00 header")
	}

	root, err := parseBplist(binIn)
	if err != nil {
		t.Fatalf("parseBplist: %v", err)
	}

	cfgBin, err := plutilToBinary([]byte(plistHeader+`<dict><key>type</key><string>imageFile</string></dict>`+"\n"+plistFooter), "-")
	if err != nil {
		t.Fatalf("cfg plutilToBinary: %v", err)
	}
	now := time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)
	updated, err := updateWallpaperStore(root, "file:///Users/test/wall.jpg", cfgBin, now)
	if err != nil {
		t.Fatalf("updateWallpaperStore: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected 1 updated desktop, got %d", updated)
	}

	var out bytes.Buffer
	if err := writeBplist(&out, root); err != nil {
		t.Fatalf("writeBplist: %v", err)
	}

	tmp := filepath.Join(t.TempDir(), "out.plist")
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if cmdOut, err := exec.Command("plutil", "-lint", tmp).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint failed: %v: %s", err, cmdOut)
	}

	// Re-parse our own output to verify everything is intact.
	root2, err := parseBplist(out.Bytes())
	if err != nil {
		t.Fatalf("re-parseBplist: %v", err)
	}
	idle := root2.dictGet("AllSpacesAndDisplays").dictGet("Idle")
	if got := idle.dictGet("Label").text; got != "ünïcödé-壁" {
		t.Fatalf("unicode string mismatch: got %q", got)
	}
	if got := idle.dictGet("Active").kind; got != "true" {
		t.Fatalf("bool mismatch: got %q", got)
	}
	choices := root2.dictGet("AllSpacesAndDisplays").dictGet("Desktop").dictGet("Content").dictGet("Choices")
	if len(choices.items) != 1 || !bytes.Equal(choices.items[0].dictGet("Configuration").raw, cfgBin) {
		t.Fatalf("updated Configuration bytes mismatch")
	}
}

func TestBplistSentinelDateAndImageConfig(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil is only available on macOS")
	}
	cfgBin, err := buildImageConfigBplist("file:///Users/test/wall.jpg")
	if err != nil {
		t.Fatalf("buildImageConfigBplist: %v", err)
	}
	tmpCfg := filepath.Join(t.TempDir(), "cfg.plist")
	if err := os.WriteFile(tmpCfg, cfgBin, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", tmpCfg).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint cfg failed: %v: %s", err, out)
	}

	// Distant future sentinel date (4001-01-01) must round-trip without int64 overflow.
	xmlIn := plistHeader + `<dict><key>Far</key><date>4001-01-01T00:00:00Z</date></dict>` + "\n" + plistFooter
	binIn, err := plutilToBinary([]byte(xmlIn), "-")
	if err != nil {
		t.Fatal(err)
	}
	root, err := parseBplist(binIn)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeBplist(&buf, root); err != nil {
		t.Fatal(err)
	}
	tmpDate := filepath.Join(t.TempDir(), "date.plist")
	if err := os.WriteFile(tmpDate, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", tmpDate).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint date failed: %v: %s", err, out)
	}
	outXML, err := plutilToXML(tmpDate)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(outXML, []byte("4001-01-01T00:00:00Z")) {
		t.Fatalf("sentinel date did not round-trip losslessly:\n%s", outXML)
	}
}
