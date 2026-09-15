package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type sqliteFixtureCode int

func (e sqliteFixtureCode) Error() string { return "sqlite fixture" }
func (e sqliteFixtureCode) Code() int     { return int(e) }
func TestV15FixtureRetriesOnlyTransientSQLite(t *testing.T) {
	calls := 0
	if e := retryFixture(context.Background(), time.Second, func() error {
		calls++
		if calls < 3 {
			return sqliteFixtureCode(517)
		}
		return nil
	}); e != nil || calls != 3 {
		t.Fatal(e, calls)
	}
	calls = 0
	if e := retryFixture(context.Background(), time.Second, func() error { calls++; return fmt.Errorf("corrupt data") }); e == nil || calls != 1 {
		t.Fatal(e, calls)
	}
}
func TestV15FixturePersistentLockStillFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	if e := retryFixture(ctx, time.Second, func() error { calls++; return nil }); e == nil || calls != 0 {
		t.Fatal(e, calls)
	}
	if e := retryFixture(context.Background(), time.Millisecond, func() error { return sqliteFixtureCode(5) }); e == nil {
		t.Fatal("persistent lock masked")
	}
}
