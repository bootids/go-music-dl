package web

// 本 fork（bootids/go-music-dl）在 music.go 下载与探测流程里的 Apple Music 分支，独立成文件以减少合并上游时的冲突。

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/guohuiyuan/go-music-dl/core"
	"github.com/guohuiyuan/music-lib/model"
)

// adjustAppleInspectPayload 是 inspectSuccessPayload 的钩子：未配置 wrapper 的 Apple 歌曲只有 30 秒试听；
// 配置 wrapper 后探测的仍是试听，大小和码率都不代表 wrapper 下载的整曲。
func adjustAppleInspectPayload(source string, previewOnly bool, size, bitrate string) (bool, string, string) {
	if !strings.EqualFold(strings.TrimSpace(source), "apple") {
		return previewOnly, size, bitrate
	}
	if !core.AppleWrapperConfigured() {
		return true, size, bitrate
	}
	return previewOnly, "-", "-"
}

// downloadQueryDuration 读取 /download 的 duration 参数（秒）。
// 时长用于识别配置 wrapper 前存下的 Apple 30 秒试听，缺失或无效时按 0（未知）处理。
func downloadQueryDuration(c *gin.Context) int {
	if d, err := strconv.Atoi(c.Query("duration")); err == nil && d > 0 {
		return d
	}
	return 0
}

// serveAppleWrapperDownload 在配置了 wrapper 时通过 wrapper 下载 Apple 整曲并直接返回给浏览器。
// 已处理请求时返回 true，调用方应直接返回。
func serveAppleWrapperDownload(c *gin.Context, song *model.Song, settings core.WebSettings, streamPlayback bool) bool {
	if song.Source != "apple" || streamPlayback || !core.AppleWrapperConfigured() {
		return false
	}
	// 浏览器中止下载时随请求一起取消，不再继续拉取和解密整曲。
	result, err := core.DownloadSongDataWithTemplateContext(c.Request.Context(), song, false, false, settings.DownloadFilenameTemplate)
	if err != nil {
		c.String(502, "Apple full download error: "+err.Error())
		return true
	}
	warnings := make([]string, 0, 2)
	if result.Warning != "" {
		warnings = append(warnings, result.Warning)
	}
	if core.WebDAVConfigured(settings) {
		if err := core.UploadSongToWebDAV(settings, result.Filename, result.Data); err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	if len(warnings) > 0 {
		c.Header("X-MusicDL-Warning", strings.Join(warnings, "; "))
	}
	// 不支持 Range：每次请求都要完整下载并解密，分段续传只会成倍重复下载。
	setDownloadHeader(c, result.Filename)
	c.Data(200, result.ContentType, result.Data)
	return true
}

// embedDownloadErrorText 是 /download 内嵌元数据分支的出错文案：Apple 整曲下载沿用 Apple 分支的格式
// （错误里的 CDN 地址已去掉查询参数），其他情况保持上游的 "Upstream stream error"。
func embedDownloadErrorText(song *model.Song, err error) string {
	if song.Source == "apple" && core.AppleWrapperConfigured() {
		return "Apple full download error: " + err.Error()
	}
	return "Upstream stream error"
}
