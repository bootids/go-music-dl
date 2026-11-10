package core

// 本 fork（bootids/go-music-dl）在下载流程里新增的代码（文件名选项、Apple wrapper 取音频），独立于上游 download.go，减少合并上游时的冲突。

import (
	"context"

	"github.com/guohuiyuan/music-lib/model"
)

// artistAlbumDirPrefix 是“按歌手/专辑分目录”开启时加在文件名模板前的目录。
// 专辑为空时该层目录会被 sanitizeDownloadRelativePath 跳过，文件直接放在歌手目录下。
const artistAlbumDirPrefix = "{artist}/{album}/"

// DownloadFilenameOptions 是影响下载文件名的设置项，便于在保存前预览未生效的设置。
type DownloadFilenameOptions struct {
	Template                 string
	OrganizeByArtistAlbum    bool
	CleanAlbumEpisode        bool
	NormalizeChineseVariants bool
}

// DownloadFilenameOptionsFromSettings 取当前设置里与文件名相关的部分。
func DownloadFilenameOptionsFromSettings(settings WebSettings, filenameTemplate string) DownloadFilenameOptions {
	return DownloadFilenameOptions{
		Template:                 filenameTemplate,
		OrganizeByArtistAlbum:    settings.OrganizeByArtistAlbum,
		CleanAlbumEpisode:        settings.CleanAlbumEpisode,
		NormalizeChineseVariants: settings.NormalizeChineseVariants,
	}
}

// fetchAppleWrapperAudio 是 fetchSongAudio 的钩子：配置了 wrapper 时，Apple 歌曲通过 wrapper 下载并解密整曲。
// handled 为 false 时调用方继续走上游的取音频流程。
func fetchAppleWrapperAudio(ctx context.Context, song *model.Song) (data []byte, contentType string, handled bool, err error) {
	if song.Source != "apple" || !AppleWrapperConfigured() {
		return nil, "", false, nil
	}
	data, err = FetchFullAppleAudioContext(ctx, song)
	if err != nil {
		return nil, "", true, err
	}
	return data, "audio/mp4", true, nil
}

// appleRemuxWarning 是 downloadSongDataWithTemplate 的钩子：wrapper 整曲下载在没有 ffmpeg 时
// 返回的是未重封装的分片 MP4（见 FetchFullAppleAudioContext），通过下载结果的 Warning 提示用户。
func appleRemuxWarning(song *model.Song) string {
	if song.Source != "apple" || !AppleWrapperConfigured() {
		return ""
	}
	if _, err := ResolveFFmpegPath(); err != nil {
		return "ffmpeg not found, Apple track not remuxed"
	}
	return ""
}
