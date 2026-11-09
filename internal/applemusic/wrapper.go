package applemusic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var adamIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

// Client talks to a wrapper-lite HTTP API.
type Client struct {
	baseURL string
	hc      *http.Client
}

// NewClient returns a client for baseURL. A nil hc uses a 60 s timeout.
func NewClient(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), hc: hc}
}

// KeyTemplate is the /key response.
type KeyTemplate struct {
	AdamID     string `json:"adamId"`
	KeyURI     string `json:"keyUri"`
	ContentKey string `json:"contentKey"`
	Ctx        string `json:"ctx"`
	State      string `json:"state"`
	Rcx        string `json:"rcx"`
	Rax        string `json:"rax"`
	Rdx        string `json:"rdx"`
	R9         string `json:"r9"`
	Rbp        string `json:"rbp"`
}

type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func getJSON[T any](ctx context.Context, c *Client, endpoint string, q url.Values) (T, error) {
	var zero T
	u := c.baseURL + "/" + endpoint
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return zero, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return zero, fmt.Errorf("apple wrapper unreachable at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, fmt.Errorf("apple wrapper /%s: read body: %w", endpoint, err)
	}

	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		if !ok {
			return zero, &APIError{Endpoint: endpoint, Code: resp.StatusCode, Msg: http.StatusText(resp.StatusCode)}
		}
		return zero, fmt.Errorf("apple wrapper /%s: decode response: %w", endpoint, err)
	}
	if env.Code != 0 {
		return zero, &APIError{Endpoint: endpoint, Code: env.Code, Msg: env.Msg}
	}
	if !ok {
		return zero, &APIError{Endpoint: endpoint, Code: resp.StatusCode, Msg: http.StatusText(resp.StatusCode)}
	}
	var data T
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			return zero, fmt.Errorf("apple wrapper /%s: decode data: %w", endpoint, err)
		}
	}
	return data, nil
}

func checkAdamID(adamID string) error {
	if !adamIDPattern.MatchString(adamID) {
		return fmt.Errorf("%w: %q", ErrInvalidAdamID, adamID)
	}
	return nil
}

// Status returns the storefront regions the wrapper can serve.
func (c *Client) Status(ctx context.Context) ([]string, error) {
	data, err := getJSON[struct {
		Regions []string `json:"regions"`
	}](ctx, c, "status", nil)
	if err != nil {
		return nil, err
	}
	if len(data.Regions) == 0 {
		return nil, ErrWrapperNotLoggedIn
	}
	return data.Regions, nil
}

// M3U8 returns the master playlist URL for a catalog song.
func (c *Client) M3U8(ctx context.Context, adamID string) (string, error) {
	if err := checkAdamID(adamID); err != nil {
		return "", err
	}
	data, err := getJSON[struct {
		M3U8 string `json:"m3u8"`
	}](ctx, c, "m3u8", url.Values{"adamId": {adamID}})
	if err != nil {
		return "", err
	}
	if data.M3U8 == "" {
		return "", fmt.Errorf("apple wrapper /m3u8: empty m3u8 for %s", adamID)
	}
	return data.M3U8, nil
}

// Key returns the decryption template for (adamID, uri). adamID "0" is
// accepted only with PrefetchKeyURI.
func (c *Client) Key(ctx context.Context, adamID, uri string) (*KeyTemplate, error) {
	if adamID == "0" {
		if uri != PrefetchKeyURI {
			return nil, fmt.Errorf("%w: adamId 0 requires the prefetch key uri", ErrInvalidAdamID)
		}
	} else if err := checkAdamID(adamID); err != nil {
		return nil, err
	}
	kt, err := getJSON[KeyTemplate](ctx, c, "key", url.Values{"adamId": {adamID}, "uri": {uri}})
	if err != nil {
		return nil, err
	}
	if kt.Ctx == "" || kt.State == "" {
		return nil, fmt.Errorf("%w for %s %s", ErrNoKeyTemplate, adamID, uri)
	}
	return &kt, nil
}

// Lyrics returns the line-timed TTML lyrics of a catalog song.
func (c *Client) Lyrics(ctx context.Context, adamID string) (string, error) {
	if err := checkAdamID(adamID); err != nil {
		return "", err
	}
	data, err := getJSON[struct {
		Lyrics string `json:"lyrics"`
	}](ctx, c, "lyrics", url.Values{"adamId": {adamID}, "syllable": {"0"}})
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
			return "", fmt.Errorf("%w: %w", ErrLyricsNotFound, err)
		}
		return "", err
	}
	return data.Lyrics, nil
}
