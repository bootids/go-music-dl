package core

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// MP4TagEdit 描述要改的 iTunes 风格标签：nil 表示不动，指向 "" 表示删除该项。
type MP4TagEdit struct {
	Lyric, Album, Artist *string
}

// EditMP4Tags 只改 moov/udta/meta/ilst 里的 ©lyr、©alb、©ART 三项，其他标签（含 ---- 自定义项、封面、
// 曲目号）、其他盒子和 mdat 音频按原字节保留。
//
//   - 已有的项原位替换；同名项有多个或一项里有多个 data 时，合成一项放在第一次出现的位置；新项追加到 ilst 末尾。
//   - 缺 udta/meta/ilst 时按 iTunes/ffmpeg 的写法新建（meta 为 FullBox，hdlr 为 mdir/appl）；缺 moov 报错。
//   - ilst 后、meta 后、udta 后紧跟的 free/skip 填充优先吸收大小变化，moov 大小不变时不用改任何偏移。
//   - moov 大小变了时，指向原 moov 之后的绝对偏移（stco、co64、分片里 tfhd 的 base-data-offset）都平移；
//     有 mfra（tfra 存绝对偏移）、moov 之前有 sidx、stbl 里有 saio（非分片时是绝对偏移）时拒绝，不猜。
//   - 盒子结构损坏、meta 的 handler 不是 mdir（如 QuickTime 的 mdta/keys）、路径上有重复盒子时报错，不写坏文件。
func EditMP4Tags(data []byte, edit MP4TagEdit) ([]byte, error) {
	var edits []mp4IlstEdit
	for _, f := range []struct {
		typ string
		val *string
	}{{"\xa9ART", edit.Artist}, {"\xa9alb", edit.Album}, {"\xa9lyr", edit.Lyric}} {
		if f.val == nil {
			continue
		}
		e := mp4IlstEdit{typ: f.typ}
		if *f.val != "" {
			atom, err := mp4TextItem(f.typ, *f.val)
			if err != nil {
				return nil, err
			}
			e.atom = atom
		}
		edits = append(edits, e)
	}
	if len(edits) == 0 {
		return append([]byte(nil), data...), nil
	}
	return editMP4Ilst(data, edits)
}

// mp4IlstEdit 把 ilst 里类型为 typ 的项全部换成 atom（完整的项盒子）；atom 为 nil 表示删除。
type mp4IlstEdit struct {
	typ  string
	atom []byte
}

type mp4Box struct {
	typ        string
	start, end int // 整个盒子在 data 里的范围
	hdr        int // 头长度：8，或 64 位 largesize 时 16
	large      bool
	toEOF      bool // 头里 size 为 0：延伸到文件末尾，只允许出现在顶层最后
}

func (b mp4Box) payload() int { return b.start + b.hdr }

// mp4Container 是一个已解析出子盒子的容器。kidsEnd 是最后一个子盒子的结束位置，
// 其后到 box.end 之间只可能是 QuickTime 的 4 字节 0 结束符，按原字节保留。
type mp4Container struct {
	box     mp4Box
	kids    []mp4Box
	kidsEnd int
}

// parseMP4Boxes 解析 [start,end) 里首尾相接的盒子，每个大小都校验不超出父范围。
func parseMP4Boxes(data []byte, start, end int, topLevel bool) ([]mp4Box, int, error) {
	if start < 0 || end > len(data) || start > end {
		return nil, 0, errors.New("MP4 盒子范围无效")
	}
	var boxes []mp4Box
	pos := start
	for pos < end {
		remain := end - pos
		if remain < 8 {
			if !topLevel && remain == 4 && binary.BigEndian.Uint32(data[pos:]) == 0 {
				return boxes, pos, nil
			}
			return nil, 0, fmt.Errorf("MP4 偏移 %d 处剩余 %d 字节，不足一个盒子头", pos, remain)
		}
		b := mp4Box{typ: string(data[pos+4 : pos+8]), start: pos, hdr: 8}
		size := uint64(binary.BigEndian.Uint32(data[pos:]))
		switch size {
		case 0:
			if !topLevel {
				return nil, 0, fmt.Errorf("MP4 盒子 %q 在偏移 %d 处大小为 0", b.typ, pos)
			}
			b.toEOF = true
			size = uint64(remain)
		case 1:
			if remain < 16 {
				return nil, 0, fmt.Errorf("MP4 盒子 %q 的 64 位大小被截断", b.typ)
			}
			b.large, b.hdr = true, 16
			size = binary.BigEndian.Uint64(data[pos+8:])
		}
		if size < uint64(b.hdr) || size > uint64(remain) {
			return nil, 0, fmt.Errorf("MP4 盒子 %q 在偏移 %d 处的大小 %d 无效（小于盒子头或超出父盒子范围）", b.typ, pos, size)
		}
		b.end = pos + int(size)
		boxes = append(boxes, b)
		pos = b.end
	}
	return boxes, pos, nil
}

func openMP4Container(data []byte, b mp4Box, skip int) (mp4Container, error) {
	if b.end-b.payload() < skip {
		return mp4Container{}, fmt.Errorf("MP4 盒子 %q 太短", b.typ)
	}
	kids, kidsEnd, err := parseMP4Boxes(data, b.payload()+skip, b.end, false)
	if err != nil {
		return mp4Container{}, err
	}
	return mp4Container{box: b, kids: kids, kidsEnd: kidsEnd}, nil
}

// openMP4Meta 兼容两种 meta：ISO/iTunes 的 FullBox（子盒子前有 4 字节 version+flags，均为 0），
// 和 QuickTime 风格直接跟子盒子的写法（那 4 字节是第一个子盒子的大小，必然非 0）。
func openMP4Meta(data []byte, b mp4Box) (mp4Container, error) {
	p := b.payload()
	if b.end-p >= 4 && binary.BigEndian.Uint32(data[p:]) == 0 {
		return openMP4Container(data, b, 4)
	}
	c, err := openMP4Container(data, b, 0)
	if err != nil {
		return mp4Container{}, fmt.Errorf("无法识别 meta 盒子的结构: %w", err)
	}
	return c, nil
}

// findMP4Box 返回唯一一个 typ 盒子的下标，没有时返回 -1；有多个时报错，不猜用哪个。
func findMP4Box(boxes []mp4Box, typ string) (int, error) {
	idx := -1
	for i, b := range boxes {
		if b.typ == typ {
			if idx >= 0 {
				return 0, fmt.Errorf("MP4 里有多个 %q 盒子，暂不改写", typ)
			}
			idx = i
		}
	}
	return idx, nil
}

// mp4BoxHeader 按 like 的大小编码（32 位、64 位 largesize 或延伸到末尾）生成盒子头。
func mp4BoxHeader(typ string, payloadLen int, like mp4Box) ([]byte, error) {
	switch {
	case like.toEOF:
		return append([]byte{0, 0, 0, 0}, typ...), nil
	case like.large:
		h := binary.BigEndian.AppendUint32(nil, 1)
		h = append(h, typ...)
		return binary.BigEndian.AppendUint64(h, uint64(payloadLen)+16), nil
	}
	total := uint64(payloadLen) + 8
	if total > math.MaxUint32 {
		return nil, fmt.Errorf("MP4 盒子 %q 超过 4GB", typ)
	}
	return append(binary.BigEndian.AppendUint32(nil, uint32(total)), typ...), nil
}

func mp4NewBox(typ string, payload ...[]byte) ([]byte, error) {
	n := 0
	for _, p := range payload {
		n += len(p)
	}
	out, err := mp4BoxHeader(typ, n, mp4Box{})
	if err != nil {
		return nil, err
	}
	for _, p := range payload {
		out = append(out, p...)
	}
	return out, nil
}

// mp4TextItem 生成 iTunes 文本项：项盒子里一个 data 盒子，类型 1（UTF-8）、locale 0。
func mp4TextItem(typ, text string) ([]byte, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("MP4 标签 %q 的内容不是有效的 UTF-8", typ)
	}
	data, err := mp4NewBox("data", []byte{0, 0, 0, 1, 0, 0, 0, 0}, []byte(text))
	if err != nil {
		return nil, err
	}
	return mp4NewBox(typ, data)
}

func isMP4Padding(typ string) bool { return typ == "free" || typ == "skip" }

// spliceMP4Child 把容器 c 里下标 idx 的子盒子换成 child（idx < 0 表示追加在最后一个子盒子之后），
// 返回新的整个容器。紧跟其后的 free/skip 先吸收大小变化：放得下就改成剩余大小的填充，
// 刚好或不够就整个用掉，剩 1~7 字节（不够一个盒子头）时不动填充。
func spliceMP4Child(data []byte, c mp4Container, idx int, child []byte) ([]byte, error) {
	from, to := c.kidsEnd, c.kidsEnd
	if idx >= 0 {
		from, to = c.kids[idx].start, c.kids[idx].end
	}
	var pad []byte
	if idx >= 0 && idx+1 < len(c.kids) && isMP4Padding(c.kids[idx+1].typ) {
		f := c.kids[idx+1]
		size := (f.end - f.start) - (len(child) - (to - from))
		switch {
		case size <= 0:
			to = f.end
		case size >= f.hdr:
			h, err := mp4BoxHeader(f.typ, size-f.hdr, f)
			if err != nil {
				return nil, err
			}
			pad = append(h, make([]byte, size-f.hdr)...)
			to = f.end
		}
	}
	n := (from - c.box.payload()) + len(child) + len(pad) + (c.box.end - to)
	out, err := mp4BoxHeader(c.box.typ, n, c.box)
	if err != nil {
		return nil, err
	}
	out = append(out, data[c.box.payload():from]...)
	out = append(out, child...)
	out = append(out, pad...)
	return append(out, data[to:c.box.end]...), nil
}

// mp4ItunesHdlr 是 iTunes/ffmpeg 写在 udta/meta 里的 hdlr：handler mdir，厂商 appl，空名字。
var mp4ItunesHdlr = []byte{
	0, 0, 0, 33, 'h', 'd', 'l', 'r',
	0, 0, 0, 0, // version + flags
	0, 0, 0, 0, // pre_defined
	'm', 'd', 'i', 'r',
	'a', 'p', 'p', 'l',
	0, 0, 0, 0, 0, 0, 0, 0,
	0,
}

func editMP4Ilst(data []byte, edits []mp4IlstEdit) ([]byte, error) {
	top, _, err := parseMP4Boxes(data, 0, len(data), true)
	if err != nil {
		return nil, err
	}
	mi, err := findMP4Box(top, "moov")
	if err != nil {
		return nil, err
	}
	if mi < 0 {
		return nil, errors.New("MP4 文件没有 moov 盒子")
	}
	moov, err := openMP4Container(data, top[mi], 0)
	if err != nil {
		return nil, err
	}

	ui, err := findMP4Box(moov.kids, "udta")
	if err != nil {
		return nil, err
	}
	var udta, meta, ilst mp4Container
	mti, ili := -1, -1
	if ui >= 0 {
		if udta, err = openMP4Container(data, moov.kids[ui], 0); err != nil {
			return nil, err
		}
		if mti, err = findMP4Box(udta.kids, "meta"); err != nil {
			return nil, err
		}
	}
	if mti >= 0 {
		if meta, err = openMP4Meta(data, udta.kids[mti]); err != nil {
			return nil, err
		}
		hi, err := findMP4Box(meta.kids, "hdlr")
		if err != nil {
			return nil, err
		}
		if hi < 0 {
			return nil, errors.New("udta/meta 里没有 hdlr，暂不改写")
		}
		h := meta.kids[hi]
		if h.end-h.payload() < 12 {
			return nil, errors.New("udta/meta 的 hdlr 太短")
		}
		if handler := string(data[h.payload()+8 : h.payload()+12]); handler != "mdir" {
			return nil, fmt.Errorf("udta/meta 的 handler 是 %q，不是 iTunes 标签（mdir），暂不改写", handler)
		}
		if ili, err = findMP4Box(meta.kids, "ilst"); err != nil {
			return nil, err
		}
	}

	var items []byte
	if ili >= 0 {
		if ilst, err = openMP4Container(data, meta.kids[ili], 0); err != nil {
			return nil, err
		}
		items = rebuildMP4IlstItems(data, ilst.kids, edits)
		// 保留 QuickTime 结束符之类的尾随字节。
		items = append(items, data[ilst.kidsEnd:ilst.box.end]...)
	} else {
		items = rebuildMP4IlstItems(data, nil, edits)
	}

	var newIlst, newMeta, newUdta, newMoov []byte
	if ili >= 0 {
		h, err := mp4BoxHeader("ilst", len(items), ilst.box)
		if err != nil {
			return nil, err
		}
		newIlst = append(h, items...)
	} else if newIlst, err = mp4NewBox("ilst", items); err != nil {
		return nil, err
	}
	if mti >= 0 {
		newMeta, err = spliceMP4Child(data, meta, ili, newIlst)
	} else {
		newMeta, err = mp4NewBox("meta", []byte{0, 0, 0, 0}, mp4ItunesHdlr, newIlst)
	}
	if err != nil {
		return nil, err
	}
	if ui >= 0 {
		newUdta, err = spliceMP4Child(data, udta, mti, newMeta)
	} else {
		newUdta, err = mp4NewBox("udta", newMeta)
	}
	if err != nil {
		return nil, err
	}
	if newMoov, err = spliceMP4Child(data, moov, ui, newUdta); err != nil {
		return nil, err
	}

	old := moov.box
	out := make([]byte, 0, len(data)-(old.end-old.start)+len(newMoov))
	out = append(out, data[:old.start]...)
	out = append(out, newMoov...)
	out = append(out, data[old.end:]...)
	if delta := len(newMoov) - (old.end - old.start); delta != 0 {
		if err := shiftMP4Offsets(out, old, delta); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// rebuildMP4IlstItems 按原顺序拷贝 ilst 的项；要改的类型在第一次出现处换成新项、其余同名项丢掉，
// 原来没有的追加到末尾。
func rebuildMP4IlstItems(data []byte, kids []mp4Box, edits []mp4IlstEdit) []byte {
	done := make([]bool, len(edits))
	var out []byte
	for _, k := range kids {
		hit := -1
		for i, e := range edits {
			if e.typ == k.typ {
				hit = i
				break
			}
		}
		if hit < 0 {
			out = append(out, data[k.start:k.end]...)
			continue
		}
		if !done[hit] {
			done[hit] = true
			out = append(out, edits[hit].atom...)
		}
	}
	for i, e := range edits {
		if !done[i] {
			out = append(out, e.atom...)
		}
	}
	return out
}

// shiftMP4Offsets 在 moov（原位置 old）大小变化 delta 后，修正 out 里指向原 moov 之后的绝对偏移：
// 各 trak 的 stco/co64，以及分片 moof 里带 base-data-offset 的 tfhd。相对 moof 的偏移跟着 moof 一起移动，不用改。
// 指向原 moov 内部的偏移、mfra、moov 之前的 sidx、stbl 里的 saio 都无法确定怎么改，直接报错。
func shiftMP4Offsets(out []byte, old mp4Box, delta int) error {
	shift := func(v uint64, limit uint64) (uint64, error) {
		switch {
		case v >= uint64(old.end):
			if delta < 0 {
				return v - uint64(-delta), nil
			}
			if v > limit-uint64(delta) {
				return 0, errors.New("改写标签后 MP4 数据偏移溢出")
			}
			return v + uint64(delta), nil
		case v >= uint64(old.start):
			return 0, errors.New("MP4 数据偏移指向 moov 内部，暂不改写")
		}
		return v, nil
	}

	top, _, err := parseMP4Boxes(out, 0, len(out), true)
	if err != nil {
		return err
	}
	for _, b := range top {
		switch {
		case b.typ == "mfra":
			return errors.New("MP4 带 mfra 随机访问索引，标签变大小后无法安全改写")
		case b.typ == "sidx" && b.start < old.start:
			return errors.New("MP4 的 sidx 在 moov 之前，标签变大小后无法安全改写")
		}
	}
	mi, err := findMP4Box(top, "moov")
	if err != nil || mi < 0 {
		return errors.New("改写后找不到 moov")
	}
	moov, err := openMP4Container(out, top[mi], 0)
	if err != nil {
		return err
	}
	for _, trak := range moov.kids {
		if trak.typ != "trak" {
			continue
		}
		stbl, ok, err := openMP4Path(out, trak, "mdia", "minf", "stbl")
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		for _, b := range stbl.kids {
			width := 0
			switch b.typ {
			case "stco":
				width = 4
			case "co64":
				width = 8
			case "saio":
				return errors.New("MP4 的 stbl 带 saio（绝对偏移），标签变大小后无法安全改写")
			default:
				continue
			}
			p, n := b.payload(), b.end-b.payload()
			if n < 8 {
				return fmt.Errorf("MP4 %s 盒子太短", b.typ)
			}
			count := uint64(binary.BigEndian.Uint32(out[p+4:]))
			if count*uint64(width) > uint64(n-8) {
				return fmt.Errorf("MP4 %s 的条目数超出盒子范围", b.typ)
			}
			for i := 0; i < int(count); i++ {
				at := p + 8 + i*width
				if width == 4 {
					v, err := shift(uint64(binary.BigEndian.Uint32(out[at:])), math.MaxUint32)
					if err != nil {
						return err
					}
					binary.BigEndian.PutUint32(out[at:], uint32(v))
				} else {
					v, err := shift(binary.BigEndian.Uint64(out[at:]), math.MaxUint64)
					if err != nil {
						return err
					}
					binary.BigEndian.PutUint64(out[at:], v)
				}
			}
		}
	}
	for _, moof := range top {
		if moof.typ != "moof" {
			continue
		}
		mc, err := openMP4Container(out, moof, 0)
		if err != nil {
			return err
		}
		for _, traf := range mc.kids {
			if traf.typ != "traf" {
				continue
			}
			tc, err := openMP4Container(out, traf, 0)
			if err != nil {
				return err
			}
			ti, err := findMP4Box(tc.kids, "tfhd")
			if err != nil {
				return err
			}
			if ti < 0 {
				return errors.New("MP4 的 traf 缺少 tfhd")
			}
			t := tc.kids[ti]
			p := t.payload()
			if t.end-p < 8 {
				return errors.New("MP4 tfhd 太短")
			}
			if binary.BigEndian.Uint32(out[p:])&0x000001 == 0 {
				continue // 没有绝对 base-data-offset：偏移相对 moof，不受影响
			}
			if t.end-p < 16 {
				return errors.New("MP4 tfhd 太短")
			}
			v, err := shift(binary.BigEndian.Uint64(out[p+8:]), math.MaxUint64)
			if err != nil {
				return err
			}
			binary.BigEndian.PutUint64(out[p+8:], v)
		}
	}
	return nil
}

// openMP4Path 从 b 沿唯一子盒子路径往下走，任一层缺失时 ok 为 false。
func openMP4Path(data []byte, b mp4Box, path ...string) (mp4Container, bool, error) {
	c, err := openMP4Container(data, b, 0)
	if err != nil {
		return mp4Container{}, false, err
	}
	for _, typ := range path {
		i, err := findMP4Box(c.kids, typ)
		if err != nil {
			return mp4Container{}, false, err
		}
		if i < 0 {
			return mp4Container{}, false, nil
		}
		if c, err = openMP4Container(data, c.kids[i], 0); err != nil {
			return mp4Container{}, false, err
		}
	}
	return c, true, nil
}
