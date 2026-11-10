package core

import (
	"path/filepath"
	"testing"

	"github.com/guohuiyuan/music-lib/model"
)

func TestRenderTrackToken(t *testing.T) {
	withTrack := func(extra map[string]string) *model.Song {
		return &model.Song{Name: "真夏の通り雨", Extra: extra}
	}
	single := withTrack(map[string]string{"tag_track": "7", "tag_track_total": "11", "tag_disc": "1", "tag_disc_total": "1"})
	noTrack := withTrack(nil)
	cases := []struct {
		name     string
		template string
		song     *model.Song
		want     string
	}{
		{"single disc", "{track}. {name}", single, "07. {name}"},
		{"multi disc", "{track}. {name}", withTrack(map[string]string{"tag_track": "5", "tag_track_total": "18", "tag_disc": "2", "tag_disc_total": "2"}), "2-05. {name}"},
		{"multi disc without disc number", "{track}. {name}", withTrack(map[string]string{"tag_track": "5", "tag_disc_total": "2"}), "05. {name}"},
		{"three digits", "{track}. {name}", withTrack(map[string]string{"tag_track": "7", "tag_track_total": "120"}), "007. {name}"},
		{"missing dot", "{track}. {name}", noTrack, "{name}"},
		{"missing dash", "{track} - {name}", noTrack, "{name}"},
		{"missing underscore", "{track}_{name}", noTrack, "{name}"},
		{"missing at end", "{name} - {track}", noTrack, "{name}"},
		{"missing at segment end", "{artist}/{album} {track}/{name}", noTrack, "{artist}/{album}/{name}"},
		{"missing in dir", "{artist}/{album}/{track}. {name}", noTrack, "{artist}/{album}/{name}"},
		{"missing only token", "{artist}/{album}/{track}", noTrack, "{artist}/{album}/{name}"},
		{"nil song", "{track}. {name}", nil, "{name}"},
		{"non-numeric", "{track}. {name}", withTrack(map[string]string{"tag_track": "A1"}), "{name}"},
		{"zero", "{track}. {name}", withTrack(map[string]string{"tag_track": "0"}), "{name}"},
		{"no token", "{artist} - {name}", single, "{artist} - {name}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderTrackToken(tc.template, tc.song); got != tc.want {
				t.Fatalf("renderTrackToken(%q) = %q, want %q", tc.template, got, tc.want)
			}
		})
	}
}

func TestBuildDownloadFilenameWithTrackToken(t *testing.T) {
	song := &model.Song{Name: "真夏の通り雨", Artist: "宇多田光", Album: "Fantôme", Extra: map[string]string{"tag_track": "7", "tag_track_total": "11"}}
	opts := DownloadFilenameOptions{Template: "{track}. {name}", OrganizeByArtistAlbum: true}
	if got := filepath.ToSlash(BuildDownloadFilenameWithOptions(song, "m4a", opts)); got != "宇多田光/Fantôme/07. 真夏の通り雨.m4a" {
		t.Fatalf("filename = %q", got)
	}
	song.Extra = nil
	if got := filepath.ToSlash(BuildDownloadFilenameWithOptions(song, "m4a", opts)); got != "宇多田光/Fantôme/真夏の通り雨.m4a" {
		t.Fatalf("filename without track = %q", got)
	}
	if got := BuildDownloadFilenameWithOptions(song, "m4a", DownloadFilenameOptions{Template: "{track}"}); got == "" || got == ".m4a" {
		t.Fatalf("only {track} without track gave empty name: %q", got)
	}
}
