package handler

import (
	"testing"
	"time"

	"xbt2/server/internal/model"
)

func TestSharePublicViewEmitsExpiryText(t *testing.T) {
	expiresAt := time.Date(2026, 10, 22, 14, 3, 12, 0, time.UTC)
	view := sharePublicView(model.SignShare{ExpiresAt: expiresAt})
	if got := view["expires_at_text"]; got != "2026/10/22 22:03:12" {
		t.Fatalf("expires_at_text = %v, want 2026/10/22 22:03:12", got)
	}
	if got := view["expires_at"]; got != expiresAt.UnixMilli() {
		t.Fatalf("expires_at = %v, want %d", got, expiresAt.UnixMilli())
	}
}

func TestSharePublicViewEmitsEmptyTextForZeroExpiry(t *testing.T) {
	view := sharePublicView(model.SignShare{})
	if got := view["expires_at_text"]; got != "" {
		t.Fatalf("expires_at_text = %v, want empty", got)
	}
}
