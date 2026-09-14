package protocol

import "fmt"

// Suggestions cannot carry credentials, arbitrary methods or weaken TLS. The
// canonical dial target must remain the endpoint the agent actually discovered.
func ValidateSuggestions(ep DiscoveredEndpoint) error {
	if len(ep.Suggestions) > 4 || len(ep.IdentificationNote) > 1024 {
		return fmt.Errorf("discovery advice exceeds budget")
	}
	for _, s := range ep.Suggestions {
		d := s.Definition
		if err := ValidateCheck(d); err != nil {
			return err
		}
		if d.URL != ep.URL || d.DialTarget != ep.DialTarget || d.HostHeader != ep.HostHeader || d.TLSServerName != ep.TLSServerName || d.InsecureTLS || d.Method != "GET" || len(d.Headers) > 0 || d.Body != "" || d.SecretID != "" || d.BodySecretID != "" || d.AllowPOST || len(s.Reason) > 1024 {
			return fmt.Errorf("unsafe discovery advice")
		}
		if s.AutoEligible && (s.Confidence != "high" || !d.ExpectHealth || d.Kind != "http_health" || len(d.ExpectedStatus) != 1 || d.ExpectedStatus[0] != 200) {
			return fmt.Errorf("unverified automatic health advice")
		}
	}
	return nil
}
