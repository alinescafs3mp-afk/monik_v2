package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
)

// Read exactly one bounded JSON object. A valid prefix must never conceal a
// trailing object, truncated body, oversized request, or missing parameters.
func parseJSONLimit(r *http.Request, v any, max int64) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > max {
		return fmt.Errorf("JSON body exceeds %d bytes", max)
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return fmt.Errorf("JSON object required")
	}
	return jsonutil.Unmarshal(b, v)
}
func parseJSON(r *http.Request, v any) error { return parseJSONLimit(r, v, 8<<20) }

// Strict decoding is used on the unauthenticated registration boundary so a
// future/unknown field cannot be silently interpreted as accepted authority.
func parseJSONStrictLimit(r *http.Request, v any, max int64) error {
	var raw json.RawMessage
	if err := parseJSONLimit(r, &raw, max); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}
