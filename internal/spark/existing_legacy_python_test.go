//go:build linux

package spark

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExistingActualLegacyPythonProducerFrames(t *testing.T) {
	const dir = "testdata/existing-legacy-python"
	manifest, err := os.ReadFile(filepath.Join(dir, "MANIFEST.json"))
	if err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(manifest, &provenance) != nil || len(provenance.Files) != 10 {
		t.Fatal("missing genuine producer provenance")
	}
	for name, want := range provenance.Files {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			sha := sha256.Sum256(raw)
			if hex.EncodeToString(sha[:]) != want {
				t.Fatal("producer bytes changed")
			}
			var envelope map[string]json.RawMessage
			if json.Unmarshal(raw, &envelope) != nil {
				t.Fatal("invalid original frame")
			}
			var models []map[string]json.RawMessage
			if json.Unmarshal(envelope["models"], &models) != nil || len(models) != 1 {
				t.Fatal("invalid producer model count")
			}
			var id string
			_ = json.Unmarshal(models[0]["model_id"], &id)
			if !existingLegacyID(id) {
				t.Fatal("foreign fixture profile")
			}
			_, _, p := existingFixture(t)
			p.ID = id
			// Refresh only the expired capture timestamp; all generated identity,
			// declaration and shutdown bytes remain the original Python output.
			models[0]["observed_at_ms"], _ = json.Marshal(time.Now().UnixMilli())
			envelope["models"], _ = json.Marshal(models)
			current, _ := json.Marshal(envelope)
			request := existingEnvelope{SchemaVersion: 1, Method: "observe_many", ProfileIDs: []string{id}}
			var response existingResponse
			if err := decodeExistingResponse(current, request, p, &response); err != nil {
				t.Fatalf("actual Python frame rejected: %v", err)
			}
			s := response.Models[0]
			if strings.Contains(name, "pre-capture") {
				if s.Instance.LegacyDeclarationSHA256 != nil || s.APIReady {
					t.Fatal("pre-create frame invented qualification")
				}
			} else {
				if s.Instance.LegacyDeclarationSHA256 == nil {
					t.Fatal("captured legacy declaration omitted")
				}
				for _, replacement := range []json.RawMessage{json.RawMessage("null"), json.RawMessage(`"unknown"`)} {
					var changed map[string]json.RawMessage
					_ = json.Unmarshal(models[0]["instance"], &changed)
					changed["legacy_declaration_sha256"] = replacement
					badModel := make(map[string]json.RawMessage)
					for key, value := range models[0] {
						badModel[key] = value
					}
					badModel["instance"], _ = json.Marshal(changed)
					bad, _ := json.Marshal(map[string]any{"schema_version": 1, "models": []any{badModel}})
					if decodeExistingResponse(bad, request, p, &response) == nil {
						t.Fatal("malformed declaration accepted")
					}
				}
			}
			if strings.Contains(name, "stopped") || strings.Contains(name, "reaped") {
				if !validExistingShutdown(s.existingResult, id, s.RunID, valueGeneration(s.Generation)) {
					t.Fatal("real producer shutdown witness does not bind its physical instance")
				}
			}
			duplicate := bytes.Replace(current, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1)
			if decodeExistingResponse(duplicate, request, p, &response) == nil {
				t.Fatal("duplicate field accepted")
			}
		})
	}
}
