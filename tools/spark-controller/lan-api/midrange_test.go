package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var midrangeModels = map[int]string{31006: "glm45-air-106b-derestricted", 31007: "gemma4-31b-deckard-heretic", 31008: "nemotron-super-49b-v15-abliterated"}

func TestMidrangeExactModelsAndUnchangedWireBody(t *testing.T) {
	for port, model := range midrangeModels {
		t.Run(model, func(t *testing.T) {
			body := ` {"model":"` + model + `","messages":[{"role":"system","content":"Проверь данные"}],"stream":true,"max_tokens":8192,"temperature":0.6,"unknown":{"x":1}} `
			calls := 0
			g, _ := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				data, err := io.ReadAll(r.Body)
				if err != nil || string(data) != body || r.ContentLength != int64(len(body)) || r.URL.Host != fmt.Sprintf("127.0.0.1:%d", port) || r.URL.RawQuery != "owned=yes" || r.Header.Get("Authorization") != "" {
					t.Fatal("fixed target/auth removal/request bytes changed")
				}
				return fakeResponse(r, "data: [DONE]\n\n"), nil
			}))
			h, err := g.handler(port)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, authenticatedRequest("POST", "/v1/chat/completions?owned=yes", strings.NewReader(body)))
			if w.Code != http.StatusOK || calls != 1 || w.Body.String() != "data: [DONE]\n\n" || g.lingBodyBytes.Load() != 0 {
				t.Fatal("valid fixed request failed or leaked body budget")
			}
		})
	}
}

func TestMidrangeAuthenticationAndWrongDuplicateModelsRefuseWithoutUpstream(t *testing.T) {
	for port, model := range midrangeModels {
		calls := 0
		g, _ := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) { calls++; return fakeResponse(r, "unexpected"), nil }))
		h, _ := g.handler(port)
		for _, body := range []string{`{}`, `{"model":"wrong"}`, `{"model":null}`, `{"model":"` + model + `","model":"` + model + `"}`, `{"mo\u0064el":"wrong","model":"` + model + `"}`} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, authenticatedRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
			if w.Code != http.StatusBadRequest || w.Header().Get("Content-Type") != "application/json" || g.lingBodyBytes.Load() != 0 {
				t.Fatal("model rejection weakened")
			}
		}
		w := httptest.NewRecorder()
		r := authenticatedRequest("GET", "/v1/models", nil)
		r.Header.Del("Authorization")
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized || calls != 0 {
			t.Fatal("unauthorized or wrong model contacted upstream")
		}
	}
	if fixedPort(31009) || fixedPort(30000) {
		t.Fatal("unallocated port accepted")
	}
}
