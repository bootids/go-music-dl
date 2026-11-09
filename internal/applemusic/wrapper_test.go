package applemusic_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guohuiyuan/go-music-dl/internal/applemusic"
	"github.com/guohuiyuan/go-music-dl/internal/applemusic/applemusictest"
)

func newClient(t *testing.T) (*applemusictest.Server, *applemusic.Client) {
	t.Helper()
	fake := applemusictest.New(t)
	return fake, applemusic.NewClient(fake.URL+"/", nil)
}

func TestWrapperAPIErrorOnHTTP200(t *testing.T) {
	fake, c := newClient(t)
	fake.M3U8["123"] = "https://example.invalid/master.m3u8"
	fake.Fail["/m3u8"] = 500
	_, err := c.M3U8(context.Background(), "123")
	var apiErr *applemusic.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %v", err)
	}
	if apiErr.Code != 500 || apiErr.Msg != "fake failure" || apiErr.Endpoint != "m3u8" {
		t.Fatalf("unexpected APIError %+v", apiErr)
	}
	if got := apiErr.Error(); got != "apple wrapper /m3u8: code 500: fake failure" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestWrapperStatus(t *testing.T) {
	fake, c := newClient(t)
	if _, err := c.Status(context.Background()); !errors.Is(err, applemusic.ErrWrapperNotLoggedIn) {
		t.Fatalf("empty regions: want ErrWrapperNotLoggedIn, got %v", err)
	}
	fake.Regions = []string{"143441"}
	regions, err := c.Status(context.Background())
	if err != nil || len(regions) != 1 || regions[0] != "143441" {
		t.Fatalf("Status = %v, %v", regions, err)
	}
}

func TestWrapperM3U8(t *testing.T) {
	fake, c := newClient(t)
	fake.M3U8["123"] = ""
	if _, err := c.M3U8(context.Background(), "123"); err == nil {
		t.Fatal("empty m3u8: want error")
	}
	fake.M3U8["123"] = "https://example.invalid/master.m3u8"
	got, err := c.M3U8(context.Background(), "123")
	if err != nil || got != "https://example.invalid/master.m3u8" {
		t.Fatalf("M3U8 = %q, %v", got, err)
	}
}

func TestWrapperKey(t *testing.T) {
	fake, c := newClient(t)
	ctx := context.Background()
	uri := "skd://itunes.apple.com/P000000000/s1/e2?a=b&c=d e+f"
	fake.Keys["123|"+uri] = applemusic.KeyTemplate{AdamID: "123", KeyURI: uri, ContentKey: "ck"}
	if _, err := c.Key(ctx, "123", uri); !errors.Is(err, applemusic.ErrNoKeyTemplate) {
		t.Fatalf("missing ctx: want ErrNoKeyTemplate, got %v", err)
	}
	if calls := fake.CallLog(); calls[len(calls)-1] != "/key?adamId=123&uri="+uri {
		t.Fatalf("uri did not round-trip: %q", calls[len(calls)-1])
	}

	fake.Keys["123|"+uri] = applemusic.KeyTemplate{AdamID: "123", KeyURI: uri, Ctx: "Y3R4", State: "c3Q=", Rcx: "0x1"}
	kt, err := c.Key(ctx, "123", uri)
	if err != nil || kt.KeyURI != uri || kt.Ctx != "Y3R4" || kt.Rcx != "0x1" {
		t.Fatalf("Key = %+v, %v", kt, err)
	}

	fake.Keys["0|"+applemusic.PrefetchKeyURI] = applemusic.KeyTemplate{Ctx: "Y3R4", State: "c3Q="}
	if _, err := c.Key(ctx, "0", applemusic.PrefetchKeyURI); err != nil {
		t.Fatalf("prefetch key: %v", err)
	}
	if _, err := c.Key(ctx, "0", uri); !errors.Is(err, applemusic.ErrInvalidAdamID) {
		t.Fatalf("adamId 0 with other uri: want ErrInvalidAdamID, got %v", err)
	}
}

func TestWrapperInvalidAdamID(t *testing.T) {
	fake, c := newClient(t)
	ctx := context.Background()
	for _, id := range []string{"i.abc", "", "0123", "12a"} {
		if _, err := c.M3U8(ctx, id); !errors.Is(err, applemusic.ErrInvalidAdamID) {
			t.Fatalf("M3U8(%q): want ErrInvalidAdamID, got %v", id, err)
		}
	}
	if _, err := c.Lyrics(ctx, "i.abc"); !errors.Is(err, applemusic.ErrInvalidAdamID) {
		t.Fatalf("Lyrics: want ErrInvalidAdamID, got %v", err)
	}
	if n := len(fake.CallLog()); n != 0 {
		t.Fatalf("invalid ids reached the wrapper %d times", n)
	}
}

func TestWrapperLyrics(t *testing.T) {
	fake, c := newClient(t)
	ctx := context.Background()
	if _, err := c.Lyrics(ctx, "123"); !errors.Is(err, applemusic.ErrLyricsNotFound) {
		t.Fatalf("want ErrLyricsNotFound, got %v", err)
	}
	fake.Lyrics["123"] = "<tt/>"
	got, err := c.Lyrics(ctx, "123")
	if err != nil || got != "<tt/>" {
		t.Fatalf("Lyrics = %q, %v", got, err)
	}
	if calls := fake.CallLog(); calls[len(calls)-1] != "/lyrics?adamId=123&syllable=0" {
		t.Fatalf("unexpected call %q", calls[len(calls)-1])
	}
}

func TestWrapperUnreachable(t *testing.T) {
	fake := applemusictest.New(t)
	base := fake.URL
	fake.Close()
	_, err := applemusic.NewClient(base, nil).Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("want unreachable error, got %v", err)
	}
	// The message reaches anonymous /download callers: no wrapper address.
	host := strings.TrimPrefix(base, "http://")
	hostname, _, _ := strings.Cut(host, ":")
	if strings.Contains(err.Error(), hostname) {
		t.Fatalf("error leaks wrapper address: %v", err)
	}
}

func TestWrapperNotWrapperJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := applemusic.NewClient(srv.URL, nil)
	if _, err := c.Status(context.Background()); !errors.Is(err, applemusic.ErrNotWrapper) {
		t.Fatalf("Status: want ErrNotWrapper, got %v", err)
	}
	if _, err := c.M3U8(context.Background(), "123"); !errors.Is(err, applemusic.ErrNotWrapper) {
		t.Fatalf("M3U8: want ErrNotWrapper, got %v", err)
	}
}
