package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const gemmaModel = "gemma4-31b-deckard-heretic"

func TestGemmaCountAliasRealProxyPreservesCompleteBodyQueryAndResponse(t *testing.T) {
	body := ` {"model":"` + gemmaModel + `","messages":[{"role":"system","content":"Факты"},{"role":"user","content":"nonce-unique"}],"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}],"chat_template_kwargs":{"enable_thinking":true},"add_generation_prompt":true,"max_tokens":8192} `
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		data, err := io.ReadAll(r.Body)
		if err != nil || string(data) != body || r.Method != "POST" || r.URL.RequestURI() != "/tokenize?native=exact%2Bbody&x=1&x=2" || r.Host != "127.0.0.1:31007" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("count alias changed full body, route, query or credential stripping")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		io.WriteString(w, `{"count":32734,"tokens":null}`)
	}))
	defer upstream.Close()
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "127.0.0.1:31007" {
			t.Error("request selected a foreign target")
		}
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}}
	defer tr.CloseIdleConnections()
	g, _ := newGateway(testKey, tr)
	h, _ := g.handler(31007)
	front := httptest.NewServer(h)
	defer front.Close()
	r, _ := http.NewRequest("POST", front.URL+"/v1/tokenize?native=exact%2Bbody&x=1&x=2", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testKey)
	r.Header.Set("Proxy-Authorization", "secret-to-strip")
	r.Header.Set("Content-Type", "application/json")
	response, err := front.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 201 || string(data) != `{"count":32734,"tokens":null}` || calls != 1 || g.lingBodyBytes.Load() != 0 {
		t.Fatal("native count response changed or admission leaked")
	}
}

func TestGemmaCountAliasRefusalsNeverContactUpstream(t *testing.T) {
	calls := 0
	g, _ := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { calls++; return fakeResponse(r, "unexpected"), nil }))
	h, _ := g.handler(31007)
	good := `{"model":"` + gemmaModel + `","messages":[]}`
	badBodies := []string{`{}`, `{"model":null}`, `{"model":7}`, `{"model":"wrong"}`, `{"model":"` + gemmaModel + `","model":"` + gemmaModel + `"}`, `{"mo\u0064el":"wrong","model":"` + gemmaModel + `"}`, `{"model":"` + gemmaModel + `","mo\u0064el":"` + gemmaModel + `"}`, good + `{}`, `{"model":"` + gemmaModel + `"`, `[]`}
	for _, body := range badBodies {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest("POST", "/v1/tokenize", strings.NewReader(body)))
		if w.Code != 400 || g.lingBodyBytes.Load() != 0 {
			t.Fatal("invalid count body accepted or admission leaked")
		}
	}
	for _, path := range []string{"/tokenize", "/v1/%74okenize", "/v1/token%69ze", "/v1%2ftokenize", "/v1/../tokenize"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest("POST", path, strings.NewReader(good)))
		if w.Code != 404 {
			t.Fatalf("path bypass %q accepted: %d", path, w.Code)
		}
	}
	for _, method := range []string{"GET", "HEAD", "PUT", "DELETE"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest(method, "/v1/tokenize", nil))
		if w.Code != 405 {
			t.Fatal("non-POST count accepted")
		}
	}
	r := authenticatedRequest("POST", "/v1/tokenize", strings.NewReader(good))
	r.Header.Del("Authorization")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("missing auth accepted")
	}
	r = authenticatedRequest("POST", "/v1/tokenize", strings.NewReader(good))
	r.ContentLength = maxRequestBytes + 1
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal("body cap bypass")
	}
	r = authenticatedRequest("POST", "/v1/tokenize", strings.NewReader(good))
	r.ContentLength++
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || g.lingBodyBytes.Load() != 0 {
		t.Fatal("truncated body accepted")
	}
	r = authenticatedRequest("POST", "/v1/tokenize", strings.NewReader(good))
	r.TransferEncoding = []string{"chunked"}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("transfer encoding bypass")
	}
	if !g.reserveLingBody(maxRequestBytes) {
		t.Fatal("cannot occupy existing byte budget")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, authenticatedRequest("POST", "/v1/tokenize", strings.NewReader(good)))
	if w.Code != 503 {
		t.Fatal("count bypassed shared aggregate byte budget")
	}
	g.lingBodyBytes.Store(0)
	for i := 0; i < maxInFlight; i++ {
		g.inFlight <- struct{}{}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, authenticatedRequest("POST", "/v1/tokenize", strings.NewReader(good)))
	if w.Code != 503 || g.lingBodyBytes.Load() != 0 {
		t.Fatal("count bypassed existing request admission")
	}
	for i := 0; i < maxInFlight; i++ {
		<-g.inFlight
	}
	if calls != 0 {
		t.Fatal("refusal contacted upstream")
	}
}

func TestGemmaCountAliasIsLimitedToExactRouteAndPort(t *testing.T) {
	for _, port := range fixedPorts {
		calls := 0
		g, _ := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path != "/v1/tokenize" {
				t.Fatal("count alias escaped Gemma-only port")
			}
			return fakeResponse(r, "unchanged"), nil
		}))
		h, _ := g.handler(port)
		if port == 31007 {
			continue
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest("POST", "/v1/tokenize", strings.NewReader(`{"model":"unchanged-existing-route"}`)))
		if w.Code != 200 || calls != 1 {
			t.Fatal("another port changed")
		}
	}
	for _, path := range []string{"/v1/tokenize/", "/v1/chat/completions/input_tokens", "/v1/models"} {
		g, _ := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.RequestURI() != path {
				t.Fatal("unrelated Gemma route changed")
			}
			return fakeResponse(r, "ok"), nil
		}))
		h, _ := g.handler(31007)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authenticatedRequest("POST", path, strings.NewReader(`{"model":"unmodified"}`)))
		if w.Code != 200 {
			t.Fatal("unrelated route refused")
		}
	}
}
