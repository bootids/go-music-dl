package applemusic

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/grafov/m3u8"
)

// Quality selects the codec family PickVariant prefers.
type Quality string

const (
	QualityALAC Quality = "alac"
	QualityAAC  Quality = "aac"
)

// Variant is one EXT-X-STREAM-INF entry of an Apple Music master playlist.
type Variant struct {
	URI, Codec, Group    string
	SampleRate, BitDepth int
	Bandwidth            uint32
}

var alacGroupRe = regexp.MustCompile(`^audio-alac-stereo-(\d+)-(\d+)$`)

// ParseMaster decodes a master playlist and resolves every variant URI against base.
func ParseMaster(body []byte, base *url.URL) ([]Variant, error) {
	pl, typ, err := m3u8.DecodeFrom(bytes.NewReader(body), false)
	if err != nil {
		return nil, fmt.Errorf("%w: decode master: %v", ErrUnsupportedPlaylist, err)
	}
	if typ != m3u8.MASTER {
		return nil, fmt.Errorf("%w: not a master playlist", ErrUnsupportedPlaylist)
	}
	master := pl.(*m3u8.MasterPlaylist)
	out := make([]Variant, 0, len(master.Variants))
	for _, v := range master.Variants {
		if v == nil {
			continue
		}
		u, err := resolveURI(base, v.URI)
		if err != nil {
			return nil, err
		}
		vr := Variant{URI: u, Codec: v.Codecs, Group: v.Audio, Bandwidth: v.AverageBandwidth}
		if vr.Bandwidth == 0 {
			vr.Bandwidth = v.Bandwidth
		}
		if m := alacGroupRe.FindStringSubmatch(v.Audio); m != nil {
			vr.SampleRate, _ = strconv.Atoi(m[1])
			vr.BitDepth, _ = strconv.Atoi(m[2])
		}
		out = append(out, vr)
	}
	return out, nil
}

// PickVariant chooses the best variant for pref. For QualityALAC it takes the
// highest-bandwidth ALAC variant whose sample rate is at most alacMaxRate
// (alacMaxRate <= 0 means no cap) and falls back to AAC. Dolby (ec-3/ac-3)
// variants are never picked.
func PickVariant(vs []Variant, pref Quality, alacMaxRate int) (Variant, error) {
	sorted := append([]Variant(nil), vs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Bandwidth > sorted[j].Bandwidth })

	if pref == QualityALAC {
		for _, v := range sorted {
			if v.Codec == "alac" && (alacMaxRate <= 0 || v.SampleRate <= alacMaxRate) {
				return v, nil
			}
		}
	}
	var firstAAC *Variant
	for i, v := range sorted {
		if v.Codec != "mp4a.40.2" {
			continue
		}
		if v.Group == "audio-stereo-256" {
			return v, nil
		}
		if firstAAC == nil {
			firstAAC = &sorted[i]
		}
	}
	if firstAAC != nil {
		return *firstAAC, nil
	}
	labels := make([]string, 0, len(sorted))
	for _, v := range sorted {
		labels = append(labels, v.Codec+"/"+v.Group)
	}
	return Variant{}, fmt.Errorf("%w: available: %s", ErrNoUsableVariant, strings.Join(labels, ", "))
}

// Segment is one byte range of the single media file, with the key URI in
// effect for it (carried forward from earlier segments when omitted).
type Segment struct {
	Offset, Length int64
	KeyURI         string
}

// MediaPlaylist is a single-file byte-range media playlist.
type MediaPlaylist struct {
	FileURL  string
	Segments []Segment
}

// ParseMediaPlaylist decodes an Apple Music media playlist. Only FairPlay
// (streamingkeydelivery) keys are kept; every segment must be a byte range of
// the same file.
func ParseMediaPlaylist(body []byte, base *url.URL) (*MediaPlaylist, error) {
	filtered, err := filterKeyLines(body)
	if err != nil {
		return nil, err
	}
	pl, typ, err := m3u8.DecodeFrom(bytes.NewReader(filtered), false)
	if err != nil {
		return nil, fmt.Errorf("%w: decode media: %v", ErrUnsupportedPlaylist, err)
	}
	if typ != m3u8.MEDIA {
		return nil, fmt.Errorf("%w: not a media playlist", ErrUnsupportedPlaylist)
	}
	media := pl.(*m3u8.MediaPlaylist)

	out := &MediaPlaylist{}
	var fileURI, keyURI string
	for i, seg := range media.Segments {
		if seg == nil {
			break
		}
		if seg.Limit <= 0 {
			return nil, fmt.Errorf("%w: segment %d has no byte range", ErrUnsupportedPlaylist, i)
		}
		if i == 0 {
			fileURI = seg.URI
		} else if seg.URI != fileURI {
			return nil, fmt.Errorf("%w: segment %d uses a different file", ErrUnsupportedPlaylist, i)
		}
		if seg.Key != nil && seg.Key.URI != "" {
			keyURI = seg.Key.URI
		}
		if keyURI == "" {
			return nil, fmt.Errorf("%w: segment %d has no FairPlay key", ErrUnsupportedPlaylist, i)
		}
		out.Segments = append(out.Segments, Segment{Offset: seg.Offset, Length: seg.Limit, KeyURI: keyURI})
	}
	if len(out.Segments) == 0 {
		return nil, fmt.Errorf("%w: no segments", ErrUnsupportedPlaylist)
	}
	if out.FileURL, err = resolveURI(base, fileURI); err != nil {
		return nil, err
	}
	return out, nil
}

// filterKeyLines drops #EXT-X-KEY lines that are not FairPlay streaming keys.
func filterKeyLines(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if bytes.HasPrefix(line, []byte("#EXT-X-KEY:")) && !bytes.Contains(line, []byte("streamingkeydelivery")) {
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedPlaylist, err)
	}
	return buf.Bytes(), nil
}

func resolveURI(base *url.URL, ref string) (string, error) {
	u, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("%w: bad URI %q: %v", ErrUnsupportedPlaylist, ref, err)
	}
	if base == nil {
		return u.String(), nil
	}
	return base.ResolveReference(u).String(), nil
}
