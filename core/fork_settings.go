package core

// 本 fork（bootids/go-music-dl）新增设置项的归一化与校验，独立于上游 config_store.go，减少合并上游时的冲突。

import (
	"fmt"
	"strings"
)

// normalizeAppleWebSettings 是 normalizeWebSettings 的钩子：整理 Apple Music wrapper 地址和下载音质。
func normalizeAppleWebSettings(settings WebSettings) WebSettings {
	settings.AppleWrapperURL = strings.TrimSuffix(strings.TrimSpace(settings.AppleWrapperURL), "/")
	settings.AppleAudioQuality = strings.ToLower(strings.TrimSpace(settings.AppleAudioQuality))
	if settings.AppleAudioQuality != "alac" && settings.AppleAudioQuality != "aac" {
		settings.AppleAudioQuality = "alac"
	}
	return settings
}

// validateAppleWebSettings 是 validateWebSettings 的钩子：校验 Apple Music wrapper 地址。
func validateAppleWebSettings(settings WebSettings) error {
	if settings.AppleWrapperURL != "" {
		if err := ValidateAppleWrapperURL(settings.AppleWrapperURL); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidWebSettings, err)
		}
	}
	return nil
}

// normalizeArtistAliasSettings 是 normalizeWebSettings 的钩子：去掉歌手别名设置首尾的空白。
func normalizeArtistAliasSettings(settings WebSettings) WebSettings {
	settings.ArtistAliases = strings.TrimSpace(settings.ArtistAliases)
	return settings
}
