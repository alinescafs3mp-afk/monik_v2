package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testNemoModel = "nemotron-3-super-120b-heretic-v2"

func nemoGateway(t *testing.T, tr http.RoundTripper) (*gateway, http.Handler) {
	t.Helper()
	g, _ := testGateway(t, tr)
	h, err := g.handler(31004)
	if err != nil {
		t.Fatal(err)
	}
	return g, h
}

func TestNemoModelRefusalsNeverReachUpstream(t *testing.T) {
	var calls atomic.Int32
	g, h := nemoGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(r, "unexpected"), nil
	}))
	for _, tc := range []struct{ name, body string }{
		{"empty", ``}, {"null", `null`}, {"array", `[]`}, {"string", `"` + testNemoModel + `"`},
		{"missing", `{}`}, {"messages-only", `{"messages":[]}`},
		{"unknown", `{"model":"wrong"}`}, {"ling", `{"model":"` + lingModel + `"}`},
		{"case", `{"model":"Nemotron-3-super-120b-heretic-v2"}`},
		{"null-model", `{"model":null}`}, {"number-model", `{"model":1}`},
		{"bool-model", `{"model":true}`}, {"array-model", `{"model":[]}`}, {"object-model", `{"model":{}}`},
		{"duplicate-same", `{"model":"` + testNemoModel + `","model":"` + testNemoModel + `"}`},
		{"duplicate-wrong-first", `{"model":"wrong","model":"` + testNemoModel + `"}`},
		{"duplicate-wrong-last", `{"model":"` + testNemoModel + `","model":"wrong"}`},
		{"duplicate-escaped-first", `{"mo\u0064el":"` + testNemoModel + `","model":"` + testNemoModel + `"}`},
		{"duplicate-escaped-last", `{"model":"` + testNemoModel + `","mo\u0064el":"` + testNemoModel + `"}`},
		{"nested-only", `{"messages":{"model":"` + testNemoModel + `"}}`},
		{"coalesced-model", `{"model":"` + testNemoModel + `, ` + testNemoModel + `"}`},
		{"second-document", `{"model":"` + testNemoModel + `"} {}`},
		{"trailing-text", `{"model":"` + testNemoModel + `"}garbage`},
		{"trailing-comma", `{"model":"` + testNemoModel + `",}`},
		{"truncated", `{"model":"` + testNemoModel + `"`},
		{"bad-escape", `{"model":"` + testNemoModel + `","messages":["bad\x20escape"]}`},
		{"long-model", `{"model":"` + strings.Repeat("x", 300) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(tc.body)))
			var envelope struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Param   string `json:"param"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			if w.Code != http.StatusBadRequest || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Error.Message != "Invalid model for this endpoint." || envelope.Error.Type != "invalid_request_error" || envelope.Error.Param != "model" || envelope.Error.Code != "model_not_found" {
				t.Errorf("Nemo model refusal got status %d body %.100q", w.Code, w.Body.String())
			}
			if g.lingBodyBytes.Load() != 0 {
				t.Fatal("refused Nemo request leaked shared body reservation")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("Nemo model refusals contacted upstream %d times", calls.Load())
	}
}

func TestNemoTruncatedUploadKeepsGenericError(t *testing.T) {
	var calls atomic.Int32
	g, h := nemoGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(r, "unexpected"), nil
	}))
	r := authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+testNemoModel+`"}`))
	r.ContentLength++
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || w.Body.String() != "Bad Request\n" || calls.Load() != 0 || g.lingBodyBytes.Load() != 0 {
		t.Fatal("truncated Nemo upload changed generic error, reached upstream or leaked reservation")
	}
}

func TestNemoValidJSONAndToolSSEPreservedByteForByte(t *testing.T) {
	const toolSSE = "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"city\\\":\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"Moscow\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	var expected string
	var calls atomic.Int32
	g, h := nemoGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != expected || r.ContentLength != int64(len(expected)) {
			t.Fatal("accepted Nemo request bytes/length changed")
		}
		if r.URL.Host != "127.0.0.1:31004" || r.URL.RequestURI() != "/v1/chat/completions?native=yes" || r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("Nemo target/query/credential/content type contract changed")
		}
		response := fakeResponse(r, toolSSE)
		response.Header.Set("Content-Type", "text/event-stream")
		return response, nil
	}))
	for _, body := range []string{
		`{"model":"` + testNemoModel + `"}`,
		" \t\r\n{ \"model\" : \"" + testNemoModel + "\" }\n",
		`{"mo\u0064el":"nemotron-3-super-120b-heretic-v\u0032"}`,
		`{"messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_0","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Moscow\"}"}}]},{"role":"tool","tool_call_id":"call_0","content":"{\"ok\":true}"}],"model":"` + testNemoModel + `","tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}],"tool_choice":"auto","stream":true,"reasoning_effort":"none","chat_template_kwargs":{"low_effort":true},"max_tokens":1200}`,
		`{"nested":{"model":"wrong"},"model":"` + testNemoModel + `","unknown":"` + strings.Repeat("x", 2<<20) + `"}`,
	} {
		expected = body
		r := authenticatedRequest("POST", "/v1/chat/completions?native=yes", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.String() != toolSSE || w.Header().Get("Content-Type") != "text/event-stream" || !w.Flushed || g.lingBodyBytes.Load() != 0 {
			t.Fatal("accepted Nemo tool SSE changed, was buffered or leaked body capacity")
		}
	}
	if calls.Load() != 5 {
		t.Fatal("accepted Nemo call count changed")
	}
}

func TestNemoGuardLeavesOtherPortsAndRoutesUnchanged(t *testing.T) {
	var calls atomic.Int32
	g, _ := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(r, "native"), nil
	}))
	for _, port := range []int{31001, 31003, 31005} {
		h, _ := g.handler(port)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"wrong"}`)))
		if w.Code != http.StatusOK {
			t.Fatalf("unrelated port %d was changed", port)
		}
	}
	h, _ := g.handler(31004)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/v1/models", ""}, {"GET", "/v1/chat/completions", ""},
		{"POST", "/v1/completions", "opaque-original-body"},
		{"POST", "/v1/chat/completions/", "opaque-original-body"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != http.StatusOK || g.lingBodyBytes.Load() != 0 {
			t.Fatal("unrelated Nemo route was changed")
		}
	}
	if calls.Load() != 7 {
		t.Fatal("unrelated native calls did not reach upstream")
	}
}

func TestNemoAuthFramingCapsAndSharedBudget(t *testing.T) {
	var calls atomic.Int32
	g, h := nemoGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(r, "native"), nil
	}))
	body := `{"model":"` + testNemoModel + `"}`
	for _, values := range [][]string{nil, {"Bearer wrong"}, {"Bearer " + testKey, "Bearer " + testKey}, {"Bearer " + testKey + ", Bearer " + testKey}} {
		r := authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		r.Header.Del("Authorization")
		for _, value := range values {
			r.Header.Add("Authorization", value)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized || g.lingBodyBytes.Load() != 0 {
			t.Fatal("duplicate/coalesced/wrong/missing auth did not fail before body admission")
		}
	}
	for _, tc := range []struct {
		length int64
		code   int
	}{
		{-1, http.StatusLengthRequired}, {maxRequestBytes + 1, http.StatusRequestEntityTooLarge},
		{int64(len(body) + 1), http.StatusBadRequest},
	} {
		r := authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		r.ContentLength = tc.length
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code || g.lingBodyBytes.Load() != 0 {
			t.Fatal("Nemo framing/body cap refusal changed or leaked capacity")
		}
	}
	if !g.reserveLingBody(maxRequestBytes) {
		t.Fatal("could not reserve existing shared Ling body budget")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
	if w.Code != http.StatusServiceUnavailable || calls.Load() != 0 || g.lingBodyBytes.Load() != maxRequestBytes {
		t.Fatal("Nemo bypassed the existing aggregate body budget or refusals reached upstream")
	}
	g.lingBodyBytes.Add(-maxRequestBytes)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
	if w.Code != http.StatusOK || calls.Load() != 1 || g.lingBodyBytes.Load() != 0 {
		t.Fatal("released shared body budget was not reusable by Nemo")
	}
}

func TestNemoPostSSECancellationReleasesSharedBody(t *testing.T) {
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
	g, h := nemoGateway(t, ephemeralTransport(t, backend))
	front := httptest.NewServer(h)
	defer front.Close()
	body := `{"model":"` + testNemoModel + `","stream":true}`
	r, _ := http.NewRequest("POST", front.URL+"/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testKey)
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" || g.lingBodyBytes.Load() != int64(len(body)) {
		_ = response.Body.Close()
		t.Fatal("accepted Nemo stream buffered or lost its body reservation")
	}
	_ = response.Body.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Nemo downstream disconnect did not cancel upstream")
	}
	deadline := time.Now().Add(time.Second)
	for g.lingBodyBytes.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if g.lingBodyBytes.Load() != 0 {
		t.Fatal("Nemo stream disconnect leaked shared body capacity")
	}
}
