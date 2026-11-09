package applemusic

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/Eyevinn/mp4ff/aac"
	"github.com/Eyevinn/mp4ff/mp4"
)

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
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	fx := &fixture{
		keys: map[string][]byte{"skd://a": randBytes(rng, 16), "skd://b": randBytes(rng, 16)},
		iv:   randBytes(rng, 16),
		segs: []Segment{{KeyURI: "skd://a"}, {KeyURI: "skd://a"}, {KeyURI: "skd://b"}},
	}
	kid, _ := mp4.NewUUIDFromString("00112233-4455-6677-8899-aabbccddeeff")

	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(44100, "audio", "und")
	if err := trak.SetAACDescriptor(aac.AAClc, 44100); err != nil {
		t.Fatal(err)
	}
	trackID := trak.Tkhd.TrackID
	ipd, err := mp4.InitProtect(init, fx.keys["skd://a"], fx.iv, "cbcs", kid, nil)
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

func TestDecryptStreamSegmentMismatch(t *testing.T) {
	fx := buildFixture(t, 0)
	ks := &countingKeys{calls: map[string]int{}, fx: fx}
	err := DecryptStream(context.Background(), bytes.NewReader(fx.file), &bytes.Buffer{}, fx.segs[:2], ks.source)
	if !errors.Is(err, ErrSegmentMismatch) {
		t.Fatalf("got %v, want ErrSegmentMismatch", err)
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
