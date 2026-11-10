package core

// 本 fork（bootids/go-music-dl）新增：下载 Apple Music 歌曲时，从公开的 iTunes Lookup 接口（不需要 token）
// 查曲序、碟序、专辑发行日期和专辑艺人，写进文件标签，让 Navidrome 等播放器按专辑正确排序。
// 其他来源拿不到逐曲的曲序、碟序，不处理。独立于上游 download.go / service.go，减少合并上游时的冲突。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guohuiyuan/music-lib/model"
)

// itunesLookupBaseURL 是 iTunes Lookup 接口地址，测试里替换成 httptest 服务。
var itunesLookupBaseURL = "https://itunes.apple.com/lookup"

const itunesLookupTimeout = 5 * time.Second

// 写进 song.Extra 的标签键，只由本文件填写；带 tag_ 前缀，避免和 music-lib 自带的
// release_date（Apple 给的是单曲首发日期）、track_number（Spotify）等键混用。
const (
	extraTagTrack       = "tag_track"
	extraTagTrackTotal  = "tag_track_total"
	extraTagDisc        = "tag_disc"
	extraTagDiscTotal   = "tag_disc_total"
	extraTagDate        = "tag_date"
	extraTagAlbumArtist = "tag_album_artist"
)

// appleLookupFallbackStorefronts 排在 cookie 里的 storefront 之后依次尝试；
// 只在国区上架的中文歌在美区查不到，所以 cn 在前。
var appleLookupFallbackStorefronts = []string{"cn", "us", "jp"}

// appleAlbumDateCache 按专辑 collectionId 缓存发行日期，同一专辑的多首歌只查一次专辑。
var appleAlbumDateCache = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// forkExtraTags 是 EmbedSongMetadata 交给 embedAudioMetadataByFFmpeg 的额外标签。
type forkExtraTags struct {
	AlbumArtist       string
	Track, TrackTotal int
	Disc, DiscTotal   int
	Date              string // YYYY-MM-DD
}

// forkExtraTagsForSong 是 EmbedSongMetadata 的钩子：专辑艺人见 albumArtistForSong，
// 曲序、碟序、日期只取 fillAppleTrackTags 填进 song.Extra 的值，没有就不写。
func forkExtraTagsForSong(song *model.Song) forkExtraTags {
	tags := forkExtraTags{AlbumArtist: albumArtistForSong(song)}
	if song == nil {
		return tags
	}
	tags.Track, _ = strconv.Atoi(song.Extra[extraTagTrack])
	tags.TrackTotal, _ = strconv.Atoi(song.Extra[extraTagTrackTotal])
	tags.Disc, _ = strconv.Atoi(song.Extra[extraTagDisc])
	tags.DiscTotal, _ = strconv.Atoi(song.Extra[extraTagDiscTotal])
	tags.Date = strings.TrimSpace(song.Extra[extraTagDate])
	return tags
}

// ffmpegArgs 是 embedAudioMetadataByFFmpeg 的钩子：追加专辑艺人、曲序（N/T）、碟序（N/T）和日期参数。
func (t forkExtraTags) ffmpegArgs(ext string) []string {
	args := albumArtistFFmpegArgs(ext, t.AlbumArtist)
	if t.Track > 0 {
		args = append(args, "-metadata", "track="+numberOfTotal(t.Track, t.TrackTotal))
	}
	if t.Disc > 0 {
		args = append(args, "-metadata", "disc="+numberOfTotal(t.Disc, t.DiscTotal))
	}
	if t.Date != "" {
		args = append(args, "-metadata", "date="+t.Date)
	}
	return args
}

func numberOfTotal(n, total int) string {
	if total > 0 {
		return fmt.Sprintf("%d/%d", n, total)
	}
	return strconv.Itoa(n)
}

// fillAppleTrackTags 是 downloadSongDataWithTemplate 的钩子：Apple 歌曲查 iTunes Lookup，
// 把曲序、碟序、专辑发行日期和专辑艺人写进 song.Extra 的一份拷贝（不改调用方共用的 map）。
// 尽力而为：查不到或出错就什么都不写，不影响下载，也不提示用户。
func fillAppleTrackTags(ctx context.Context, song *model.Song, artistAliases string) {
	if song == nil || song.Source != "apple" {
		return
	}
	songID := strings.TrimSpace(song.ID)
	if _, err := strconv.ParseInt(songID, 10, 64); err != nil {
		return
	}

	var track itunesLookupItem
	storefront := ""
	for _, sf := range appleLookupStorefronts(song) {
		item, err := itunesLookup(ctx, songID, sf, "track")
		if err != nil {
			if errors.Is(err, errItunesLookupUnreachable) {
				return
			}
			continue
		}
		if item != nil {
			track, storefront = *item, sf
			break
		}
	}
	if storefront == "" {
		return
	}

	extra := make(map[string]string, len(song.Extra)+6)
	for k, v := range song.Extra {
		extra[k] = v
	}
	if track.TrackNumber > 0 {
		extra[extraTagTrack] = strconv.Itoa(track.TrackNumber)
		if track.TrackCount > 0 {
			extra[extraTagTrackTotal] = strconv.Itoa(track.TrackCount)
		}
	}
	if track.DiscNumber > 0 {
		extra[extraTagDisc] = strconv.Itoa(track.DiscNumber)
		if track.DiscCount > 0 {
			extra[extraTagDiscTotal] = strconv.Itoa(track.DiscCount)
		}
	}
	if name := strings.TrimSpace(track.CollectionArtistName); name != "" {
		extra[extraTagAlbumArtist] = ApplyArtistAlias(name, artistAliases)
	}
	// 歌曲自己的 releaseDate 是单曲首发日期，同一专辑的歌会散落在不同年份，所以只写专辑的发行日期。
	if date := appleAlbumReleaseDate(ctx, track.CollectionID, storefront); date != "" {
		extra[extraTagDate] = date
	}
	song.Extra = extra
}

// appleLookupStorefronts 依次是：歌曲自带的 storefront（排行榜歌曲有）、Apple cookie 里的 storefront、cn、us、jp，去重。
func appleLookupStorefronts(song *model.Song) []string {
	candidates := []string{song.Extra["storefront"], appleCookieStorefront(CM.Get("apple"))}
	candidates = append(candidates, appleLookupFallbackStorefronts...)
	var out []string
	seen := map[string]bool{}
	for _, sf := range candidates {
		sf = strings.ToLower(strings.TrimSpace(sf))
		if sf != "" && !seen[sf] {
			seen[sf] = true
			out = append(out, sf)
		}
	}
	return out
}

// appleCookieStorefront 从 "token=xxx;media-user-token=xxx;storefront=cn" 这类 cookie 里取 storefront，与 music-lib 的写法一致。
func appleCookieStorefront(cookie string) string {
	for _, part := range strings.Split(cookie, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.TrimSpace(key) == "storefront" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func appleAlbumReleaseDate(ctx context.Context, collectionID int64, storefront string) string {
	if collectionID <= 0 {
		return ""
	}
	key := strconv.FormatInt(collectionID, 10)
	appleAlbumDateCache.Lock()
	date, ok := appleAlbumDateCache.m[key]
	appleAlbumDateCache.Unlock()
	if ok {
		return date
	}
	album, err := itunesLookup(ctx, key, storefront, "collection")
	if err != nil || album == nil {
		return ""
	}
	date = itunesDate(album.ReleaseDate)
	if date == "" {
		return ""
	}
	appleAlbumDateCache.Lock()
	appleAlbumDateCache.m[key] = date
	appleAlbumDateCache.Unlock()
	return date
}

// itunesDate 把 "2016-09-28T07:00:00Z" 取成 "2016-09-28"，格式不对返回空串。
func itunesDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) < 10 {
		return ""
	}
	if _, err := time.Parse("2006-01-02", raw[:10]); err != nil {
		return ""
	}
	return raw[:10]
}

type itunesLookupItem struct {
	WrapperType          string `json:"wrapperType"`
	TrackNumber          int    `json:"trackNumber"`
	TrackCount           int    `json:"trackCount"`
	DiscNumber           int    `json:"discNumber"`
	DiscCount            int    `json:"discCount"`
	ReleaseDate          string `json:"releaseDate"`
	CollectionID         int64  `json:"collectionId"`
	CollectionArtistName string `json:"collectionArtistName"`
}

// errItunesLookupUnreachable 表示请求没发出去或超时，换 storefront 也没用，直接放弃。
var errItunesLookupUnreachable = errors.New("itunes lookup unreachable")

// itunesLookup 按 id 和 storefront 查一次，返回第一条 wrapperType 匹配的结果；查不到返回 nil, nil。
func itunesLookup(ctx context.Context, id, storefront, wrapperType string) (*itunesLookupItem, error) {
	ctx, cancel := context.WithTimeout(ctx, itunesLookupTimeout)
	defer cancel()
	query := url.Values{"id": {id}, "country": {storefront}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, itunesLookupBaseURL+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errItunesLookupUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("itunes lookup status %d", resp.StatusCode)
	}
	var body struct {
		Results []itunesLookupItem `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	for i := range body.Results {
		if body.Results[i].WrapperType == wrapperType {
			return &body.Results[i], nil
		}
	}
	return nil, nil
}
