package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestV15JSONBoundaryRejectsDuplicateAuthority(t *testing.T) {
	for _, body := range []string{`{"enabled":false,"enabled":true}`, `{"params":{"paused":true,"p\u0061used":false}}`, `{"a":1} {"a":2}`, `null`} {
		t.Run(body, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			var v map[string]any
			if e := parseJSONLimit(r, &v, 4096); e == nil {
				t.Fatal("ambiguous or non-object request accepted")
			}
		})
	}
}
func TestV15JSONBoundaryAcceptsDistinctFieldsAndRejectsLargeBody(t *testing.T) {
	var v map[string]any
	if e := parseJSONLimit(httptest.NewRequest("POST", "/", strings.NewReader(`{"a":false,"b":{"a":true}}`)), &v, 128); e != nil {
		t.Fatal(e)
	}
	if e := parseJSONLimit(httptest.NewRequest("POST", "/", strings.NewReader(`{"a":"`+strings.Repeat("x", 129)+`"}`)), &v, 128); e == nil {
		t.Fatal("oversize accepted")
	}
}
