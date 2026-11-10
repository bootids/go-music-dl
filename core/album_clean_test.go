package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guohuiyuan/go-music-dl/internal/applemusic"
	"github.com/guohuiyuan/music-lib/model"
)

func TestCleanAlbumEpisode(t *testing.T) {
	tests := map[string]string{
		"声生不息·家年华 (第九期 live)": "声生不息·家年华",
		"声生不息·家年华（第九期）":       "声生不息·家年华",
		"声生不息·家年华 【第9期】":      "声生不息·家年华",
		"声生不息·家年华 第九期":        "声生不息·家年华",
		"声生不息·家年华 第九期 live":   "声生不息·家年华",
		"乘风2023 第3期":          "乘风2023",
		"乘风2023第3期":           "乘风2023",
		"歌手2024 EP12":         "歌手2024",
		"歌手2024 ep.12 live":   "歌手2024",
		"中国好声音 第 12 集":        "中国好声音",
		"跨年晚会 - 第二场":          "跨年晚会",
		"声生不息 (第九期) (Live)":   "声生不息 (Live)",
		"  叶惠美  ":             "叶惠美",
		// 不是期数：版本说明、季、EP 唱片类型、无编号的括号都保留。
		"叶惠美 (Deluxe)":    "叶惠美 (Deluxe)",
		"范特西 (Live)":      "范特西 (Live)",
		"21 (Remastered)": "21 (Remastered)",
		"声生不息·宝岛季":        "声生不息·宝岛季",
		"乘风破浪的姐姐 第二季":     "乘风破浪的姐姐 第二季",
		"Love Story EP":   "Love Story EP",
		"Deep Cuts (EP)":  "Deep Cuts (EP)",
		"STEP 1":          "STEP 1",
		"":                "",
		// 去完为空时保留原名。
		"第一期":   "第一期",
		"(第一期)": "(第一期)",
	}
	for in, want := range tests {
		if got := CleanAlbumEpisode(in); got != want {
			t.Errorf("CleanAlbumEpisode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildDownloadFilenameCleansAlbumEpisode(t *testing.T) {
	song := &model.Song{Name: "歌", Artist: "某歌手", Album: "声生不息·家年华 (第九期 live)"}
	opts := DownloadFilenameOptions{Template: "{artist} - {name}", OrganizeByArtistAlbum: true, CleanAlbumEpisode: true}
	if got, want := BuildDownloadFilenameWithOptions(song, "m4a", opts), filepath.Join("某歌手", "声生不息·家年华", "某歌手 - 歌.m4a"); got != want {
		t.Fatalf("clean on: %q, want %q", got, want)
	}
	opts.Template = "{album}/{name}"
	if got, want := BuildDownloadFilenameWithOptions(song, "m4a", opts), filepath.Join("声生不息·家年华", "歌.m4a"); got != want {
		t.Fatalf("{album} token: %q, want %q", got, want)
	}
	opts.CleanAlbumEpisode = false
	if got, want := BuildDownloadFilenameWithOptions(song, "m4a", opts), filepath.Join("声生不息·家年华 (第九期 live)", "歌.m4a"); got != want {
		t.Fatalf("clean off: %q, want %q", got, want)
	}
}

// 开启整理后，文件里的专辑标签和保存路径都用整理后的专辑名。
func TestDownloadCleansAlbumEpisodeInTagAndPath(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}
	setupAppleConfigForTest(t)
	t.Setenv(AppleWrapperEnvURL, "http://127.0.0.1:1")
	t.Setenv("MUSIC_DL_FFMPEG", "")
	clear := buildClearFragmentedMP4(t)
	stubAppleDownload(t, func(context.Context, applemusic.DownloadOptions) (*applemusic.Result, error) {
		return &applemusic.Result{Data: clear, Codec: "aac"}, nil
	})
	if err := SaveWebSettings(WebSettings{OrganizeByArtistAlbum: true, CleanAlbumEpisode: true}); err != nil {
		t.Fatalf("SaveWebSettings: %v", err)
	}

	song := &model.Song{ID: "1", Source: "apple", Name: "歌", Artist: "某歌手", Album: "声生不息·家年华 (第九期 live)"}
	outDir := t.TempDir()
	result, err := SaveSongToFileWithoutLyricsSidecar(song, outDir, false, false, "{artist} - {name}")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	wantPath := filepath.Join(outDir, "某歌手", "声生不息·家年华", "某歌手 - 歌.m4a")
	if result.SavedPath != wantPath {
		t.Fatalf("SavedPath = %q, want %q", result.SavedPath, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags=album", "-of", "default=nw=1:nk=1", wantPath).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "声生不息·家年华" {
		t.Fatalf("album tag = %q, want 声生不息·家年华", got)
	}
	if song.Album != "声生不息·家年华 (第九期 live)" {
		t.Fatalf("caller's song mutated: %q", song.Album)
	}
}
