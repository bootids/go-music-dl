package core

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dhowden/tag"
	"github.com/guohuiyuan/go-music-dl/internal/applemusic"
	"github.com/guohuiyuan/music-lib/model"
)

// TestMain 让包里所有测试默认查一个总是返回空结果的本地 Lookup 服务，任何测试都不会访问 itunes.apple.com。
func TestMain(m *testing.M) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"resultCount":0,"results":[]}`))
	}))
	itunesLookupBaseURL = srv.URL
	code := m.Run()
	srv.Close()
	os.Exit(code)
}

const (
	testAppleSongID       = "1428768893"
	testAppleCollectionID = "1428766398"
)

type itunesLookupStub struct {
	mu       sync.Mutex
	requests []string // "id@country"
}

func (s *itunesLookupStub) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func (s *itunesLookupStub) count(id string) int {
	n := 0
	for _, r := range s.calls() {
		if strings.HasPrefix(r, id+"@") {
			n++
		}
	}
	return n
}

// stubItunesLookup 换上测试用的 Lookup 服务并清空专辑日期缓存。handler 返回 HTTP 状态码和 JSON。
func stubItunesLookup(t *testing.T, handler func(id, country string) (int, string)) *itunesLookupStub {
	t.Helper()
	stub := &itunesLookupStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, country := r.URL.Query().Get("id"), r.URL.Query().Get("country")
		stub.mu.Lock()
		stub.requests = append(stub.requests, id+"@"+country)
		stub.mu.Unlock()
		status, body := handler(id, country)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	orig := itunesLookupBaseURL
	itunesLookupBaseURL = srv.URL
	resetAppleAlbumDateCache()
	t.Cleanup(func() {
		itunesLookupBaseURL = orig
		srv.Close()
		resetAppleAlbumDateCache()
	})
	return stub
}

func resetAppleAlbumDateCache() {
	appleAlbumDateCache.Lock()
	appleAlbumDateCache.m = map[string]string{}
	appleAlbumDateCache.Unlock()
}

func itunesTrackJSON(collectionArtist string) string {
	extra := ""
	if collectionArtist != "" {
		extra = fmt.Sprintf(`,"collectionArtistName":%q`, collectionArtist)
	}
	return fmt.Sprintf(`{"resultCount":1,"results":[{"wrapperType":"track","kind":"song","trackId":%s,"collectionId":%s,"artistName":"宇多田ヒカル","trackNumber":7,"trackCount":11,"discNumber":1,"discCount":1,"releaseDate":"2016-04-15T12:00:00Z"%s}]}`,
		testAppleSongID, testAppleCollectionID, extra)
}

func itunesAlbumJSON() string {
	return fmt.Sprintf(`{"resultCount":1,"results":[{"wrapperType":"collection","collectionId":%s,"collectionName":"Fantôme","releaseDate":"2016-09-28T07:00:00Z"}]}`, testAppleCollectionID)
}

// fantomeLookup 模拟只在 us 区上架的歌：其他 storefront 返回空结果。
func fantomeLookup(collectionArtist string) func(id, country string) (int, string) {
	return func(id, country string) (int, string) {
		if country != "us" {
			return http.StatusOK, `{"resultCount":0,"results":[]}`
		}
		switch id {
		case testAppleSongID:
			return http.StatusOK, itunesTrackJSON(collectionArtist)
		case testAppleCollectionID:
			return http.StatusOK, itunesAlbumJSON()
		}
		return http.StatusOK, `{"resultCount":0,"results":[]}`
	}
}

func TestFillAppleTrackTagsStorefrontFallback(t *testing.T) {
	setupAppleConfigForTest(t)
	CM.SetAll(map[string]string{"apple": "media-user-token=x; storefront=TW"})
	stub := stubItunesLookup(t, fantomeLookup(""))

	callerExtra := map[string]string{"release_date": "2016-04-15"}
	song := &model.Song{Source: "apple", ID: testAppleSongID, Artist: "宇多田ヒカル", Extra: callerExtra}
	fillAppleTrackTags(context.Background(), song, "")

	if got, want := strings.Join(stub.calls(), ","), "1428768893@tw,1428768893@cn,1428768893@us,1428766398@us"; got != want {
		t.Fatalf("lookup calls = %s, want %s", got, want)
	}
	want := map[string]string{
		"release_date":     "2016-04-15",
		extraTagTrack:      "7",
		extraTagTrackTotal: "11",
		extraTagDisc:       "1",
		extraTagDiscTotal:  "1",
		extraTagDate:       "2016-09-28",
	}
	if fmt.Sprint(song.Extra) != fmt.Sprint(want) {
		t.Fatalf("Extra = %v, want %v", song.Extra, want)
	}
	if len(callerExtra) != 1 {
		t.Fatalf("caller's Extra map was mutated: %v", callerExtra)
	}
	if got := forkExtraTagsForSong(song); got != (forkExtraTags{AlbumArtist: "宇多田ヒカル", Track: 7, TrackTotal: 11, Disc: 1, DiscTotal: 1, Date: "2016-09-28"}) {
		t.Fatalf("forkExtraTagsForSong = %+v", got)
	}
}

// 歌曲自带的 storefront（排行榜歌曲）排在最前，命中后不再试其他区。
func TestFillAppleTrackTagsUsesSongStorefrontFirst(t *testing.T) {
	setupAppleConfigForTest(t)
	stub := stubItunesLookup(t, fantomeLookup(""))
	song := &model.Song{Source: "apple", ID: testAppleSongID, Artist: "A", Extra: map[string]string{"storefront": "us"}}
	fillAppleTrackTags(context.Background(), song, "")
	if got := stub.calls(); len(got) != 2 || got[0] != testAppleSongID+"@us" {
		t.Fatalf("lookup calls = %v, want song@us then album@us", got)
	}
}

func TestFillAppleTrackTagsCachesAlbumDate(t *testing.T) {
	setupAppleConfigForTest(t)
	stub := stubItunesLookup(t, fantomeLookup(""))
	for i := 0; i < 2; i++ {
		song := &model.Song{Source: "apple", ID: testAppleSongID, Artist: "A"}
		fillAppleTrackTags(context.Background(), song, "")
		if song.Extra[extraTagDate] != "2016-09-28" {
			t.Fatalf("download %d: date = %q", i, song.Extra[extraTagDate])
		}
	}
	if n := stub.count(testAppleCollectionID); n != 1 {
		t.Fatalf("album lookups = %d, want 1 (cached)", n)
	}
}

func TestFillAppleTrackTagsCollectionArtist(t *testing.T) {
	setupAppleConfigForTest(t)
	stubItunesLookup(t, fantomeLookup("Various Artists"))

	song := &model.Song{Source: "apple", ID: testAppleSongID, Artist: "宇多田ヒカル"}
	fillAppleTrackTags(context.Background(), song, "")
	if got := forkExtraTagsForSong(song).AlbumArtist; got != "Various Artists" {
		t.Fatalf("album artist = %q, want Various Artists", got)
	}

	song = &model.Song{Source: "apple", ID: testAppleSongID, Artist: "宇多田ヒカル"}
	fillAppleTrackTags(context.Background(), song, "群星 = Various Artists")
	if got := forkExtraTagsForSong(song).AlbumArtist; got != "群星" {
		t.Fatalf("album artist with alias = %q, want 群星", got)
	}

	// 歌手为空（本地文件换歌词）时仍不写专辑艺人。
	song.Artist = ""
	if got := forkExtraTagsForSong(song).AlbumArtist; got != "" {
		t.Fatalf("album artist without song artist = %q, want empty", got)
	}
}

func TestFillAppleTrackTagsFailuresWriteNothing(t *testing.T) {
	setupAppleConfigForTest(t)
	cases := map[string]func(id, country string) (int, string){
		"http error": func(string, string) (int, string) { return http.StatusInternalServerError, "boom" },
		"bad json":   func(string, string) (int, string) { return http.StatusOK, "{" },
		"empty":      func(string, string) (int, string) { return http.StatusOK, `{"resultCount":0,"results":[]}` },
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			stubItunesLookup(t, handler)
			song := &model.Song{Source: "apple", ID: testAppleSongID, Artist: "A", Extra: map[string]string{"k": "v"}}
			fillAppleTrackTags(context.Background(), song, "")
			if len(song.Extra) != 1 {
				t.Fatalf("Extra = %v, want unchanged", song.Extra)
			}
		})
	}

	t.Run("unreachable", func(t *testing.T) {
		stub := stubItunesLookup(t, fantomeLookup(""))
		srv := httptest.NewServer(http.NotFoundHandler())
		itunesLookupBaseURL = srv.URL
		srv.Close()
		song := &model.Song{Source: "apple", ID: testAppleSongID, Artist: "A"}
		fillAppleTrackTags(context.Background(), song, "")
		if song.Extra != nil || len(stub.calls()) != 0 {
			t.Fatalf("Extra = %v, calls = %v", song.Extra, stub.calls())
		}
	})

	t.Run("not apple", func(t *testing.T) {
		stub := stubItunesLookup(t, fantomeLookup(""))
		song := &model.Song{Source: "netease", ID: testAppleSongID, Artist: "A"}
		fillAppleTrackTags(context.Background(), song, "")
		if song.Extra != nil || len(stub.calls()) != 0 {
			t.Fatalf("non-apple song looked up: Extra = %v, calls = %v", song.Extra, stub.calls())
		}
	})
}

// ffprobeFormatTag 返回 ffprobe 读到的某个 format 标签。
func ffprobeFormatTag(t *testing.T, data []byte, ext, key string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe."+ext)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags="+key, "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func assertAppleTrackTags(t *testing.T, data []byte) {
	t.Helper()
	meta, err := tag.ReadFrom(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("read tags: %v", err)
	}
	if n, total := meta.Track(); n != 7 || total != 11 {
		t.Fatalf("track = %d/%d, want 7/11", n, total)
	}
	if n, total := meta.Disc(); n != 1 || total != 1 {
		t.Fatalf("disc = %d/%d, want 1/1", n, total)
	}
	if meta.Year() != 2016 {
		t.Fatalf("year = %d, want 2016", meta.Year())
	}
	if got := ffprobeFormatTag(t, data, "m4a", "date"); got != "2016-09-28" {
		t.Fatalf("date = %q, want album date 2016-09-28", got)
	}
}

func TestEmbedSongMetadataWritesAppleTrackTagsM4A(t *testing.T) {
	audio := ffmpegAudioForAlbumArtistTest(t, "m4a")
	song := &model.Song{Name: "道", Artist: "宇多田ヒカル", Album: "Fantôme", Ext: "m4a", Extra: map[string]string{
		extraTagTrack: "7", extraTagTrackTotal: "11", extraTagDisc: "1", extraTagDiscTotal: "1",
		extraTagDate: "2016-09-28", extraTagAlbumArtist: "Various Artists",
	}}
	embedded, err := EmbedSongMetadata(audio, song, "", nil, "")
	if err != nil {
		t.Fatalf("EmbedSongMetadata: %v", err)
	}
	assertAppleTrackTags(t, embedded)
	if got := ffprobeAlbumArtists(t, embedded, "m4a"); len(got) != 1 || got[0] != "Various Artists" {
		t.Fatalf("album artist = %q, want [Various Artists]", got)
	}
}

// 整条下载流程：Apple 歌曲写入曲序、碟序和专辑日期，调用方的 song.Extra 不变；Lookup 失败时照常下载、无警告。
func TestDownloadAppleWritesTrackDiscDate(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	setupAppleConfigForTest(t)
	t.Setenv(AppleWrapperEnvURL, "http://127.0.0.1:1")
	t.Setenv("MUSIC_DL_FFMPEG", "")
	clear := buildClearFragmentedMP4(t)
	stubAppleDownload(t, func(context.Context, applemusic.DownloadOptions) (*applemusic.Result, error) {
		return &applemusic.Result{Data: clear, Codec: "aac"}, nil
	})

	t.Run("lookup ok", func(t *testing.T) {
		stubItunesLookup(t, fantomeLookup(""))
		callerExtra := map[string]string{"storefront": "cn"}
		song := &model.Song{Source: "apple", ID: testAppleSongID, Name: "道", Artist: "宇多田ヒカル", Album: "Fantôme", Extra: callerExtra}
		res, err := DownloadSongDataWithTemplateContext(context.Background(), song, false, false, "{track}. {name}")
		if err != nil {
			t.Fatal(err)
		}
		if res.Ext != "m4a" || res.Warning != "" {
			t.Fatalf("ext = %q, warning = %q", res.Ext, res.Warning)
		}
		if res.Filename != "07. 道.m4a" {
			t.Fatalf("filename = %q, want track prefix", res.Filename)
		}
		assertAppleTrackTags(t, res.Data)
		if len(callerExtra) != 1 || len(song.Extra) != 1 {
			t.Fatalf("caller's Extra changed: %v", song.Extra)
		}
	})

	t.Run("lookup fails", func(t *testing.T) {
		stubItunesLookup(t, func(string, string) (int, string) { return http.StatusServiceUnavailable, "" })
		song := &model.Song{Source: "apple", ID: testAppleSongID, Name: "道", Artist: "宇多田ヒカル", Album: "Fantôme"}
		res, err := DownloadSongDataWithTemplateContext(context.Background(), song, false, false, "{track}. {name}")
		if err != nil {
			t.Fatal(err)
		}
		if res.Warning != "" {
			t.Fatalf("warning = %q, want none", res.Warning)
		}
		if res.Filename != "道.m4a" {
			t.Fatalf("filename = %q, want no track prefix", res.Filename)
		}
		meta, err := tag.ReadFrom(bytes.NewReader(res.Data))
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := meta.Track(); n != 0 || meta.Album() != "Fantôme" {
			t.Fatalf("track = %d, album = %q; want no track and album still written", n, meta.Album())
		}
	})
}
