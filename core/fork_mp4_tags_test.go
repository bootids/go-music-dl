package core

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/dhowden/tag"
)

// 以下测试夹具都手工拼字节，不经被测代码，期望值也直接按字节构造。

func tu32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func tu64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

func tcat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func tbox(typ string, payload ...[]byte) []byte {
	p := tcat(payload...)
	return tcat(tu32(uint32(len(p)+8)), []byte(typ), p)
}

func tboxLarge(typ string, payload ...[]byte) []byte {
	p := tcat(payload...)
	return tcat(tu32(1), []byte(typ), tu64(uint64(len(p)+16)), p)
}

func titem(typ, text string) []byte {
	return tbox(typ, tbox("data", tu32(1), tu32(0), []byte(text)))
}

var (
	tItunesHdlr = tbox("hdlr", tu32(0), tu32(0), []byte("mdirappl"), make([]byte, 8), []byte{0})
	tFreeform   = tbox("----",
		tbox("mean", tu32(0), []byte("com.apple.iTunes")),
		tbox("name", tu32(0), []byte("replaygain_track_gain")),
		tbox("data", tu32(1), tu32(0), []byte("-6.50 dB")))
	tTrkn = tbox("trkn", tbox("data", tu32(0), tu32(0), []byte{0, 0, 0, 3, 0, 9, 0, 0}))
	tCovr = tbox("covr", tbox("data", tu32(13), tu32(0), []byte{0xff, 0xd8, 0xff, 0xd9}))
)

func tFullItems() [][]byte {
	return [][]byte{
		titem("\xa9nam", "歌"), titem("\xa9ART", "歌手"), titem("\xa9alb", "专辑"),
		titem("\xa9lyr", "[00:01.00]旧"), tTrkn, tCovr, titem("aART", "群星"), tFreeform,
	}
}

type tMP4Opts struct {
	moovLast bool
	co64     bool
	large    bool     // moov 和 mdat 用 64 位 largesize
	noUdta   bool     // 完全没有 udta
	udta     []byte   // 覆盖默认的 udta 盒子
	items    [][]byte // ilst 里的项
	ilstPad  int      // ilst 后 free 的总大小，0 表示没有
}

const tChunk = 100

// tBuildMP4 拼一个 ftyp + moov + mdat（或 mdat 在 moov 前）的文件，3 个 chunk 各 100 字节。
// 返回文件和 mdat 负载，chunk i 是负载的 [i*100, (i+1)*100)。
func tBuildMP4(o tMP4Opts) ([]byte, []byte) {
	ftyp := tbox("ftyp", []byte("M4A "), tu32(0), []byte("M4A isom"))
	audio := make([]byte, 3*tChunk)
	for i := range audio {
		audio[i] = byte(i*7 + 3)
	}
	wrap := tbox
	if o.large {
		wrap = tboxLarge
	}
	mdat := wrap("mdat", audio)
	mdatHdr := len(mdat) - len(audio)
	moovFor := func(offsets []uint64) []byte {
		var stco []byte
		if o.co64 {
			stco = tcat(tu32(0), tu32(uint32(len(offsets))))
			for _, v := range offsets {
				stco = append(stco, tu64(v)...)
			}
			stco = tbox("co64", stco)
		} else {
			stco = tcat(tu32(0), tu32(uint32(len(offsets))))
			for _, v := range offsets {
				stco = append(stco, tu32(uint32(v))...)
			}
			stco = tbox("stco", stco)
		}
		stbl := tbox("stbl", tbox("stsd", tu32(0), tu32(0)), tbox("stts", tu32(0), tu32(1), tu32(3), tu32(1024)),
			tbox("stsc", tu32(0), tu32(0)), tbox("stsz", tu32(0), tu32(0), tu32(0)), stco)
		mdia := tbox("mdia", tbox("mdhd", make([]byte, 24)),
			tbox("hdlr", tu32(0), tu32(0), []byte("soun"), make([]byte, 12), []byte("SoundHandler\x00")),
			tbox("minf", tbox("smhd", make([]byte, 8)), stbl))
		kids := [][]byte{tbox("mvhd", make([]byte, 100)), tbox("trak", tbox("tkhd", make([]byte, 84)), mdia)}
		switch {
		case o.udta != nil:
			kids = append(kids, o.udta)
		case !o.noUdta:
			metaKids := [][]byte{tu32(0), tItunesHdlr, tbox("ilst", o.items...)}
			if o.ilstPad > 0 {
				metaKids = append(metaKids, tbox("free", make([]byte, o.ilstPad-8)))
			}
			kids = append(kids, tbox("udta", tbox("meta", metaKids...)))
		}
		return wrap("moov", kids...)
	}
	offsets := func(base int) []uint64 {
		return []uint64{uint64(base), uint64(base + tChunk), uint64(base + 2*tChunk)}
	}
	if o.moovLast {
		return tcat(ftyp, mdat, moovFor(offsets(len(ftyp)+mdatHdr))), audio
	}
	moovLen := len(moovFor(offsets(0)))
	return tcat(ftyp, moovFor(offsets(len(ftyp)+moovLen+mdatHdr)), mdat), audio
}

const tFtypLen = 24

// tChunkOffsets 直接按字节找夹具里唯一的 stco/co64 读出偏移；不是 largesize 的文件再让 mp4ff 解析一遍
// （mp4ff 不支持 moov 用 64 位大小）。
func tChunkOffsets(t *testing.T, data []byte) []uint64 {
	t.Helper()
	if !bytes.Equal(data[tFtypLen:tFtypLen+4], tu32(1)) {
		f, err := mp4.DecodeFile(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("mp4ff decode: %v", err)
		}
		if f.Moov == nil || f.Moov.Trak == nil {
			t.Fatal("mp4ff: no moov/trak")
		}
	}
	width, at := 4, bytes.Index(data, []byte("stco"))
	if at < 0 {
		width, at = 8, bytes.Index(data, []byte("co64"))
	}
	p := at + 4
	n := int(binary.BigEndian.Uint32(data[p+4:]))
	var out []uint64
	for i := 0; i < n; i++ {
		if width == 4 {
			out = append(out, uint64(binary.BigEndian.Uint32(data[p+8+i*4:])))
		} else {
			out = append(out, binary.BigEndian.Uint64(data[p+8+i*8:]))
		}
	}
	return out
}

func tCheckChunks(t *testing.T, data, audio []byte) []uint64 {
	t.Helper()
	offs := tChunkOffsets(t, data)
	for i, off := range offs {
		if off+tChunk > uint64(len(data)) || !bytes.Equal(data[off:off+tChunk], audio[i*tChunk:(i+1)*tChunk]) {
			t.Fatalf("chunk %d at %d no longer points at its audio bytes", i, off)
		}
	}
	return offs
}

func tStr(s string) *string { return &s }

func tReplaceItem(items [][]byte, idx int, item []byte) [][]byte {
	out := append([][]byte(nil), items...)
	out[idx] = item
	return out
}

func TestEditMP4TagsLyricShiftsChunkOffsets(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    tMP4Opts
	}{
		{"stco", tMP4Opts{}},
		{"co64", tMP4Opts{co64: true}},
		{"largesize", tMP4Opts{large: true}},
		{"largesize-co64", tMP4Opts{large: true, co64: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.o.items = tFullItems()
			in, audio := tBuildMP4(tc.o)
			before := tCheckChunks(t, in, audio)
			orig := append([]byte(nil), in...)
			lyric := "[00:01.00]<00:01.00>新<00:01.50>歌<00:02.00>词更长一些"
			out, err := EditMP4Tags(in, MP4TagEdit{Lyric: &lyric})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(in, orig) {
				t.Fatal("input slice modified")
			}
			want := tbox("ilst", tReplaceItem(tFullItems(), 3, titem("\xa9lyr", lyric))...)
			if !bytes.Contains(out, want) {
				t.Fatal("ilst items not byte-identical / in order")
			}
			delta := uint64(len(out) - len(in))
			if delta == 0 {
				t.Fatal("want moov to grow")
			}
			after := tCheckChunks(t, out, audio)
			for i := range before {
				if after[i] != before[i]+delta {
					t.Fatalf("chunk %d offset %d -> %d, want +%d", i, before[i], after[i], delta)
				}
			}
			mdatLen := len(audio) + 8
			if tc.o.large {
				mdatLen += 8
				if !bytes.Equal(out[tFtypLen:tFtypLen+4], tu32(1)) || string(out[tFtypLen+4:tFtypLen+8]) != "moov" {
					t.Fatal("moov lost its largesize header")
				}
				if binary.BigEndian.Uint64(out[tFtypLen+8:]) != uint64(len(out)-tFtypLen-mdatLen) {
					t.Fatal("moov largesize not updated")
				}
			}
			if !bytes.Equal(out[:tFtypLen], in[:tFtypLen]) || !bytes.Equal(out[len(out)-mdatLen:], in[len(in)-mdatLen:]) {
				t.Fatal("ftyp or mdat changed")
			}
		})
	}
}

func TestEditMP4TagsUsesPadding(t *testing.T) {
	lyric := strings.Repeat("长", 20) // 比原歌词多 60 字节左右
	grow := len(titem("\xa9lyr", lyric)) - len(titem("\xa9lyr", "[00:01.00]旧"))
	for _, tc := range []struct {
		name     string
		pad      int
		sameSize bool
	}{
		{"enough", 200, true},
		{"exact", grow, true},
		{"remainder-too-small", grow + 4, false}, // 剩 4 字节放不下 free 头，退回改大小
		{"partial", grow - 10, false},            // 填充不够：吃掉全部，余下的照常变大
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, audio := tBuildMP4(tMP4Opts{items: tFullItems(), ilstPad: tc.pad})
			before := tCheckChunks(t, in, audio)
			out, err := EditMP4Tags(in, MP4TagEdit{Lyric: &lyric})
			if err != nil {
				t.Fatal(err)
			}
			after := tCheckChunks(t, out, audio)
			want := tbox("ilst", tReplaceItem(tFullItems(), 3, titem("\xa9lyr", lyric))...)
			if !bytes.Contains(out, want) {
				t.Fatal("ilst not as expected")
			}
			if tc.sameSize {
				if len(out) != len(in) || after[0] != before[0] {
					t.Fatalf("len %d -> %d, offset %d -> %d; want unchanged", len(in), len(out), before[0], after[0])
				}
				if tc.pad > grow && !bytes.Contains(out, tbox("free", make([]byte, tc.pad-grow-8))) {
					t.Fatal("remaining padding missing")
				}
				return
			}
			wantGrow := grow
			if tc.name == "partial" {
				wantGrow = grow - tc.pad
			}
			if len(out)-len(in) != wantGrow || after[0] != before[0]+uint64(wantGrow) {
				t.Fatalf("grew %d, offset %d -> %d; want +%d", len(out)-len(in), before[0], after[0], wantGrow)
			}
		})
	}

	// udta 后面（moov 里）的 free 同样吸收变化。
	udta := tbox("udta", tbox("meta", tu32(0), tItunesHdlr, tbox("ilst", tFullItems()...)))
	in, audio := tBuildMP4(tMP4Opts{udta: tcat(udta, tbox("free", make([]byte, 192)))})
	out, err := EditMP4Tags(in, MP4TagEdit{Lyric: &lyric})
	if err != nil {
		t.Fatal(err)
	}
	if tCheckChunks(t, out, audio); len(out) != len(in) || !bytes.Contains(out, tbox("free", make([]byte, 192-grow))) {
		t.Fatal("padding after udta not used")
	}

	// 变短时填充变大，文件大小不变。
	in, audio = tBuildMP4(tMP4Opts{items: tFullItems(), ilstPad: 16})
	out, err = EditMP4Tags(in, MP4TagEdit{Lyric: tStr("短")})
	if err != nil {
		t.Fatal(err)
	}
	tCheckChunks(t, out, audio)
	if len(out) != len(in) {
		t.Fatalf("shrink changed length %d -> %d", len(in), len(out))
	}
}

func TestEditMP4TagsMoovAfterMdat(t *testing.T) {
	in, audio := tBuildMP4(tMP4Opts{moovLast: true, items: tFullItems()})
	before := tCheckChunks(t, in, audio)
	out, err := EditMP4Tags(in, MP4TagEdit{Lyric: tStr(strings.Repeat("新", 50))})
	if err != nil {
		t.Fatal(err)
	}
	after := tCheckChunks(t, out, audio)
	if len(out) == len(in) || after[0] != before[0] {
		t.Fatalf("offsets changed: %v -> %v", before, after)
	}
	n := tFtypLen + 8 + len(audio)
	if !bytes.Equal(out[:n], in[:n]) {
		t.Fatal("ftyp/mdat changed")
	}
}

func TestEditMP4TagsCreatesMissingStructure(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    tMP4Opts
	}{
		{"no-udta", tMP4Opts{noUdta: true}},
		{"udta-without-meta", tMP4Opts{udta: tbox("udta", tbox("XTRA", []byte("other-udta")))}},
		{"meta-without-ilst", tMP4Opts{udta: tbox("udta", tbox("meta", tu32(0), tItunesHdlr))}},
		// QuickTime 风格 meta：没有 version/flags。
		{"quicktime-meta", tMP4Opts{udta: tbox("udta", tbox("meta", tItunesHdlr, tbox("ilst", titem("\xa9nam", "歌"))))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, audio := tBuildMP4(tc.o)
			out, err := EditMP4Tags(in, MP4TagEdit{Lyric: tStr("词"), Album: tStr("专辑"), Artist: tStr("歌手")})
			if err != nil {
				t.Fatal(err)
			}
			tCheckChunks(t, out, audio)
			items := tcat(titem("\xa9ART", "歌手"), titem("\xa9alb", "专辑"), titem("\xa9lyr", "词"))
			switch tc.name {
			case "quicktime-meta":
				want := tbox("meta", tItunesHdlr, tbox("ilst", titem("\xa9nam", "歌"), items))
				if !bytes.Contains(out, want) {
					t.Fatal("QuickTime meta not edited in place")
				}
				return // dhowden/tag 只认 FullBox 写法的 meta
			case "udta-without-meta":
				if !bytes.Contains(out, tbox("udta", tbox("XTRA", []byte("other-udta")), tbox("meta", tu32(0), tItunesHdlr, tbox("ilst", items)))) {
					t.Fatal("meta not appended to udta")
				}
			default:
				if !bytes.Contains(out, tbox("meta", tu32(0), tItunesHdlr, tbox("ilst", items))) {
					t.Fatal("created structure not as expected")
				}
			}
			m, err := tag.ReadFrom(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("tag read: %v", err)
			}
			if m.Lyrics() != "词" || m.Album() != "专辑" || m.Artist() != "歌手" {
				t.Fatalf("got lyric=%q album=%q artist=%q", m.Lyrics(), m.Album(), m.Artist())
			}
		})
	}
}

func TestEditMP4TagsAlbumArtistRemoveLyricAndCollapse(t *testing.T) {
	items := [][]byte{
		titem("\xa9nam", "歌"), titem("\xa9lyr", "一"), tFreeform, titem("\xa9ART", "旧歌手"),
		titem("\xa9lyr", "二"), titem("\xa9alb", "旧专辑"), tbox("\xa9alb", tbox("data", tu32(1), tu32(0), []byte("x"))),
	}
	in, audio := tBuildMP4(tMP4Opts{items: items})
	out, err := EditMP4Tags(in, MP4TagEdit{Lyric: tStr(""), Album: tStr("新专辑"), Artist: tStr("新歌手")})
	if err != nil {
		t.Fatal(err)
	}
	tCheckChunks(t, out, audio)
	want := tbox("ilst", titem("\xa9nam", "歌"), tFreeform, titem("\xa9ART", "新歌手"), titem("\xa9alb", "新专辑"))
	if !bytes.Contains(out, want) {
		t.Fatal("ilst not as expected")
	}

	// 一项里有多个 data 的 ©lyr，以及重复的 ©lyr，都合成一项放在第一次出现处。
	multi := tbox("\xa9lyr", tbox("data", tu32(1), tu32(0), []byte("a")), tbox("data", tu32(1), tu32(0), []byte("b")))
	in, _ = tBuildMP4(tMP4Opts{items: [][]byte{multi, titem("\xa9nam", "歌"), titem("\xa9lyr", "c")}})
	out, err = EditMP4Tags(in, MP4TagEdit{Lyric: tStr("新")})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, tbox("ilst", titem("\xa9lyr", "新"), titem("\xa9nam", "歌"))) {
		t.Fatal("duplicates not collapsed")
	}
}

func TestEditMP4TagsRejectsNonItunesMeta(t *testing.T) {
	mdta := tbox("hdlr", tu32(0), tu32(0), []byte("mdta"), make([]byte, 12), []byte{0})
	for name, udta := range map[string][]byte{
		"mdta":    tbox("udta", tbox("meta", tu32(0), mdta, tbox("keys", tu32(0), tu32(0)), tbox("ilst"))),
		"no-hdlr": tbox("udta", tbox("meta", tu32(0), tbox("ilst"))),
		"two-meta": tbox("udta", tbox("meta", tu32(0), tItunesHdlr, tbox("ilst")),
			tbox("meta", tu32(0), tItunesHdlr, tbox("ilst"))),
	} {
		in, _ := tBuildMP4(tMP4Opts{udta: udta})
		if _, err := EditMP4Tags(in, MP4TagEdit{Lyric: tStr("x")}); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// tBuildFragmented 拼 ftyp + moov(mvex) + moof + mdat，tfhd 用给定 flags；
// 带 base-data-offset 时指向 mdat 负载开头。
func tBuildFragmented(tfhdFlags uint32, tail ...[]byte) ([]byte, []byte) {
	ftyp := tbox("ftyp", []byte("iso6"), tu32(0), []byte("iso6"))
	moov := tbox("moov", tbox("mvhd", make([]byte, 100)),
		tbox("trak", tbox("tkhd", make([]byte, 84))),
		tbox("mvex", tbox("trex", tu32(0), tu32(1), tu32(1), tu32(0), tu32(0), tu32(0))),
		tbox("udta", tbox("meta", tu32(0), tItunesHdlr, tbox("ilst", tFreeform))))
	audio := []byte("fragment-audio-bytes")
	moofFor := func(base uint64) []byte {
		tfhd := tcat(tu32(tfhdFlags), tu32(1))
		if tfhdFlags&1 != 0 {
			tfhd = append(tfhd, tu64(base)...)
		}
		return tbox("moof", tbox("mfhd", tu32(0), tu32(1)), tbox("traf", tbox("tfhd", tfhd),
			tbox("trun", tu32(0x000201), tu32(1), tu32(0), tu32(uint32(len(audio))))))
	}
	moofLen := len(moofFor(0))
	base := uint64(len(ftyp) + len(moov) + moofLen + 8)
	return tcat(ftyp, moov, moofFor(base), tbox("mdat", audio), tcat(tail...)), audio
}

func TestEditMP4TagsFragmented(t *testing.T) {
	lyric := strings.Repeat("词", 30)
	tfhdBase := func(data []byte) uint64 {
		i := bytes.Index(data, []byte("tfhd"))
		return binary.BigEndian.Uint64(data[i+12:])
	}

	// 带绝对 base-data-offset：平移 delta，仍指向同一段音频。
	in, audio := tBuildFragmented(0x000001)
	out, err := EditMP4Tags(in, MP4TagEdit{Lyric: &lyric})
	if err != nil {
		t.Fatal(err)
	}
	delta := uint64(len(out) - len(in))
	if got := tfhdBase(out); got != tfhdBase(in)+delta || !bytes.Equal(out[got:got+uint64(len(audio))], audio) {
		t.Fatalf("tfhd base %d -> %d, delta %d", tfhdBase(in), got, delta)
	}
	if _, err := mp4.DecodeFile(bytes.NewReader(out)); err != nil {
		t.Fatalf("mp4ff decode: %v", err)
	}
	if !bytes.Contains(out, tFreeform) {
		t.Fatal("freeform lost")
	}

	// default-base-is-moof：相对偏移，moof 原样保留。
	in, _ = tBuildFragmented(0x020000)
	out, err = EditMP4Tags(in, MP4TagEdit{Lyric: &lyric})
	if err != nil {
		t.Fatal(err)
	}
	moofAt := bytes.Index(in, []byte("moof")) - 4
	if !bytes.HasSuffix(out, in[moofAt:]) {
		t.Fatal("moof/mdat changed")
	}

	// mfra 里的 tfra 存 moof 的绝对偏移，拒绝；sidx 在 moov 之前也拒绝。输入不被修改。
	mfra := tbox("mfra", tbox("tfra", tu32(0), tu32(1), tu32(0), tu32(0)), tbox("mfro", tu32(0), tu32(32)))
	in, _ = tBuildFragmented(0x020000, mfra)
	orig := append([]byte(nil), in...)
	if _, err := EditMP4Tags(in, MP4TagEdit{Lyric: &lyric}); err == nil || !bytes.Equal(in, orig) {
		t.Fatalf("mfra: want error and untouched input, err=%v", err)
	}
	path := filepath.Join(t.TempDir(), "frag.m4a")
	if err := os.WriteFile(path, in, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceLocalSongLyric(path, lyric, ""); err == nil {
		t.Fatal("ReplaceLocalSongLyric: want error for mfra file")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, orig) {
		t.Fatal("file changed on rejection")
	}
	in, _ = tBuildFragmented(0x020000)
	sidxFirst := tcat(in[:24], tbox("sidx", make([]byte, 24)), in[24:])
	if _, err := EditMP4Tags(sidxFirst, MP4TagEdit{Lyric: &lyric}); err == nil {
		t.Fatal("sidx before moov: want error")
	}
	// moov 大小不变时不需要任何偏移修正，有 mfra 也照常改写。
	in, _ = tBuildFragmented(0x000001, mfra)
	if out, err := EditMP4Tags(in, MP4TagEdit{Lyric: tStr("")}); err != nil || !bytes.Equal(out, in) {
		t.Fatalf("same-size edit with mfra: err=%v changed=%v", err, !bytes.Equal(out, in))
	}
}

func TestEditMP4TagsRejectsMalformed(t *testing.T) {
	good, _ := tBuildMP4(tMP4Opts{items: tFullItems()})
	moovAt := tFtypLen
	ilstAt := bytes.Index(good, []byte("ilst")) - 4
	set32 := func(at int, v uint32) []byte {
		b := append([]byte(nil), good...)
		binary.BigEndian.PutUint32(b[at:], v)
		return b
	}
	cases := map[string][]byte{
		"empty":                   {},
		"truncated-header":        good[:moovAt+5],
		"truncated-moov":          good[:moovAt+200],
		"moov-larger-than-file":   set32(moovAt, uint32(len(good))),
		"ilst-larger-than-meta":   set32(ilstAt, 0x7fffffff),
		"zero-size-inner":         set32(ilstAt+8, 0),
		"inner-size-below-header": set32(ilstAt+8, 4),
		"truncated-largesize":     tcat(tu32(1), []byte("moov"), tu32(0)),
		"largesize-too-big":       tcat(tu32(1), []byte("moov"), tu64(1<<62)),
		"no-moov":                 tbox("ftyp", []byte("M4A ")),
		"two-moov":                tcat(tbox("moov"), tbox("moov")),
		"stco-count-too-big":      nil,
	}
	stcoAt := bytes.Index(good, []byte("stco")) - 4
	cases["stco-count-too-big"] = set32(stcoAt+12, 1000)
	for name, data := range cases {
		lyric := strings.Repeat("x", 40)
		_, err := EditMP4Tags(data, MP4TagEdit{Lyric: &lyric})
		if err == nil {
			t.Errorf("%s: want error", name)
		}
		t.Logf("%s: %v", name, err)
	}
}

func FuzzEditMP4Tags(f *testing.F) {
	for _, o := range []tMP4Opts{
		{items: tFullItems()}, {co64: true, items: tFullItems()}, {large: true, items: tFullItems()},
		{moovLast: true, items: tFullItems()}, {noUdta: true}, {items: tFullItems(), ilstPad: 64},
		{udta: tbox("udta", tbox("meta", tItunesHdlr, tbox("ilst", titem("\xa9nam", "歌"))))},
	} {
		in, _ := tBuildMP4(o)
		f.Add(in)
	}
	frag, _ := tBuildFragmented(0x000001)
	f.Add(frag)
	f.Fuzz(func(t *testing.T, data []byte) {
		edit := MP4TagEdit{Lyric: tStr("新歌词"), Album: tStr(""), Artist: tStr("歌手")}
		out, err := EditMP4Tags(data, edit)
		if err != nil {
			return
		}
		// 成功的输出必须仍能被自己解析，且再改一次同样的值不再变化。
		again, err := EditMP4Tags(out, edit)
		if err != nil {
			t.Fatalf("re-edit failed: %v", err)
		}
		if !bytes.Equal(again, out) {
			t.Fatal("edit not idempotent")
		}
	})
}

// 真实 ffmpeg 生成的 AAC m4a（moov 在前、在后两种），改完后 ffprobe 读到新值、自定义项还在、能完整解码。
func TestEditMP4TagsFFmpegRoundTrip(t *testing.T) {
	ffmpeg, err1 := exec.LookPath("ffmpeg")
	ffprobe, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg/ffprobe not available")
	}
	probe := func(t *testing.T, path string) map[string]string {
		t.Helper()
		out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "format_tags", "-of", "json", path).Output()
		if err != nil {
			t.Fatalf("ffprobe: %v", err)
		}
		var res struct {
			Format struct {
				Tags map[string]string `json:"tags"`
			} `json:"format"`
		}
		if err := json.Unmarshal(out, &res); err != nil {
			t.Fatal(err)
		}
		return res.Format.Tags
	}
	for _, faststart := range []bool{true, false} {
		name := "moov-last"
		if faststart {
			name = "faststart"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a.m4a")
			args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "sine=d=3", "-c:a", "aac",
				"-metadata", "title=歌", "-metadata", "artist=旧歌手", "-metadata", "album=旧专辑",
				"-metadata", "lyrics=[00:01.00]旧", "-metadata", "replaygain_track_gain=-6.5"}
			if faststart {
				args = append(args, "-movflags", "+faststart")
			}
			if out, err := exec.Command(ffmpeg, append(args, path)...).CombinedOutput(); err != nil {
				t.Fatalf("ffmpeg: %v\n%s", err, out)
			}
			// ffmpeg 的 mp4 封装不写 ---- 自定义项（-metadata 的 replaygain 被丢掉），这里补一个进去。
			data, _ := os.ReadFile(path)
			data, err := editMP4Ilst(data, []mp4IlstEdit{{typ: "----", atom: tFreeform}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if tags := probe(t, path); tags["replaygain_track_gain"] != "-6.50 dB" {
				t.Fatalf("fixture freeform not readable: %v", tags)
			}

			lyric := "[00:01.00]<00:01.00>新<00:02.00>" + strings.Repeat("长", 40)
			out, err := EditMP4Tags(data, MP4TagEdit{Lyric: &lyric, Album: tStr("新专辑"), Artist: tStr("新歌手")})
			if err != nil {
				t.Fatal(err)
			}
			outPath := filepath.Join(dir, "out.m4a")
			if err := os.WriteFile(outPath, out, 0o644); err != nil {
				t.Fatal(err)
			}
			tags := probe(t, outPath)
			if tags["lyrics"] != lyric || tags["album"] != "新专辑" || tags["artist"] != "新歌手" ||
				tags["title"] != "歌" || tags["replaygain_track_gain"] != "-6.50 dB" {
				t.Fatalf("tags = %v", tags)
			}
			if out, err := exec.Command(ffmpeg, "-v", "error", "-i", outPath, "-f", "null", "-").CombinedOutput(); err != nil || len(out) != 0 {
				t.Fatalf("decode: %v\n%s", err, out)
			}
			if _, err := mp4.DecodeFile(bytes.NewReader(out)); err != nil {
				t.Fatalf("mp4ff decode: %v", err)
			}
		})
	}
}
