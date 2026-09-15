package jsonutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestV13StrictJSONBoundaries(t *testing.T) {
	good := []string{`{}`, `[]`, `null`, `true`, `0`, `{"ready":true,"checks":[{"ready":false},{"ready":true}]}`, `{"a":1,"A":2}`, " {\"name\":\"Сервер\"}\n "}
	for _, s := range good {
		if err := Validate([]byte(s)); err != nil {
			t.Errorf("valid %q: %v", s, err)
		}
	}
	bad := []string{"", `{"ready":false,"ready":true}`, `{"status":0,"st\u0061tus":1}`, `{"a":{"b":1,"b":2}}`, `{} {}`, `{}x`, `{"x":}`, `[1,]`, string([]byte{'"', 0xff, '"'}), strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)}
	for _, s := range bad {
		if err := Validate([]byte(s)); err == nil {
			t.Errorf("accepted ambiguous/invalid input: %q", s)
		}
	}
}
func TestV13ReadObjectChecksTailBudgetAndReadErrors(t *testing.T) {
	for _, s := range []string{`null`, `[]`, `true`, `{}false`, "{}   "} {
		var dst map[string]any
		if err := ReadObject(strings.NewReader(s), 4, &dst); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	var dst map[string]any
	if err := ReadObject(strings.NewReader("{} \n"), 4, &dst); err != nil {
		t.Fatal(err)
	}
	if err := ReadObject(io.MultiReader(strings.NewReader(`{"secret":"private fixture"}`), errorReader{}), 1024, &dst); err == nil || strings.Contains(err.Error(), "private fixture") {
		t.Fatal("read error hidden or source leaked", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("secret detail must not escape") }

func FuzzV13StrictJSON(f *testing.F) {
	for _, b := range [][]byte{[]byte(`{"ready":true}`), []byte(`{"ready":false,"ready":true}`), []byte(`{} {}`), []byte(`{"x":[{},null]}`), {0xff}, []byte(`{"r\u0065ady":false,"ready":true}`)} {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 65536 {
			t.Skip()
		}
		err := Validate(b)
		if err == nil && !json.Valid(b) {
			t.Fatal("accepted invalid JSON")
		}
		var v map[string]any
		if ReadObject(bytes.NewReader(b), int64(len(b)), &v) == nil {
			if err != nil || v == nil {
				t.Fatal("object decoder accepted invalid/null JSON")
			}
		}
	})
}
