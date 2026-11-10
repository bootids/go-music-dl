package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/go-music-dl/core"
	"github.com/guohuiyuan/go-music-dl/internal/applemusic/applemusictest"
)

func useTempAppleConfig(t *testing.T, wrapperURL string) {
	t.Helper()
	if err := core.CloseConfigDB(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSIC_DL_CONFIG_DB", filepath.Join(t.TempDir(), "settings.db"))
	t.Setenv(core.AppleWrapperEnvURL, wrapperURL)
	t.Cleanup(func() {
		if err := core.CloseConfigDB(); err != nil {
			t.Error(err)
		}
	})
}

func getAppleWrapperStatus(t *testing.T) map[string]any {
	return getAppleWrapperStatusQuery(t, "")
}

func getAppleWrapperStatusQuery(t *testing.T, query string) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/apple/wrapper/status", appleWrapperStatusHandler)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/apple/wrapper/status"+query, nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestAppleInspectPreviewOnlyFollowsWrapperConfig(t *testing.T) {
	useTempAppleConfig(t, "")
	if got := inspectSuccessPayload("apple", "u", "1", "256")["preview_only"]; got != true {
		t.Fatalf("unconfigured preview_only = %v, want true", got)
	}

	fake := applemusictest.New(t)
	t.Setenv(core.AppleWrapperEnvURL, fake.URL)
	full := inspectSuccessPayload("apple", "u", "1.0 MB", "33 kbps")
	if full["preview_only"] != false {
		t.Fatalf("configured preview_only = %v, want false", full["preview_only"])
	}
	// inspect probes the preview, so its size/bitrate must not be shown for the full track.
	if full["size"] != "-" || full["bitrate"] != "-" {
		t.Fatalf("configured size/bitrate = %v/%v, want -/-", full["size"], full["bitrate"])
	}
	if got := inspectSuccessPayload("spotify", "u", "1", "256")["preview_only"]; got != true {
		t.Fatalf("spotify preview_only = %v, want true", got)
	}
}

func TestAppleWrapperStatusRoute(t *testing.T) {
	useTempAppleConfig(t, "")
	payload := getAppleWrapperStatus(t)
	if payload["ok"] != false || payload["configured"] != false || payload["error"] != "未配置" {
		t.Fatalf("unconfigured payload = %v", payload)
	}

	fake := applemusictest.New(t)
	fake.Regions = []string{"us"}
	t.Setenv(core.AppleWrapperEnvURL, fake.URL)
	payload = getAppleWrapperStatus(t)
	regions, _ := payload["regions"].([]any)
	if payload["ok"] != true || payload["configured"] != true || len(regions) != 1 || regions[0] != "us" {
		t.Fatalf("configured payload = %v", payload)
	}
	if _, ok := payload["ffmpeg"].(bool); !ok {
		t.Fatalf("ffmpeg field missing: %v", payload)
	}

	empty := applemusictest.New(t)
	t.Setenv(core.AppleWrapperEnvURL, empty.URL)
	payload = getAppleWrapperStatus(t)
	if payload["ok"] != false || payload["configured"] != true {
		t.Fatalf("not-logged-in payload = %v", payload)
	}
	if msg, _ := payload["error"].(string); msg == "" {
		t.Fatalf("not-logged-in payload has no error: %v", payload)
	}
}

// The settings page tests the typed (unsaved) URL; the env URL wins when set.
func TestAppleWrapperStatusTypedURL(t *testing.T) {
	useTempAppleConfig(t, "")
	fake := applemusictest.New(t)
	fake.Regions = []string{"jp"}
	payload := getAppleWrapperStatusQuery(t, "?url="+url.QueryEscape(fake.URL+"/"))
	if payload["ok"] != true || payload["configured"] != true {
		t.Fatalf("typed URL payload = %v", payload)
	}
	payload = getAppleWrapperStatusQuery(t, "?url=127.0.0.1:12340")
	if payload["ok"] != false || payload["configured"] != false {
		t.Fatalf("malformed typed URL payload = %v", payload)
	}
	payload = getAppleWrapperStatusQuery(t, "?url=")
	if payload["ok"] != false || payload["error"] != "未配置" {
		t.Fatalf("empty typed URL payload = %v", payload)
	}

	empty := applemusictest.New(t)
	t.Setenv(core.AppleWrapperEnvURL, empty.URL)
	payload = getAppleWrapperStatusQuery(t, "?url="+url.QueryEscape(fake.URL))
	if payload["ok"] != false {
		t.Fatalf("env override ignored, payload = %v", payload)
	}
}

func TestAppleSettingsPayloadFields(t *testing.T) {
	useTempAppleConfig(t, "")
	settings := core.GetWebSettings()
	settings.AppleWrapperURL = "http://127.0.0.1:12340/"
	settings.AppleAudioQuality = "AAC"
	if err := core.SaveWebSettings(settings); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	get := func(disableAuth bool) map[string]any {
		r := gin.New()
		r.GET("/settings", func(c *gin.Context) { c.JSON(200, settingsPayloadFor(c, disableAuth)) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/settings", nil))
		var payload map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	// With auth on, an anonymous visitor must not learn the wrapper address.
	if got := get(false)["appleWrapperUrl"]; got != "" {
		t.Fatalf("anonymous appleWrapperUrl = %v, want empty", got)
	}
	payload := get(true)
	if payload["appleWrapperUrl"] != "http://127.0.0.1:12340" || payload["appleAudioQuality"] != "aac" {
		t.Fatalf("settings payload = %v", payload)
	}
	if payload["appleWrapperConfigured"] != true || payload["appleWrapperEnvOverride"] != false {
		t.Fatalf("runtime flags = %v", payload)
	}
}

// Probing a caller-supplied URL must come from the settings page, not a
// cross-site navigation carrying the admin cookie.
func TestAppleWrapperStatusURLRequiresSameOrigin(t *testing.T) {
	useTempAppleConfig(t, "")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/apple/wrapper/status", appleWrapperStatusHandler)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/apple/wrapper/status?url="+url.QueryEscape("http://169.254.169.254/x?y="), nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want 403", w.Code)
	}
}

func TestDownloadFilenamePreview(t *testing.T) {
	useTempAppleConfig(t, "")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/download/filename_preview", downloadFilenamePreviewHandler)
	get := func(query string) []map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/download/filename_preview?"+query, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		var payload struct {
			Examples []map[string]any `json:"examples"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Examples) != 2 {
			t.Fatalf("examples = %v", payload.Examples)
		}
		return payload.Examples
	}

	ex := get("template=" + url.QueryEscape("{artist} - {name}"))
	if ex[0]["path"] != "周杰伦 - 晴天.flac" || ex[1]["albumTag"] != ex[1]["album"] {
		t.Fatalf("plain preview = %v", ex)
	}
	ex = get("template=" + url.QueryEscape("{artist} - {name}") + "&organizeByArtistAlbum=1&cleanAlbumEpisode=1")
	if ex[0]["path"] != "周杰伦/叶惠美/周杰伦 - 晴天.flac" ||
		ex[1]["path"] != "示例歌手/声生不息·家年华/示例歌手 - 示例歌曲.m4a" ||
		ex[1]["albumTag"] != "声生不息·家年华" {
		t.Fatalf("organized preview = %v", ex)
	}
	// 预览只读表单值，不改动已保存设置。
	if s := core.GetWebSettings(); s.OrganizeByArtistAlbum || s.CleanAlbumEpisode {
		t.Fatalf("preview changed saved settings: %+v", s)
	}
}
