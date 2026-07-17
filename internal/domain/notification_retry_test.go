package domain

import (
	"testing"
	"time"
)

func TestNextDeliveryRetryAt(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	// Attempt 1 failing schedules the first retry 1 minute out.
	got, ok := NextDeliveryRetryAt(1, now)
	if !ok || !got.Equal(now.Add(1*time.Minute)) {
		t.Fatalf("attempt 1: got %v ok=%v, want +1m", got, ok)
	}

	// Backoff grows: attempt 4 → +1h.
	if got, ok := NextDeliveryRetryAt(4, now); !ok || !got.Equal(now.Add(1*time.Hour)) {
		t.Errorf("attempt 4: got %v ok=%v, want +1h", got, ok)
	}

	// The last backoff entry (attempt 5) still schedules (+6h)...
	if _, ok := NextDeliveryRetryAt(5, now); !ok {
		t.Error("attempt 5 should still schedule a retry")
	}
	// ...but attempt 6 (the final attempt) failing gives up.
	if _, ok := NextDeliveryRetryAt(6, now); ok {
		t.Error("attempt 6 must give up (failed_permanent)")
	}

	// Total attempts cap matches the backoff length + 1.
	if NotificationMaxDeliveryAttempts != len(notificationRetryBackoff)+1 {
		t.Errorf("NotificationMaxDeliveryAttempts=%d, want %d",
			NotificationMaxDeliveryAttempts, len(notificationRetryBackoff)+1)
	}
}
