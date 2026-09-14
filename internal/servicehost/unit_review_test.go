package servicehost

import (
	"strings"
	"testing"
)

func TestReviewUnitQuotesPathsAndSpecifiers(t *testing.T) {
	s := PlanUnitState("/opt/monik space/host", "/etc/monik/a%u$b.json", "/var/monik data", "monik")
	if !strings.Contains(s, `ExecStart="/opt/monik space/host"`) || !strings.Contains(s, `"/etc/monik/a%%u$$b.json"`) || !strings.Contains(s, `--state "/var/monik data"`) {
		t.Fatal(s)
	}
}
