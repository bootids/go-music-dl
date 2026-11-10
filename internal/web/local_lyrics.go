package web

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/go-music-dl/core"
	"github.com/guohuiyuan/music-lib/model"
)

// 本地音乐“歌词检查”：识别逐行/逐字/无歌词，从 QQ 音乐找逐字歌词并写回文件。

type localLyricKindEntry struct {
	size    int64
	modTime time.Time
	kind    string
}

var (
	localLyricKindMu    sync.Mutex
	localLyricKindCache = map[string]localLyricKindEntry{}
	// 便于测试替换 QQ 搜索与取歌词。
	localLyricSearchQQ = core.SearchQQForLyrics
	localLyricFetchQQ  = core.FetchQQVerbatimLyric
)

// localTrackLyricKind 读取（内嵌优先、其次同名 .lrc）并判断歌词类型，按大小+修改时间缓存。
func localTrackLyricKind(track *localMusicTrack) string {
	if track == nil {
		return core.LyricKindNone
	}
	if track.Extra != nil && track.Extra["lyric"] != "true" {
		return core.LyricKindNone
	}
	localLyricKindMu.Lock()
	entry, ok := localLyricKindCache[track.absPath]
	localLyricKindMu.Unlock()
	if ok && entry.size == track.Size && entry.modTime.Equal(track.modTime) && !localLyricSidecarNewer(track, entry.modTime) {
		return entry.kind
	}
	lyric, err := readLocalMusicLyrics(track.absPath)
	kind := core.LyricKindNone
	if err == nil {
		kind = core.DetectLyricKind(lyric)
	}
	localLyricKindMu.Lock()
	localLyricKindCache[track.absPath] = localLyricKindEntry{size: track.Size, modTime: track.modTime, kind: kind}
	localLyricKindMu.Unlock()
	return kind
}

// 只换了同名 .lrc 时音频文件的修改时间不变，按 .lrc 的修改时间判断缓存是否过期。
func localLyricSidecarNewer(track *localMusicTrack, since time.Time) bool {
	path, _, ok := localMusicSidecarFile(track.absPath, localMusicLyricExts)
	if !ok {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.ModTime().After(since)
}

type localLyricCheckTrack struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Duration int    `json:"duration"`
	Ext      string `json:"ext"`
	RelPath  string `json:"rel_path"`
	Kind     string `json:"kind"`
	Writable bool   `json:"writable"`
}

type localLyricCandidate struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Artist   string            `json:"artist"`
	Album    string            `json:"album"`
	Duration int               `json:"duration"`
	Cover    string            `json:"cover"`
	Extra    map[string]string `json:"extra"`
	Diff     *int              `json:"diff"`
	Match    bool              `json:"match"`
}

func localLyricWritableExt(ext string) bool {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "mp3", "flac", "m4a":
		return true
	}
	return false
}

// localTrackDuration 优先用扫描结果，没有时现场 ffprobe（变量便于测试替换）。
var localTrackDuration = defaultLocalTrackDuration

func defaultLocalTrackDuration(track *localMusicTrack) int {
	if track == nil {
		return 0
	}
	if track.Duration > 0 {
		return track.Duration
	}
	if probe, err := probeLocalMusicTrack(track); err == nil && probe != nil {
		return probe.Duration
	}
	return 0
}

func localLyricDurationDiff(local, remote int) (*int, bool) {
	if local <= 0 || remote <= 0 {
		return nil, false
	}
	diff := remote - local
	abs := diff
	if abs < 0 {
		abs = -abs
	}
	return &diff, abs <= core.LyricDurationTolerance
}

func RegisterLocalLyricRoutes(api *gin.RouterGroup, configAPI *gin.RouterGroup) {
	api.GET("/local_music/lyric_check", func(c *gin.Context) {
		tracks, _, exists, err, _, _ := scanLocalMusicTracksCached(c.Query("refresh") == "1")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		counts := map[string]int{core.LyricKindVerbatim: 0, core.LyricKindLine: 0, core.LyricKindPlain: 0, core.LyricKindNone: 0}
		items := make([]localLyricCheckTrack, 0, len(tracks))
		for _, t := range tracks {
			track, err := localMusicTrackByID(t.ID)
			if err != nil {
				continue
			}
			kind := localTrackLyricKind(track)
			counts[kind]++
			items = append(items, localLyricCheckTrack{
				ID: track.ID, Name: track.Name, Artist: track.Artist, Album: track.Album,
				Duration: track.Duration, Ext: track.Ext, RelPath: track.RelPath, Kind: kind,
				Writable: localLyricWritableExt(track.Ext),
			})
		}
		c.JSON(http.StatusOK, gin.H{"exists": exists, "total": len(items), "counts": counts, "tracks": items})
	})

	api.GET("/local_music/lyric_candidates", func(c *gin.Context) {
		track, err := localMusicTrackByID(c.Query("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "本地歌曲不存在"})
			return
		}
		keyword := strings.TrimSpace(c.Query("q"))
		if keyword == "" {
			keyword = strings.TrimSpace(track.Name + " " + strings.TrimPrefix(track.Artist, "未知歌手"))
		}
		localDuration := localTrackDuration(track)
		local := gin.H{"id": track.ID, "name": track.Name, "artist": track.Artist, "album": track.Album, "duration": localDuration, "kind": localTrackLyricKind(track), "writable": localLyricWritableExt(track.Ext)}
		songs, err := localLyricSearchQQ(keyword)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "QQ 音乐搜索失败: " + err.Error(), "local": local, "keyword": keyword})
			return
		}
		candidates := make([]localLyricCandidate, 0, len(songs))
		for _, s := range songs {
			diff, match := localLyricDurationDiff(localDuration, s.Duration)
			candidates = append(candidates, localLyricCandidate{
				ID: s.ID, Name: s.Name, Artist: s.Artist, Album: s.Album, Duration: s.Duration,
				Cover: s.Cover, Extra: s.Extra, Diff: diff, Match: match,
			})
		}
		c.JSON(http.StatusOK, gin.H{"local": local, "keyword": keyword, "tolerance": core.LyricDurationTolerance, "candidates": candidates})
	})

	api.POST("/local_music/lyric_preview", func(c *gin.Context) {
		var req localLyricCandidate
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid candidate"})
			return
		}
		lyric, err := localLyricFetchQQ(localLyricCandidateSong(req))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"kind": core.DetectLyricKind(lyric), "lyric": core.EnhancedToInlineVerbatim(lyric)})
	})

	configAPI.POST("/local_music/lyric_apply", func(c *gin.Context) {
		if !allowSameOriginWrite(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		var req struct {
			ID        string              `json:"id"`
			Candidate localLyricCandidate `json:"candidate"`
			Force     bool                `json:"force"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		track, err := localMusicTrackByID(req.ID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "本地歌曲不存在"})
			return
		}
		if !localLyricWritableExt(track.Ext) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "暂不支持给 " + track.Ext + " 文件写入内嵌歌词"})
			return
		}
		localDuration := localTrackDuration(track)
		diff, match := localLyricDurationDiff(localDuration, req.Candidate.Duration)
		if !match && !req.Force {
			c.JSON(http.StatusConflict, gin.H{"error": "duration_mismatch", "local_duration": localDuration, "remote_duration": req.Candidate.Duration, "diff": diff})
			return
		}
		lyric, err := localLyricFetchQQ(localLyricCandidateSong(req.Candidate))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if core.DetectLyricKind(lyric) != core.LyricKindVerbatim {
			c.JSON(http.StatusBadGateway, gin.H{"error": "QQ 音乐的这首歌没有逐字歌词"})
			return
		}
		sidecar, _, hasSidecar := localMusicSidecarFile(track.absPath, []string{".lrc"})
		if !hasSidecar {
			sidecar = ""
		}
		if err := core.ReplaceLocalSongLyric(track.absPath, lyric, sidecar); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, core.ErrFFmpegNotFound) {
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": "写入歌词失败: " + err.Error()})
			return
		}
		updated := refreshLocalMusicTrackAfterWrite(track)
		c.JSON(http.StatusOK, gin.H{
			"status":         "ok",
			"kind":           localTrackLyricKind(updated),
			"sidecar":        hasSidecar,
			"sidecar_backup": hasSidecar,
			"forced":         !match,
		})
	})
}

func localLyricCandidateSong(c localLyricCandidate) *model.Song {
	return &model.Song{ID: c.ID, Source: "qq", Name: c.Name, Artist: c.Artist, Album: c.Album, Duration: c.Duration, Extra: c.Extra}
}

// refreshLocalMusicTrackAfterWrite 写回后重建这首歌的元数据，并刷新索引与扫描缓存。
func refreshLocalMusicTrackAfterWrite(track *localMusicTrack) *localMusicTrack {
	rebuilt, err := localMusicTrackByID(track.ID)
	if err != nil {
		return track
	}
	upsertLocalMusicIndexRow(rebuilt)
	invalidateLocalMusicScanCache()
	return rebuilt
}
