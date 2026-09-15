package servicehost

import (
	"strings"
	"testing"
)

func TestV16ManagedUnitGrantsOnlyNetRawCapability(t *testing.T) {
	s := PlanManagedUnit("/usr/lib/monik/monik-service-host", "/var/lib/monik-agent/bin/monik-agent", "/var/lib/monik-agent/agent.json", "/var/lib/monik-agent", "monik")
	for _, required := range []string{"User=monik\n", "NoNewPrivileges=true\n", "CapabilityBoundingSet=CAP_NET_RAW\n", "AmbientCapabilities=CAP_NET_RAW\n"} {
		if !strings.Contains(s, required) {
			t.Fatalf("missing %s", required)
		}
	}
	if strings.Contains(s, "CAP_SYS_ADMIN") {
		t.Fatal("broad privilege")
	}
}
