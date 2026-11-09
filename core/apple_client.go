package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Eyevinn/mp4ff/mp4"
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

// appleDownload 和 appleCookieLyrics 是包级变量，测试里可以替换掉。
var (
	appleDownload     = applemusic.Download
	appleCookieLyrics = func(cookie string, song *model.Song) (string, error) {
		return apple.New(cookie).GetLyrics(song)
	}
)

// ValidateAppleWrapperURL 检查 wrapper 地址是否为 http(s)://host[:port]。
func ValidateAppleWrapperURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("Apple wrapper 地址需为 http(s)://host:port")
	}
	return nil
}

func normalizeAppleWrapperURL(raw string) string {
	return strings.TrimSuffix(strings.TrimSpace(raw), "/")
}

var warnAppleWrapperEnvOnce sync.Once

// appleWrapperEnvURL 返回环境变量里的 wrapper 地址；格式不合法时返回错误。
func appleWrapperEnvURL() (string, error) {
	v := normalizeAppleWrapperURL(os.Getenv(AppleWrapperEnvURL))
	if v == "" {
		return "", nil
	}
	if err := ValidateAppleWrapperURL(v); err != nil {
		return "", fmt.Errorf("环境变量 %s 无效（%q）：%w", AppleWrapperEnvURL, v, err)
	}
	return v, nil
}

// AppleWrapperEnvError 返回环境变量 wrapper 地址的格式错误；未设置或合法时为 nil。
func AppleWrapperEnvError() error {
	_, err := appleWrapperEnvURL()
	return err
}

// AppleWrapperURL 返回当前生效的 wrapper 地址：环境变量优先，其次是界面设置。
// 环境变量格式不合法时忽略它（打印一次警告），与界面设置的校验一致。
func AppleWrapperURL() string {
	v, err := appleWrapperEnvURL()
	if err != nil {
		warnAppleWrapperEnvOnce.Do(func() { fmt.Fprintln(os.Stderr, "Warning:", err) })
	}
	if v != "" {
		return v
	}
	return GetWebSettings().AppleWrapperURL
}

// AppleWrapperEnvOverride 表示 wrapper 地址是否由（合法的）环境变量指定。
func AppleWrapperEnvOverride() bool {
	v, _ := appleWrapperEnvURL()
	return v != ""
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
	return NewAppleWrapperClientFor(AppleWrapperURL())
}

// NewAppleWrapperClientFor 按给定地址构造 wrapper 客户端（设置页检测未保存的地址时用）。
func NewAppleWrapperClientFor(baseURL string) *applemusic.Client {
	return applemusic.NewClient(normalizeAppleWrapperURL(baseURL), &http.Client{Timeout: 60 * time.Second, Transport: http.DefaultTransport})
}

// FetchFullAppleAudio 通过 wrapper 下载并解密整曲，返回 MP4 字节。
// ffmpeg 不可用时返回未重封装的分片 MP4。
func FetchFullAppleAudio(song *model.Song) ([]byte, error) {
	return FetchFullAppleAudioContext(context.Background(), song)
}

// appleDownloadSlots 限制同时进行的整曲下载数：每首都在内存里缓冲整曲（ffmpeg 重封装时再一份），
// 且都走管理员的同一个 Apple 账号；/download 对匿名用户开放，不能无限并发。
var appleDownloadSlots = make(chan struct{}, 2)

// FetchFullAppleAudioContext 同 FetchFullAppleAudio，ctx 取消（如浏览器中止下载）时停止下载。
func FetchFullAppleAudioContext(ctx context.Context, song *model.Song) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	select {
	case appleDownloadSlots <- struct{}{}:
		defer func() { <-appleDownloadSlots }()
	case <-ctx.Done():
		return nil, fmt.Errorf("apple full download: %w", ctx.Err())
	}

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

// appleLyricFunc 优先从 wrapper 取 TTML 歌词，失败、为空或未配置时回退到 music-lib，
// 两种来源都转成 LRC；转换失败时原样返回。
func appleLyricFunc(cookie string) func(*model.Song) (string, error) {
	return func(song *model.Song) (string, error) {
		text, err := "", errors.New("apple wrapper not configured")
		if AppleWrapperConfigured() && isDigits(song.ID) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			text, err = NewAppleWrapperClient().Lyrics(ctx, song.ID)
			cancel()
		}
		if err != nil || strings.TrimSpace(text) == "" {
			text, err = appleCookieLyrics(cookie, song)
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

// applePreviewMaxSeconds 是判定为 Apple Music 试听文件的最长时长。
const applePreviewMaxSeconds = 35

// isApplePreviewFile 判断已下载的文件是否是 30 秒试听（而歌曲本身更长）。
// 读不出 MP4 时长或歌曲时长未知时按“不是试听”处理。
func isApplePreviewFile(song *model.Song, path string) bool {
	// 时长未知（如网页 save_local 不带时长）时无法区分试听和真正的短曲，按“不是试听”处理，
	// 否则短曲每次都会被重新下载。
	if song.Duration <= applePreviewMaxSeconds+5 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	file, err := mp4.DecodeFile(f, mp4.WithDecodeMode(mp4.DecModeLazyMdat))
	if err != nil || file.Moov == nil || file.Moov.Mvhd == nil || file.Moov.Mvhd.Timescale == 0 {
		return false
	}
	seconds := float64(file.Moov.Mvhd.Duration) / float64(file.Moov.Mvhd.Timescale)
	return seconds > 0 && seconds <= applePreviewMaxSeconds
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
