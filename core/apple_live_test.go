package core

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/guohuiyuan/music-lib/model"
)

// TestAppleLive 需要已登录订阅账号的 wrapper-lite，默认跳过。运行方式：
//
//	APPLE_LIVE_TEST=1 MUSIC_DL_APPLE_WRAPPER_URL=http://127.0.0.1:12340 APPLE_LIVE_ADAM_ID=<id> go test ./core/ -run AppleLive -v
func TestAppleLive(t *testing.T) {
	adamID := os.Getenv("APPLE_LIVE_ADAM_ID")
	if os.Getenv("APPLE_LIVE_TEST") != "1" || os.Getenv(AppleWrapperEnvURL) == "" || adamID == "" {
		t.Skip("set APPLE_LIVE_TEST=1, " + AppleWrapperEnvURL + " and APPLE_LIVE_ADAM_ID to run")
	}
	ffprobe, err := ResolveFFprobePath()
	if err != nil {
		t.Fatalf("ffprobe is required: %v", err)
	}
	t.Setenv("MUSIC_DL_CONFIG_DB", filepath.Join(t.TempDir(), "s.db"))
	resetConfigStateForTest()
	t.Cleanup(resetConfigStateForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	regions, err := NewAppleWrapperClient().Status(ctx)
	cancel()
	if err != nil || len(regions) == 0 {
		t.Fatalf("status: regions=%v err=%v", regions, err)
	}
	t.Logf("wrapper regions: %v", regions)

	for _, tc := range []struct{ quality, codec string }{{"alac", "alac"}, {"aac", "aac"}} {
		t.Run(tc.quality, func(t *testing.T) {
			s := GetWebSettings()
			s.AppleAudioQuality = tc.quality
			if err := SaveWebSettings(s); err != nil {
				t.Fatalf("SaveWebSettings: %v", err)
			}
			data, err := FetchFullAppleAudio(&model.Song{ID: adamID, Source: "apple"})
			if err != nil {
				t.Fatalf("FetchFullAppleAudio: %v", err)
			}
			path := filepath.Join(t.TempDir(), "track.m4a")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(ffprobe, "-v", "error", "-print_format", "json", "-show_streams", path).Output()
			if err != nil {
				t.Fatalf("ffprobe: %v", err)
			}
			var probe struct {
				Streams []struct {
					CodecName string `json:"codec_name"`
					Duration  string `json:"duration"`
				} `json:"streams"`
			}
			if err := json.Unmarshal(out, &probe); err != nil || len(probe.Streams) == 0 {
				t.Fatalf("ffprobe output %s: %v", out, err)
			}
			st := probe.Streams[0]
			dur, _ := strconv.ParseFloat(st.Duration, 64)
			t.Logf("%s: %d bytes, codec=%s duration=%.1fs", tc.quality, len(data), st.CodecName, dur)
			if st.CodecName != tc.codec || dur <= 60 {
				t.Fatalf("got codec=%s duration=%.1f, want codec=%s duration>60", st.CodecName, dur, tc.codec)
			}
		})
	}
}
