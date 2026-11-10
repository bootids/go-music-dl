package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/guohuiyuan/go-music-dl/internal/applemusic"
	"github.com/guohuiyuan/music-lib/model"
)

func TestApplyArtistAlias(t *testing.T) {
	const utada = "宇多田光 = Utada, Hikaru Utada, 宇多田ヒカル"
	tests := []struct {
		name    string
		artist  string
		aliases string
		want    string
	}{
		{"alias", "Utada", utada, "宇多田光"},
		{"alias case-insensitive", "hikaru utada", utada, "宇多田光"},
		{"japanese alias", "宇多田ヒカル", utada, "宇多田光"},
		{"canonical itself", "宇多田光", utada, "宇多田光"},
		{"surrounding spaces", "  Utada ", utada, "宇多田光"},
		{"multi-artist not split", "Utada & Paul Carter", utada, "Utada & Paul Carter"},
		{"multi-artist with 、 not split", "Utada、郁可唯", utada, "Utada、郁可唯"},
		{"no match", "郁可唯", utada, "郁可唯"},
		{"empty config", "Utada", "", "Utada"},
		{"blank and comment lines", "Utada", "\n# Utada = 别人\n   \n" + utada + "\n", "宇多田光"},
		{"full-width separators", "Hikaru Utada", "宇多田光 ＝ Utada，Hikaru Utada", "宇多田光"},
		{"malformed lines ignored", "Utada", "Utada\n = Utada\n" + utada, "宇多田光"},
		{"only malformed lines", "Utada", "Utada, 宇多田光\n = Utada", "Utada"},
		{"second rule", "Jay Chou", utada + "\n周杰伦 = Jay Chou", "周杰伦"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ApplyArtistAlias(tt.artist, tt.aliases); got != tt.want {
				t.Fatalf("ApplyArtistAlias(%q) = %q, want %q", tt.artist, got, tt.want)
			}
		})
	}
}

func TestArtistAliasesSettingRoundTrip(t *testing.T) {
	t.Setenv("MUSIC_DL_CONFIG_DB", filepath.Join(t.TempDir(), "s.db"))
	resetConfigStateForTest()
	t.Cleanup(resetConfigStateForTest)

	if got := GetWebSettings().ArtistAliases; got != "" {
		t.Fatalf("default ArtistAliases = %q, want empty", got)
	}
	if err := SaveWebSettings(WebSettings{ArtistAliases: "\n 宇多田光 = Utada \n\n"}); err != nil {
		t.Fatalf("SaveWebSettings: %v", err)
	}
	if got := GetWebSettings().ArtistAliases; got != "宇多田光 = Utada" {
		t.Fatalf("ArtistAliases = %q, want trimmed value", got)
	}
}

// 下载结果的文件名和“按歌手/专辑分目录”的歌手目录都用标准名。
func TestDownloadUsesCanonicalArtistAlias(t *testing.T) {
	setupAppleConfigForTest(t)
	t.Setenv(AppleWrapperEnvURL, "http://127.0.0.1:1")
	t.Setenv("MUSIC_DL_FFMPEG", filepath.Join(t.TempDir(), "missing-ffmpeg"))
	stubAppleDownload(t, func(context.Context, applemusic.DownloadOptions) (*applemusic.Result, error) {
		return &applemusic.Result{Data: []byte("fmp4"), Codec: "alac"}, nil
	})
	if err := SaveWebSettings(WebSettings{
		OrganizeByArtistAlbum: true,
		ArtistAliases:         "宇多田光 = Utada, Hikaru Utada, 宇多田ヒカル",
	}); err != nil {
		t.Fatalf("SaveWebSettings: %v", err)
	}

	song := &model.Song{Source: "apple", ID: "123", Name: "First Love", Artist: "Hikaru Utada", Album: "First Love"}
	res, err := DownloadSongDataWithTemplateContext(context.Background(), song, false, false, "{artist} - {name}")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("宇多田光", "First Love", "宇多田光 - First Love."+res.Ext)
	if res.Filename != want {
		t.Fatalf("Filename = %q, want %q", res.Filename, want)
	}
	if song.Artist != "Hikaru Utada" {
		t.Fatalf("caller's song artist changed to %q", song.Artist)
	}
}
