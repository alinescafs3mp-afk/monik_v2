package checks

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestV13DuplicateJSONFieldsCannotManufactureHealth(t *testing.T) {
	for _, body := range []string{`{"ready":false,"ready":true}`, `{"status":"down","st\u0061tus":"ok"}`, `{"ready":true,"other":{"x":1,"x":2}}`} {
		t.Run(body, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, body)
			}))
			defer ts.Close()
			d := advanced(ts.URL)
			d.ExpectHealth = true
			if obs := Run(context.Background(), d, nil, "", ""); obs.AppResult == "pass" {
				t.Fatalf("ambiguous duplicate object reported healthy: %+v", obs)
			}
			d.ExpectHealth = false
			d.ExpectJSONPath = "ready"
			d.ExpectJSONType = "boolean"
			d.ExpectJSONValue = "true"
			if obs := Run(context.Background(), d, nil, "", ""); obs.AppResult == "pass" {
				t.Fatalf("ambiguous duplicate object matched an assertion: %+v", obs)
			}
		})
	}
}
