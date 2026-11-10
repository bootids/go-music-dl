package core

import (
	"regexp"
	"strings"
)

// 综艺期数标记：第九期 / 第 3 集 / 第十二场 / 第2回 / EP12。
// 只认期数，(Live)、(Deluxe) 这类版本说明不算。
const albumEpisodeMarker = `(?:第\s*[0-9０-９一二三四五六七八九十百千零〇两]+\s*[期集场回]|(?i:\bEP\.?\s*\d+\b))`

var (
	// 任意位置、内容含期数标记的括号段，如 “(第九期 live)”“【第3期】”。
	albumEpisodeBracketRe = regexp.MustCompile(`\s*[\(（\[【][^\(（\[【\)）\]】]*` + albumEpisodeMarker + `[^\(（\[【\)）\]】]*[\)）\]】]`)
	// 结尾不带括号的期数及其后的修饰，如 “乘风2023 第3期”“歌手2024 EP12 live”。
	albumEpisodeTailRe = regexp.MustCompile(albumEpisodeMarker + `[^\(（\[【\)）\]】]*$`)
)

// CleanAlbumEpisode 去掉专辑名里的综艺期数，如
// “声生不息·家年华 (第九期 live)” → “声生不息·家年华”。
// 去完为空（专辑名本身就是“第一期”）时原样返回。
func CleanAlbumEpisode(album string) string {
	original := strings.TrimSpace(album)
	cleaned := albumEpisodeBracketRe.ReplaceAllString(original, "")
	cleaned = albumEpisodeTailRe.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimRight(cleaned, " \t·・-—–_:：|｜,，")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return original
	}
	return cleaned
}
