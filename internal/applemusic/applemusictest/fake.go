// Package applemusictest provides an in-memory fake of the wrapper-lite HTTP
// API for tests. It must not be imported by non-test code.
package applemusictest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/guohuiyuan/go-music-dl/internal/applemusic"
)

// Server is a fake wrapper-lite. Configure the maps before issuing requests.
type Server struct {
	*httptest.Server
	Regions []string
	M3U8    map[string]string                 // adamID -> master playlist URL
	Keys    map[string]applemusic.KeyTemplate // adamID+"|"+uri -> template
	Lyrics  map[string]string                 // adamID -> TTML
	Fail    map[string]int                    // endpoint ("/key") -> error code

	mu    sync.Mutex
	Calls []string
}

// New starts a fake wrapper that is closed when the test ends.
func New(t testing.TB) *Server {
	s := &Server{
		M3U8:   map[string]string{},
		Keys:   map[string]applemusic.KeyTemplate{},
		Lyrics: map[string]string{},
		Fail:   map[string]int{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// CallLog returns a copy of the recorded calls.
func (s *Server) CallLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Calls...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	adamID := q.Get("adamId")

	s.mu.Lock()
	defer s.mu.Unlock()

	call := r.URL.Path
	switch r.URL.Path {
	case "/m3u8":
		call += "?adamId=" + adamID
	case "/key":
		call += "?adamId=" + adamID + "&uri=" + q.Get("uri")
	case "/lyrics":
		call += "?adamId=" + adamID + "&syllable=" + q.Get("syllable")
	}
	s.Calls = append(s.Calls, call)

	if code, ok := s.Fail[r.URL.Path]; ok {
		writeError(w, http.StatusOK, code, "fake failure")
		return
	}

	switch r.URL.Path {
	case "/status":
		regions := s.Regions
		if regions == nil {
			regions = []string{}
		}
		writeOK(w, map[string]any{"regions": regions})
	case "/m3u8":
		if adamID == "" {
			writeError(w, http.StatusOK, 400, "missing adamId")
			return
		}
		m, ok := s.M3U8[adamID]
		if !ok {
			writeError(w, http.StatusOK, 404, "failed to get m3u8")
			return
		}
		writeOK(w, map[string]string{"adamId": adamID, "m3u8": m})
	case "/key":
		uri := q.Get("uri")
		switch {
		case adamID == "":
			writeError(w, http.StatusOK, 400, "missing adamId")
			return
		case uri == "":
			writeError(w, http.StatusOK, 400, "missing uri")
			return
		case adamID != "0" && uri == applemusic.PrefetchKeyURI:
			writeError(w, http.StatusOK, 400, "invalid uri for adamId")
			return
		}
		kt, ok := s.Keys[adamID+"|"+uri]
		if !ok {
			writeError(w, http.StatusOK, 500, "key retrieval failed")
			return
		}
		writeOK(w, kt)
	case "/lyrics":
		if adamID == "" {
			writeError(w, http.StatusOK, 400, "missing adamId")
			return
		}
		l, ok := s.Lyrics[adamID]
		if !ok || l == "" {
			writeError(w, http.StatusOK, 404, "lyrics not found")
			return
		}
		writeOK(w, map[string]string{"adamId": adamID, "syllable": "false", "lyrics": l})
	default:
		writeError(w, http.StatusNotFound, 404, "not found")
	}
}

func writeOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "SUCCESS", "data": data})
}

func writeError(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": msg})
}
