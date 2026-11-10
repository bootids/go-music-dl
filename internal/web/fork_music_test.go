package web

import (
	"slices"
	"testing"

	"github.com/guohuiyuan/music-lib/model"
)

// Apple playback is always a 30-second preview, so auto switching must not pick it.
func TestSwitchCandidateSourcesExcludesApple(t *testing.T) {
	withSwitchSourceTestHooks(t)
	switchDefaultSourceNames = func() []string { return []string{"apple", "qq"} }
	switchAllSourceNames = func() []string { return []string{"apple", "spotify", "qq", "kuwo"} }
	switchSearchFuncProvider = func(string) func(string) ([]model.Song, error) {
		return func(string) ([]model.Song, error) { return nil, nil }
	}

	got := switchCandidateSources("netease", "")
	if slices.Contains(got, "apple") || slices.Contains(got, "spotify") || !slices.Equal(got, []string{"qq", "kuwo"}) {
		t.Fatalf("candidates = %v, want [qq kuwo]", got)
	}
}
