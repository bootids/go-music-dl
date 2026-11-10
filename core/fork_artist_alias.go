package core

// 本 fork（bootids/go-music-dl）新增的“歌手别名”：下载时把同一歌手在不同来源的不同写法统一成一个标准名，
// 独立于上游 download.go，减少合并上游时的冲突。

import "strings"

// ApplyArtistAlias 按设置里的别名表把歌手名换成标准名，未命中时原样返回。
//
// aliases 每行一条：`标准名 = 别名1, 别名2`，等号可用全角“＝”，别名可用中文逗号分隔；
// 空行、# 开头的行和格式不对的行（没有等号、标准名为空）忽略。
// 只拿整个歌手名（去掉首尾空白后，不区分大小写）去比对标准名和各个别名，
// 不拆分合唱，“A & B”“A、B”这类多歌手字符串原样返回。
func ApplyArtistAlias(artist, aliases string) string {
	name := strings.TrimSpace(artist)
	if name == "" || strings.TrimSpace(aliases) == "" {
		return artist
	}
	for _, line := range strings.Split(aliases, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		canonical, rest, ok := strings.Cut(strings.ReplaceAll(line, "＝", "="), "=")
		canonical = strings.TrimSpace(canonical)
		if !ok || canonical == "" {
			continue
		}
		if strings.EqualFold(name, canonical) {
			return canonical
		}
		for _, alias := range strings.Split(strings.ReplaceAll(rest, "，", ","), ",") {
			if alias = strings.TrimSpace(alias); alias != "" && strings.EqualFold(name, alias) {
				return canonical
			}
		}
	}
	return artist
}
