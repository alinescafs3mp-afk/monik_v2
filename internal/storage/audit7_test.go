package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAudit7RetentionCancelledContextMakesNoProgress(t *testing.T) {
	s, _ := audit5Store(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := s.retainRaw(ctx, 48*time.Hour); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestAudit7PinMissingServiceRejected(t *testing.T) {
	s, _ := audit5Store(t)
	if e := s.SetServicePinned("missing", true, nil); e != ErrNotFound {
		t.Fatal(e)
	}
}
