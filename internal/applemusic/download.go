package applemusic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/guohuiyuan/go-music-dl/internal/applemusic/temari"
)

// cdnIdleTimeout cancels the media download when a read makes no progress.
const cdnIdleTimeout = 30 * time.Second

// DownloadOptions configures Download.
type DownloadOptions struct {
	Client      *Client
	HTTP        *http.Client // CDN client; nil uses http.DefaultClient
	AdamID      string
	Quality     Quality
	ALACMaxRate int
	// NewDecrypter turns a /key response into a sample decrypter. nil uses temari.
	NewDecrypter func(*KeyTemplate) (SampleDecrypter, error)
}

// Result is a clear fragmented MP4.
type Result struct {
	Data  []byte
	Codec string
	Label string // e.g. "ALAC 24bit/192kHz", "AAC 256"
}

// Download fetches, decrypts and returns the full track for o.AdamID.
func Download(ctx context.Context, o DownloadOptions) (*Result, error) {
	if o.Client == nil {
		return nil, errors.New("apple music: no wrapper client")
	}
	hc := o.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	newDec := o.NewDecrypter
	if newDec == nil {
		newDec = temariDecrypter
	}

	masterURL, err := o.Client.M3U8(ctx, o.AdamID)
	if err != nil {
		return nil, err
	}
	body, base, err := fetchPlaylist(ctx, hc, masterURL)
	if err != nil {
		return nil, err
	}
	variants, err := ParseMaster(body, base)
	if err != nil {
		return nil, err
	}
	v, err := PickVariant(variants, o.Quality, o.ALACMaxRate)
	if err != nil {
		return nil, err
	}
	body, base, err = fetchPlaylist(ctx, hc, v.URI)
	if err != nil {
		return nil, err
	}
	media, err := ParseMediaPlaylist(body, base)
	if err != nil {
		return nil, err
	}

	keys := func(ctx context.Context, keyURI string) (SampleDecrypter, error) {
		adamID := o.AdamID
		if keyURI == PrefetchKeyURI {
			adamID = "0"
		}
		kt, err := o.Client.Key(ctx, adamID, keyURI)
		if err != nil {
			return nil, err
		}
		return newDec(kt)
	}

	var out bytes.Buffer
	if err := downloadMedia(ctx, hc, media, &out, keys); err != nil {
		return nil, err
	}
	return &Result{Data: out.Bytes(), Codec: v.Codec, Label: variantLabel(v)}, nil
}

func temariDecrypter(kt *KeyTemplate) (SampleDecrypter, error) {
	return temari.New(kt.Ctx, kt.State, temari.Registers{RCX: kt.Rcx, RAX: kt.Rax, RDX: kt.Rdx, R9: kt.R9, RBP: kt.Rbp})
}

func fetchPlaylist(ctx context.Context, hc *http.Client, rawURL string) ([]byte, *url.URL, error) {
	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("apple music: bad playlist URL %q: %w", rawURL, err)
	}
	resp, err := cdnGet(ctx, hc, rawURL)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("apple music: read playlist: %w", err)
	}
	return body, base, nil
}

func cdnGet(ctx context.Context, hc *http.Client, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apple music: GET %s: %w", redactURL(req.URL), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("apple music: GET %s: HTTP %s", redactURL(req.URL), resp.Status)
	}
	return resp, nil
}

// redactURL drops the query, which may carry CDN tokens.
func redactURL(u *url.URL) string {
	c := *u
	c.RawQuery = ""
	return c.String()
}

// downloadMedia streams the whole media file once from offset 0 and decrypts it.
func downloadMedia(ctx context.Context, hc *http.Client, media *MediaPlaylist, out io.Writer, keys KeySource) error {
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := cdnGet(reqCtx, hc, media.FileURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	ir := newIdleReader(resp.Body, cdnIdleTimeout, cancel)
	defer ir.stop()
	if err := DecryptStream(ctx, ir, out, media.Segments, keys); err != nil {
		if ir.timedOut() {
			return fmt.Errorf("apple music: media download stalled for %s: %w", cdnIdleTimeout, err)
		}
		return err
	}
	return nil
}

// idleReader calls cancel when no Read makes progress for d.
type idleReader struct {
	r     io.Reader
	d     time.Duration
	timer *time.Timer
	fired atomic.Bool
}

func newIdleReader(r io.Reader, d time.Duration, cancel context.CancelFunc) *idleReader {
	ir := &idleReader{r: r, d: d}
	ir.timer = time.AfterFunc(d, func() {
		ir.fired.Store(true)
		cancel()
	})
	return ir
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.timer.Reset(ir.d)
	}
	return n, err
}

func (ir *idleReader) stop()          { ir.timer.Stop() }
func (ir *idleReader) timedOut() bool { return ir.fired.Load() }

func variantLabel(v Variant) string {
	if v.Codec == "alac" {
		khz := strconv.FormatFloat(float64(v.SampleRate)/1000, 'f', -1, 64)
		return fmt.Sprintf("ALAC %dbit/%skHz", v.BitDepth, khz)
	}
	if rate, ok := strings.CutPrefix(v.Group, "audio-stereo-"); ok {
		return "AAC " + rate
	}
	return "AAC"
}
