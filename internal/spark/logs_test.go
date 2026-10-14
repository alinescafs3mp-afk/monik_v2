package spark

import (
	"context"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestLogCompleteRecordsRedactionAcrossChunksAndOversize(t *testing.T) {
	r := &Redactor{secrets: []string{"secret-cross-chunk"}}
	a := RecordAssembler{}
	records := []string{}
	emit := func(b []byte, _ bool) { records = append(records, r.Clean(b)) }
	a.Feed([]byte("authorization: Bearer abc123 secret-cross-"), emit)
	if len(records) != 0 {
		t.Fatal("partial secret escaped")
	}
	a.Feed([]byte("chunk\x1b[31m\r\n"), emit)
	if len(records) != 1 || strings.Contains(records[0], "abc123") || strings.Contains(records[0], "secret-cross") || strings.ContainsAny(records[0], "\r\x1b") {
		t.Fatalf("unsafe record %q", records)
	}
	a.Feed([]byte(strings.Repeat("x", LogMaxLine+1)), emit)
	a.Feed([]byte("\n"), emit)
	if records[1] != "[oversized log record dropped]" {
		t.Fatalf("giant line retained %q", records[1])
	}
	a.Feed([]byte("incomplete hf_ABCDEFGHIJKLMNOP"), emit)
	a.Finish(emit)
	if records[2] != "[incomplete log record dropped]" {
		t.Fatal(records)
	}
}
func TestLogViewerSharingAndHostBounds(t *testing.T) {
	_, f, p := controlFixture(t)
	p.LogEnabled = true
	ctx, end := context.WithCancel(context.Background())
	defer end()
	h := NewLogHub()
	subs := []LogSubscription{}
	for i := 0; i < 16; i++ {
		sub, e := h.Subscribe(ctx, p, f)
		if e != nil {
			t.Fatal(e)
		}
		subs = append(subs, sub)
	}
	if _, e := h.Subscribe(ctx, p, f); e == nil {
		t.Fatal("viewer limit not enforced")
	}
	deadline := time.Now().Add(time.Second)
	for {
		f.mu.Lock()
		calls := f.logCalls
		f.mu.Unlock()
		if calls == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upstream did not start")
		}
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 100; i++ {
		h.append(p.ID, "", false)
	}
	h.mu.Lock()
	if h.viewers != 0 {
		t.Fatalf("slow viewers retained %d", h.viewers)
	}
	h.mu.Unlock()
	for _, sub := range subs {
		sub.Close()
	}
	end()
	f.mu.Lock()
	calls := f.logCalls
	f.mu.Unlock()
	if calls != 1 {
		t.Fatalf("viewers multiplied source %d", calls)
	}
}
func TestLogRingBoundsIncludingEmptyRecords(t *testing.T) {
	h := NewLogHub()
	h.profiles["test"] = &logProfile{viewers: map[int]chan protocol.SparkLogRecord{1: make(chan protocol.SparkLogRecord, 10000)}, cancel: func() {}}
	for i := 0; i < 5000; i++ {
		h.append("test", "", false)
	}
	h.mu.Lock()
	p := h.profiles["test"]
	if len(p.records) > 4096 || p.used > LogProfileBytes {
		t.Fatal("empty records unbounded")
	}
	h.mu.Unlock()
}
