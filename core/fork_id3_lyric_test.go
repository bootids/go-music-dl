package core

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhowden/tag"
)

var testMPEGFrames = bytes.Repeat([]byte{0xff, 0xfb, 0x90, 0x64, 0x01, 0x02, 0x03, 0x04}, 16)

type testID3Frame struct {
	id      string
	flags   [2]byte
	payload []byte
}

func (f testID3Frame) bytes(version byte) []byte {
	out := []byte(f.id)
	if version == 4 {
		size := id3SynchsafeSize(len(f.payload))
		out = append(out, size[:]...)
	} else {
		out = binary.BigEndian.AppendUint32(out, uint32(len(f.payload)))
	}
	out = append(out, f.flags[:]...)
	return append(out, f.payload...)
}

func latin1Text(s string) []byte { return append([]byte{0x00}, s...) }

// buildTestID3 生成 ID3v2.3/v2.4 标签 + 填充 + 假 MPEG 帧。APIC 超过 127 字节，
// v2.4 的 synchsafe 大小与普通大端写法不同，能测出按错误方式解析帧大小的问题。
func buildTestID3(version byte, padding int) ([]byte, []testID3Frame) {
	apic := append([]byte{0x00}, "image/jpeg\x00\x03\x00"...)
	apic = append(apic, 0xff, 0xd8)
	apic = append(apic, bytes.Repeat([]byte{0xab}, 300)...)
	apic = append(apic, 0xff, 0xd9)
	oldLyric := append([]byte{0x00, 'e', 'n', 'g', 0x00}, "[00:01.00]旧逐行"...)
	frames := []testID3Frame{
		{id: "TIT2", payload: latin1Text("Song")},
		{id: "TPE1", payload: latin1Text("Artist")},
		{id: "USLT", payload: oldLyric},
		{id: "TRCK", payload: latin1Text("3/9")},
		{id: "TDRC", payload: latin1Text("2020")},
		{id: "TCON", payload: latin1Text("Pop")},
		{id: "TPE2", payload: latin1Text("Various")},
		{id: "TXXX", flags: [2]byte{0x00, 0x00}, payload: append(latin1Text("REPLAYGAIN_TRACK_GAIN\x00"), "-6.50 dB"...)},
		{id: "APIC", payload: apic},
		{id: "USLT", payload: append([]byte{0x00, 'c', 'h', 'i', 'x', 0x00}, "second"...)},
	}
	var body []byte
	for _, f := range frames {
		body = append(body, f.bytes(version)...)
	}
	body = append(body, make([]byte, padding)...)
	size := id3SynchsafeSize(len(body))
	out := append([]byte{'I', 'D', '3', version, 0x00, 0x00}, size[:]...)
	out = append(out, body...)
	return append(out, testMPEGFrames...), frames
}

// parseTestID3 按版本解析帧，返回每帧原始字节和标签后的数据。
func parseTestID3(t *testing.T, data []byte) (version byte, frames [][]byte, rest []byte) {
	t.Helper()
	if string(data[:3]) != "ID3" {
		t.Fatal("no ID3 tag")
	}
	tagSize, ok := decodeID3SynchsafeSize(data[6:10])
	if !ok {
		t.Fatal("bad tag size")
	}
	tagData := data[10 : 10+tagSize]
	for pos := 0; pos+10 <= len(tagData) && tagData[pos] != 0; {
		size := int(binary.BigEndian.Uint32(tagData[pos+4 : pos+8]))
		if data[3] == 4 {
			size, _ = decodeID3SynchsafeSize(tagData[pos+4 : pos+8])
		}
		frames = append(frames, tagData[pos:pos+10+size])
		pos += 10 + size
	}
	return data[3], frames, data[10+tagSize:]
}

func TestReplaceLocalSongLyricMP3KeepsAllID3Frames(t *testing.T) {
	for _, version := range []byte{3, 4} {
		for _, padding := range []int{0, 2048} {
			original, fixtureFrames := buildTestID3(version, padding)
			path := filepath.Join(t.TempDir(), "歌.mp3")
			if err := os.WriteFile(path, original, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ReplaceLocalSongLyric(path, testEnhancedLyric, ""); err != nil {
				t.Fatalf("v2.%d padding=%d: %v", version, padding, err)
			}
			data, _ := os.ReadFile(path)
			gotVersion, frames, rest := parseTestID3(t, data)
			if gotVersion != version || data[4] != 0 {
				t.Fatalf("version = 2.%d.%d, want 2.%d.0", gotVersion, data[4], version)
			}
			if !bytes.Equal(rest, testMPEGFrames) {
				t.Fatalf("v2.%d: audio bytes after tag changed", version)
			}
			var wantOther, gotOther [][]byte
			for _, f := range fixtureFrames {
				if f.id != "USLT" {
					wantOther = append(wantOther, f.bytes(version))
				}
			}
			uslt := 0
			for _, f := range frames {
				if string(f[:4]) == "USLT" {
					uslt++
				} else {
					gotOther = append(gotOther, f)
				}
			}
			if uslt != 1 {
				t.Fatalf("v2.%d: USLT frames = %d, want 1", version, uslt)
			}
			if len(gotOther) != len(wantOther) {
				t.Fatalf("v2.%d: kept %d frames, want %d", version, len(gotOther), len(wantOther))
			}
			for i := range wantOther {
				if !bytes.Equal(gotOther[i], wantOther[i]) {
					t.Fatalf("v2.%d: frame %s changed", version, wantOther[i][:4])
				}
			}
			m, err := tag.ReadFrom(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(m.Lyrics()) != testEnhancedLyric || m.Title() != "Song" || m.Picture() == nil {
				t.Fatalf("v2.%d: read back lyrics=%q title=%q cover=%v", version, m.Lyrics(), m.Title(), m.Picture() != nil)
			}
		}
	}
}

func TestReplaceLocalSongLyricMP3WithoutTag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "歌.mp3")
	if err := os.WriteFile(path, testMPEGFrames, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceLocalSongLyric(path, testEnhancedLyric, ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	_, frames, rest := parseTestID3(t, data)
	if len(frames) != 1 || string(frames[0][:4]) != "USLT" || !bytes.Equal(rest, testMPEGFrames) {
		t.Fatalf("frames=%d rest changed=%v", len(frames), !bytes.Equal(rest, testMPEGFrames))
	}
	m, err := tag.ReadFrom(bytes.NewReader(data))
	if err != nil || strings.TrimSpace(m.Lyrics()) != testEnhancedLyric {
		t.Fatalf("lyrics = %v err=%v", m, err)
	}
}

func TestReplaceLocalSongLyricMP3RejectsUnsafeTags(t *testing.T) {
	v22Body := append([]byte("TT2\x00\x00\x05"), latin1Text("Song")...)
	v22Size := id3SynchsafeSize(len(v22Body))
	v22 := append([]byte{'I', 'D', '3', 0x02, 0x00, 0x00}, v22Size[:]...)
	v22 = append(append(v22, v22Body...), testMPEGFrames...)

	unsync, _ := buildTestID3(3, 0)
	unsync[5] = 0x80
	ext, _ := buildTestID3(4, 0)
	ext[5] = 0x40
	garbage, _ := buildTestID3(4, 16)
	garbage[len(garbage)-len(testMPEGFrames)-3] = 0x7f

	for name, data := range map[string][]byte{"v2.2": v22, "unsync": unsync, "extended": ext, "garbage": garbage} {
		path := filepath.Join(t.TempDir(), "歌.mp3")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ReplaceLocalSongLyric(path, testEnhancedLyric, ""); err == nil {
			t.Fatalf("%s: want error", name)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
			t.Fatalf("%s: file changed", name)
		}
	}
}
