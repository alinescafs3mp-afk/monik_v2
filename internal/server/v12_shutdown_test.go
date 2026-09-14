package server

import (
	"context"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"testing"
	"time"
)

func TestV12CancelledServerRunReturnsNormally(t *testing.T) {
	a, _ := testApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("normal service stop reported as failure: %v", err)
	}
}
func TestV12ConsoleShutdownRevokesOutstandingTickets(t *testing.T) {
	a, _ := testApp(t)
	s := &storage.Session{ID: "session"}
	target := consoleTarget{Host: "127.0.0.1", Port: 22, Username: "fixture"}
	a.console.tickets = map[string]consoleTicket{"ticket": {Session: s.ID, Agent: "host", Target: targetIdentity(target), Expires: time.Now().Add(time.Minute)}}
	a.console.closeAll()
	if a.takeConsoleTicket("ticket", s, "host", target) {
		t.Fatal("shutdown still admits a session after waiting for active connections")
	}
}
