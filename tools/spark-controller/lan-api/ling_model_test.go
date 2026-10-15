package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func lingGateway(t *testing.T, tr http.RoundTripper) (*gateway, http.Handler) {
	t.Helper()
	g, _ := testGateway(t, tr)
	h, err := g.handler(31002)
	if err != nil {
		t.Fatal(err)
	}
	return g, h
}

func TestLingModelRefusalsNeverReachUpstream(t *testing.T) {
	var calls atomic.Int32
	g, h := lingGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(r, "unexpected"), nil
	}))
	for _, body := range []string{
		``, `null`, `[]`, `"` + lingModel + `"`, `{}`, `{"messages":[]}`,
		`{"model":"wrong"}`, `{"model":"LING-3-flash-124b-heretic-text"}`,
		`{"model":null}`, `{"model":1}`, `{"model":true}`, `{"model":[]}`, `{"model":{}}`,
		`{"model":"` + lingModel + `","model":"` + lingModel + `"}`,
		`{"model":"wrong","model":"` + lingModel + `"}`,
		`{"mo\u0064el":"` + lingModel + `","model":"` + lingModel + `"}`,
		`{"model":"` + lingModel + `","mo\u0064el":"` + lingModel + `"}`,
		`{"messages":{"model":"` + lingModel + `"}}`,
		`{"model":"` + lingModel + `"} {}`, `{"model":"` + lingModel + `"}garbage`,
		`{"model":"` + lingModel + `",}`, `{"model":"` + lingModel + `"`,
		`{"model":"` + lingModel + `","messages":["bad\x20escape"]}`,
		`{"model":"` + strings.Repeat("x", 300) + `"}`,
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || w.Body.String() != "Bad Request\n" {
			t.Fatalf("malformed/wrong model was not refused: %.200q (%d)", body, w.Code)
		}
		if g.lingBodyBytes.Load() != 0 {
			t.Fatal("refused request leaked body reservation")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("model refusal contacted upstream")
	}
}

func TestLingValidJSONPreservedByteForByte(t *testing.T) {
	var expected string
	var calls atomic.Int32
	g, h := lingGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != expected || r.ContentLength != int64(len(expected)) {
			t.Fatal("accepted Ling body/length changed")
		}
		if r.Header.Get("Authorization") != "" || r.URL.Host != "127.0.0.1:31002" {
			t.Fatal("Ling target/credential contract changed")
		}
		return fakeResponse(r, "unchanged"), nil
	}))
	for _, body := range []string{
		`{"model":"` + lingModel + `"}`,
		" \t\r\n{ \"model\" : \"" + lingModel + "\" " + strings.Repeat(" ", 300) + "}\n",
		`{"mo\u0064el":"ling-3-flash-124b-heretic-tex\u0074"}`,
		`{"messages":[{"model":"wrong","content":"a brace } comma , quote \" and slash \\"}],"model":"` + lingModel + `","seed":-2.5e+3,"nothing":null,"flags":[true,false]}`,
		`{"nested":{"nested":[1,{"model":"not-top-level"}]},"model":"` + lingModel + `"}`,
		`{"` + strings.Repeat("m", 500) + `":{},"model":"` + lingModel + `"}`,
		`{"model":"` + lingModel + `","stream":true,"unknown":"` + strings.Repeat("x", 2<<20) + `"}`,
	} {
		expected = body
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest("POST", "/v1/chat/completions?original=yes", strings.NewReader(body)))
		if w.Code != 200 || w.Body.String() != "unchanged" || g.lingBodyBytes.Load() != 0 {
			t.Fatal("valid Ling body refused or body reservation leaked")
		}
	}
	if calls.Load() != 7 {
		t.Fatal("accepted Ling call count changed")
	}
}

func TestLingGuardOnlyTouchesExactPostChatRoute(t *testing.T) {
	var calls atomic.Int32
	g, h := lingGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(r, "native"), nil
	}))
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/v1/models", ""}, {"GET", "/v1/chat/completions", ""},
		{"POST", "/v1/completions", "opaque-original-body"},
		{"POST", "/v1/chat/completions/", "opaque-original-body"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != 200 || g.lingBodyBytes.Load() != 0 {
			t.Fatal("unrelated route was changed")
		}
	}
	if calls.Load() != 4 {
		t.Fatal("unrelated native route did not reach upstream")
	}
}

func TestLingBodyBudgetRejectsAndReleases(t *testing.T) {
	g, h := lingGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return fakeResponse(r, "native"), nil
	}))
	if !g.reserveLingBody(maxRequestBytes) || g.reserveLingBody(1) || g.reserveLingBody(-1) {
		t.Fatal("body budget admission is not bounded")
	}
	w := httptest.NewRecorder()
	body := `{"model":"` + lingModel + `"}`
	h.ServeHTTP(w, authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
	if w.Code != 503 || g.lingBodyBytes.Load() != maxRequestBytes {
		t.Fatal("body saturation did not fail before upstream")
	}
	g.lingBodyBytes.Add(-maxRequestBytes)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
	if w.Code != 200 || g.lingBodyBytes.Load() != 0 {
		t.Fatal("released body budget not reusable")
	}
}

func TestLingBodyBudgetConcurrentAdmission(t *testing.T) {
	g, _ := lingGateway(t, nil)
	const size = maxRequestBytes / 8
	entered := make(chan bool, 16)
	release := make(chan struct{})
	done := make(chan struct{}, 16)
	for i := 0; i < 16; i++ {
		go func() {
			accepted := g.reserveLingBody(size)
			entered <- accepted
			if accepted {
				<-release
				g.lingBodyBytes.Add(-size)
			}
			done <- struct{}{}
		}()
	}
	accepted := 0
	for i := 0; i < 16; i++ {
		if <-entered {
			accepted++
		}
	}
	if accepted != 8 || g.lingBodyBytes.Load() != maxRequestBytes {
		close(release)
		t.Fatal("concurrent admissions exceeded or underused the shared body budget")
	}
	close(release)
	for i := 0; i < 16; i++ {
		<-done
	}
	if g.lingBodyBytes.Load() != 0 {
		t.Fatal("concurrent release leaked retained-body capacity")
	}
}

func TestLingTruncatedBodyAndAuthDoNotReserveOrForward(t *testing.T) {
	var calls atomic.Int32
	g, h := lingGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(r, "unexpected"), nil
	}))
	r := authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+lingModel+`"}`))
	r.ContentLength++
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || g.lingBodyBytes.Load() != 0 {
		t.Fatal("truncated upload accepted or leaked reservation")
	}
	r = authenticatedRequest("POST", "/v1/chat/completions", nil)
	r.ContentLength = maxRequestBytes
	r.Header.Del("Authorization")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || calls.Load() != 0 || g.lingBodyBytes.Load() != 0 {
		t.Fatal("unauthenticated Ling body was consumed/reserved/forwarded")
	}
}

func TestLingPostSSECancellationAndBodyRelease(t *testing.T) {
	canceled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer backend.Close()
	g, h := lingGateway(t, ephemeralTransport(t, backend))
	front := httptest.NewServer(h)
	defer front.Close()
	body := `{"model":"` + lingModel + `","stream":true}`
	r, _ := http.NewRequest("POST", front.URL+"/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testKey)
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" || g.lingBodyBytes.Load() != int64(len(body)) {
		t.Fatal("accepted Ling stream was buffered or lost reservation")
	}
	_ = response.Body.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("accepted Ling disconnect did not cancel upstream")
	}
	deadline := time.Now().Add(time.Second)
	for g.lingBodyBytes.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if g.lingBodyBytes.Load() != 0 {
		t.Fatal("stream disconnect leaked retained-body reservation")
	}
}

func TestLingPartialUploadDisconnectNeverReachesUpstream(t *testing.T) {
	var calls atomic.Int32
	g, h := lingGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(r, "unexpected"), nil
	}))
	front := httptest.NewServer(h)
	defer front.Close()
	conn, err := net.DialTimeout("tcp4", strings.TrimPrefix(front.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer %s\r\nContent-Length: 64\r\n\r\nx", testKey)
	_ = conn.(*net.TCPConn).CloseWrite()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatal("truncated Ling upload did not receive 400")
	}
	_ = response.Body.Close()
	_ = conn.Close()
	deadline := time.Now().Add(time.Second)
	for len(g.inFlight) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if g.lingBodyBytes.Load() != 0 || calls.Load() != 0 {
		t.Fatal("incomplete Ling upload retained bytes or contacted upstream")
	}
}

func TestLingSilentThinkingPostCancellation(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}))
	defer backend.Close()
	_, h := lingGateway(t, ephemeralTransport(t, backend))
	front := httptest.NewServer(h)
	defer front.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "POST", front.URL+"/v1/chat/completions", strings.NewReader(`{"model":"`+lingModel+`"}`))
	r.Header.Set("Authorization", "Bearer "+testKey)
	done := make(chan struct{})
	go func() {
		response, _ := http.DefaultClient.Do(r)
		if response != nil {
			response.Body.Close()
		}
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("silent Ling request did not arrive")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("client cancellation stalled")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("silent Ling upstream did not cancel")
	}
}

func FuzzExactLingModel(f *testing.F) {
	for _, body := range []string{
		`{"model":"` + lingModel + `"}`,
		`{"mo\u0064el":"` + lingModel + `","messages":[{"content":"\\\"},["}]}`,
		`{"model":"wrong"}`, `{"model":null}`, `null`, `[]`, `{}`, ``,
		`{"model":"` + lingModel + `","model":"` + lingModel + `"}`,
		`{"long":"` + strings.Repeat("x", 500) + `","model":"` + lingModel + `"}`,
	} {
		f.Add([]byte(body))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		got := exactLingModel(body)
		// Independent bounded-input stdlib token walk for the oracle. Fuzzer
		// inputs stay below 1 MiB; production never uses this allocating path.
		if len(body) > 1<<20 {
			return
		}
		want := false
		if json.Valid(body) {
			d := json.NewDecoder(bytes.NewReader(body))
			first, _ := d.Token()
			if first == json.Delim('{') {
				count, model := 0, ""
				for d.More() {
					key, _ := d.Token()
					var raw json.RawMessage
					_ = d.Decode(&raw)
					if key == "model" {
						count++
						_ = json.Unmarshal(raw, &model)
					}
				}
				want = count == 1 && model == lingModel
			}
		}
		if got != want {
			t.Fatalf("contract scanner differs from stdlib oracle: got %v want %v body %.500q", got, want, body)
		}
	})
}
