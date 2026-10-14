package installerbundle

import (
	"bytes"
	"encoding/json"
	"testing"
)

func sparkTemplate(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := PackSpark(&out, testELF(183), testELF(183), "arm64", "fixture"); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestSparkTemplateHasTwoVerifiedProgramsAndBoundProfile(t *testing.T) {
	raw := sparkTemplate(t)
	b, err := Open(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if b.Manifest.Flavor() != "spark-light" || b.Manifest.RuntimeMode != "direct" || b.PayloadSize != 256 {
		t.Fatal(b.Manifest, b.PayloadSize)
	}
	if err = b.CopySupervisor(&bytes.Buffer{}); err == nil {
		t.Fatal("light template exposed supervisor")
	}
	var bound bytes.Buffer
	if _, err = b.Write(&bound, testProfile(t)); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(bytes.NewReader(bound.Bytes()), int64(bound.Len())); err != nil {
		t.Fatal(err)
	}
}
func TestSparkRejectsCrossFlavorArchitectureAndRuntime(t *testing.T) {
	for _, edit := range []func(*Manifest){
		func(m *Manifest) { m.AgentFlavor = "general" },
		func(m *Manifest) { m.RuntimeMode = "managed-host" },
		func(m *Manifest) { m.Arch = "amd64" },
		func(m *Manifest) { m.Format = 1 },
		func(m *Manifest) { m.Supervisor = m.Worker },
		func(m *Manifest) { m.MinimumControllerFeatures = []string{"invented"} },
	} {
		raw := rewriteManifest(t, sparkTemplate(t), func(b []byte) []byte {
			var m Manifest
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			edit(&m)
			out, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			return out
		})
		if _, err := Open(bytes.NewReader(raw), int64(len(raw))); err == nil {
			t.Fatal("cross-flavor or unsupported variant accepted")
		}
	}
	var out bytes.Buffer
	if err := PackSpark(&out, testELF(62), testELF(62), "amd64", "fixture"); err == nil || out.Len() != 0 {
		t.Fatal("amd64 light package accepted")
	}
}
