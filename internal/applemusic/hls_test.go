package applemusic

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

var testBase, _ = url.Parse("https://aod.itunes.apple.com/itunes-assets/HLSMusic/x/P1.m3u8")

func loadMaster(t *testing.T, name string) []Variant {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	vs, err := ParseMaster(body, testBase)
	if err != nil {
		t.Fatalf("ParseMaster(%s): %v", name, err)
	}
	return vs
}

func TestParseMaster(t *testing.T) {
	vs := loadMaster(t, "master.m3u8")
	if len(vs) != 6 {
		t.Fatalf("got %d variants, want 6", len(vs))
	}
	var alac192 *Variant
	for i := range vs {
		if vs[i].Group == "audio-alac-stereo-192000-24" {
			alac192 = &vs[i]
		}
	}
	if alac192 == nil {
		t.Fatal("192k ALAC variant missing")
	}
	want := Variant{
		URI:        "https://aod.itunes.apple.com/itunes-assets/HLSMusic/x/P1_alac_192/prog_index.m3u8",
		Codec:      "alac",
		Group:      "audio-alac-stereo-192000-24",
		SampleRate: 192000,
		BitDepth:   24,
		Bandwidth:  5800000,
	}
	if *alac192 != want {
		t.Fatalf("got %+v, want %+v", *alac192, want)
	}
	if _, err := ParseMaster([]byte("#EXTM3U\n#EXTINF:10,\na.mp4\n"), testBase); !errors.Is(err, ErrUnsupportedPlaylist) {
		t.Fatalf("media playlist as master: got %v", err)
	}
}

func TestPickVariant(t *testing.T) {
	full := loadMaster(t, "master.m3u8")
	cases := []struct {
		name    string
		vs      []Variant
		pref    Quality
		maxRate int
		group   string
	}{
		{"alac max 192k", full, QualityALAC, 192000, "audio-alac-stereo-192000-24"},
		{"alac max 48k", full, QualityALAC, 48000, "audio-alac-stereo-44100-16"},
		{"aac", full, QualityAAC, 192000, "audio-stereo-256"},
		{"alac falls back to aac", loadMaster(t, "master_aac_only.m3u8"), QualityALAC, 192000, "audio-stereo-256"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := PickVariant(c.vs, c.pref, c.maxRate)
			if err != nil {
				t.Fatal(err)
			}
			if v.Group != c.group {
				t.Fatalf("picked %s, want %s", v.Group, c.group)
			}
		})
	}

	_, err := PickVariant(loadMaster(t, "master_ec3_only.m3u8"), QualityALAC, 192000)
	if !errors.Is(err, ErrNoUsableVariant) {
		t.Fatalf("ec-3 only: got %v, want ErrNoUsableVariant", err)
	}
}

func TestParseMediaPlaylist(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "media.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	mp, err := ParseMediaPlaylist(body, testBase)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://aod.itunes.apple.com/itunes-assets/HLSMusic/x/track.mp4"; mp.FileURL != want {
		t.Fatalf("FileURL = %s, want %s", mp.FileURL, want)
	}
	want := []Segment{
		{Offset: 1200, Length: 5000, KeyURI: PrefetchKeyURI},
		{Offset: 6200, Length: 5100, KeyURI: PrefetchKeyURI},
		{Offset: 11300, Length: 5200, KeyURI: "skd://1234567890/c23"},
		{Offset: 16500, Length: 2300, KeyURI: "skd://1234567890/c23"},
	}
	if len(mp.Segments) != len(want) {
		t.Fatalf("got %d segments, want %d: %+v", len(mp.Segments), len(want), mp.Segments)
	}
	for i := range want {
		if mp.Segments[i] != want[i] {
			t.Errorf("segment %d = %+v, want %+v", i, mp.Segments[i], want[i])
		}
	}
}

func TestParseMediaPlaylistNoByteRange(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "media_no_byterange.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseMediaPlaylist(body, testBase); !errors.Is(err, ErrUnsupportedPlaylist) {
		t.Fatalf("got %v, want ErrUnsupportedPlaylist", err)
	}
}
