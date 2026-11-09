package core

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Eyevinn/mp4ff/aac"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/guohuiyuan/go-music-dl/internal/applemusic"
	"github.com/guohuiyuan/go-music-dl/internal/applemusic/applemusictest"
	"github.com/guohuiyuan/music-lib/model"
)

func setupAppleConfigForTest(t *testing.T) {
	t.Helper()
	t.Setenv("MUSIC_DL_CONFIG_DB", filepath.Join(t.TempDir(), "s.db"))
	t.Setenv(AppleWrapperEnvURL, "")
	resetConfigStateForTest()
	t.Cleanup(resetConfigStateForTest)
}

func stubAppleDownload(t *testing.T, fn func(context.Context, applemusic.DownloadOptions) (*applemusic.Result, error)) {
	t.Helper()
	orig := appleDownload
	appleDownload = fn
	t.Cleanup(func() { appleDownload = orig })
}

func TestAppleWrapperSettings(t *testing.T) {
	setupAppleConfigForTest(t)

	if AppleWrapperConfigured() {
		t.Fatal("configured with no URL")
	}
	if got := GetWebSettings().AppleAudioQuality; got != "alac" {
		t.Fatalf("default quality = %q, want alac", got)
	}

	for _, bad := range []string{"ftp://x", "foo"} {
		err := SaveWebSettings(WebSettings{AppleWrapperURL: bad})
		if !errors.Is(err, ErrInvalidWebSettings) {
			t.Fatalf("SaveWebSettings(%q) err = %v, want ErrInvalidWebSettings", bad, err)
		}
	}

	if err := SaveWebSettings(WebSettings{AppleWrapperURL: " http://127.0.0.1:12340/ ", AppleAudioQuality: "AAC"}); err != nil {
		t.Fatalf("SaveWebSettings: %v", err)
	}
	s := GetWebSettings()
	if s.AppleWrapperURL != "http://127.0.0.1:12340" || s.AppleAudioQuality != "aac" {
		t.Fatalf("round trip = %q/%q", s.AppleWrapperURL, s.AppleAudioQuality)
	}
	if AppleWrapperURL() != "http://127.0.0.1:12340" || AppleAudioQuality() != applemusic.QualityAAC {
		t.Fatalf("effective = %q/%q", AppleWrapperURL(), AppleAudioQuality())
	}
	if AppleWrapperEnvOverride() {
		t.Fatal("env override reported with empty env")
	}

	t.Setenv(AppleWrapperEnvURL, " http://wrapper:12340/ ")
	if got := AppleWrapperURL(); got != "http://wrapper:12340" {
		t.Fatalf("env URL = %q", got)
	}
	if !AppleWrapperEnvOverride() {
		t.Fatal("env override not reported")
	}

	// A malformed env value is ignored, like an invalid value in the settings UI.
	t.Setenv(AppleWrapperEnvURL, "127.0.0.1:12340")
	if AppleWrapperEnvOverride() || AppleWrapperEnvError() == nil {
		t.Fatalf("malformed env: override=%v err=%v", AppleWrapperEnvOverride(), AppleWrapperEnvError())
	}
	if got := AppleWrapperURL(); got != "http://127.0.0.1:12340" {
		t.Fatalf("malformed env URL = %q, want saved setting", got)
	}
	t.Setenv(AppleWrapperEnvURL, " http://wrapper:12340/ ")

	if err := SaveWebSettings(WebSettings{AppleAudioQuality: "flac"}); err != nil {
		t.Fatalf("SaveWebSettings: %v", err)
	}
	if got := GetWebSettings().AppleAudioQuality; got != "alac" {
		t.Fatalf("invalid quality normalised to %q, want alac", got)
	}
}

func TestAppleFetchSongAudioUsesWrapper(t *testing.T) {
	setupAppleConfigForTest(t)
	t.Setenv(AppleWrapperEnvURL, "http://127.0.0.1:1")
	t.Setenv("MUSIC_DL_FFMPEG", filepath.Join(t.TempDir(), "missing-ffmpeg"))

	var got applemusic.DownloadOptions
	stubAppleDownload(t, func(_ context.Context, o applemusic.DownloadOptions) (*applemusic.Result, error) {
		got = o
		return &applemusic.Result{Data: []byte("fmp4"), Codec: "alac"}, nil
	})

	data, mime, err := fetchSongAudio(context.Background(), &model.Song{Source: "apple", ID: "123"})
	if err != nil {
		t.Fatalf("fetchSongAudio: %v", err)
	}
	if string(data) != "fmp4" || mime != "audio/mp4" {
		t.Fatalf("got %q %q", data, mime)
	}
	if got.AdamID != "123" || got.Quality != applemusic.QualityALAC || got.ALACMaxRate != appleALACMaxRate || got.Client == nil || got.HTTP == nil {
		t.Fatalf("options = %+v", got)
	}
}

func TestAppleFetchSongAudioNoPreviewFallback(t *testing.T) {
	setupAppleConfigForTest(t)
	t.Setenv(AppleWrapperEnvURL, "http://127.0.0.1:1")

	stubAppleDownload(t, func(context.Context, applemusic.DownloadOptions) (*applemusic.Result, error) {
		return nil, applemusic.ErrNoKeyTemplate
	})

	data, _, err := fetchSongAudio(context.Background(), &model.Song{Source: "apple", ID: "123"})
	if !errors.Is(err, applemusic.ErrNoKeyTemplate) || data != nil {
		t.Fatalf("got data=%q err=%v, want ErrNoKeyTemplate and no data", data, err)
	}
	if !strings.HasPrefix(err.Error(), "apple full download: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestAppleNotConfiguredSkipsWrapper(t *testing.T) {
	setupAppleConfigForTest(t)
	called := false
	stubAppleDownload(t, func(context.Context, applemusic.DownloadOptions) (*applemusic.Result, error) {
		called = true
		return nil, nil
	})
	if AppleWrapperConfigured() {
		t.Fatal("configured with no URL")
	}
	if called {
		t.Fatal("stub called")
	}
}

func TestAppleRemuxMP4(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	t.Setenv("MUSIC_DL_FFMPEG", "")

	out, err := remuxAppleMP4(buildClearFragmentedMP4(t))
	if err != nil {
		t.Fatalf("remuxAppleMP4: %v", err)
	}
	f, err := mp4.DecodeFile(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	var order []string
	for _, b := range f.Children {
		order = append(order, b.Type())
	}
	joined := strings.Join(order, ",")
	moov, mdat := strings.Index(joined, "moov"), strings.Index(joined, "mdat")
	if moov < 0 || mdat < 0 || moov > mdat || strings.Contains(joined, "moof") {
		t.Fatalf("top-level boxes = %s, want moov before mdat and no moof", joined)
	}
}

func TestAppleLyricsFromWrapper(t *testing.T) {
	setupAppleConfigForTest(t)
	fake := applemusictest.New(t)
	fake.Lyrics["123"] = `<tt xmlns="http://www.w3.org/ns/ttml" xmlns:itunes="http://music.apple.com/lyric-ttml-internal" itunes:timing="Line">` +
		`<body><div><p begin="1.5" end="3">Hello</p><p begin="1:02.25">World</p></div></body></tt>`
	t.Setenv(AppleWrapperEnvURL, fake.URL)

	fn := GetLyricFunc("apple")
	lrc, err := fn(&model.Song{Source: "apple", ID: "123"})
	if err != nil {
		t.Fatalf("lyrics: %v", err)
	}
	if want := "[00:01.50]Hello\n[01:02.25]World\n"; lrc != want {
		t.Fatalf("lrc = %q, want %q", lrc, want)
	}
	if calls := fake.CallLog(); len(calls) != 1 || calls[0] != "/lyrics?adamId=123&syllable=0" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestAppleLyricsEmptyWrapperFallsBackToCookie(t *testing.T) {
	setupAppleConfigForTest(t)
	fake := applemusictest.New(t)
	fake.Lyrics["123"] = ""
	t.Setenv(AppleWrapperEnvURL, fake.URL)
	orig := appleCookieLyrics
	appleCookieLyrics = func(string, *model.Song) (string, error) { return "[00:01.00]cookie\n", nil }
	t.Cleanup(func() { appleCookieLyrics = orig })

	lrc, err := GetLyricFunc("apple")(&model.Song{Source: "apple", ID: "123"})
	if err != nil || lrc != "[00:01.00]cookie\n" {
		t.Fatalf("lrc = %q, err = %v, want cookie lyrics", lrc, err)
	}
}

// buildClearFragmentedMP4 builds a small unencrypted AAC fMP4 (init plus two fragments).
func buildClearFragmentedMP4(t *testing.T) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(44100, "audio", "und")
	if err := trak.SetAACDescriptor(aac.AAClc, 44100); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	var decodeTime uint64
	for f := 0; f < 2; f++ {
		frag, err := mp4.CreateFragment(uint32(f+1), trak.Tkhd.TrackID)
		if err != nil {
			t.Fatal(err)
		}
		for s := 0; s < 5; s++ {
			data := make([]byte, 100+s)
			rng.Read(data)
			frag.AddFullSample(mp4.FullSample{
				Sample:     mp4.Sample{Flags: mp4.SyncSampleFlags, Dur: 1024, Size: uint32(len(data))},
				DecodeTime: decodeTime,
				Data:       data,
			})
			decodeTime += 1024
		}
		if err := frag.Encode(&buf); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func writeMP4WithDuration(t *testing.T, path string, seconds uint64) {
	t.Helper()
	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(44100, "audio", "und")
	init.Moov.Mvhd.Duration = seconds * uint64(init.Moov.Mvhd.Timescale)
	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAppleDedupReplacesPreview(t *testing.T) {
	setupAppleConfigForTest(t)
	dir := t.TempDir()
	writeMP4WithDuration(t, filepath.Join(dir, "preview.m4a"), 30)
	writeMP4WithDuration(t, filepath.Join(dir, "full.m4a"), 200)
	song := &model.Song{Source: "apple", ID: "123", Name: "N", Artist: "A", Duration: 200}

	index := DownloadDedupIndex{SongKey(song): "preview.m4a"}
	if !IsSongStillDownloaded(song, index, dir) {
		t.Fatal("preview not skipped without wrapper")
	}
	t.Setenv(AppleWrapperEnvURL, "http://127.0.0.1:1")
	if IsSongStillDownloaded(song, index, dir) {
		t.Fatal("preview skipped with wrapper configured")
	}
	index = DownloadDedupIndex{SongKey(song): "full.m4a"}
	if !IsSongStillDownloaded(song, index, dir) {
		t.Fatal("full track not skipped")
	}
}
