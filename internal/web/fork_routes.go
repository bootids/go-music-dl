package web

// 本 fork（bootids/go-music-dl）新增的 Web 路由和处理函数，独立于上游 server.go，减少合并上游时的冲突。

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/go-music-dl/core"
	"github.com/guohuiyuan/music-lib/model"
)

// registerForkRoutes 注册 fork 新增的路由，由 StartWithOptions 调用。
func registerForkRoutes(api, configAPI *gin.RouterGroup) {
	api.GET("/fork.css", func(c *gin.Context) { c.FileFromFS("templates/static/css/fork.css", http.FS(templateFS)) })
	api.GET("/fork.js", func(c *gin.Context) { c.FileFromFS("templates/static/js/fork.js", http.FS(templateFS)) })
	configAPI.GET("/apple/wrapper/status", appleWrapperStatusHandler)
	configAPI.GET("/download/filename_preview", downloadFilenamePreviewHandler)
	RegisterLocalLyricRoutes(api, configAPI)
}

// settingsPayloadFor 返回 GET /settings 的内容。未登录的访客拿不到 wrapper 地址：
// wrapper-lite 没有鉴权，地址本身就等于账号的取密钥能力。
func settingsPayloadFor(c *gin.Context, disableAuth bool) webSettingsPayload {
	payload := publicWebSettingsPayload()
	if !disableAuth && !requestAuthenticated(c) {
		payload.AppleWrapperURL = ""
	}
	return payload
}

func requestAuthenticated(c *gin.Context) bool {
	settings, err := core.GetWebAuthSettings()
	if err != nil || !authConfigured(settings) {
		return false
	}
	value, err := c.Cookie(authCookieName)
	return err == nil && validateSessionValue(settings, value, time.Now())
}

// downloadFilenamePreviewSamples 是设置页“文件整理”预览用的示例歌曲：
// 一首普通专辑（没有音轨号），一首带综艺期数和音轨号的 Apple 专辑。
var downloadFilenamePreviewSamples = []struct {
	song model.Song
	ext  string
}{
	{song: model.Song{ID: "preview-1", Source: "qq", Name: "晴天", Artist: "周杰伦", Album: "叶惠美"}, ext: "flac"},
	{song: model.Song{ID: "preview-2", Source: "apple", Name: "示例歌曲", Artist: "示例歌手", Album: "声生不息·家年华 (第九期 live)", Extra: map[string]string{"tag_track": "7", "tag_track_total": "11"}}, ext: "m4a"},
}

// downloadFilenamePreviewHandler 按页面上尚未保存的模板和开关生成示例保存路径，
// 与真实下载共用 BuildDownloadFilenameWithOptions，预览与实际结果一致。
func downloadFilenamePreviewHandler(c *gin.Context) {
	opts := core.DownloadFilenameOptions{
		Template:                 c.Query("template"),
		OrganizeByArtistAlbum:    c.Query("organizeByArtistAlbum") == "1",
		CleanAlbumEpisode:        c.Query("cleanAlbumEpisode") == "1",
		NormalizeChineseVariants: c.Query("normalizeChineseVariants") == "1",
	}
	examples := make([]gin.H, 0, len(downloadFilenamePreviewSamples))
	for _, sample := range downloadFilenamePreviewSamples {
		song := sample.song
		path := filepath.ToSlash(core.BuildDownloadFilenameWithOptions(&song, sample.ext, opts))
		album := song.Album
		if opts.CleanAlbumEpisode {
			album = core.CleanAlbumEpisode(album)
		}
		examples = append(examples, gin.H{"album": song.Album, "albumTag": album, "path": path})
	}
	c.JSON(http.StatusOK, gin.H{"examples": examples})
}

// appleWrapperStatusHandler 检测 wrapper-lite 是否可达并已登录，同时报告 ffmpeg 是否可用。
// 带 url 参数时检测该地址（设置页里尚未保存的输入）；环境变量指定地址时忽略它。
func appleWrapperStatusHandler(c *gin.Context) {
	_, ffmpegErr := core.ResolveFFmpegPath()
	ffmpegOK := ffmpegErr == nil
	baseURL := core.AppleWrapperURL()
	if typed, ok := c.GetQuery("url"); ok && !core.AppleWrapperEnvOverride() {
		// 让服务端去请求调用方给的地址：只接受本站页面发起的请求，挡住跨站导航借管理员会话探测内网。
		if !allowSameOriginWrite(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		baseURL = strings.TrimSpace(typed)
		if baseURL != "" {
			if err := core.ValidateAppleWrapperURL(baseURL); err != nil {
				c.JSON(http.StatusOK, gin.H{"ok": false, "configured": false, "error": err.Error(), "ffmpeg": ffmpegOK})
				return
			}
		}
	}
	if baseURL == "" {
		msg := "未配置"
		if err := core.AppleWrapperEnvError(); err != nil {
			msg = err.Error()
		}
		c.JSON(http.StatusOK, gin.H{"ok": false, "configured": false, "error": msg, "ffmpeg": ffmpegOK})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	regions, err := core.NewAppleWrapperClientFor(baseURL).Status(ctx)
	payload := gin.H{"ok": err == nil, "configured": true, "regions": regions, "ffmpeg": ffmpegOK}
	if err != nil {
		payload["error"] = err.Error()
	}
	c.JSON(http.StatusOK, payload)
}
