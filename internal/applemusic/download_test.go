package applemusic_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/mp4ff/aac"
	"github.com/Eyevinn/mp4ff/mp4"

	"github.com/guohuiyuan/go-music-dl/internal/applemusic"
	"github.com/guohuiyuan/go-music-dl/internal/applemusic/applemusictest"
)

const (
	dlAdamID  = "1234567890"
	dlSongKey = "skd://itunes.apple.com/1234567890/c23"
)

type cbcDec struct {
	block cipher.Block
	iv    []byte
}

func (d cbcDec) DecryptInto(dst, src []byte) {
	cipher.NewCBCDecrypter(d.block, d.iv).CryptBlocks(dst, src)
}

// dlFixture is an encrypted single-track AAC fMP4 of 3 fragments x 5 samples
// (same approach as buildFixture in decrypt_test.go, which this external test
// package cannot import): fragments 0-1 are full-sample cbcs under KeyA,
// fragment 2 uses subsamples and a senc box under KeyB.
type dlFixture struct {
	File       []byte
	Plain      [][][]byte
	KeyA, KeyB []byte
	IV         []byte
}

func buildDLFixture(t *testing.T) *dlFixture {
	t.Helper()
	rng := rand.New(rand.NewSource(2))
	rb := func(n int) []byte {
		b := make([]byte, n)
		rng.Read(b)
		return b
	}
	fx := &dlFixture{KeyA: rb(16), KeyB: rb(16), IV: rb(16)}
	kid, _ := mp4.NewUUIDFromString("00112233-4455-6677-8899-aabbccddeeff")

	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(44100, "audio", "und")
	if err := trak.SetAACDescriptor(aac.AAClc, 44100); err != nil {
		t.Fatal(err)
	}
	ipd, err := mp4.InitProtect(init, fx.KeyA, fx.IV, "cbcs", kid, nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		t.Fatal(err)
	}

	var decodeTime uint64
	for f := 0; f < 3; f++ {
		frag, err := mp4.CreateFragment(uint32(f+1), trak.Tkhd.TrackID)
		if err != nil {
			t.Fatal(err)
		}
		var plains [][]byte
		for s := 0; s < 5; s++ {
			data := rb(70 + 16*s + 3*f + 5) // never a multiple of 16
			plains = append(plains, append([]byte(nil), data...))
			frag.AddFullSample(mp4.FullSample{
				Sample:     mp4.Sample{Flags: mp4.SyncSampleFlags, Dur: 1024, Size: uint32(len(data))},
				DecodeTime: decodeTime,
				Data:       data,
			})
			decodeTime += 1024
		}
		fx.Plain = append(fx.Plain, plains)
		if f < 2 {
			if _, err := mp4.EncryptFragment(frag, fx.KeyA, fx.IV, ipd); err != nil {
				t.Fatal(err)
			}
		} else {
			encryptSubsamples(t, frag, ipd, fx.KeyB, fx.IV)
		}
		if err := frag.Encode(&buf); err != nil {
			t.Fatal(err)
		}
	}
	fx.File = buf.Bytes()
	return fx
}

func encryptSubsamples(t *testing.T, frag *mp4.Fragment, ipd *mp4.InitProtectData, key, iv []byte) {
	t.Helper()
	samples, err := frag.GetFullSamples(ipd.Trex)
	if err != nil {
		t.Fatal(err)
	}
	senc := mp4.NewSencBox(0, len(samples))
	saiz := mp4.NewSaizBox(len(samples))
	for _, s := range samples {
		subs := []mp4.SubSamplePattern{
			{BytesOfClearData: 5, BytesOfProtectedData: 37},
			{BytesOfClearData: 2, BytesOfProtectedData: uint32(len(s.Data) - 44)},
		}
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
	if err := frag.Moof.Traf.AddChild(saiz); err != nil {
		t.Fatal(err)
	}
	if err := frag.Moof.Traf.AddChild(senc); err != nil {
		t.Fatal(err)
	}
}

type downloadEnv struct {
	wrapper *applemusictest.Server
	cdn     *httptest.Server
	fx      *dlFixture
	opts    applemusic.DownloadOptions
	// pauseAt > 0 makes the ALAC track handler send that many bytes, then
	// wait pause before sending the rest.
	pauseAt int64
	pause   time.Duration
}

// newDownloadEnv serves a 3-fragment encrypted track: fragments 0-1 use the
// prefetch key, fragment 2 uses the song key.
func newDownloadEnv(t *testing.T) *downloadEnv {
	t.Helper()
	fx := buildDLFixture(t)
	ranges := topLevelFragments(t, fx.File)
	if len(ranges) != 1+len(fx.Plain) {
		t.Fatalf("fixture has %d ranges, want %d", len(ranges), 1+len(fx.Plain))
	}

	var media strings.Builder
	media.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXT-X-VERSION:7\n#EXT-X-PLAYLIST-TYPE:VOD\n")
	fmt.Fprintf(&media, "#EXT-X-MAP:URI=\"track.mp4\",BYTERANGE=\"%d@%d\"\n", ranges[0][1], ranges[0][0])
	keyLine := func(uri string) string {
		return fmt.Sprintf("#EXT-X-KEY:METHOD=SAMPLE-AES,URI=%q,KEYFORMAT=\"com.apple.streamingkeydelivery\",KEYFORMATVERSIONS=\"1\"\n", uri)
	}
	for i, r := range ranges[1:] {
		switch i {
		case 0:
			media.WriteString(keyLine(applemusic.PrefetchKeyURI))
		case 2:
			media.WriteString(keyLine(dlSongKey))
		}
		fmt.Fprintf(&media, "#EXTINF:1.0,\n#EXT-X-BYTERANGE:%d@%d\ntrack.mp4\n", r[1], r[0])
	}
	media.WriteString("#EXT-X-ENDLIST\n")

	master := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=280000,CODECS="mp4a.40.2",AUDIO="audio-stereo-256"
aac/media.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1100000,CODECS="alac",AUDIO="audio-alac-stereo-44100-16"
alac/media.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=7000000,CODECS="alac",AUDIO="audio-alac-stereo-192000-24"
hires/media.m3u8
`
	env := &downloadEnv{fx: fx}
	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, master) })
	mux.HandleFunc("/alac/media.m3u8", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, media.String()) })
	mux.HandleFunc("/aac/media.m3u8", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, media.String()) })
	mux.HandleFunc("/alac/track.mp4", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			http.Error(w, "unexpected Range", http.StatusBadRequest)
			return
		}
		if env.pauseAt > 0 {
			_, _ = w.Write(fx.File[:env.pauseAt])
			w.(http.Flusher).Flush()
			time.Sleep(env.pause)
			_, _ = w.Write(fx.File[env.pauseAt:])
			return
		}
		_, _ = w.Write(fx.File)
	})
	mux.HandleFunc("/aac/track.mp4", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(fx.File) })
	cdn := httptest.NewServer(mux)
	t.Cleanup(cdn.Close)

	wr := applemusictest.New(t)
	wr.M3U8[dlAdamID] = cdn.URL + "/master.m3u8"
	wr.Keys["0|"+applemusic.PrefetchKeyURI] = keyTemplate("0", applemusic.PrefetchKeyURI, fx.KeyA)
	wr.Keys[dlAdamID+"|"+dlSongKey] = keyTemplate(dlAdamID, dlSongKey, fx.KeyB)

	env.wrapper = wr
	env.cdn = cdn
	env.opts = applemusic.DownloadOptions{
		Client:      applemusic.NewClient(wr.URL, nil),
		HTTP:        cdn.Client(),
		AdamID:      dlAdamID,
		Quality:     applemusic.QualityALAC,
		ALACMaxRate: 48000,
		NewDecrypter: func(kt *applemusic.KeyTemplate) (applemusic.SampleDecrypter, error) {
			key, err := hex.DecodeString(kt.ContentKey)
			if err != nil {
				return nil, err
			}
			block, err := aes.NewCipher(key)
			if err != nil {
				return nil, err
			}
			return cbcDec{block: block, iv: fx.IV}, nil
		},
	}
	return env
}

func keyTemplate(adamID, uri string, key []byte) applemusic.KeyTemplate {
	return applemusic.KeyTemplate{AdamID: adamID, KeyURI: uri, ContentKey: hex.EncodeToString(key), Ctx: "c", State: "s"}
}

// topLevelFragments returns [offset,length] of the init (ftyp+moov) and of
// each moof+mdat pair.
func topLevelFragments(t *testing.T, b []byte) [][2]int64 {
	t.Helper()
	var out [][2]int64
	var start int64
	var pos int64
	for pos < int64(len(b)) {
		size := int64(binary.BigEndian.Uint32(b[pos:]))
		typ := string(b[pos+4 : pos+8])
		if size < 8 {
			t.Fatalf("unsupported box size %d at %d", size, pos)
		}
		pos += size
		if typ == "moov" || typ == "mdat" {
			out = append(out, [2]int64{start, pos - start})
			start = pos
		}
	}
	return out
}

func TestDownload(t *testing.T) {
	env := newDownloadEnv(t)
	res, err := applemusic.Download(context.Background(), env.opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Codec != "alac" || res.Label != "ALAC 16bit/44.1kHz" {
		t.Fatalf("codec %q label %q, want alac / ALAC 16bit/44.1kHz", res.Codec, res.Label)
	}

	f, err := mp4.DecodeFile(bytes.NewReader(res.Data))
	if err != nil {
		t.Fatal(err)
	}
	var frags []*mp4.Fragment
	for _, seg := range f.Segments {
		frags = append(frags, seg.Fragments...)
	}
	if len(frags) != len(env.fx.Plain) {
		t.Fatalf("got %d fragments, want %d", len(frags), len(env.fx.Plain))
	}
	for i, frag := range frags {
		samples, err := frag.GetFullSamples(f.Init.Moov.Mvex.Trex)
		if err != nil {
			t.Fatal(err)
		}
		for j, s := range samples {
			if !bytes.Equal(s.Data, env.fx.Plain[i][j]) {
				t.Errorf("fragment %d sample %d differs from plaintext", i, j)
			}
		}
	}

	calls := env.wrapper.CallLog()
	for _, want := range []string{
		"/m3u8?adamId=" + dlAdamID,
		"/key?adamId=0&uri=" + applemusic.PrefetchKeyURI,
		"/key?adamId=" + dlAdamID + "&uri=" + dlSongKey,
	} {
		if !slices.Contains(calls, want) {
			t.Errorf("wrapper calls %v lack %q", calls, want)
		}
	}
	if len(calls) != 3 {
		t.Errorf("wrapper calls = %v, want 3 (one key fetch per URI)", calls)
	}
}

// A key fetch slower than the CDN idle timeout is not a stalled download.
func TestDownloadSlowKeyIsNotStall(t *testing.T) {
	env := newDownloadEnv(t)
	applemusic.SetCDNIdleTimeout(t, 50*time.Millisecond)
	// The rest of the file arrives while the first key fetch is running.
	ranges := topLevelFragments(t, env.fx.File)
	env.pauseAt = ranges[1][0] + ranges[1][1]
	env.pause = 100 * time.Millisecond
	newDec := env.opts.NewDecrypter
	env.opts.NewDecrypter = func(kt *applemusic.KeyTemplate) (applemusic.SampleDecrypter, error) {
		time.Sleep(200 * time.Millisecond)
		return newDec(kt)
	}
	if _, err := applemusic.Download(context.Background(), env.opts); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadAACLabel(t *testing.T) {
	env := newDownloadEnv(t)
	env.opts.Quality = applemusic.QualityAAC
	res, err := applemusic.Download(context.Background(), env.opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Codec != "mp4a.40.2" || res.Label != "AAC 256" {
		t.Fatalf("codec %q label %q, want mp4a.40.2 / AAC 256", res.Codec, res.Label)
	}
}

func TestDownloadWrapperError(t *testing.T) {
	env := newDownloadEnv(t)
	env.wrapper.Fail["/m3u8"] = 500
	_, err := applemusic.Download(context.Background(), env.opts)
	var apiErr *applemusic.APIError
	if !errors.As(err, &apiErr) || apiErr.Endpoint != "m3u8" || apiErr.Code != 500 {
		t.Fatalf("got %v, want *APIError for m3u8 code 500", err)
	}
}

func TestDownloadCDNStatus(t *testing.T) {
	env := newDownloadEnv(t)
	env.wrapper.M3U8[dlAdamID] = env.cdn.URL + "/missing.m3u8"
	_, err := applemusic.Download(context.Background(), env.opts)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("got %v, want an error with the 404 status", err)
	}
}
