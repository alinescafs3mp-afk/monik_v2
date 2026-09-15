package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// The fixture competes with intentional browser test writes. Like a real agent
// it retries the SAME observation when SQLite reports transient busy/locked.
// Non-transient failures and exhausted deadlines still fail browser acceptance.
func retryFixture(ctx context.Context, budget time.Duration, fn func() error) error {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		e := fn()
		if e == nil {
			return nil
		}
		var c interface{ Code() int }
		if !errors.As(e, &c) || (c.Code()&255 != 5 && c.Code()&255 != 6) {
			return e
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("fixture database stayed busy: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
