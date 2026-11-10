package applemusic

import (
	"testing"
	"time"
)

// SetCDNIdleTimeout shortens the CDN idle timeout for one test.
func SetCDNIdleTimeout(t testing.TB, d time.Duration) {
	old := cdnIdleTimeout
	cdnIdleTimeout = d
	t.Cleanup(func() { cdnIdleTimeout = old })
}
