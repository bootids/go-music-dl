package applemusic

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"github.com/Eyevinn/mp4ff/aac"
	"github.com/Eyevinn/mp4ff/mp4"

	"github.com/guohuiyuan/go-music-dl/internal/applemusic/temari"
)

// The temari decryptor must plug into DecryptStream as a SampleDecrypter.
var _ SampleDecrypter = (*temari.Template)(nil)

// cbcDecrypter is a stand-in for *temari.Template: AES-128-CBC from the
// constant IV, with the chain reset on every DecryptInto call.
type cbcDecrypter struct {
	block cipher.Block
	iv    []byte
}

func (d cbcDecrypter) DecryptInto(dst, src []byte) {
	cipher.NewCBCDecrypter(d.block, d.iv).CryptBlocks(dst, src)
}

type fixture struct {
	file  []byte
	plain [][][]byte // [fragment][sample]data
	segs  []Segment
	keys  map[string][]byte
	iv    []byte
}

// buildFixture builds an encrypted single-track AAC fMP4 in memory:
// 3 fragments of 5 samples; fragments 0-1 use skd://a with full-sample cbcs
// (mp4ff EncryptFragment, no senc), fragment 2 uses skd://b with subsamples
// and a senc box. skip != 0 sets a 1:skip pattern in tenc.
func buildFixture(t testing.TB, skip byte) *fixture {
	return buildFixtureOpts(t, skip, false)
}

// buildFixtureOpts with dual set mimics Apple's two-entry stsd: key a is the
// prefetch key, the init carries two identical sample entries and every tfhd
// names its entry (1 for fragments 0-1, 2 for fragment 2).
func buildFixtureOpts(t testing.TB, skip byte, dual bool) *fixture {
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	keyA := "skd://a"
	if dual {
		keyA = PrefetchKeyURI
	}
	fx := &fixture{
		keys: map[string][]byte{keyA: randBytes(rng, 16), "skd://b": randBytes(rng, 16)},
		iv:   randBytes(rng, 16),
		segs: []Segment{{KeyURI: keyA}, {KeyURI: keyA}, {KeyURI: "skd://b"}},
	}
	kid, _ := mp4.NewUUIDFromString("00112233-4455-6677-8899-aabbccddeeff")

	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(44100, "audio", "und")
	if err := trak.SetAACDescriptor(aac.AAClc, 44100); err != nil {
		t.Fatal(err)
	}
	trackID := trak.Tkhd.TrackID
	ipd, err := mp4.InitProtect(init, fx.keys[keyA], fx.iv, "cbcs", kid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if skip != 0 {
		ipd.Tenc.DefaultCryptByteBlock = 1
		ipd.Tenc.DefaultSkipByteBlock = skip
	}
	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if dual {
		dup, err := mp4.DecodeFile(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		stsd := init.Moov.Trak.Mdia.Minf.Stbl.Stsd
		stsd.AddChild(dup.Init.Moov.Trak.Mdia.Minf.Stbl.Stsd.Children[0])
		buf.Reset()
		if err := init.Encode(&buf); err != nil {
			t.Fatal(err)
		}
	}

	var decodeTime uint64
	for f := 0; f < 3; f++ {
		frag, err := mp4.CreateFragment(uint32(f+1), trackID)
		if err != nil {
			t.Fatal(err)
		}
		var plains [][]byte
		for s := 0; s < 5; s++ {
			size := 70 + 16*s + 3*f + 5 // never a multiple of 16
			data := randBytes(rng, size)
			plains = append(plains, append([]byte(nil), data...))
			frag.AddFullSample(mp4.FullSample{
				Sample:     mp4.Sample{Flags: mp4.SyncSampleFlags, Dur: 1024, Size: uint32(size)},
				DecodeTime: decodeTime,
				Data:       data,
			})
			decodeTime += 1024
		}
		fx.plain = append(fx.plain, plains)
		if dual {
			tfhd := frag.Moof.Traf.Tfhd
			tfhd.Flags |= mp4.TfhdSampleDescriptionIndexPresentFlag
			tfhd.SampleDescriptionIndex = 1
			if f == 2 {
				tfhd.SampleDescriptionIndex = 2
			}
		}

		key := fx.keys[fx.segs[f].KeyURI]
		if f < 2 {
			if _, err := mp4.EncryptFragment(frag, key, fx.iv, ipd); err != nil {
				t.Fatal(err)
			}
		} else {
			encryptWithSubsamples(t, frag, ipd, key, fx.iv)
		}
		if err := frag.Encode(&buf); err != nil {
			t.Fatal(err)
		}
	}
	fx.file = buf.Bytes()
	return fx
}

func encryptWithSubsamples(t testing.TB, frag *mp4.Fragment, ipd *mp4.InitProtectData, key, iv []byte) {
	t.Helper()
	samples, err := frag.GetFullSamples(ipd.Trex)
	if err != nil {
		t.Fatal(err)
	}
	senc := mp4.NewSencBox(0, len(samples))
	saiz := mp4.NewSaizBox(len(samples))
	for _, s := range samples {
		// clear 5, protected 37, clear 2, protected rest
		rest := uint32(len(s.Data) - 5 - 37 - 2)
		subs := []mp4.SubSamplePattern{{BytesOfClearData: 5, BytesOfProtectedData: 37}, {BytesOfClearData: 2, BytesOfProtectedData: rest}}
		if err := mp4.EncryptSampleCbcs(s.Data, key, iv, subs, ipd.Tenc); err != nil {
			t.Fatal(err)
		}
		if err := senc.AddSample(mp4.SencSample{SubSamples: subs}); err != nil {
			t.Fatal(err)
		}
		if err := saiz.AddSampleInfo(nil, subs); err != nil {
			t.Fatal(err)
		}
	}
	traf := frag.Moof.Traf
	if err := traf.AddChild(saiz); err != nil {
		t.Fatal(err)
	}
	if err := traf.AddChild(senc); err != nil {
		t.Fatal(err)
	}
}

func randBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}

type countingKeys struct {
	mu    sync.Mutex
	calls map[string]int
	fx    *fixture
}

func (c *countingKeys) source(_ context.Context, uri string) (SampleDecrypter, error) {
	c.mu.Lock()
	c.calls[uri]++
	c.mu.Unlock()
	key, ok := c.fx.keys[uri]
	if !ok {
		return nil, errors.New("unknown key " + uri)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cbcDecrypter{block: block, iv: c.fx.iv}, nil
}

func TestDecryptStream(t *testing.T) {
	fx := buildFixture(t, 0)
	in, err := mp4.DecodeFile(bytes.NewReader(fx.file))
	if err != nil {
		t.Fatal(err)
	}
	inTypes := map[string]bool{}
	collectTypes(in.Init.Children, inTypes)
	collectTypes(in.Segments[0].Fragments[2].Children, inTypes)
	if !inTypes["enca"] || !inTypes["sinf"] || !inTypes["senc"] {
		t.Fatalf("fixture lacks encryption boxes: %v", inTypes)
	}
	for i := range fx.plain {
		if bytes.Contains(fx.file, fx.plain[i][0][:32]) {
			t.Fatalf("fixture fragment %d is not encrypted", i)
		}
	}

	ks := &countingKeys{calls: map[string]int{}, fx: fx}
	var out bytes.Buffer
	if err := DecryptStream(context.Background(), bytes.NewReader(fx.file), &out, fx.segs, ks.source); err != nil {
		t.Fatal(err)
	}

	if ks.calls["skd://a"] != 1 || ks.calls["skd://b"] != 1 || len(ks.calls) != 2 {
		t.Fatalf("key source calls = %v, want one per URI", ks.calls)
	}

	f, err := mp4.DecodeFile(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if f.Init == nil {
		t.Fatal("no init segment in output")
	}
	stsd := f.Init.Moov.Trak.Mdia.Minf.Stbl.Stsd
	if len(stsd.Children) != 1 || stsd.Children[0].Type() != "mp4a" {
		t.Fatalf("stsd entries = %v, want one mp4a", boxTypes(stsd.Children))
	}
	forbidden := []string{"enca", "sinf", "senc", "saiz", "saio", "pssh"}
	check := func(where string, bs []mp4.Box) {
		types := map[string]bool{}
		collectTypes(bs, types)
		for _, ft := range forbidden {
			if types[ft] {
				t.Errorf("%s contains %s box", where, ft)
			}
		}
	}
	check("init", f.Init.Children)

	var frags []*mp4.Fragment
	for _, seg := range f.Segments {
		frags = append(frags, seg.Fragments...)
	}
	if len(frags) != len(fx.plain) {
		t.Fatalf("got %d fragments, want %d", len(frags), len(fx.plain))
	}
	trex := f.Init.Moov.Mvex.Trex
	for i, frag := range frags {
		check(fmt.Sprintf("fragment %d", i), frag.Children)
		if seq := frag.Moof.Mfhd.SequenceNumber; seq != uint32(i+1) {
			t.Errorf("fragment %d has sequence number %d", i, seq)
		}
		samples, err := frag.GetFullSamples(trex)
		if err != nil {
			t.Fatal(err)
		}
		if len(samples) != len(fx.plain[i]) {
			t.Fatalf("fragment %d: %d samples, want %d", i, len(samples), len(fx.plain[i]))
		}
		for j, s := range samples {
			if !bytes.Equal(s.Data, fx.plain[i][j]) {
				t.Errorf("fragment %d sample %d differs from plaintext", i, j)
			}
		}
	}
}

func TestDecryptStreamUnsupportedPattern(t *testing.T) {
	fx := buildFixture(t, 9)
	ks := &countingKeys{calls: map[string]int{}, fx: fx}
	err := DecryptStream(context.Background(), bytes.NewReader(fx.file), &bytes.Buffer{}, fx.segs, ks.source)
	if !errors.Is(err, ErrUnsupportedPattern) {
		t.Fatalf("got %v, want ErrUnsupportedPattern", err)
	}
}

// The decrypter only knows the constant IV, so per-sample IVs must be rejected
// instead of producing noise.
func TestDecryptStreamPerSampleIVUnsupported(t *testing.T) {
	fx := buildFixture(t, 0)
	f, err := mp4.DecodeFile(bytes.NewReader(fx.file))
	if err != nil {
		t.Fatal(err)
	}
	stsd := f.Init.Moov.Trak.Mdia.Minf.Stbl.Stsd
	tenc := stsd.Children[0].(*mp4.AudioSampleEntryBox).Sinf.Schi.Tenc
	tenc.DefaultPerSampleIVSize = 16
	tenc.DefaultConstantIV = nil
	var buf bytes.Buffer
	if err := f.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	ks := &countingKeys{calls: map[string]int{}, fx: fx}
	err = DecryptStream(context.Background(), bytes.NewReader(buf.Bytes()), &bytes.Buffer{}, fx.segs, ks.source)
	if !errors.Is(err, ErrUnsupportedScheme) || !strings.Contains(err.Error(), "per-sample IV") {
		t.Fatalf("got %v, want ErrUnsupportedScheme for per-sample IV", err)
	}
}

func TestDecryptStreamSegmentMismatch(t *testing.T) {
	fx := buildFixture(t, 0)
	ks := &countingKeys{calls: map[string]int{}, fx: fx}
	err := DecryptStream(context.Background(), bytes.NewReader(fx.file), &bytes.Buffer{}, fx.segs[:2], ks.source)
	if !errors.Is(err, ErrSegmentMismatch) {
		t.Fatalf("got %v, want ErrSegmentMismatch", err)
	}
}

func TestDecryptStreamTruncated(t *testing.T) {
	fx := buildFixture(t, 0)
	ks := &countingKeys{calls: map[string]int{}, fx: fx}
	segs := append(append([]Segment(nil), fx.segs...), Segment{KeyURI: "skd://b"})
	err := DecryptStream(context.Background(), bytes.NewReader(fx.file), &bytes.Buffer{}, segs, ks.source)
	if !errors.Is(err, ErrSegmentMismatch) {
		t.Fatalf("got %v, want ErrSegmentMismatch", err)
	}
}

// The playlist lists only the song key (the prefetch key was dropped or left
// implicit); the tfhd sample description index must still pick the prefetch
// key for entry 1, and the output must only reference the one kept entry.
func TestDecryptStreamDualEntryKeysByDescIndex(t *testing.T) {
	fx := buildFixtureOpts(t, 0, true)
	ks := &countingKeys{calls: map[string]int{}, fx: fx}
	segs := []Segment{{KeyURI: "skd://b"}, {KeyURI: "skd://b"}, {KeyURI: "skd://b"}}
	var out bytes.Buffer
	if err := DecryptStream(context.Background(), bytes.NewReader(fx.file), &out, segs, ks.source); err != nil {
		t.Fatal(err)
	}
	if ks.calls[PrefetchKeyURI] != 1 || ks.calls["skd://b"] != 1 {
		t.Fatalf("key source calls = %v", ks.calls)
	}
	f, err := mp4.DecodeFile(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.Init.Moov.Trak.Mdia.Minf.Stbl.Stsd.Children); n != 1 {
		t.Fatalf("stsd has %d entries, want 1", n)
	}
	trex := f.Init.Moov.Mvex.Trex
	if trex.DefaultSampleDescriptionIndex != 1 {
		t.Fatalf("trex default index = %d, want 1", trex.DefaultSampleDescriptionIndex)
	}
	var i int
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			if idx := frag.Moof.Traf.Tfhd.SampleDescriptionIndex; idx != 1 {
				t.Errorf("fragment %d tfhd index = %d, want 1", i, idx)
			}
			samples, err := frag.GetFullSamples(trex)
			if err != nil {
				t.Fatal(err)
			}
			for j, s := range samples {
				if !bytes.Equal(s.Data, fx.plain[i][j]) {
					t.Errorf("fragment %d sample %d differs from plaintext", i, j)
				}
			}
			i++
		}
	}
	if i != len(fx.plain) {
		t.Fatalf("got %d fragments, want %d", i, len(fx.plain))
	}
}

func collectTypes(bs []mp4.Box, into map[string]bool) {
	for _, b := range bs {
		into[b.Type()] = true
		switch c := b.(type) {
		case *mp4.StsdBox: // not a ContainerBox
			collectTypes(c.Children, into)
		case *mp4.AudioSampleEntryBox:
			collectTypes(c.Children, into)
		case mp4.ContainerBox:
			collectTypes(c.GetChildren(), into)
		}
	}
}

func boxTypes(bs []mp4.Box) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Type()
	}
	return out
}

// A trun whose sample sizes overrun the mdat payload must fail the download,
// not panic inside a decrypt worker goroutine.
func TestDecryptStreamSampleSizeOverrun(t *testing.T) {
	fx := buildFixture(t, 0)
	in, err := mp4.DecodeFile(bytes.NewReader(fx.file))
	if err != nil {
		t.Fatal(err)
	}
	in.Segments[0].Fragments[0].Moof.Traf.Trun.Samples[0].Size = 1 << 20
	var corrupt bytes.Buffer
	if err := in.Encode(&corrupt); err != nil {
		t.Fatal(err)
	}
	ks := &countingKeys{calls: map[string]int{}, fx: fx}
	err = DecryptStream(context.Background(), bytes.NewReader(corrupt.Bytes()), &bytes.Buffer{}, fx.segs, ks.source)
	if err == nil || !strings.Contains(err.Error(), "malformed fragment") {
		t.Fatalf("got %v, want malformed fragment error", err)
	}
}
