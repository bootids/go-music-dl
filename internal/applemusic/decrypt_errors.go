package applemusic

import "errors"

var (
	// ErrNoUsableVariant means the master playlist has no ALAC or AAC variant.
	ErrNoUsableVariant = errors.New("apple music: no usable audio variant")
	// ErrUnsupportedPlaylist means the playlist is not a single-file byte-range FairPlay playlist.
	ErrUnsupportedPlaylist = errors.New("apple music: unsupported playlist")
	// ErrUnsupportedPattern means the cbcs pattern uses skip blocks.
	ErrUnsupportedPattern = errors.New("apple music: unsupported cbcs pattern")
	// ErrUnsupportedScheme means a track is protected with a scheme other than cbcs.
	ErrUnsupportedScheme = errors.New("apple music: unsupported protection scheme")
	// ErrSegmentMismatch means the file has a different number of fragments than the playlist has segments.
	ErrSegmentMismatch = errors.New("apple music: fragments do not match playlist segments")
)
