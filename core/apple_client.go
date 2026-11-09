package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/guohuiyuan/go-music-dl/internal/applemusic"
	"github.com/guohuiyuan/music-lib/apple"
	"github.com/guohuiyuan/music-lib/model"
)

// Apple Music 整曲下载走外部 wrapper-lite 服务（只在配置了地址时启用）；
// 未配置时 apple 源保持 30 秒试听，与之前一致。
const (
	// AppleWrapperEnvURL 优先于界面设置里的 wrapper 地址。
	AppleWrapperEnvURL = "MUSIC_DL_APPLE_WRAPPER_URL"
	appleALACMaxRate   = 192000
)

// appleDownload 是包级变量，测试里可以替换掉。
var appleDownload = applemusic.Download

// AppleWrapperURL 返回当前生效的 wrapper 地址：环境变量优先，其次是界面设置。
func AppleWrapperURL() string {
	if v := strings.TrimSuffix(strings.TrimSpace(os.Getenv(AppleWrapperEnvURL)), "/"); v != "" {
		return v
	}
	return GetWebSettings().AppleWrapperURL
}

// AppleWrapperEnvOverride 表示 wrapper 地址是否由环境变量指定。
func AppleWrapperEnvOverride() bool {
	return strings.TrimSpace(os.Getenv(AppleWrapperEnvURL)) != ""
}

// AppleWrapperConfigured 表示是否启用了 Apple Music 整曲下载。
func AppleWrapperConfigured() bool {
	return AppleWrapperURL() != ""
}

// AppleAudioQuality 返回设置里的音质偏好（alac 或 aac）。
func AppleAudioQuality() applemusic.Quality {
	if GetWebSettings().AppleAudioQuality == string(applemusic.QualityAAC) {
		return applemusic.QualityAAC
	}
	return applemusic.QualityALAC
}

// NewAppleWrapperClient 按当前生效的地址构造 wrapper 客户端。
func NewAppleWrapperClient() *applemusic.Client {
	return applemusic.NewClient(AppleWrapperURL(), &http.Client{Timeout: 60 * time.Second, Transport: http.DefaultTransport})
}

// FetchFullAppleAudio 通过 wrapper 下载并解密整曲，返回 MP4 字节。
// ffmpeg 不可用时返回未重封装的分片 MP4。
func FetchFullAppleAudio(song *model.Song) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	res, err := appleDownload(ctx, applemusic.DownloadOptions{
		Client:      NewAppleWrapperClient(),
		HTTP:        &http.Client{},
		AdamID:      song.ID,
		Quality:     AppleAudioQuality(),
		ALACMaxRate: appleALACMaxRate,
	})
	if err != nil {
		return nil, fmt.Errorf("apple full download: %w", err)
	}
	data, err := remuxAppleMP4(res.Data)
	if errors.Is(err, ErrFFmpegNotFound) {
		return res.Data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("apple full download: %w", err)
	}
	return data, nil
}

// remuxAppleMP4 把分片 MP4 无损重封装成普通 MP4（moov 在前）。
func remuxAppleMP4(data []byte) ([]byte, error) {
	ffmpegPath, err := ResolveFFmpegPath()
	if err != nil {
		return nil, ErrFFmpegNotFound
	}
	dir, err := os.MkdirTemp("", "gomusicdl-apple-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	inPath := filepath.Join(dir, "in.mp4")
	outPath := filepath.Join(dir, "out.m4a")
	if err := os.WriteFile(inPath, data, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.Command(ffmpegPath, "-y", "-hide_banner", "-loglevel", "error",
		"-i", inPath, "-map", "0:a:0", "-c", "copy", "-movflags", "+faststart", "-f", "mp4", outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg remux: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return os.ReadFile(outPath)
}

// appleLyricFunc 优先从 wrapper 取 TTML 歌词，失败或未配置时回退到 music-lib，
// 两种来源都转成 LRC；转换失败时原样返回。
func appleLyricFunc(cookie string) func(*model.Song) (string, error) {
	return func(song *model.Song) (string, error) {
		text, err := "", errors.New("apple wrapper not configured")
		if AppleWrapperConfigured() && isDigits(song.ID) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			text, err = NewAppleWrapperClient().Lyrics(ctx, song.ID)
			cancel()
		}
		if err != nil {
			text, err = apple.New(cookie).GetLyrics(song)
			if err != nil {
				return "", err
			}
		}
		if lrc, convErr := applemusic.TTMLToLRC(text); convErr == nil {
			return lrc, nil
		}
		return text, nil
	}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
