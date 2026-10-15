package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "isolated-test-credential-only"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeResponse(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: r}
}

func testGateway(t *testing.T, transport http.RoundTripper) (*gateway, http.Handler) {
	t.Helper()
	g, err := newGateway(testKey, transport)
	if err != nil {
		t.Fatal(err)
	}
	h, err := g.handler(31001)
	if err != nil {
		t.Fatal(err)
	}
	return g, h
}

func authenticatedRequest(method, path string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, path, body)
	r.Header.Set("Authorization", "Bearer "+testKey)
	return r
}

func TestMissingEmptyAndMalformedKeyFailClosed(t *testing.T) {
	for _, key := range []string{"", " ", "with space", "with\nnewline", "with\x00nul", strings.Repeat("x", 1025)} {
		g, err := newGateway(key, nil)
		if err == nil || g != nil || strings.Contains(err.Error(), key) && len(key) > 1 {
			t.Fatalf("invalid credential accepted or reflected")
		}
	}
	g, err := newGateway(testKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr := g.transport.(*http.Transport)
	if tr.Proxy != nil || tr.ResponseHeaderTimeout != 0 {
		t.Fatal("environment proxies or thinking deadline enabled")
	}
}

func TestAuthDenialsNeverReachUpstream(t *testing.T) {
	var calls atomic.Int32
	_, handler := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(r, "ok"), nil
	}))
	for _, values := range [][]string{nil, {"Bearer wrong"}, {"Basic " + testKey}, {"Bearer " + testKey, "Bearer " + testKey}, {"Bearer " + testKey + ", Bearer " + testKey}, {"Bearer  " + testKey}} {
		r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		for _, value := range values {
			r.Header.Add("Authorization", value)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), testKey) {
			t.Fatal("auth did not fail with fixed response")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("auth denial contacted upstream")
	}
}

func TestFixedPortsTransparentPayloadAndCredentialStripping(t *testing.T) {
	payload := `{"model":"nemotron-3-super-120b-heretic-v2","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"none","chat_template_kwargs":{"low_effort":true},"max_tokens":1200}`
	var expectedPort int
	var expectedPayload string
	g, _ := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "http" || r.URL.Host != fmt.Sprintf("127.0.0.1:%d", expectedPort) || r.Host != r.URL.Host || r.URL.RequestURI() != "/v1/chat/completions?native=yes" {
			t.Fatal("target/path/query differs from fixed API")
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Fatal("credential or spoofed forwarding header reached upstream")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != expectedPayload {
			t.Fatal("native request body changed")
		}
		response := fakeResponse(r, `{"native":"unchanged"}`)
		response.Header.Set("Content-Type", "application/json")
		return response, nil
	}))
	for _, port := range fixedPorts {
		expectedPort = port
		expectedPayload = payload
		if port == 31002 {
			expectedPayload = strings.Replace(payload, "nemotron-3-super-120b-heretic-v2", lingModel, 1)
		}
		h, err := g.handler(port)
		if err != nil {
			t.Fatal(err)
		}
		r := authenticatedRequest(http.MethodPost, "/v1/chat/completions?native=yes", strings.NewReader(expectedPayload))
		r.Header.Set("Proxy-Authorization", "secret-not-forwarded")
		r.Header.Set("Forwarded", "host=attacker.invalid")
		r.Header.Set("X-Forwarded-Host", "attacker.invalid")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != `{"native":"unchanged"}` || w.Header().Get("Content-Type") != "application/json" {
			t.Fatal("response transparency failed")
		}
	}
	for _, port := range []int{0, 30000, 31000, 31006, 65535} {
		if _, err := g.handler(port); err == nil {
			t.Fatal("non-V4 port accepted")
		}
	}
}

func TestRoutesMethodsAndFramingNeverEscape(t *testing.T) {
	var calls atomic.Int32
	_, handler := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(r, "ok"), nil
	}))
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/", 404}, {"GET", "/v1", 404}, {"GET", "/other", 404},
		{"GET", "/v1/../other", 404}, {"GET", "/v1/%2e%2e/other", 404},
		{"GET", "/v1/%2fother", 404}, {"GET", "http://attacker.invalid/v1/models", 404},
		{"POST", "/health", 405}, {"CONNECT", "/v1/models", 405}, {"OPTIONS", "/v1/models", 405}, {"PUT", "/v1/models", 405},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, authenticatedRequest(tc.method, tc.path, nil))
		if w.Code != tc.code {
			t.Fatalf("%s %s: got %d want %d", tc.method, tc.path, w.Code, tc.code)
		}
	}
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.TransferEncoding = []string{"chunked"} },
		func(r *http.Request) { r.Trailer = http.Header{"Unsafe": []string{"value"}} },
		func(r *http.Request) { r.Header.Set("Upgrade", "websocket") },
		func(r *http.Request) { r.Header.Set("Connection", "keep-alive, content-length") },
		func(r *http.Request) { r.Method = "GET" },
	} {
		r := authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader("x"))
		change(r)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("unsafe framing got %d", w.Code)
		}
	}
	r := authenticatedRequest("POST", "/v1/chat/completions", nil)
	r.ContentLength = maxRequestBytes + 1
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal("oversized body accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("denied request reached upstream")
	}
}

func TestRawDuplicateAuthAndConflictingFraming(t *testing.T) {
	var calls atomic.Int32
	_, h := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { calls.Add(1); return fakeResponse(r, "ok"), nil }))
	front := httptest.NewServer(h)
	defer front.Close()
	address := strings.TrimPrefix(front.URL, "http://")
	for _, headers := range []string{
		"Authorization: Bearer " + testKey + "\r\nAuthorization: Bearer " + testKey + "\r\nContent-Length: 0\r\n",
		"Authorization: Bearer " + testKey + "\r\nContent-Length: 1\r\nContent-Length: 2\r\n",
		"Authorization: Bearer " + testKey + "\r\nTransfer-Encoding: chunked\r\nContent-Length: 5\r\n",
	} {
		conn, err := net.DialTimeout("tcp4", address, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: test\r\n%sConnection: close\r\n\r\n0\r\n\r\n", headers)
		response, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil || response.StatusCode != 400 && response.StatusCode != 401 {
			t.Fatal("unsafe wire request accepted")
		}
		_ = response.Body.Close()
		_ = conn.Close()
	}
	if calls.Load() != 0 {
		t.Fatal("unsafe wire request reached upstream")
	}
}

// Test-only routing uses ephemeral loopback fake servers. The actual gateway
// target remains fixed and can be inspected before this transport rewrite.
func ephemeralTransport(t *testing.T, backend *httptest.Server) http.RoundTripper {
	t.Helper()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, DisableCompression: true}
	t.Cleanup(transport.CloseIdleConnections)
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		copy.URL.Host = target.Host
		copy.Host = target.Host
		return transport.RoundTrip(copy)
	})
}

func doGet(t *testing.T, address string) *http.Response {
	t.Helper()
	r, err := http.NewRequest("GET", address+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+testKey)
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestChunkedSSEFirstChunkAndDisconnectCancelsUpstream(t *testing.T) {
	canceled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credential reached fake backend")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer backend.Close()
	_, handler := testGateway(t, ephemeralTransport(t, backend))
	front := httptest.NewServer(handler)
	defer front.Close()
	start := time.Now()
	response := doGet(t, front.URL)
	if response.StatusCode != 200 || len(response.TransferEncoding) != 1 || response.TransferEncoding[0] != "chunked" {
		t.Fatal("SSE not chunked downstream")
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" || time.Since(start) > time.Second {
		t.Fatal("first SSE chunk buffered until completion")
	}
	_ = response.Body.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("downstream disconnect did not close/cancel upstream")
	}
}

func TestStoppedUpstreamAndErrorsNeverReflectSecrets(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	transport := ephemeralTransport(t, backend)
	backend.Close()
	for _, tr := range []http.RoundTripper{transport, roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("private " + testKey) })} {
		_, h := testGateway(t, tr)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest("GET", "/health", nil))
		if w.Code != 502 || strings.Contains(w.Body.String(), testKey) || w.Body.String() != "Bad Gateway\n" {
			t.Fatal("upstream failure was not generic 502")
		}
	}
}

func TestRealHTTPPostPreservesNativeJSONAndStripsCredential(t *testing.T) {
	payload := `{"model":"mistral-small-4-119b-heretic","messages":[{"role":"user","content":"isolated fake request"}],"reasoning_effort":"none","max_tokens":8192,"temperature":0.7,"stream":false}`
	responseJSON := `{"id":"fake-only","choices":[{"message":{"role":"assistant","content":"unchanged"}}]}`
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.RequestURI() != "/v1/chat/completions?native=yes" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("real upstream route or content type changed")
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("real upstream received a credential header")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != payload {
			t.Error("real native JSON model/effort/budget changed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Native-Fake", "preserved")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, responseJSON)
	}))
	defer backend.Close()
	_, h := testGateway(t, ephemeralTransport(t, backend))
	front := httptest.NewServer(h)
	defer front.Close()
	r, err := http.NewRequest("POST", front.URL+"/v1/chat/completions?native=yes", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+testKey)
	r.Header.Set("Proxy-Authorization", "isolated-not-forwarded")
	r.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusCreated || string(body) != responseJSON || response.Header.Get("X-Native-Fake") != "preserved" || calls.Load() != 1 {
		t.Fatal("real response JSON/status/header changed")
	}
}

func TestRawUnauthenticatedLargePOSTRefusesWithoutWaitingForBody(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer backend.Close()
	_, h := testGateway(t, ephemeralTransport(t, backend))
	front := httptest.NewServer(h)
	defer front.Close()
	conn, err := net.DialTimeout("tcp4", strings.TrimPrefix(front.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	start := time.Now()
	_, err = fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: test\r\nContent-Length: %d\r\nConnection: keep-alive\r\n\r\n", maxRequestBytes)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal("auth rejection waited for absent request body")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || !response.Close || time.Since(start) > time.Second {
		t.Fatal("unauthenticated large POST did not immediately close with 401")
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatal("rejected POST left connection open")
	}
	if calls.Load() != 0 {
		t.Fatal("unauthenticated large POST contacted backend")
	}
}

func TestPartialUploadDisconnectClosesUpstream(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		close(canceled)
	}))
	defer backend.Close()
	_, h := testGateway(t, ephemeralTransport(t, backend))
	front := httptest.NewServer(h)
	defer front.Close()
	conn, err := net.DialTimeout("tcp4", strings.TrimPrefix(front.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer %s\r\nContent-Length: 64\r\n\r\nx", testKey)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("partial upload never reached fake backend")
	}
	_ = conn.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("partial upload left upstream alive")
	}
}

func TestConcurrencySaturationReturns503WithoutAnotherUpstream(t *testing.T) {
	entered := make(chan struct{}, maxInFlight)
	release := make(chan struct{})
	var calls atomic.Int32
	_, h := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return fakeResponse(r, "ok"), nil
	}))
	var done sync.WaitGroup
	for i := 0; i < maxInFlight; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			h.ServeHTTP(httptest.NewRecorder(), authenticatedRequest("GET", "/v1/models", nil))
		}()
	}
	for i := 0; i < maxInFlight; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("active request failed to enter")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, authenticatedRequest("GET", "/v1/models", nil))
	close(release)
	done.Wait()
	if w.Code != 503 || calls.Load() != maxInFlight {
		t.Fatal("saturation accepted another upstream")
	}
}

func TestBindFailureClosesAllPreviouslyBoundSockets(t *testing.T) {
	var opened []net.Listener
	var seen []string
	listeners, err := bindListeners(func(network, address string) (net.Listener, error) {
		seen = append(seen, network+" "+address)
		if len(seen) == 3 {
			return nil, errors.New("isolated intentional bind failure")
		}
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		opened = append(opened, listener)
		return listener, err
	})
	if err == nil || listeners != nil || len(opened) != 2 || !strings.Contains(err.Error(), "31003") {
		t.Fatal("bind failure not all-or-nothing")
	}
	for i, listener := range opened {
		if seen[i] != fmt.Sprintf("tcp4 192.168.1.195:%d", fixedPorts[i]) {
			t.Fatal("bind target changed")
		}
		if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Fatal("earlier listener remains open")
		}
	}
}

func TestConnectionLimitAndSlotRelease(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	conn := &limitedConn{Conn: a, slots: slots}
	_ = conn.Close()
	_ = conn.Close()
	if len(slots) != 0 {
		t.Fatal("connection slot not released exactly once")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	limited := &limitedListener{Listener: listener, slots: make(chan struct{}, 1)}
	accepted := make(chan net.Conn, 1)
	go func() { c, _ := limited.Accept(); accepted <- c }()
	first, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	serverConn := <-accepted
	defer serverConn.Close()
	go func() {
		c, _ := limited.Accept()
		if c != nil {
			c.Close()
		}
	}()
	extra, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	_ = extra.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := extra.Read(make([]byte, 1)); err == nil {
		t.Fatal("excess connection remains accepted")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("excess connection was left idle instead of closed")
	}
}

func TestSilentThinkingDisconnectCancelsBeforeResponseHeaders(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}))
	defer backend.Close()
	_, handler := testGateway(t, ephemeralTransport(t, backend))
	front := httptest.NewServer(handler)
	defer front.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", front.URL+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+testKey)
	finished := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(r)
		if response != nil {
			response.Body.Close()
		}
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request never reached silent backend")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("client request was not canceled")
		}
	case <-time.After(time.Second):
		t.Fatal("client cancellation stalled")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("silent backend was not canceled")
	}
}

func TestServeCancellationClosesEveryListener(t *testing.T) {
	g, _ := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { return fakeResponse(r, "ok"), nil }))
	ctx, cancel := context.WithCancel(context.Background())
	opened := make(chan net.Listener, len(fixedPorts))
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, g, func(network, address string) (net.Listener, error) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err == nil {
				opened <- listener
			}
			return listener, err
		})
	}()
	var listeners []net.Listener
	for range fixedPorts {
		select {
		case listener := <-opened:
			listeners = append(listeners, listener)
		case <-time.After(time.Second):
			cancel()
			t.Fatal("startup incomplete")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown failed")
	}
	for _, listener := range listeners {
		if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Fatal("listener survives shutdown")
		}
	}
}

type countingReader struct {
	reader io.Reader
	reads  *atomic.Int32
}

func (r countingReader) Read(p []byte) (int, error) {
	r.reads.Add(1)
	return r.reader.Read(p)
}

func TestLargePromptBodyIsForwardedAsStream(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 2<<20)
	var reads atomic.Int32
	_, h := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if reads.Load() != 0 {
			t.Fatal("proxy consumed/buffered prompt before forwarding")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, payload) {
			t.Fatal("large prompt changed")
		}
		return fakeResponse(r, "ok"), nil
	}))
	w := httptest.NewRecorder()
	r := authenticatedRequest("POST", "/v1/chat/completions", countingReader{reader: bytes.NewReader(payload), reads: &reads})
	r.ContentLength = int64(len(payload))
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("large prompt refused")
	}
}
