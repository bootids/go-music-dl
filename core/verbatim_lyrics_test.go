package core

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhowden/tag"
	"github.com/guohuiyuan/music-lib/lyrics"
	"github.com/guohuiyuan/music-lib/model"
)

func TestDetectLyricKind(t *testing.T) {
	tests := map[string]string{
		"":           LyricKindNone,
		"   \n ":     LyricKindNone,
		"只有文字\n没有时间": LyricKindPlain,
		"[ti:歌]\n[00:01.00]第一行\n[00:03.50]第二行":       LyricKindLine,
		"[00:01.00]原文\n[00:01.00]translation":        LyricKindLine, // 翻译同时间戳不算逐字
		"[00:01.00][00:20.00]副歌重复":                   LyricKindLine,
		"[00:01.00]你[00:01.50]好[00:02.00]":           LyricKindVerbatim, // music-lib 行内格式
		"[00:01.00]<00:01.00>你<00:01.50>好<00:02.00>": LyricKindVerbatim, // 增强 LRC
	}
	for in, want := range tests {
		if got := DetectLyricKind(in); got != want {
			t.Errorf("DetectLyricKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildEnhancedLRCFromQRCAndTranslation(t *testing.T) {
	_, orig := lyrics.ParseQRC("[ti:桜流し]\n[1000,1500]開(1000,500)い(1500,500)た(2000,500)\n[3000,1000]花(3000,1000)")
	_, trans := lyrics.ParseLRC("[00:01.00]盛放\n[00:03.00]//")
	got := BuildEnhancedLRC(map[string]string{"ti": "桜流し", "ar": "宇多田光"}, orig, trans)
	want := strings.Join([]string{
		"[ti:桜流し]",
		"[ar:宇多田光]",
		"[00:01.00]<00:01.00>開<00:01.50>い<00:02.00>た<00:02.50>",
		"[00:01.00]盛放",
		"[00:03.00]<00:03.00>花<00:04.00>",
	}, "\n")
	if got != want {
		t.Fatalf("BuildEnhancedLRC =\n%s\nwant\n%s", got, want)
	}
	if DetectLyricKind(got) != LyricKindVerbatim {
		t.Fatalf("enhanced output not detected as verbatim")
	}
}

func TestEnhancedToInlineVerbatim(t *testing.T) {
	in := "[ti:歌]\n[00:01.00]<00:01.00>開<00:01.50>い<00:02.50>\n[00:01.00]盛放\n[00:05.00]普通行"
	want := "[ti:歌]\n[00:01.00]開[00:01.50]い[00:02.50]\n[00:01.00]盛放\n[00:05.00]普通行"
	if got := EnhancedToInlineVerbatim(in); got != want {
		t.Fatalf("EnhancedToInlineVerbatim =\n%s\nwant\n%s", got, want)
	}
	line := "[00:01.00]第一行"
	if got := EnhancedToInlineVerbatim(line); got != line {
		t.Fatalf("line lyric changed: %q", got)
	}
}

const testEnhancedLyric = "[00:01.00]<00:01.00>你<00:01.50>好<00:02.00>"

func TestReplaceLocalSongLyricMP3KeepsTagsAndBacksUpSidecar(t *testing.T) {
	dir := t.TempDir()
	audio := []byte{0xff, 0xfb, 0x90, 0x64, 0x00, 0x00, 0x00, 0x00}
	cover := []byte{0xff, 0xd8, 0xff, 0xd9}
	original, err := EmbedSongMetadata(audio, &model.Song{Name: "歌", Artist: "歌手", Album: "专辑", Ext: "mp3"}, "[00:01.00]旧逐行", cover, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "歌.mp3")
	lrc := filepath.Join(dir, "歌.lrc")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lrc, []byte("[00:01.00]旧逐行\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ReplaceLocalSongLyric(path, testEnhancedLyric, lrc); err != nil {
		t.Fatalf("ReplaceLocalSongLyric: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.HasSuffix(data, audio) {
		t.Fatal("audio frames changed")
	}
	m, err := tag.ReadFrom(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if m.Title() != "歌" || m.Artist() != "歌手" || m.Album() != "专辑" || m.Picture() == nil {
		t.Fatalf("tags not preserved: %q %q %q cover=%v", m.Title(), m.Artist(), m.Album(), m.Picture() != nil)
	}
	if strings.TrimSpace(m.Lyrics()) != testEnhancedLyric {
		t.Fatalf("lyrics = %q", m.Lyrics())
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("audio mode = %v", info.Mode().Perm())
	}
	if got, _ := os.ReadFile(lrc); strings.TrimSpace(string(got)) != testEnhancedLyric {
		t.Fatalf("sidecar = %q", got)
	}
	if got, _ := os.ReadFile(lrc + ".bak"); string(got) != "[00:01.00]旧逐行\n" {
		t.Fatalf("backup = %q", got)
	}
	if info, _ := os.Stat(lrc); info.Mode().Perm() != 0o600 {
		t.Fatalf("sidecar mode = %v", info.Mode().Perm())
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".musicdl-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files left: %v", leftovers)
	}
}

// m4a / flac 走 ffmpeg：确认歌词写进可读的标签，且原有标签、曲目号、封面都还在。
func TestReplaceLocalSongLyricFFmpegFormats(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	t.Setenv("MUSIC_DL_FFMPEG", "")
	// dhowden/tag 读不了 Vorbis 的 “3/9” 写法，flac 用纯数字曲目号。
	for _, tc := range []struct{ ext, codec, track string }{{"m4a", "aac", "3/9"}, {"flac", "flac", "3"}} {
		t.Run(tc.ext, func(t *testing.T) {
			dir := t.TempDir()
			cover := filepath.Join(dir, "c.png")
			path := filepath.Join(dir, "歌."+tc.ext)
			run := func(args ...string) {
				t.Helper()
				if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
					t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
				}
			}
			run("-v", "error", "-y", "-f", "lavfi", "-i", "color=c=red:s=16x16:d=1", "-frames:v", "1", cover)
			run("-v", "error", "-y", "-f", "lavfi", "-i", "sine=d=2", "-i", cover,
				"-map", "0:a", "-map", "1:v", "-c:a", tc.codec, "-c:v", "copy", "-disposition:v", "attached_pic",
				"-metadata", "title=歌", "-metadata", "artist=歌手", "-metadata", "album=专辑",
				"-metadata", "album_artist=群星", "-metadata", "track="+tc.track, "-metadata", "lyrics=[00:01.00]旧逐行", path)

			if err := ReplaceLocalSongLyric(path, testEnhancedLyric, ""); err != nil {
				t.Fatalf("ReplaceLocalSongLyric: %v", err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			m, err := tag.ReadFrom(f)
			if err != nil {
				t.Fatal(err)
			}
			track, _ := m.Track()
			if m.Title() != "歌" || m.Artist() != "歌手" || m.Album() != "专辑" || m.AlbumArtist() != "群星" || track != 3 {
				t.Fatalf("tags not preserved: %q %q %q %q track=%d", m.Title(), m.Artist(), m.Album(), m.AlbumArtist(), track)
			}
			if m.Picture() == nil || len(m.Picture().Data) == 0 {
				t.Fatal("cover lost")
			}
			if strings.TrimSpace(m.Lyrics()) != testEnhancedLyric {
				t.Fatalf("lyrics = %q", m.Lyrics())
			}
		})
	}
}

func TestReplaceLocalSongLyricRejectsUnsupportedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	_ = os.WriteFile(path, []byte("RIFF"), 0o644)
	if err := ReplaceLocalSongLyric(path, testEnhancedLyric, ""); err == nil {
		t.Fatal("want error for wav")
	}
}

func TestSearchQQForLyricsKeepsVIPSongs(t *testing.T) {
	orig := qqLyricSearchGet
	t.Cleanup(func() { qqLyricSearchGet = orig })
	var gotURL string
	qqLyricSearchGet = func(apiURL, cookie string) ([]byte, error) {
		gotURL = apiURL
		return []byte(`{"data":{"song":{"list":[
			{"songid":1,"songname":"闹哄哄","songmid":"m1","albumname":"Dear Life","albummid":"a1","interval":244,"singer":[{"name":"郁可唯"}],"pay":{"payplay":1}},
			{"songid":2,"songname":"闹哄哄 (Live)","songmid":"m2","interval":222,"singer":[{"name":"郁可唯"},{"name":"许魏洲"}],"pay":{"payplay":0}}
		]}}}`), nil
	}
	songs, err := SearchQQForLyrics("闹哄哄 郁可唯")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotURL, "n=20") {
		t.Fatalf("url = %s", gotURL)
	}
	if len(songs) != 2 || songs[0].Album != "Dear Life" || songs[0].Duration != 244 || songs[0].Extra["song_id"] != "1" || songs[1].Artist != "郁可唯、许魏洲" {
		t.Fatalf("songs = %+v", songs)
	}
}
