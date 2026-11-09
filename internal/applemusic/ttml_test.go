package applemusic

import "testing"

func TestTTMLToLRC(t *testing.T) {
	const ns = `xmlns="http://www.w3.org/ns/ttml" xmlns:itunes="http://music.apple.com/lyric-ttml-internal" xmlns:ttm="http://www.w3.org/ns/ttml#metadata"`
	cases := []struct {
		name, in, want string
	}{
		{
			name: "line timed",
			in: `<tt ` + ns + ` itunes:timing="Line"><head><metadata><ttm:agent xml:id="v1"/></metadata></head><body dur="120.0">` +
				`<div begin="26.663"><p begin="26.663" end="31.467">說不上為什麼 我變得很主動</p><p begin="1:31.673" end="1:36.360">Second &amp; line</p></div>` +
				`<div><p begin="01:02:03.4">Hour</p></div></body></tt>`,
			want: "[00:26.66]說不上為什麼 我變得很主動\n[01:31.67]Second & line\n[62:03.40]Hour\n",
		},
		{
			name: "untimed",
			in:   `<tt ` + ns + ` itunes:timing="None"><body><div><p>First</p><p>Second</p></div></body></tt>`,
			want: "First\nSecond\n",
		},
		{
			name: "untimed via missing begin",
			in:   `<tt ` + ns + `><body><div><p>Only</p></div></body></tt>`,
			want: "Only\n",
		},
		{
			name: "word timed",
			in: `<tt ` + ns + ` itunes:timing="Word"><body><div><p begin="7.439" end="9.794"><span begin="7.439" end="7.988">Don't</span> <span begin="7.988" end="8.700">rush</span></p>` +
				`<p begin="00:00:09.304s"><span begin="9.304">under</span><span begin="13.822">co</span><span begin="14.125">ver</span></p></div></body></tt>`,
			want: "[00:07.43]Don't rush\n[00:09.30]undercover\n",
		},
		{
			name: "non-xml passthrough",
			in:   "[00:01.00]already lrc\n",
			want: "[00:01.00]already lrc\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TTMLToLRC(tc.in)
			if err != nil {
				t.Fatalf("TTMLToLRC: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestTTMLToLRCBadTime(t *testing.T) {
	if _, err := TTMLToLRC(`<tt><body><p begin="x">a</p></body></tt>`); err == nil {
		t.Fatal("want error for bad begin")
	}
}
