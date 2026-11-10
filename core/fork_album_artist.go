package core

// 本 fork（bootids/go-music-dl）新增：下载时把歌手同时写进“专辑艺人”标签，
// 让 Navidrome 等按专辑艺人归类的播放器不再显示为空。独立于上游 service.go，减少合并上游时的冲突。

import (
	"strings"

	"github.com/guohuiyuan/music-lib/model"
)

// albumArtistForSong 返回要写入的专辑艺人：默认是歌曲自带的歌手名；
// Apple 歌曲查到了与歌手不同的专辑艺人（如合辑的 Various Artists）时用查到的值（见 fork_apple_track_tags.go）。
// 歌手为空时（如给本地文件换歌词）返回空串，不碰文件里已有的专辑艺人。
func albumArtistForSong(song *model.Song) string {
	if song == nil || strings.TrimSpace(song.Artist) == "" {
		return ""
	}
	if albumArtist := strings.TrimSpace(song.Extra[extraTagAlbumArtist]); albumArtist != "" {
		return albumArtist
	}
	return strings.TrimSpace(song.Artist)
}

// albumArtistID3Frame 是 embedMP3ID3v23Metadata 的钩子：albumArtist 非空时把旧的 TPE2 帧标记为替换，
// 并返回新的 TPE2 帧；为空时返回 nil，保留原有 TPE2。
func albumArtistID3Frame(albumArtist string, replaceFrames map[string]bool) []byte {
	if albumArtist == "" {
		return nil
	}
	replaceFrames["TPE2"] = true
	return id3v23Frame("TPE2", id3TextFramePayload(albumArtist))
}

// albumArtistFFmpegArgs 由 forkExtraTags.ffmpegArgs 调用：albumArtist 非空时追加写专辑艺人的参数。
// FLAC 里旧文件可能用 “ALBUM ARTIST” 这种带空格的键，-map_metadata 0 会把它原样带过来，
// 和新写的 ALBUMARTIST 并存，所以一并清掉。
func albumArtistFFmpegArgs(ext, albumArtist string) []string {
	if albumArtist == "" {
		return nil
	}
	args := []string{"-metadata", "album_artist=" + albumArtist}
	if ext == "flac" {
		args = append(args, "-metadata", "ALBUM ARTIST=")
	}
	return args
}
