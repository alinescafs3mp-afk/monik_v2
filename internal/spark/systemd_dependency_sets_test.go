package spark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Eight actual systemctl-show Requires observations from the native canary.
// Source definition-props-samples.json SHA256:
// 88d3f03fd95770ffa837d5a90558802112fd2c8977ac67d51e8ea6b2a74deb79.
// Its PartOf and BindsTo observations were empty in all eight samples.
var nativeRequiresSamples = []string{
	"system.slice -.mount sysinit.target",
	"system.slice -.mount sysinit.target",
	"sysinit.target system.slice -.mount",
	"-.mount sysinit.target system.slice",
	"system.slice sysinit.target -.mount",
	"-.mount sysinit.target system.slice",
	"system.slice sysinit.target -.mount",
	"sysinit.target system.slice -.mount",
}

func dependencyDefinitionFixture(t *testing.T, observation bool) (Profile, map[string]string) {
	t.Helper()
	p, properties := rootDefinitionFixture(t)
	if observation {
		p.ObservationOnly = true
		p.Target.AllowRootUnit = false
		p.Target.TrustedFiles = nil
	}
	return p, properties
}

func TestSystemdDependencySetCanonicalizesActualNativePermutations(t *testing.T) {
	for _, observation := range []bool{false, true} {
		for _, property := range []string{"Requires", "PartOf", "BindsTo"} {
			t.Run(map[bool]string{false: "effect", true: "observation"}[observation]+"/"+property, func(t *testing.T) {
				var first string
				for index, sample := range nativeRequiresSamples {
					p, properties := dependencyDefinitionFixture(t, observation)
					// Requires uses all eight exact native observations. The other
					// two unordered-set contracts use the same actual unit set.
					properties[property] = sample
					hash, err := canonicalSystemdDefinition(p, properties)
					if err != nil {
						t.Fatal(err)
					}
					if index == 0 {
						first = hash
					} else if hash != first {
						t.Fatalf("actual sample %d changed %s hash only by dependency order: %q", index, property, sample)
					}
				}
			})
		}
	}
}

func TestSystemdDependencySetStillBindsAddedAndRemovedUnits(t *testing.T) {
	for _, observation := range []bool{false, true} {
		for _, property := range []string{"Requires", "PartOf", "BindsTo"} {
			p, properties := dependencyDefinitionFixture(t, observation)
			properties[property] = nativeRequiresSamples[0]
			before, err := canonicalSystemdDefinition(p, properties)
			if err != nil {
				t.Fatal(err)
			}
			for _, changed := range []string{nativeRequiresSamples[0] + " basic.target", "system.slice sysinit.target"} {
				p, properties = dependencyDefinitionFixture(t, observation)
				properties[property] = changed
				after, err := canonicalSystemdDefinition(p, properties)
				if err != nil {
					t.Fatal(err)
				}
				if before == after {
					t.Fatalf("membership drift ignored: observation=%v property=%s changed=%q", observation, property, changed)
				}
			}
		}
	}
}

func TestSystemdDependencyNormalizationPreservesPropertyPresence(t *testing.T) {
	for _, observation := range []bool{false, true} {
		for _, property := range []string{"Requires", "PartOf", "BindsTo"} {
			p, properties := dependencyDefinitionFixture(t, observation)
			absent, err := canonicalSystemdDefinition(p, properties)
			if err != nil {
				t.Fatal(err)
			}
			p, properties = dependencyDefinitionFixture(t, observation)
			properties[property] = ""
			empty, err := canonicalSystemdDefinition(p, properties)
			if err != nil {
				t.Fatal(err)
			}
			if absent == empty {
				t.Fatalf("unobserved %s treated as observed empty set: observation=%v", property, observation)
			}
		}
	}
}

func TestSystemdDependencyNormalizationPreservesOrderedProperties(t *testing.T) {
	properties := []string{"Environment", "DropInPaths", "ExecCondition", "ExecConditionEx", "ExecStart", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecStartEx", "ExecStartPreEx", "ExecStartPostEx", "ExecStopEx", "ExecStopPostEx"}
	for _, observation := range []bool{false, true} {
		for _, property := range properties {
			t.Run(map[bool]string{false: "effect", true: "observation"}[observation]+"/"+property, func(t *testing.T) {
				var hashes []string
				for _, reverse := range []bool{false, true} {
					p, values := dependencyDefinitionFixture(t, observation)
					left, right := "MODEL_ORDER=first", "MODEL_ORDER=second"
					switch property {
					case "DropInPaths":
						left, right = "/usr/lib/systemd/system/sockets.target", "/usr/lib/systemd/system/timers.target"
						if _, err := os.Stat(left); err != nil {
							t.Skip("protected ordered unit-file fixture missing")
						}
						if _, err := os.Stat(right); err != nil {
							t.Skip("protected ordered unit-file fixture missing")
						}
					case "Environment":
					default:
						exe, err := filepath.EvalSymlinks("/usr/bin/true")
						if err != nil {
							t.Fatal(err)
						}
						left = "{ path=" + exe + " ; argv[]=" + exe + " --first ; ignore_errors=no }"
						right = "{ path=" + exe + " ; argv[]=" + exe + " --second ; ignore_errors=no }"
					}
					if reverse {
						left, right = right, left
					}
					values[property] = strings.Join([]string{left, right}, " ")
					values["Requires"] = nativeRequiresSamples[0]
					hash, err := canonicalSystemdDefinition(p, values)
					if err != nil {
						t.Fatal(err)
					}
					hashes = append(hashes, hash)
				}
				if hashes[0] == hashes[1] {
					t.Fatalf("ordered %s changes disappeared: observation=%v", property, observation)
				}
			})
		}
	}
}
