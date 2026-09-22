package xxt

import (
	"net/http"
	"testing"
	"time"
)

// A malformed redirect Location (upstream-controlled) must return an error, not
// panic on a nil *http.Request from a discarded http.NewRequest error.
func TestCampusQRRedirectURLRejectsMalformedLocation(t *testing.T) {
	c := &Client{http: &http.Client{Timeout: time.Second}}
	cli := &http.Client{Timeout: time.Second}
	_, err := c.campusQRRedirectURL(cli, "http://\x7f", "https://i.chaoxing.com/")
	if err == nil {
		t.Fatal("malformed redirect URL did not return an error")
	}
}
