package wallpaper

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	plistHeader = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n" +
		"<plist version=\"1.0\">\n"
	plistFooter = "</plist>\n"
)

type plistEntry struct {
	key string
	val *plistNode
}

type plistNode struct {
	kind    string
	text    string
	raw     []byte
	dateVal time.Time
	items   []*plistNode
	entries []plistEntry
}

func newScalar(kind, text string) *plistNode {
	return &plistNode{kind: kind, text: text}
}

func newArray(items ...*plistNode) *plistNode {
	return &plistNode{kind: "array", items: items}
}

func newDict(entries ...plistEntry) *plistNode {
	return &plistNode{kind: "dict", entries: entries}
}

func (n *plistNode) dictGet(key string) *plistNode {
	if n == nil || n.kind != "dict" {
		return nil
	}
	for _, e := range n.entries {
		if e.key == key {
			return e.val
		}
	}
	return nil
}

func (n *plistNode) dictSet(key string, val *plistNode) {
	for i := range n.entries {
		if n.entries[i].key == key {
			n.entries[i].val = val
			return
		}
	}
	n.entries = append(n.entries, plistEntry{key: key, val: val})
}

func parsePlistXML(data []byte) (*plistNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("parse plist: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "plist" {
			root, err := nextPlistValue(dec)
			if err != nil {
				return nil, err
			}
			if root == nil {
				return nil, errors.New("parse plist: empty <plist> element")
			}
			return root, nil
		}
		return decodePlistElement(dec, se)
	}
}

func nextPlistValue(dec *xml.Decoder) (*plistNode, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return decodePlistElement(dec, t)
		case xml.EndElement:
			return nil, nil
		}
	}
}

func decodePlistElement(dec *xml.Decoder, start xml.StartElement) (*plistNode, error) {
	switch start.Name.Local {
	case "dict":
		node := &plistNode{kind: "dict"}
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.EndElement:
				return node, nil
			case xml.StartElement:
				if t.Name.Local != "key" {
					return nil, fmt.Errorf("parse plist: expected <key>, got <%s>", t.Name.Local)
				}
				var key string
				if err := dec.DecodeElement(&key, &t); err != nil {
					return nil, err
				}
				val, err := nextPlistValue(dec)
				if err != nil {
					return nil, err
				}
				if val == nil {
					return nil, fmt.Errorf("parse plist: missing value for key %q", key)
				}
				node.entries = append(node.entries, plistEntry{key: key, val: val})
			}
		}
	case "array":
		node := &plistNode{kind: "array"}
		for {
			val, err := nextPlistValue(dec)
			if err != nil {
				return nil, err
			}
			if val == nil {
				return node, nil
			}
			node.items = append(node.items, val)
		}
	case "string", "data", "date", "integer", "real":
		var text string
		if err := dec.DecodeElement(&text, &start); err != nil {
			return nil, err
		}
		if start.Name.Local == "data" {
			text = strings.TrimSpace(text)
		}
		return &plistNode{kind: start.Name.Local, text: text}, nil
	case "true", "false":
		if err := dec.Skip(); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		return &plistNode{kind: start.Name.Local}, nil
	default:
		return nil, fmt.Errorf("parse plist: unsupported element <%s>", start.Name.Local)
	}
}

func marshalPlistXML(root *plistNode) ([]byte, error) {
	if root == nil {
		return nil, errors.New("marshal plist: nil root")
	}
	var buf bytes.Buffer
	buf.WriteString(plistHeader)
	if err := writePlistNode(&buf, root, 0); err != nil {
		return nil, err
	}
	buf.WriteString(plistFooter)
	return buf.Bytes(), nil
}

func writePlistNode(buf *bytes.Buffer, n *plistNode, depth int) error {
	indent := strings.Repeat("\t", depth)
	switch n.kind {
	case "dict":
		if len(n.entries) == 0 {
			buf.WriteString(indent + "<dict/>\n")
			return nil
		}
		buf.WriteString(indent + "<dict>\n")
		for _, e := range n.entries {
			buf.WriteString(indent + "\t<key>")
			if err := xml.EscapeText(buf, []byte(e.key)); err != nil {
				return err
			}
			buf.WriteString("</key>\n")
			if err := writePlistNode(buf, e.val, depth+1); err != nil {
				return err
			}
		}
		buf.WriteString(indent + "</dict>\n")
	case "array":
		if len(n.items) == 0 {
			buf.WriteString(indent + "<array/>\n")
			return nil
		}
		buf.WriteString(indent + "<array>\n")
		for _, item := range n.items {
			if err := writePlistNode(buf, item, depth+1); err != nil {
				return err
			}
		}
		buf.WriteString(indent + "</array>\n")
	case "true", "false":
		buf.WriteString(indent + "<" + n.kind + "/>\n")
	case "string", "data", "date", "integer", "real":
		buf.WriteString(indent + "<" + n.kind + ">")
		if err := xml.EscapeText(buf, []byte(n.text)); err != nil {
			return err
		}
		buf.WriteString("</" + n.kind + ">\n")
	default:
		return fmt.Errorf("marshal plist: unsupported node kind %q", n.kind)
	}
	return nil
}

func buildImageConfigNode(fileURL string) *plistNode {
	return newDict(
		plistEntry{key: "type", val: newScalar("string", "imageFile")},
		plistEntry{key: "url", val: newDict(
			plistEntry{key: "relative", val: newScalar("string", fileURL)},
		)},
	)
}

// buildImageConfigXML builds the XML plist for the binary Configuration blob
// embedded inside each wallpaper Choice.
func buildImageConfigXML(fileURL string) ([]byte, error) {
	return marshalPlistXML(buildImageConfigNode(fileURL))
}

// buildImageConfigBplist encodes the ImageConfig directly as bplist00 bytes
// without spawning plutil.
func buildImageConfigBplist(fileURL string) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeBplist(&buf, buildImageConfigNode(fileURL)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// updateWallpaperStore rewrites every Desktop wallpaper target in an
// Index.plist tree (AllSpacesAndDisplays when present, Displays, Spaces, and
// SystemDefault) while preserving Idle entries, scaling options, and unrelated keys.
func updateWallpaperStore(root *plistNode, fileURL string, configBytes []byte, now time.Time) (int, error) {
	if root == nil || root.kind != "dict" {
		return 0, errors.New("unexpected Index.plist root (expected dict)")
	}
	configB64 := base64.StdEncoding.EncodeToString(configBytes)
	nowStr := now.UTC().Format(time.RFC3339)

	updated := 0
	updateContainer := func(container *plistNode) {
		if desktop := container.dictGet("Desktop"); updateDesktopNode(desktop, configBytes, configB64, now, nowStr) {
			updated++
		}
	}

	// Preserve the user's "Show on all Spaces" mode: only update
	// AllSpacesAndDisplays if it already contains a Desktop configuration.
	updateContainer(root.dictGet("AllSpacesAndDisplays"))

	if displays := root.dictGet("Displays"); displays != nil && displays.kind == "dict" {
		for _, disp := range displays.entries {
			updateContainer(disp.val)
		}
	}

	if spaces := root.dictGet("Spaces"); spaces != nil && spaces.kind == "dict" {
		for _, space := range spaces.entries {
			updateContainer(space.val.dictGet("Default"))
			if spaceDisplays := space.val.dictGet("Displays"); spaceDisplays != nil && spaceDisplays.kind == "dict" {
				for _, disp := range spaceDisplays.entries {
					updateContainer(disp.val)
				}
			}
		}
	}

	updateContainer(root.dictGet("SystemDefault"))

	if updated == 0 {
		return 0, errors.New("no Desktop entries found in wallpaper Index.plist")
	}
	return updated, nil
}

func updateDesktopNode(desktop *plistNode, configBytes []byte, configB64 string, now time.Time, nowStr string) bool {
	if desktop == nil || desktop.kind != "dict" {
		return false
	}

	choice := newDict(
		plistEntry{key: "Configuration", val: &plistNode{kind: "data", text: configB64, raw: configBytes}},
		plistEntry{key: "Files", val: newArray()},
		plistEntry{key: "Provider", val: newScalar("string", "com.apple.wallpaper.choice.image")},
	)
	content := newDict(
		plistEntry{key: "Choices", val: newArray(choice)},
		plistEntry{key: "EncodedOptionValues", val: newScalar("string", "$null")},
		plistEntry{key: "Shuffle", val: newScalar("string", "$null")},
	)

	desktop.dictSet("Content", content)
	desktop.dictSet("LastSet", &plistNode{kind: "date", text: nowStr, dateVal: now})
	desktop.dictSet("LastUse", &plistNode{kind: "date", text: nowStr, dateVal: now})
	return true
}
