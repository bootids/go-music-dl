package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/guohuiyuan/music-lib/lyrics"
	"github.com/guohuiyuan/music-lib/model"
	"github.com/guohuiyuan/music-lib/qq"
	"github.com/guohuiyuan/music-lib/utils"
)

// 歌词类型：逐字（每个字/词带时间）、逐行（只有行首时间）、纯文本（无时间）、无歌词。
const (
	LyricKindVerbatim = "verbatim"
	LyricKindLine     = "line"
	LyricKindPlain    = "plain"
	LyricKindNone     = "none"
)

// LyricDurationTolerance 是本地歌曲与候选歌曲时长“一致”的容差（秒）。
const LyricDurationTolerance = 2

var (
	lyricLeadTimesRe   = regexp.MustCompile(`^(?:\[\d+:\d+(?:[.:]\d+)?\])+`)
	lyricTimeRe        = regexp.MustCompile(`\[\d+:\d+(?:[.:]\d+)?\]`)
	lyricEnhancedRe    = regexp.MustCompile(`<\d+:\d+(?:[.:]\d+)?>`)
	lyricTagLineRe     = regexp.MustCompile(`^\[[A-Za-z]+:[^\]]*\]$`)
	lyricLeadEnhanceRe = regexp.MustCompile(`^<\d+:\d+(?:[.:]\d+)?>`)
)

// DetectLyricKind 判断歌词是逐字、逐行还是纯文本。
// 逐字：行内出现增强 LRC 的 <mm:ss.xx>，或行首时间之后的文字中间还夹着 [mm:ss.xx]。
// 只是翻译行与原文同一时间戳，仍算逐行。
func DetectLyricKind(lyric string) string {
	if strings.TrimSpace(lyric) == "" {
		return LyricKindNone
	}
	timed := false
	for _, raw := range strings.Split(lyric, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || lyricTagLineRe.MatchString(line) {
			continue
		}
		lead := lyricLeadTimesRe.FindString(line)
		if lead == "" {
			continue
		}
		timed = true
		rest := line[len(lead):]
		if lyricEnhancedRe.MatchString(rest) {
			return LyricKindVerbatim
		}
		if loc := lyricTimeRe.FindStringIndex(rest); loc != nil && strings.TrimSpace(rest[:loc[0]]) != "" {
			return LyricKindVerbatim
		}
	}
	if timed {
		return LyricKindLine
	}
	return LyricKindPlain
}

// EnhancedToInlineVerbatim 把增强 LRC（[s]<s>字<t>字<e>）转成本应用播放器使用的
// 行内方括号逐字格式（[s]字[t]字[e]）。不含 <mm:ss.xx> 的行原样保留。
func EnhancedToInlineVerbatim(lyric string) string {
	if !lyricEnhancedRe.MatchString(lyric) {
		return lyric
	}
	lines := strings.Split(lyric, "\n")
	for i, raw := range lines {
		lead := lyricLeadTimesRe.FindString(raw)
		rest := raw[len(lead):]
		if !lyricEnhancedRe.MatchString(rest) {
			continue
		}
		// 行首紧跟的第一个字时间与行首时间重复，去掉，避免被当成第二个行时间。
		rest = lyricLeadEnhanceRe.ReplaceAllString(rest, "")
		rest = lyricEnhancedRe.ReplaceAllStringFunc(rest, func(m string) string {
			return "[" + m[1:len(m)-1] + "]"
		})
		lines[i] = lead + rest
	}
	return strings.Join(lines, "\n")
}

func formatLRCTime(ms int) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d.%02d", ms/60000, (ms%60000)/1000, (ms%1000)/10)
}

// BuildEnhancedLRC 生成增强 LRC：原文每行 [行首]<字起>字<字起>字<末字止>，
// 翻译行用与原文相同的行首时间、不带逐字时间。Navidrome 等播放器按增强 LRC 解析逐字。
func BuildEnhancedLRC(tags map[string]string, orig, trans lyrics.Data) string {
	var b strings.Builder
	for _, k := range []string{"ti", "ar", "al", "by"} {
		if v := strings.TrimSpace(tags[k]); v != "" {
			fmt.Fprintf(&b, "[%s:%s]\n", k, v)
		}
	}
	transByStart := map[string]string{}
	for _, line := range trans {
		if !line.Start.OK {
			continue
		}
		var text strings.Builder
		for _, w := range line.Words {
			text.WriteString(w.Text)
		}
		value := strings.TrimSpace(text.String())
		// QQ 用 “//” 占位没有翻译的行。
		if value == "" || value == "//" {
			continue
		}
		transByStart[formatLRCTime(line.Start.MS)] = value
	}
	for _, line := range orig {
		words := make([]lyrics.Word, 0, len(line.Words))
		for _, w := range line.Words {
			if w.Text != "" && w.Start.OK {
				words = append(words, w)
			}
		}
		if len(words) == 0 || strings.TrimSpace(joinLyricWords(words)) == "" {
			continue
		}
		start := words[0].Start.MS
		if line.Start.OK {
			start = line.Start.MS
		}
		startText := formatLRCTime(start)
		fmt.Fprintf(&b, "[%s]", startText)
		for _, w := range words {
			fmt.Fprintf(&b, "<%s>%s", formatLRCTime(w.Start.MS), w.Text)
		}
		if last := words[len(words)-1]; last.End.OK {
			fmt.Fprintf(&b, "<%s>", formatLRCTime(last.End.MS))
		}
		b.WriteByte('\n')
		if t, ok := transByStart[startText]; ok {
			fmt.Fprintf(&b, "[%s]%s\n", startText, t)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func joinLyricWords(words []lyrics.Word) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString(w.Text)
	}
	return b.String()
}

// qqVerbatimPost 便于测试替换 QQ 歌词接口。
var qqVerbatimPost = func(body []byte, cookie string) ([]byte, error) {
	return utils.Post("https://u.y.qq.com/cgi-bin/musicu.fcg", bytes.NewReader(body),
		utils.WithHeader("Referer", qq.LyricReferer),
		utils.WithHeader("User-Agent", qq.UserAgent),
		utils.WithHeader("Content-Type", "application/json"),
		utils.WithHeader("Cookie", cookie),
		utils.WithRandomIPHeader(),
	)
}

// qqLyricSearchGet 便于测试替换 QQ 搜索接口。
var qqLyricSearchGet = func(apiURL, cookie string) ([]byte, error) {
	return utils.Get(apiURL,
		utils.WithHeader("User-Agent", qq.UserAgent),
		utils.WithHeader("Referer", qq.SearchReferer),
		utils.WithHeader("Cookie", cookie),
		utils.WithRandomIPHeader(),
	)
}

// SearchQQForLyrics 为找歌词搜索 QQ 音乐。与 music-lib qq.Search 用同一接口，但不过滤
// 需要会员才能播放的歌（原唱录音室版大多是会员歌，取歌词不需要播放权限），并多取一些结果。
func SearchQQForLyrics(keyword string) ([]model.Song, error) {
	params := url.Values{}
	params.Set("w", keyword)
	params.Set("format", "json")
	params.Set("p", "1")
	params.Set("n", "20")
	body, err := qqLyricSearchGet("http://c.y.qq.com/soso/fcgi-bin/search_for_qq_cp?"+params.Encode(), CM.Get("qq"))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Song struct {
				List []struct {
					SongID    int64  `json:"songid"`
					SongName  string `json:"songname"`
					SongMID   string `json:"songmid"`
					AlbumName string `json:"albumname"`
					AlbumMID  string `json:"albummid"`
					Interval  int    `json:"interval"`
					Singer    []struct {
						Name string `json:"name"`
					} `json:"singer"`
				} `json:"list"`
			} `json:"song"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("qq search json parse error: %w", err)
	}
	songs := make([]model.Song, 0, len(resp.Data.Song.List))
	for _, item := range resp.Data.Song.List {
		artists := make([]string, 0, len(item.Singer))
		for _, singer := range item.Singer {
			artists = append(artists, singer.Name)
		}
		cover := ""
		if item.AlbumMID != "" {
			cover = "https://y.gtimg.cn/music/photo_new/T002R300x300M000" + item.AlbumMID + ".jpg"
		}
		songs = append(songs, model.Song{
			Source: "qq", ID: item.SongMID, Name: item.SongName, Artist: strings.Join(artists, "、"),
			Album: item.AlbumName, Duration: item.Interval, Cover: cover,
			Extra: map[string]string{"songmid": item.SongMID, "song_id": strconv.FormatInt(item.SongID, 10)},
		})
	}
	return songs, nil
}

// FetchQQVerbatimLyric 取 QQ 音乐逐字歌词（QRC）并转成增强 LRC，保留翻译、不要罗马音。
// 与 music-lib qq.GetLyrics 用同一接口，区别是关掉罗马音并把 LRC 格式的翻译也带上
// （music-lib 只按 QRC 解析翻译，会把 LRC 翻译丢掉）。
func FetchQQVerbatimLyric(song *model.Song) (string, error) {
	if song == nil {
		return "", errors.New("song is nil")
	}
	songID := 0
	if song.Extra != nil {
		songID, _ = strconv.Atoi(song.Extra["song_id"])
	}
	if songID == 0 {
		songID, _ = strconv.Atoi(song.ID)
	}
	if songID == 0 {
		return "", errors.New("qq song id not found")
	}
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	reqData := map[string]any{
		"comm": map[string]any{
			"ct": 11, "cv": "1003006", "v": "1003006", "tmeAppID": "qqmusiclight",
			"nettype": "NETWORK_WIFI", "udid": "0", "uid": "0", "loginUin": "0",
			"platform": "yqq.json", "needNewCode": 0,
		},
		"request": map[string]any{
			"method": "GetPlayLyricInfo",
			"module": "music.musichallSong.PlayLyricInfo",
			"param": map[string]any{
				"albumName": b64(song.Album), "crypt": 1, "ct": 19, "cv": 2111,
				"interval": song.Duration, "lrc_t": 0, "qrc": 1, "qrc_t": 0,
				"roma": 0, "roma_t": 0, "singerName": b64(song.Artist), "songID": songID,
				"songName": b64(song.Name), "trans": 1, "trans_t": 0, "type": 0,
			},
		},
	}
	body, _ := json.Marshal(reqData)
	raw, err := qqVerbatimPost(body, CM.Get("qq"))
	if err != nil {
		return "", err
	}
	var resp struct {
		Code    int `json:"code"`
		Request struct {
			Code int `json:"code"`
			Data struct {
				Lyric string `json:"lyric"`
				Trans string `json:"trans"`
			} `json:"data"`
		} `json:"request"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("qq lyric json parse error: %w", err)
	}
	if resp.Code != 0 || resp.Request.Code != 0 {
		return "", fmt.Errorf("qq lyric api error code: %d/%d", resp.Code, resp.Request.Code)
	}
	if strings.TrimSpace(resp.Request.Data.Lyric) == "" {
		return "", errors.New("QQ 音乐没有这首歌的歌词")
	}
	decrypted, err := lyrics.DecryptQRCHex(resp.Request.Data.Lyric)
	if err != nil {
		return "", fmt.Errorf("qq lyric decrypt failed: %w", err)
	}
	_, orig := lyrics.ParseQRC(decrypted)
	if len(orig) == 0 {
		return "", errors.New("QQ 音乐的这首歌没有逐字歌词")
	}
	var trans lyrics.Data
	if t := strings.TrimSpace(resp.Request.Data.Trans); t != "" {
		if dec, err := lyrics.DecryptQRCHex(t); err == nil {
			if _, qrcTrans := lyrics.ParseQRC(dec); len(qrcTrans) > 0 {
				trans = qrcTrans
			} else {
				_, trans = lyrics.ParseLRC(dec)
			}
		}
	}
	tags := map[string]string{"ti": song.Name, "ar": song.Artist, "al": song.Album}
	return BuildEnhancedLRC(tags, orig, trans), nil
}

// ReplaceLocalSongLyric 把歌词写进已存在的本地音频文件（不重编码，保留其他标签和封面），
// 并在有同名 .lrc 时一并替换，旧 .lrc 备份为 .lrc.bak。
func ReplaceLocalSongLyric(audioPath string, lyric string, sidecarPath string) error {
	lyric = strings.TrimSpace(lyric)
	if lyric == "" {
		return errors.New("lyric is empty")
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(audioPath)), ".")
	switch ext {
	case "mp3", "flac", "m4a":
	default:
		return fmt.Errorf("暂不支持给 %s 文件写入内嵌歌词", ext)
	}
	info, err := os.Stat(audioPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(audioPath)
	if err != nil {
		return err
	}
	updated, err := EmbedSongMetadata(data, &model.Song{Ext: ext}, lyric, nil, "")
	if err != nil {
		return err
	}
	if err := writeFileAtomically(audioPath, updated, info.Mode().Perm()); err != nil {
		return err
	}
	if strings.TrimSpace(sidecarPath) != "" {
		if old, err := os.ReadFile(sidecarPath); err == nil {
			if err := os.WriteFile(sidecarPath+".bak", old, 0o644); err != nil {
				return fmt.Errorf("备份旧歌词失败: %w", err)
			}
		}
		sidecarInfo, statErr := os.Stat(sidecarPath)
		mode := os.FileMode(0o644)
		if statErr == nil {
			mode = sidecarInfo.Mode().Perm()
		}
		if err := writeFileAtomically(sidecarPath, []byte(lyric+"\n"), mode); err != nil {
			return err
		}
	}
	return nil
}

// writeFileAtomically 先写同目录临时文件再 rename，播放或扫描中的读者不会读到半个文件。
func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".musicdl-*.part")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
