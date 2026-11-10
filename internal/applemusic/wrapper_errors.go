package applemusic

import (
	"errors"
	"fmt"
)

var (
	// ErrWrapperNotLoggedIn means /status reported no regions.
	ErrWrapperNotLoggedIn = errors.New("apple wrapper is not logged in")
	// ErrNoKeyTemplate means /key returned no ctx/state template.
	ErrNoKeyTemplate = errors.New("apple wrapper returned no key template")
	// ErrLyricsNotFound means /lyrics answered code 404.
	ErrLyricsNotFound = errors.New("apple lyrics not found")
	// ErrNotWrapper means the URL answered JSON that is not a wrapper-lite envelope.
	ErrNotWrapper = errors.New("apple wrapper URL is not a wrapper-lite endpoint")
	// ErrInvalidAdamID means the id is not a catalog adamId.
	ErrInvalidAdamID = errors.New("invalid apple adamId")
)

// APIError is a wrapper-lite error envelope, or a non-2xx HTTP response.
type APIError struct {
	Endpoint string
	Code     int
	Msg      string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("apple wrapper /%s: code %d: %s", e.Endpoint, e.Code, e.Msg)
}
