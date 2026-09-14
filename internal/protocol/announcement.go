package protocol

import "github.com/alinescafs3mp-afk/monik_v2/internal/secure"

// Announcement carries only self-reported registration metadata, never metrics.
// The credential is locally generated and transmitted over verified TLS. It does
// not authenticate a monitoring agent until an owner explicitly approves it.
type Announcement struct {
	AgentID     string `json:"agent_id"`
	Credential  string `json:"credential"`
	Hostname    string `json:"hostname"`
	DisplayName string `json:"display_name"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Version     string `json:"version"`
}
type AnnouncementResponse struct {
	State             string          `json:"state"`
	Fingerprint       string          `json:"fingerprint"`
	RetryAfterSeconds int             `json:"retry_after_seconds"`
	Enrollment        *EnrollResponse `json:"enrollment,omitempty"`
}

func RegistrationFingerprint(id, credential string) string {
	return secure.SHA256Bytes([]byte("monik-registration-v1\x00" + id + "\x00" + credential))
}
