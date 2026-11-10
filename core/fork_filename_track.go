package core

// 本 fork（bootids/go-music-dl）新增：文件名模板支持 {track} 音轨号占位符。
// 音轨号只取 fillAppleTrackTags 填进 song.Extra 的值（仅 Apple 下载有），独立于上游 download.go，减少合并上游时的冲突。

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/guohuiyuan/music-lib/model"
)

const trackToken = "{track}"

// trackTokenSeparators 是没有音轨号时随 {track} 一起去掉的分隔符。
const trackTokenSeparators = " .-_"

// renderTrackToken 是 BuildDownloadFilenameWithOptions 的钩子：在替换其他占位符之前处理模板里的 {track}。
// 有音轨号时换成补零的号码（多碟专辑带碟号，如 2-05）；没有时连同紧跟其后的分隔符一起去掉，
// {track} 在路径段末尾时改为去掉它前面的分隔符，例如 "{track}. {name}" 和 "{name} - {track}" 都变成 "{name}"。
func renderTrackToken(template string, song *model.Song) string {
	if !strings.Contains(template, trackToken) {
		return template
	}
	if track := formatTrackNumber(song); track != "" {
		return strings.ReplaceAll(template, trackToken, track)
	}

	for {
		idx := strings.Index(template, trackToken)
		if idx < 0 {
			break
		}
		before := template[:idx]
		after := strings.TrimLeft(template[idx+len(trackToken):], trackTokenSeparators)
		if after == "" || after[0] == '/' || after[0] == '\\' {
			before = strings.TrimRight(before, trackTokenSeparators)
		}
		template = before + after
	}
	// 模板只有 {track} 一段时，最后一段不能变成空文件名，退回用歌名。
	segment := template[strings.LastIndexAny(template, "/\\")+1:]
	if strings.TrimSpace(segment) == "" && strings.TrimSpace(template) != "" {
		template += "{name}"
	}
	return template
}

// formatTrackNumber 返回 {track} 的值：音轨号补零到 2 位，曲目总数达到 100 时补到 3 位；
// 碟数大于 1 时前面加碟号，如 "2-05"。没有有效音轨号时返回空串。
func formatTrackNumber(song *model.Song) string {
	if song == nil {
		return ""
	}
	track := positiveExtraInt(song, extraTagTrack)
	if track == 0 {
		return ""
	}
	width := 2
	if positiveExtraInt(song, extraTagTrackTotal) >= 100 {
		width = 3
	}
	number := fmt.Sprintf("%0*d", width, track)
	if disc := positiveExtraInt(song, extraTagDisc); disc > 0 && positiveExtraInt(song, extraTagDiscTotal) > 1 {
		number = strconv.Itoa(disc) + "-" + number
	}
	return number
}

func positiveExtraInt(song *model.Song, key string) int {
	n, err := strconv.Atoi(strings.TrimSpace(song.Extra[key]))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
