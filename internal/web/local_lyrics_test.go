package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhowden/tag"
	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/go-music-dl/core"
	"github.com/guohuiyuan/music-lib/model"
)

const lyricTestEnhanced = "[00:01.00]<00:01.00>你<00:01.50>好<00:02.00>\n[00:01.00]hello"

func newLocalLyricTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	group := r.Group(RoutePrefix)
	RegisterMusicRoutes(group, group)
	RegisterLocalMusicRoutes(group)
	RegisterLocalLyricRoutes(group, group)
	return r
}

func writeLyricTestMP3(t *testing.T, dir, name, lyric string) string {
	t.Helper()
	data, err := core.EmbedSongMetadata([]byte{0xff, 0xfb, 0x90, 0x64, 0x00, 0x00, 0x00, 0x00},
		&model.Song{Name: name, Artist: "歌手", Album: "专辑", Ext: "mp3"}, lyric, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".mp3")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func setupLocalLyricTest(t *testing.T, duration int) string {
	t.Helper()
	initCollectionDBForTest(t)
	dir := t.TempDir()
	withLocalMusicDownloadDir(t, dir)
	invalidateLocalMusicScanCache()
	t.Cleanup(invalidateLocalMusicScanCache)
	origDuration, origSearch, origFetch := localTrackDuration, localLyricSearchQQ, localLyricFetchQQ
	localTrackDuration = func(*localMusicTrack) int { return duration }
	localLyricSearchQQ = func(keyword string) ([]model.Song, error) {
		return []model.Song{
			{ID: "1", Name: "逐行歌", Artist: "歌手", Album: "专辑", Duration: duration + 1, Extra: map[string]string{"song_id": "1"}},
			{ID: "2", Name: "逐行歌 (Live)", Artist: "歌手", Duration: duration + 30, Extra: map[string]string{"song_id": "2"}},
		}, nil
	}
	localLyricFetchQQ = func(*model.Song) (string, error) { return lyricTestEnhanced, nil }
	t.Cleanup(func() {
		localTrackDuration, localLyricSearchQQ, localLyricFetchQQ = origDuration, origSearch, origFetch
	})
	return dir
}

func doLyricRequest(t *testing.T, r http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "http://music.test"+RoutePrefix+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", "http://music.test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var payload map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &payload)
	return w.Code, payload
}

func TestLocalLyricCheckCountsKinds(t *testing.T) {
	dir := setupLocalLyricTest(t, 200)
	writeLyricTestMP3(t, dir, "逐行歌", "[00:01.00]第一行\n[00:02.00]第二行")
	writeLyricTestMP3(t, dir, "逐字歌", "[00:01.00]你[00:01.50]好[00:02.00]")
	writeLyricTestMP3(t, dir, "无词歌", "")
	sidecar := writeLyricTestMP3(t, dir, "外置歌", "")
	_ = os.WriteFile(strings.TrimSuffix(sidecar, ".mp3")+".lrc", []byte("[00:01.00]外置逐行"), 0o644)

	code, payload := doLyricRequest(t, newLocalLyricTestRouter(), http.MethodGet, "/local_music/lyric_check?refresh=1", nil)
	if code != http.StatusOK {
		t.Fatalf("status %d %v", code, payload)
	}
	counts := payload["counts"].(map[string]any)
	if counts["line"] != 2.0 || counts["verbatim"] != 1.0 || counts["none"] != 1.0 {
		t.Fatalf("counts = %v", counts)
	}
	kinds := map[string]string{}
	for _, item := range payload["tracks"].([]any) {
		m := item.(map[string]any)
		kinds[m["name"].(string)] = m["kind"].(string)
		if m["writable"] != true {
			t.Fatalf("mp3 should be writable: %v", m)
		}
	}
	if kinds["外置歌"] != "line" || kinds["逐字歌"] != "verbatim" {
		t.Fatalf("kinds = %v", kinds)
	}
}

func TestLocalLyricCandidatesMarkDurationMatch(t *testing.T) {
	dir := setupLocalLyricTest(t, 200)
	writeLyricTestMP3(t, dir, "逐行歌", "[00:01.00]第一行")
	r := newLocalLyricTestRouter()
	id := encodeLocalMusicID("逐行歌.mp3")
	code, payload := doLyricRequest(t, r, http.MethodGet, "/local_music/lyric_candidates?id="+id, nil)
	if code != http.StatusOK {
		t.Fatalf("status %d %v", code, payload)
	}
	if payload["keyword"] != "逐行歌 歌手" {
		t.Fatalf("keyword = %v", payload["keyword"])
	}
	cands := payload["candidates"].([]any)
	first, second := cands[0].(map[string]any), cands[1].(map[string]any)
	if first["match"] != true || first["diff"] != 1.0 || second["match"] != false || second["diff"] != 30.0 {
		t.Fatalf("candidates = %v", cands)
	}
}

func TestLocalLyricApplyWritesTagAndSidecar(t *testing.T) {
	dir := setupLocalLyricTest(t, 200)
	path := writeLyricTestMP3(t, dir, "逐行歌", "[00:01.00]第一行")
	lrc := strings.TrimSuffix(path, ".mp3") + ".lrc"
	_ = os.WriteFile(lrc, []byte("[00:01.00]第一行"), 0o644)
	r := newLocalLyricTestRouter()
	id := encodeLocalMusicID("逐行歌.mp3")

	mismatch := map[string]any{"id": "2", "name": "逐行歌 (Live)", "duration": 230, "extra": map[string]string{"song_id": "2"}}
	code, payload := doLyricRequest(t, r, http.MethodPost, "/local_music/lyric_apply", map[string]any{"id": id, "candidate": mismatch})
	if code != http.StatusConflict || payload["error"] != "duration_mismatch" || payload["diff"] != 30.0 {
		t.Fatalf("mismatch: %d %v", code, payload)
	}
	if data, _ := os.ReadFile(lrc); string(data) != "[00:01.00]第一行" {
		t.Fatal("mismatch must not touch files")
	}

	match := map[string]any{"id": "1", "name": "逐行歌", "duration": 201, "extra": map[string]string{"song_id": "1"}}
	code, payload = doLyricRequest(t, r, http.MethodPost, "/local_music/lyric_apply", map[string]any{"id": id, "candidate": match})
	if code != http.StatusOK || payload["kind"] != "verbatim" || payload["sidecar_backup"] != true || payload["forced"] != false {
		t.Fatalf("apply: %d %v", code, payload)
	}
	f, _ := os.Open(path)
	m, err := tag.ReadFrom(f)
	_ = f.Close()
	if err != nil || strings.TrimSpace(m.Lyrics()) != lyricTestEnhanced || m.Title() != "逐行歌" {
		t.Fatalf("embedded lyric = %q err=%v", m.Lyrics(), err)
	}
	if data, _ := os.ReadFile(lrc); strings.TrimSpace(string(data)) != lyricTestEnhanced {
		t.Fatalf("sidecar = %q", data)
	}
	if data, _ := os.ReadFile(lrc + ".bak"); string(data) != "[00:01.00]第一行" {
		t.Fatalf("backup = %q", data)
	}

	// 播放器拿到的是行内逐字格式。
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, RoutePrefix+"/lyric?source=local&id="+id, nil))
	if got := w.Body.String(); !strings.Contains(got, "[00:01.00]你[00:01.50]好[00:02.00]") || strings.Contains(got, "<") {
		t.Fatalf("served lyric = %q", got)
	}

	// 强制嵌入时长不一致的候选。
	code, payload = doLyricRequest(t, r, http.MethodPost, "/local_music/lyric_apply", map[string]any{"id": id, "candidate": mismatch, "force": true})
	if code != http.StatusOK || payload["forced"] != true {
		t.Fatalf("force: %d %v", code, payload)
	}
	if data, _ := os.ReadFile(lrc + ".bak"); string(data) != "[00:01.00]第一行" {
		t.Fatalf("backup after second apply = %q", data)
	}
}

func TestLocalLyricApplyRequiresSameOrigin(t *testing.T) {
	dir := setupLocalLyricTest(t, 200)
	writeLyricTestMP3(t, dir, "逐行歌", "[00:01.00]第一行")
	raw, _ := json.Marshal(map[string]any{"id": encodeLocalMusicID("逐行歌.mp3"), "candidate": map[string]any{"duration": 200}})
	req := httptest.NewRequest(http.MethodPost, "http://music.test"+RoutePrefix+"/local_music/lyric_apply", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	newLocalLyricTestRouter().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d", w.Code)
	}
}
