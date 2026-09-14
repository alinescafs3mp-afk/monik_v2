package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	return json.Unmarshal(b, v)
}
func parseJSON(r *http.Request, v any) error { return parseJSONLimit(r, v, 8<<20) }
