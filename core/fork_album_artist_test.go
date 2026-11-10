package core

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhowden/tag"
	"github.com/guohuiyuan/music-lib/model"
)

// ffmpegAudioForAlbumArtistTest 用 ffmpeg 生成一段很短的 m4a/flac，extraArgs 用来预先写入标签。
func ffmpegAudioForAlbumArtistTest(t *testing.T, ext string, extraArgs ...string) []byte {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	path := filepath.Join(t.TempDir(), "source."+ext)
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono", "-t", "0.05"}
	if ext == "m4a" {
		args = append(args, "-c:a", "aac")
	}
	args = append(args, extraArgs...)
	if out, err := exec.Command("ffmpeg", append(args, path)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg create %s failed: %v, output: %s", ext, err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// ffprobeAlbumArtists 返回文件里所有专辑艺人标签的值（键名忽略大小写、空格和下划线）。
func ffprobeAlbumArtists(t *testing.T, data []byte, ext string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe."+ext)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags", "-of", "default", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var values []string
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "TAG:"), "=")
		if !ok || !strings.HasPrefix(line, "TAG:") {
			continue
		}
		if strings.NewReplacer(" ", "", "_", "").Replace(strings.ToLower(key)) == "albumartist" {
			values = append(values, value)
		}
	}
	return values
}

func TestEmbedSongMetadataWritesAlbumArtistFFmpeg(t *testing.T) {
	for _, ext := range []string{"m4a", "flac"} {
		t.Run(ext, func(t *testing.T) {
			audio := ffmpegAudioForAlbumArtistTest(t, ext)
			embedded, err := EmbedSongMetadata(audio, &model.Song{Name: "First Love", Artist: "宇多田光", Album: "First Love", Ext: ext}, "", nil, "")
			if err != nil {
				t.Fatalf("EmbedSongMetadata: %v", err)
			}
			if got := ffprobeAlbumArtists(t, embedded, ext); len(got) != 1 || got[0] != "宇多田光" {
				t.Fatalf("album artist = %q, want [宇多田光]", got)
			}
		})
	}
}

func TestEmbedSongMetadataKeepsAlbumArtistWithoutSongArtistFFmpeg(t *testing.T) {
	for _, ext := range []string{"m4a", "flac"} {
		t.Run(ext, func(t *testing.T) {
			audio := ffmpegAudioForAlbumArtistTest(t, ext, "-metadata", "artist=Bar", "-metadata", "album_artist=Foo")
			embedded, err := EmbedSongMetadata(audio, &model.Song{Ext: ext}, "[00:01.00]新歌词", nil, "")
			if err != nil {
				t.Fatalf("EmbedSongMetadata: %v", err)
			}
			if got := ffprobeAlbumArtists(t, embedded, ext); len(got) != 1 || got[0] != "Foo" {
				t.Fatalf("album artist = %q, want preserved [Foo]", got)
			}
		})
	}
}

// 旧 FLAC 用带空格的 “ALBUM ARTIST” 键时，不能和新写的 ALBUMARTIST 并存。
func TestEmbedSongMetadataReplacesFLACAlbumArtistVariant(t *testing.T) {
	audio := ffmpegAudioForAlbumArtistTest(t, "flac", "-metadata", "ALBUM ARTIST=Foo")
	if got := ffprobeAlbumArtists(t, audio, "flac"); len(got) != 1 || got[0] != "Foo" {
		t.Fatalf("precondition: album artist = %q, want [Foo]", got)
	}
	embedded, err := EmbedSongMetadata(audio, &model.Song{Name: "First Love", Artist: "宇多田光", Album: "First Love", Ext: "flac"}, "", nil, "")
	if err != nil {
		t.Fatalf("EmbedSongMetadata: %v", err)
	}
	if got := ffprobeAlbumArtists(t, embedded, "flac"); len(got) != 1 || got[0] != "宇多田光" {
		t.Fatalf("album artist = %q, want exactly [宇多田光]", got)
	}
}

func TestEmbedSongMetadataWritesMP3AlbumArtist(t *testing.T) {
	audio := []byte{0xff, 0xfb, 0x90, 0x64, 0x00, 0x00, 0x00, 0x00}
	embedded, err := EmbedSongMetadata(audio, &model.Song{Name: "First Love", Artist: "宇多田光", Album: "First Love", Ext: "mp3"}, "", nil, "")
	if err != nil {
		t.Fatalf("EmbedSongMetadata: %v", err)
	}
	metadata, err := tag.ReadFrom(bytes.NewReader(embedded))
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if metadata.AlbumArtist() != "宇多田光" || metadata.Artist() != "宇多田光" {
		t.Fatalf("album artist/artist = %q/%q, want 宇多田光", metadata.AlbumArtist(), metadata.Artist())
	}

	// 已有 TPE2 的文件再次写入时替换而不是追加。
	again, err := EmbedSongMetadata(embedded, &model.Song{Name: "First Love", Artist: "Utada", Ext: "mp3"}, "", nil, "")
	if err != nil {
		t.Fatalf("second EmbedSongMetadata: %v", err)
	}
	if n := bytes.Count(again, []byte("TPE2")); n != 1 {
		t.Fatalf("TPE2 frames = %d, want 1", n)
	}
	if metadata, err = tag.ReadFrom(bytes.NewReader(again)); err != nil || metadata.AlbumArtist() != "Utada" {
		t.Fatalf("second album artist = %v/%v, want Utada", metadata, err)
	}
}

func TestEmbedSongMetadataKeepsMP3AlbumArtistWithoutSongArtist(t *testing.T) {
	frames := append(id3v23Frame("TPE1", id3TextFramePayload("Bar")), id3v23Frame("TPE2", id3TextFramePayload("Foo"))...)
	size := id3SynchsafeSize(len(frames))
	audio := append([]byte{'I', 'D', '3', 0x03, 0x00, 0x00}, size[:]...)
	audio = append(audio, frames...)
	audio = append(audio, 0xff, 0xfb, 0x90, 0x64, 0x00, 0x00, 0x00, 0x00)

	embedded, err := EmbedSongMetadata(audio, &model.Song{Ext: "mp3"}, "[00:01.00]新歌词", nil, "")
	if err != nil {
		t.Fatalf("EmbedSongMetadata: %v", err)
	}
	metadata, err := tag.ReadFrom(bytes.NewReader(embedded))
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if metadata.AlbumArtist() != "Foo" {
		t.Fatalf("album artist = %q, want preserved Foo", metadata.AlbumArtist())
	}
	if metadata.Lyrics() != "[00:01.00]新歌词" {
		t.Fatalf("lyrics = %q", metadata.Lyrics())
	}
}
