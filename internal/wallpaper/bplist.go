package wallpaper

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
	"unicode/utf16"
)

var (
	bplistMagic = []byte("bplist00")
	appleEpoch  = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
)

func isBplist(data []byte) bool {
	return len(data) >= 40 && bytes.Equal(data[:8], bplistMagic)
}

func parseBplist(data []byte) (*plistNode, error) {
	if !isBplist(data) {
		return nil, errors.New("parse bplist: invalid header or truncated data")
	}

	trailer := data[len(data)-32:]
	offSize := int(trailer[6])
	refSize := int(trailer[7])
	numObjects := binary.BigEndian.Uint64(trailer[8:16])
	topObject := binary.BigEndian.Uint64(trailer[16:24])
	offTableOffset := binary.BigEndian.Uint64(trailer[24:32])

	if !validIntSize(offSize) || !validIntSize(refSize) {
		return nil, fmt.Errorf("parse bplist: invalid sizes off=%d ref=%d", offSize, refSize)
	}
	if numObjects == 0 || topObject >= numObjects {
		return nil, errors.New("parse bplist: invalid object count or top object")
	}
	bodyEnd := uint64(len(data) - 32)
	if offTableOffset < 8 || offTableOffset > bodyEnd {
		return nil, errors.New("parse bplist: invalid offset table location")
	}
	if numObjects > (bodyEnd-offTableOffset)/uint64(offSize) {
		return nil, errors.New("parse bplist: offset table exceeds file size")
	}

	offsets := make([]uint64, numObjects)
	for i := range offsets {
		p := int(offTableOffset) + i*offSize
		offsets[i] = readUintBE(data[p : p+offSize])
		if offsets[i] < 8 || offsets[i] >= offTableOffset {
			return nil, fmt.Errorf("parse bplist: object %d offset out of bounds", i)
		}
	}

	p := &bplistParser{
		data:    data,
		limit:   int(offTableOffset),
		refSize: refSize,
		offsets: offsets,
		cache:   make([]*plistNode, numObjects),
		active:  make([]bool, numObjects),
	}
	return p.decode(topObject)
}

type bplistParser struct {
	data    []byte
	limit   int
	refSize int
	offsets []uint64
	cache   []*plistNode
	active  []bool
}

func (p *bplistParser) decode(idx uint64) (*plistNode, error) {
	if idx >= uint64(len(p.offsets)) {
		return nil, fmt.Errorf("parse bplist: object ref %d out of range", idx)
	}
	if n := p.cache[idx]; n != nil {
		return n, nil
	}
	if p.active[idx] {
		return nil, fmt.Errorf("parse bplist: cyclic object reference at %d", idx)
	}
	p.active[idx] = true
	defer func() { p.active[idx] = false }()

	off := int(p.offsets[idx])
	marker := p.data[off]
	hi, lo := marker>>4, marker&0x0f

	var node *plistNode
	switch hi {
	case 0x0:
		switch marker {
		case 0x08:
			node = &plistNode{kind: "false"}
		case 0x09:
			node = &plistNode{kind: "true"}
		case 0x00, 0x0f:
			node = &plistNode{kind: "bplist-raw", raw: p.data[off : off+1]}
		default:
			return nil, fmt.Errorf("parse bplist: unsupported singleton marker 0x%02x", marker)
		}
	case 0x1:
		size := 1 << lo
		if off+1+size > p.limit {
			return nil, errors.New("parse bplist: truncated int")
		}
		node = &plistNode{kind: "bplist-raw", raw: p.data[off : off+1+size]}
	case 0x2:
		size := 1 << lo
		if off+1+size > p.limit {
			return nil, errors.New("parse bplist: truncated real")
		}
		node = &plistNode{kind: "bplist-raw", raw: p.data[off : off+1+size]}
	case 0x3:
		if lo != 3 || off+9 > p.limit {
			return nil, errors.New("parse bplist: invalid date")
		}
		secs := math.Float64frombits(binary.BigEndian.Uint64(p.data[off+1 : off+9]))
		// time.Duration is int64 nanoseconds (~292 years); preserve macOS
		// sentinel dates (e.g. distantFuture/distantPast) losslessly as raw.
		if math.IsNaN(secs) || math.IsInf(secs, 0) || math.Abs(secs) > 9e9 {
			node = &plistNode{kind: "bplist-raw", raw: p.data[off : off+9]}
		} else {
			ns := int64(secs * 1e9)
			node = &plistNode{kind: "date", dateVal: appleEpoch.Add(time.Duration(ns))}
		}
	case 0x4:
		count, start, err := p.readCount(off+1, lo)
		if err != nil {
			return nil, err
		}
		if start+count > p.limit {
			return nil, errors.New("parse bplist: truncated data")
		}
		node = &plistNode{kind: "data", raw: p.data[start : start+count]}
	case 0x5:
		count, start, err := p.readCount(off+1, lo)
		if err != nil {
			return nil, err
		}
		if start+count > p.limit {
			return nil, errors.New("parse bplist: truncated ascii string")
		}
		node = &plistNode{kind: "string", text: string(p.data[start : start+count])}
	case 0x6:
		count, start, err := p.readCount(off+1, lo)
		if err != nil {
			return nil, err
		}
		byteLen := count * 2
		if start+byteLen > p.limit {
			return nil, errors.New("parse bplist: truncated utf16 string")
		}
		u16 := make([]uint16, count)
		for i := range u16 {
			u16[i] = binary.BigEndian.Uint16(p.data[start+i*2 : start+i*2+2])
		}
		node = &plistNode{kind: "string", text: string(utf16.Decode(u16))}
	case 0x8:
		size := int(lo) + 1
		if off+1+size > p.limit {
			return nil, errors.New("parse bplist: truncated uid")
		}
		node = &plistNode{kind: "bplist-raw", raw: p.data[off : off+1+size]}
	case 0xa:
		count, start, err := p.readCount(off+1, lo)
		if err != nil {
			return nil, err
		}
		if start+count*p.refSize > p.limit {
			return nil, errors.New("parse bplist: truncated array")
		}
		items := make([]*plistNode, count)
		for i := range items {
			ref := readUintBE(p.data[start+i*p.refSize : start+(i+1)*p.refSize])
			val, err := p.decode(ref)
			if err != nil {
				return nil, err
			}
			items[i] = val
		}
		node = &plistNode{kind: "array", items: items}
	case 0xd:
		count, start, err := p.readCount(off+1, lo)
		if err != nil {
			return nil, err
		}
		valStart := start + count*p.refSize
		if valStart+count*p.refSize > p.limit {
			return nil, errors.New("parse bplist: truncated dict")
		}
		entries := make([]plistEntry, count)
		for i := range entries {
			kRef := readUintBE(p.data[start+i*p.refSize : start+(i+1)*p.refSize])
			vRef := readUintBE(p.data[valStart+i*p.refSize : valStart+(i+1)*p.refSize])
			kNode, err := p.decode(kRef)
			if err != nil {
				return nil, err
			}
			if kNode.kind != "string" {
				return nil, fmt.Errorf("parse bplist: non-string dict key (%s)", kNode.kind)
			}
			vNode, err := p.decode(vRef)
			if err != nil {
				return nil, err
			}
			entries[i] = plistEntry{key: kNode.text, val: vNode}
		}
		node = &plistNode{kind: "dict", entries: entries}
	default:
		return nil, fmt.Errorf("parse bplist: unsupported object marker 0x%02x", marker)
	}

	p.cache[idx] = node
	return node, nil
}

func (p *bplistParser) readCount(pos int, lo byte) (int, int, error) {
	if lo < 0x0f {
		return int(lo), pos, nil
	}
	if pos >= p.limit {
		return 0, 0, errors.New("parse bplist: truncated length")
	}
	m := p.data[pos]
	if m>>4 != 0x1 {
		return 0, 0, fmt.Errorf("parse bplist: expected int length marker, got 0x%02x", m)
	}
	size := 1 << (m & 0x0f)
	if pos+1+size > p.limit {
		return 0, 0, errors.New("parse bplist: truncated int length")
	}
	v := readUintBE(p.data[pos+1 : pos+1+size])
	if v > uint64(p.limit) {
		return 0, 0, errors.New("parse bplist: length exceeds file size")
	}
	return int(v), pos + 1 + size, nil
}

func writeBplist(w io.Writer, root *plistNode) error {
	if root == nil {
		return errors.New("marshal bplist: nil root")
	}

	b := &bplistBuilder{
		seenPtr: make(map[*plistNode]uint64),
		seenStr: make(map[string]uint64),
		hasBool: [2]bool{},
	}
	topID, err := b.collect(root)
	if err != nil {
		return err
	}

	numObjects := uint64(len(b.objs))
	refSize := minIntSize(numObjects)

	bw := bufio.NewWriterSize(w, 256*1024)
	if _, err := bw.Write(bplistMagic); err != nil {
		return err
	}

	pos := uint64(len(bplistMagic))
	offsets := make([]uint64, numObjects)
	var scratch [16]byte

	for i, obj := range b.objs {
		offsets[i] = pos
		n, err := obj.writeTo(bw, refSize, &scratch)
		if err != nil {
			return err
		}
		pos += uint64(n)
	}

	offTableOffset := pos
	offSize := minIntSize(offTableOffset)
	for _, off := range offsets {
		writeUintBE(scratch[:offSize], off)
		if _, err := bw.Write(scratch[:offSize]); err != nil {
			return err
		}
	}

	var trailer [32]byte
	trailer[6] = byte(offSize)
	trailer[7] = byte(refSize)
	binary.BigEndian.PutUint64(trailer[8:16], numObjects)
	binary.BigEndian.PutUint64(trailer[16:24], topID)
	binary.BigEndian.PutUint64(trailer[24:32], offTableOffset)
	if _, err := bw.Write(trailer[:]); err != nil {
		return err
	}
	return bw.Flush()
}

type bplistObj struct {
	node     *plistNode
	str      string
	isKey    bool
	keyRefs  []uint64
	childRef []uint64
}

type bplistBuilder struct {
	objs    []bplistObj
	seenPtr map[*plistNode]uint64
	seenStr map[string]uint64
	hasBool [2]bool
	boolID  [2]uint64
}

func (b *bplistBuilder) addString(s string) uint64 {
	if id, ok := b.seenStr[s]; ok {
		return id
	}
	id := uint64(len(b.objs))
	b.objs = append(b.objs, bplistObj{isKey: true, str: s})
	b.seenStr[s] = id
	return id
}

func (b *bplistBuilder) collect(n *plistNode) (uint64, error) {
	if n == nil {
		return 0, errors.New("marshal bplist: nil node")
	}
	switch n.kind {
	case "false":
		if b.hasBool[0] {
			return b.boolID[0], nil
		}
		id := uint64(len(b.objs))
		b.objs = append(b.objs, bplistObj{node: n})
		b.hasBool[0], b.boolID[0] = true, id
		return id, nil
	case "true":
		if b.hasBool[1] {
			return b.boolID[1], nil
		}
		id := uint64(len(b.objs))
		b.objs = append(b.objs, bplistObj{node: n})
		b.hasBool[1], b.boolID[1] = true, id
		return id, nil
	case "string":
		return b.addString(n.text), nil
	}

	if id, ok := b.seenPtr[n]; ok {
		return id, nil
	}

	switch n.kind {
	case "bplist-raw", "data", "date":
		id := uint64(len(b.objs))
		b.objs = append(b.objs, bplistObj{node: n})
		b.seenPtr[n] = id
		return id, nil
	case "array":
		id := uint64(len(b.objs))
		b.objs = append(b.objs, bplistObj{node: n})
		b.seenPtr[n] = id
		refs := make([]uint64, len(n.items))
		for i, item := range n.items {
			ref, err := b.collect(item)
			if err != nil {
				return 0, err
			}
			refs[i] = ref
		}
		b.objs[id].childRef = refs
		return id, nil
	case "dict":
		id := uint64(len(b.objs))
		b.objs = append(b.objs, bplistObj{node: n})
		b.seenPtr[n] = id
		kRefs := make([]uint64, len(n.entries))
		vRefs := make([]uint64, len(n.entries))
		for i, e := range n.entries {
			kRefs[i] = b.addString(e.key)
			ref, err := b.collect(e.val)
			if err != nil {
				return 0, err
			}
			vRefs[i] = ref
		}
		b.objs[id].keyRefs = kRefs
		b.objs[id].childRef = vRefs
		return id, nil
	default:
		return 0, fmt.Errorf("marshal bplist: unsupported node kind %q", n.kind)
	}
}

func (o *bplistObj) writeTo(w io.Writer, refSize int, scratch *[16]byte) (int, error) {
	if o.isKey {
		return writeBplistString(w, o.str, scratch)
	}
	n := o.node
	switch n.kind {
	case "false":
		scratch[0] = 0x08
		return w.Write(scratch[:1])
	case "true":
		scratch[0] = 0x09
		return w.Write(scratch[:1])
	case "bplist-raw":
		return w.Write(n.raw)
	case "date":
		scratch[0] = 0x33
		secs := n.dateVal.Sub(appleEpoch).Seconds()
		binary.BigEndian.PutUint64(scratch[1:9], math.Float64bits(secs))
		return w.Write(scratch[:9])
	case "data":
		raw := n.raw
		if raw == nil && n.text != "" {
			var err error
			raw, err = base64.StdEncoding.DecodeString(n.text)
			if err != nil {
				return 0, fmt.Errorf("marshal bplist: invalid base64 data: %w", err)
			}
		}
		hdrLen, err := writeBplistHeader(w, 0x40, len(raw), scratch)
		if err != nil {
			return 0, err
		}
		dn, err := w.Write(raw)
		return hdrLen + dn, err
	case "array":
		hdrLen, err := writeBplistHeader(w, 0xa0, len(o.childRef), scratch)
		if err != nil {
			return 0, err
		}
		written := hdrLen
		for _, ref := range o.childRef {
			writeUintBE(scratch[:refSize], ref)
			rn, err := w.Write(scratch[:refSize])
			written += rn
			if err != nil {
				return written, err
			}
		}
		return written, nil
	case "dict":
		hdrLen, err := writeBplistHeader(w, 0xd0, len(o.keyRefs), scratch)
		if err != nil {
			return 0, err
		}
		written := hdrLen
		for _, ref := range o.keyRefs {
			writeUintBE(scratch[:refSize], ref)
			rn, err := w.Write(scratch[:refSize])
			written += rn
			if err != nil {
				return written, err
			}
		}
		for _, ref := range o.childRef {
			writeUintBE(scratch[:refSize], ref)
			rn, err := w.Write(scratch[:refSize])
			written += rn
			if err != nil {
				return written, err
			}
		}
		return written, nil
	default:
		return 0, fmt.Errorf("marshal bplist: unsupported node kind %q", n.kind)
	}
}

func writeBplistString(w io.Writer, s string, scratch *[16]byte) (int, error) {
	isASCII := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			isASCII = false
			break
		}
	}
	if isASCII {
		hdrLen, err := writeBplistHeader(w, 0x50, len(s), scratch)
		if err != nil {
			return 0, err
		}
		sn, err := io.WriteString(w, s)
		return hdrLen + sn, err
	}
	u16 := utf16.Encode([]rune(s))
	hdrLen, err := writeBplistHeader(w, 0x60, len(u16), scratch)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, len(u16)*2)
	for i, c := range u16 {
		binary.BigEndian.PutUint16(buf[i*2:i*2+2], c)
	}
	sn, err := w.Write(buf)
	return hdrLen + sn, err
}

func writeBplistHeader(w io.Writer, tag byte, count int, scratch *[16]byte) (int, error) {
	if count < 0x0f {
		scratch[0] = tag | byte(count)
		return w.Write(scratch[:1])
	}
	scratch[0] = tag | 0x0f
	switch {
	case count <= 0xff:
		scratch[1] = 0x10
		scratch[2] = byte(count)
		return w.Write(scratch[:3])
	case count <= 0xffff:
		scratch[1] = 0x11
		binary.BigEndian.PutUint16(scratch[2:4], uint16(count))
		return w.Write(scratch[:4])
	case count <= 0xffffffff:
		scratch[1] = 0x12
		binary.BigEndian.PutUint32(scratch[2:6], uint32(count))
		return w.Write(scratch[:6])
	default:
		scratch[1] = 0x13
		binary.BigEndian.PutUint64(scratch[2:10], uint64(count))
		return w.Write(scratch[:10])
	}
}

func validIntSize(n int) bool {
	return n == 1 || n == 2 || n == 4 || n == 8
}

func minIntSize(maxVal uint64) int {
	switch {
	case maxVal <= 0xff:
		return 1
	case maxVal <= 0xffff:
		return 2
	case maxVal <= 0xffffffff:
		return 4
	default:
		return 8
	}
}

func readUintBE(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = (v << 8) | uint64(x)
	}
	return v
}

func writeUintBE(b []byte, v uint64) {
	for i := len(b) - 1; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
}
