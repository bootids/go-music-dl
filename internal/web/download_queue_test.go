package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/go-music-dl/core"
)

func TestDownloadQueueAPIPersistsAppendAndRejectsCrossOrigin(t *testing.T) {
	if err := core.CloseConfigDB(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSIC_DL_CONFIG_DB", filepath.Join(t.TempDir(), "settings.db"))
	t.Cleanup(func() {
		if err := core.CloseConfigDB(); err != nil {
			t.Error(err)
		}
	})
	q, err := core.OpenDownloadQueue()
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterDownloadQueueRoutes(r.Group("/music"), q)
	request := func(method, path string, body []byte, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://music.test/music/api/downloads/queue"+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	payload := []byte(`{"request_id":"one","songs":[{"id":"1","source":"kuwo","name":"One"},{"id":"2","source":"kuwo","name":"Two"}]}`)
	if w := request("POST", "", payload, "http://other.test"); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("POST", "", payload, "http://music.test"); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("POST", "", payload, "http://music.test"); w.Code != 202 || !bytes.Contains(w.Body.Bytes(), []byte(`"added":0`)) {
		t.Fatal(w.Code, w.Body.String())
	}
	second := []byte(`{"request_id":"two","songs":[{"id":"3","source":"kuwo","name":"Three"}]}`)
	if w := request("POST", "", second, "http://music.test"); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := core.CloseConfigDB(); err != nil {
		t.Fatal(err)
	}
	q, err = core.OpenDownloadQueue()
	if err != nil {
		t.Fatal(err)
	}
	r = gin.New()
	RegisterDownloadQueueRoutes(r.Group("/music"), q)
	w := request("GET", "?status=active", nil, "http://music.test")
	var response struct {
		Total  int
		Tasks  []core.DownloadTask
		Counts map[string]int
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || response.Total != 3 || len(response.Tasks) != 3 || response.Counts[core.TaskWaiting] != 3 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("POST", "/1/cancel", nil, "http://music.test"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("POST", "/1/retry", nil, "http://music.test"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	invalid := []byte(`{"request_id":"invalid","songs":[{"id":"4","source":"kuwo"},{"source":"kuwo"}]}`)
	if w := request("POST", "", invalid, "http://music.test"); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	_, total, _, err := q.List(1, 50, "")
	if err != nil || total != 3 {
		t.Fatal(total, err)
	}
	if w := request("DELETE", "/1", nil, "https://other.example"); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("DELETE", "", nil, "https://other.example"); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("DELETE", "/invalid", nil, "http://music.test"); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("DELETE", "/1", nil, "http://music.test"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("DELETE", "", nil, "http://music.test"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_, total, _, err = q.List(1, 50, "")
	if err != nil || total != 0 {
		t.Fatal(total, err)
	}

}

func TestDownloadQueueAPIKeepsDuration(t *testing.T) {
	if err := core.CloseConfigDB(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSIC_DL_CONFIG_DB", filepath.Join(t.TempDir(), "settings.db"))
	t.Cleanup(func() {
		if err := core.CloseConfigDB(); err != nil {
			t.Error(err)
		}
	})
	q, err := core.OpenDownloadQueue()
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterDownloadQueueRoutes(r.Group("/music"), q)
	body := []byte(`{"request_id":"d","songs":[{"id":"1","source":"apple","name":"One","duration":200}]}`)
	req := httptest.NewRequest("POST", "http://music.test/music/api/downloads/queue", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", "http://music.test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	got := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := q.Start(ctx, func(in core.DownloadTaskInput) (core.DownloadTaskResult, error) {
		got <- in.Song.Duration
		return core.DownloadTaskResult{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-got:
		if d != 200 {
			t.Fatalf("queued song duration = %d, want 200", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued task was not processed")
	}
	// 等任务落库后再关库，避免 worker 写入已关闭的数据库。
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, total, _, err := q.List(1, 10, "active"); err == nil && total == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued task did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
