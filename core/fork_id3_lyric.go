package core

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// replaceMP3LyricFrame 只替换 MP3 开头 ID3v2.3/v2.4 标签里的 USLT（不带时间的歌词）帧：
// 其他帧按原字节保留，标签版本不变，标签后的音频数据不动。没有 ID3 标签时新建只含 USLT 的 v2.3 标签。
// 无法安全改写的标签（v2.2、标签级不同步、扩展头、footer、帧结构损坏）直接报错，不写文件。
func replaceMP3LyricFrame(data []byte, lyric string) ([]byte, error) {
	if len(data) < 10 || string(data[:3]) != "ID3" {
		frame := id3v23Frame("USLT", id3USLTPayload(lyric))
		size := id3SynchsafeSize(len(frame))
		out := make([]byte, 0, 10+len(frame)+len(data))
		out = append(out, 'I', 'D', '3', 0x03, 0x00, 0x00)
		out = append(out, size[:]...)
		out = append(out, frame...)
		return append(out, data...), nil
	}
	version, flags := data[3], data[5]
	if version != 3 && version != 4 {
		return nil, fmt.Errorf("暂不支持改写 ID3v2.%d 标签里的歌词", version)
	}
	switch {
	case flags&0x80 != 0:
		return nil, errors.New("ID3 标签使用了不同步编码，暂不支持改写歌词")
	case flags&0x40 != 0:
		return nil, errors.New("ID3 标签带扩展头，暂不支持改写歌词")
	case flags&0x10 != 0:
		return nil, errors.New("ID3 标签带 footer，暂不支持改写歌词")
	case flags&0x0F != 0:
		return nil, errors.New("ID3 标签头含未知标志，暂不支持改写歌词")
	}
	tagSize, ok := decodeID3SynchsafeSize(data[6:10])
	if !ok || 10+tagSize > len(data) {
		return nil, errors.New("ID3 标签大小无效")
	}
	tagData := data[10 : 10+tagSize]

	kept := make([]byte, 0, len(tagData))
	pos := 0
	for pos+10 <= len(tagData) && tagData[pos] != 0 {
		header := tagData[pos : pos+10]
		if !isID3v23FrameID(header[:4]) {
			return nil, fmt.Errorf("ID3 标签在偏移 %d 处有无效帧，暂不改写", pos)
		}
		frameSize := int(binary.BigEndian.Uint32(header[4:8]))
		if version == 4 {
			if frameSize, ok = decodeID3SynchsafeSize(header[4:8]); !ok {
				return nil, fmt.Errorf("ID3 帧 %s 的大小无效", header[:4])
			}
		}
		end := pos + 10 + frameSize
		if frameSize <= 0 || end > len(tagData) {
			return nil, fmt.Errorf("ID3 帧 %s 的大小超出标签范围", header[:4])
		}
		// 压缩、加密等帧按原字节整体保留，大小已含在帧头里，不需要解开。
		if string(header[:4]) != "USLT" {
			kept = append(kept, tagData[pos:end]...)
		}
		pos = end
	}
	for _, b := range tagData[pos:] {
		if b != 0 {
			return nil, errors.New("ID3 标签帧之后有非填充数据，暂不改写")
		}
	}

	var frame []byte
	if version == 4 {
		// v2.4 允许 UTF-8（编码字节 0x03）；帧大小用 synchsafe。
		payload := append([]byte{0x03, 'e', 'n', 'g', 0x00}, lyric...)
		size := id3SynchsafeSize(len(payload))
		frame = append([]byte("USLT"), size[:]...)
		frame = append(frame, 0x00, 0x00)
		frame = append(frame, payload...)
	} else {
		frame = id3v23Frame("USLT", id3USLTPayload(lyric))
	}
	frames := append(kept, frame...)

	// 新内容放得下就补零到原标签大小，音频起始位置不变；放不下就不留填充。
	newSize := len(frames)
	if newSize < tagSize {
		newSize = tagSize
	}
	if newSize > 0x0FFFFFFF {
		return nil, errors.New("ID3 标签过大")
	}
	size := id3SynchsafeSize(newSize)
	audio := data[10+tagSize:]
	out := make([]byte, 0, 10+newSize+len(audio))
	out = append(out, data[:6]...)
	out = append(out, size[:]...)
	out = append(out, frames...)
	out = append(out, make([]byte, newSize-len(frames))...)
	return append(out, audio...), nil
}
