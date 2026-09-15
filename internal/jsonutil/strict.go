// Package jsonutil rejects ambiguous JSON at the control and health boundaries.
// It deliberately retains encoding/json's typed decoding instead of changing
// the application's wire representation or persisted configuration hashes.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// Validate checks one complete value, unique decoded object names, UTF-8 and a
// bounded nesting depth. Errors intentionally omit response/request contents.
func Validate(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("JSON is not valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := value(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON data")
	}
	return nil
}
func value(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds limit")
	}
	t, err := d.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON value")
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				t, e := d.Token()
				if e != nil {
					return fmt.Errorf("invalid JSON object")
				}
				key, ok := t.(string)
				if !ok || keys[key] {
					return fmt.Errorf("duplicate or invalid JSON member")
				}
				keys[key] = true
				if e = value(d, depth+1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid JSON object end")
			}
		case '[':
			for d.More() {
				if e := value(d, depth+1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid JSON array end")
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
	}
	return nil
}
func Unmarshal(data []byte, dst any) error {
	if err := Validate(data); err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("JSON does not match expected structure")
	}
	return nil
}

// ReadObject enforces the total byte limit including trailing whitespace. An
// apparently valid prefix is not an accepted response if its tail is invalid.
func ReadObject(r io.Reader, limit int64, dst any) error {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return fmt.Errorf("JSON response read failed")
	}
	if int64(len(b)) > limit {
		return fmt.Errorf("JSON response exceeds limit")
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return fmt.Errorf("JSON object required")
	}
	return Unmarshal(b, dst)
}
