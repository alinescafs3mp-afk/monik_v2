package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/checks"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/servicehost"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tufutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/update"
)

type storedSecret struct {
	ID      string
	Header  string
	Value   string
	Version int
}

type lifecycleIntent struct {
	PreviousSession  string    `json:"previous_session,omitempty"`
	Kind             string    `json:"kind"`
	JobID            string    `json:"job_id"`
	OperationID      string    `json:"operation_id"`
	ExpectedDigest   string    `json:"expected_digest,omitempty"`
	CandidateURL     string    `json:"candidate_url,omitempty"`
	TrustPEM         string    `json:"trust_pem,omitempty"`
	Generation       int64     `json:"generation,omitempty"`
	PlanID           string    `json:"plan_id,omitempty"`
	ControllerID     string    `json:"controller_id,omitempty"`
	PreviousURL      string    `json:"previous_url,omitempty"`
	PreviousTrust    string    `json:"previous_trust,omitempty"`
	SwitchedAt       time.Time `json:"switched_at,omitempty"`
	Acknowledgements int       `json:"acknowledgements,omitempty"`
}

func intentPath(dir string) string {
	return filepath.Join(dir, "lifecycle-intent.json")
}

func saveIntent(dir string, in *lifecycleIntent) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	return secure.AtomicWrite(intentPath(dir), b, 0600)
}

func loadIntent(dir string) (*lifecycleIntent, error) {
	path := intentPath(dir)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	const limit = 1 << 20
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid lifecycle intent size/type")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, fmt.Errorf("lifecycle intent exceeds limit")
	}
	var in *lifecycleIntent
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, err
	}
	if in == nil {
		return nil, fmt.Errorf("empty lifecycle intent")
	}
	switch in.Kind {
	case "restart", "update", "rollback", "rebind_switch":
	default:
		return nil, fmt.Errorf("unknown lifecycle intent kind")
	}
	return in, nil
}

func clearIntent(dir string) {
	_ = os.Remove(intentPath(dir))
}

func (a *Agent) managed() bool {
	if a.State.File.Managed {
		return true
	}
	_, err := os.Stat(servicehost.DefaultSock(a.State.File.StateDir))
	return err == nil
}

func (a *Agent) executeJob(job protocol.JobEnvelope) protocol.JobReceipt {
	now := a.Clock.Now().UTC()
	rec := protocol.JobReceipt{
		JobID: job.JobID, OperationID: job.OperationID, Status: protocol.TargetUnsupported,
		Stage: "unsupported", Message: "action is not implemented by this worker", ErrorCode: "not_implemented", AcceptedAt: &now,
	}
	switch job.Action {
	case "secret.replace":
		return a.jobSecret(job, rec, now)
	case "agent.restart":
		return a.jobRestart(job, rec, now)
	case "update.rollout", "update.rollback":
		return a.jobUpdate(job, rec, now)
	case "rebind.prepare", "rebind.arm", "rebind.activate", "rebind.retire":
		return a.jobRebind(job, rec, now)
	case "check.trial":
		return a.jobTrial(job, rec, now)
	case "credential.rotate":
		return a.jobRotate(job, rec, now)
	case "trust.stage":
		return a.jobTrustStage(job, rec, now)
	case "trust.retire":
		return a.jobTrustRetire(job, rec, now)
	}
	return rec
}

func (a *Agent) jobTrial(job protocol.JobEnvelope, rec protocol.JobReceipt, now time.Time) protocol.JobReceipt {
	def, err := checks.ParseTrial(job.Params)
	if err != nil {
		rec.Status = protocol.TargetRejected
		rec.Stage = "rejected"
		rec.Message = err.Error()
		rec.ErrorCode = "bad_check"
		return rec
	}
	locals, _ := netutil.LocalInterfaceIPs()
	hdr, val := a.secretForLocked(def)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(def.TimeoutSeconds)*time.Second)
	defer cancel()
	body := ""
	if def.BodySecretID != "" {
		_, body = a.secretForLocked(protocol.CheckDefinition{SecretID: def.BodySecretID})
	}
	obs := checks.RunRequest(ctx, def, locals, hdr, val, body)
	rec.Status = protocol.TargetSucceeded
	rec.Stage = "redacted_trial_result"
	rec.ErrorCode = ""
	rec.Message = "trial completed; definition not saved"
	rec.AppliedAt = &now
	rec.Evidence = map[string]any{
		"trial": true, "vantage": "agent/local", "transport": obs.Transport,
		"http_status": obs.HTTPStatus, "latency_ms": obs.LatencyMS,
		"method": def.Method, "failure_layer": obs.FailureLayer, "purpose": obs.Purpose, "app_result": obs.AppResult, "app_reason": obs.AppReason, "quality": obs.Quality, "feedback": obs.Feedback,
	}
	return rec
}

func (a *Agent) jobRotate(job protocol.JobEnvelope, rec protocol.JobReceipt, now time.Time) protocol.JobReceipt {
	nextPath := filepath.Join(a.State.File.StateDir, "rotation-"+secure.SHA256Bytes([]byte(job.JobID))+".credential")
	next, err := configfile.ReadCredential(nextPath)
	if err != nil || next == "" {
		next, err = idgen.Secret(32)
		if err != nil {
			rec.Status = protocol.TargetFailed
			rec.Message = "could not generate credential"
			return rec
		}
		if err := configfile.WriteCredential(nextPath, next); err != nil {
			rec.Status = protocol.TargetFailed
			rec.Message = "could not persist next credential"
			return rec
		}
	}
	if err := a.registerNext(next, job.JobID); err != nil {
		rec.Status = protocol.TargetFailed
		rec.Stage = "register_failed"
		rec.Message = "could not register verifier over the current credential"
		rec.ErrorCode = "register"
		rec.Retryable = true
		return rec
	}
	prevPath := filepath.Join(a.State.File.StateDir, "previous.credential")
	if err := configfile.WriteCredential(prevPath, a.Cred); err != nil {
		rec.Status = protocol.TargetFailed
		rec.Message = "could not preserve prior credential"
		return rec
	}
	if err := configfile.WriteCredential(a.State.File.CredentialPath, next); err != nil {
		rec.Status = protocol.TargetFailed
		rec.Message = "could not switch credential file"
		return rec
	}
	a.Cred = next
	rec.Status = protocol.TargetSucceeded
	rec.Stage = "new_credential_authentication_receipt"
	rec.ErrorCode = ""
	rec.Message = "next credential stored and verifier registered; subsequent reports use the new token"
	rec.AppliedAt = &now
	rec.Evidence = map[string]any{"verifier_sha256": secure.HashToken(next)}
	return rec
}

func (a *Agent) registerNext(next, jobID string) error {
	body, _ := json.Marshal(map[string]string{"job_id": jobID, "verifier": secure.HashToken(next)})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(a.State.File.ControllerURL, "/")+"/api/v1/agent/credential/next", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Cred)
	req.Header.Set("X-Monik-Agent-Id", a.State.File.AgentID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func trustBundlePath(dir string) string {
	return filepath.Join(dir, "trust-bundle.pem")
}

func (a *Agent) currentTrustPEM() []byte { return []byte(a.State.File.CACertPEM) }

func (a *Agent) persistTrust(raw []byte) error {
	if _, err := tlsutil.PoolFromPEM(raw); err != nil {
		return err
	}
	old := a.State.File.CACertPEM
	a.State.File.CACertPEM = string(raw)
	if err := a.State.Save(); err != nil {
		a.State.File.CACertPEM = old
		return err
	}
	return a.applyTrustPool()
}

func (a *Agent) applyTrustPool() error {
	pool, err := tlsutil.PoolFromPEM(a.currentTrustPEM())
	if err != nil {
		return err
	}
	// Callers serialize publication with a.mu (Open has no concurrent readers).
	// An in-flight secret request keeps its original immutable client/transport.
	old := a.client
	next := *old
	next.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, Proxy: nil,
	}
	a.client = &next
	old.CloseIdleConnections()
	return nil
}

func encodeCerts(certs []*x509.Certificate) []byte {
	var buf bytes.Buffer
	for _, c := range certs {
		_ = pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	return buf.Bytes()
}

func (a *Agent) jobTrustStage(job protocol.JobEnvelope, rec protocol.JobReceipt, now time.Time) protocol.JobReceipt {
	pemText, _ := job.Params["trust_pem"].(string)
	incoming, err := tlsutil.ParseCerts([]byte(pemText))
	if err != nil {
		rec.Status = protocol.TargetRejected
		rec.Message = "trust_pem is not a certificate"
		rec.ErrorCode = "bad_trust"
		return rec
	}
	current, err := tlsutil.ParseCerts(a.currentTrustPEM())
	if err != nil {
		rec.Status = protocol.TargetFailed
		rec.Message = "current trust bundle unreadable"
		return rec
	}
	seen := map[string]bool{}
	var merged []*x509.Certificate
	for _, c := range append(current, incoming...) {
		fp := tlsutil.FingerprintDER(c.Raw)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		merged = append(merged, c)
	}
	if err := a.persistTrust(encodeCerts(merged)); err != nil {
		rec.Status = protocol.TargetFailed
		rec.Message = "could not persist trust bundle"
		return rec
	}
	if err := a.applyTrustPool(); err != nil {
		rec.Status = protocol.TargetFailed
		rec.Message = "could not load staged trust"
		return rec
	}
	fp, _ := job.Params["fingerprint"].(string)
	if fp == "" {
		fp = tlsutil.FingerprintDER(incoming[0].Raw)
	}
	rec.Status = protocol.TargetSucceeded
	rec.Stage = "persisted_scoped_trust"
	rec.ErrorCode = ""
	rec.Message = "additional controller trust stored; enrolled root retained"
	rec.AppliedAt = &now
	rec.Evidence = map[string]any{"fingerprint": fp, "roots": len(merged)}
	return rec
}

func (a *Agent) jobTrustRetire(job protocol.JobEnvelope, rec protocol.JobReceipt, now time.Time) protocol.JobReceipt {
	fp, _ := job.Params["fingerprint"].(string)
	if fp == "" {
		rec.Status = protocol.TargetRejected
		rec.Message = "fingerprint required"
		rec.ErrorCode = "missing_fingerprint"
		return rec
	}
	current, err := tlsutil.ParseCerts(a.currentTrustPEM())
	if err != nil {
		rec.Status = protocol.TargetFailed
		rec.Message = "current trust bundle unreadable"
		return rec
	}
	var kept []*x509.Certificate
	removed := false
	for _, c := range current {
		if tlsutil.FingerprintDER(c.Raw) == fp {
			removed = true
			continue
		}
		kept = append(kept, c)
	}
	if !removed {
		rec.Status = protocol.TargetRejected
		rec.Stage = "unknown_trust"
		rec.Message = "fingerprint is not in the local trust bundle"
		rec.ErrorCode = "unknown_trust"
		return rec
	}
	if len(kept) == 0 {
		rec.Status = protocol.TargetRejected
		rec.Stage = "last_root"
		rec.Message = "refusing to retire the last remaining controller root"
		rec.ErrorCode = "last_root"
		return rec
	}
	// An unused spare CA is not a recovery route. Verify the current controller
	// under the remaining roots before removing the root that may serve it.
	candidate := &protocol.MigrationPlan{PlanID: "trust-retirement-check", ControllerID: a.State.File.ControllerID, CandidateURL: a.State.File.ControllerURL, Generation: a.State.File.EndpointGeneration + 1, ExpiresAt: now.Add(time.Minute), TrustPEM: string(encodeCerts(kept)), PayloadHash: secure.SHA256Bytes([]byte(a.State.File.ControllerURL + "|" + a.State.File.ControllerID))}
	if err := a.verifyCandidate(candidate); err != nil {
		rec.Status = protocol.TargetRejected
		rec.Stage = "trust_would_disconnect"
		rec.Message = "remaining trust cannot verify the current controller; no trust was removed"
		rec.ErrorCode = "trust_would_disconnect"
		return rec
	}
	if err := a.persistTrust(encodeCerts(kept)); err != nil {
		rec.Status = protocol.TargetFailed
		rec.Message = "could not persist trust bundle"
		return rec
	}
	if err := a.applyTrustPool(); err != nil {
		rec.Status = protocol.TargetFailed
		rec.Message = "could not load remaining trust"
		return rec
	}
	rec.Status = protocol.TargetSucceeded
	rec.Stage = "acknowledged_trust_removal"
	rec.ErrorCode = ""
	rec.Message = "named trust removed; remaining roots kept"
	rec.AppliedAt = &now
	rec.Evidence = map[string]any{"fingerprint": fp, "roots": len(kept)}
	return rec
}

func (a *Agent) jobSecret(job protocol.JobEnvelope, rec protocol.JobReceipt, now time.Time) protocol.JobReceipt {
	id, _ := job.Params["secret_id"].(string)
	if id == "" {
		rec.Status = protocol.TargetRejected
		rec.Stage = "rejected"
		rec.Message = "secret_id missing from authorized job"
		rec.ErrorCode = "missing_secret"
		return rec
	}
	hdr, val, ver, err := a.fetchSecret(id)
	if err != nil {
		rec.Status = protocol.TargetFailed
		rec.Stage = "fetch_failed"
		rec.Message = "could not fetch secret from enrolled controller"
		rec.ErrorCode = "secret_fetch"
		rec.Retryable = true
		return rec
	}
	if a.secrets == nil {
		a.secrets = map[string]storedSecret{}
	}
	a.secrets[id] = storedSecret{ID: id, Header: hdr, Value: val, Version: ver}
	rec.Status = protocol.TargetSucceeded
	rec.Stage = "secret_version_applied"
	rec.Message = "secret version stored in worker memory"
	rec.ErrorCode = ""
	rec.AppliedAt = &now
	rec.Evidence = map[string]any{"secret_id": id, "version": ver}
	return rec
}

func (a *Agent) fetchSecret(id string) (header, value string, version int, err error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(a.State.File.ControllerURL, "/")+"/api/v1/agent/secrets/"+id, nil)
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Cred)
	req.Header.Set("X-Monik-Agent-Id", a.State.File.AgentID)
	resp, err := a.client.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	var body struct {
		Header  string `json:"header"`
		Value   string `json:"value"`
		Version int    `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", "", 0, err
	}
	if body.Value == "" || body.Header == "" {
		return "", "", 0, fmt.Errorf("empty secret")
	}
	return body.Header, body.Value, body.Version, nil
}

func (a *Agent) jobRestart(job protocol.JobEnvelope, rec protocol.JobReceipt, now time.Time) protocol.JobReceipt {
	if !a.managed() {
		rec.Status = protocol.TargetUnsupported
		rec.Stage = "unmanaged"
		rec.Message = "unmanaged worker cannot restart itself"
		return rec
	}
	if err := saveIntent(a.State.File.StateDir, &lifecycleIntent{Kind: "restart", PreviousSession: a.Session, JobID: job.JobID, OperationID: job.OperationID}); err != nil {
		rec.Status = protocol.TargetFailed
		rec.Message = "could not persist restart intent"
		rec.ErrorCode = "intent"
		return rec
	}
	if _, err := servicehost.Call(a.State.File.StateDir, servicehost.Request{Action: "restart_worker"}); err != nil {
		clearIntent(a.State.File.StateDir)
		rec.Status = protocol.TargetFailed
		rec.Stage = "restart_failed"
		rec.Message = err.Error()
		rec.ErrorCode = "service_host"
		rec.Retryable = true
		return rec
	}
	rec.Status = protocol.TargetAccepted
	rec.Stage = "restarting"
	rec.Message = "service host restart requested; new session is the completion proof"
	rec.ErrorCode = ""
	return rec
}

func (a *Agent) jobUpdate(job protocol.JobEnvelope, rec protocol.JobReceipt, now time.Time) protocol.JobReceipt {
	if !a.managed() {
		rec.Status = protocol.TargetUnsupported
		rec.Stage = "unmanaged"
		rec.Message = "unmanaged worker cannot replace itself"
		return rec
	}
	if job.Action == "update.rollback" {
		expect, _ := job.Params["sha256"].(string)
		journal, err := update.Load(a.State.File.StateDir)
		if err != nil || journal.OldSHA == "" || journal.TxID == "" || (expect != "" && expect != journal.OldSHA) {
			rec.Status = protocol.TargetRejected
			rec.Stage = "no_verified_rollback"
			rec.Message = "rollback requires a matching previous-good update journal"
			return rec
		}
		expect = journal.OldSHA
		if err := saveIntent(a.State.File.StateDir, &lifecycleIntent{Kind: "rollback", PreviousSession: a.Session, JobID: job.JobID, OperationID: job.OperationID, ExpectedDigest: expect}); err != nil {
			rec.Status = protocol.TargetFailed
			rec.Message = "could not persist rollback intent"
			return rec
		}
		params := map[string]string{"component": "worker"}
		if expect != "" {
			params["sha256"] = expect
		}
		if _, err := servicehost.Call(a.State.File.StateDir, servicehost.Request{Action: "rollback_update", Params: params}); err != nil {
			clearIntent(a.State.File.StateDir)
			rec.Status = protocol.TargetFailed
			rec.Stage = "rollback_failed"
			rec.Message = err.Error()
			rec.ErrorCode = "service_host"
			rec.Retryable = true
			return rec
		}
		rec.Status = protocol.TargetAccepted
		rec.Stage = "rolling_back"
		rec.ErrorCode = ""
		rec.Message = "rollback requested; next session digest is the proof"
		return rec
	}
	name, _ := job.Params["name"].(string)
	sha, _ := job.Params["sha256"].(string)
	if name == "" || sha == "" {
		rec.Status = protocol.TargetRejected
		rec.Stage = "rejected"
		rec.Message = "signed artifact name and sha256 are required"
		rec.ErrorCode = "missing_artifact"
		return rec
	}
	releaseDigest, _ := job.Params["release_digest"].(string)
	if _, present := job.Params["release_digest"]; present && !tufutil.ValidReleaseID(releaseDigest) {
		rec.Status, rec.Stage, rec.ErrorCode = protocol.TargetRejected, "bad_release", "bad_release"
		rec.Message = "a pinned release must contain a valid immutable digest"
		return rec
	}
	incoming, err := a.downloadVerifiedRelease(name, sha, releaseDigest)
	if err != nil {
		rec.Status = protocol.TargetFailed
		rec.Stage = "verify_failed"
		rec.Message = err.Error()
		rec.ErrorCode = "tuf"
		rec.Retryable = true
		return rec
	}
	if err := saveIntent(a.State.File.StateDir, &lifecycleIntent{Kind: "update", PreviousSession: a.Session, JobID: job.JobID, OperationID: job.OperationID, ExpectedDigest: sha}); err != nil {
		rec.Status = protocol.TargetFailed
		rec.Message = "could not persist update intent"
		return rec
	}
	if _, err := servicehost.Call(a.State.File.StateDir, servicehost.Request{Action: "activate_update", Params: map[string]string{
		"path": incoming, "sha256": sha, "component": "worker",
	}}); err != nil {
		clearIntent(a.State.File.StateDir)
		rec.Status = protocol.TargetFailed
		rec.Stage = "activate_failed"
		rec.Message = err.Error()
		rec.ErrorCode = "service_host"
		rec.Retryable = true
		return rec
	}
	rec.Status = protocol.TargetAccepted
	rec.Stage = "activating"
	rec.ErrorCode = ""
	rec.Message = "activation requested; new digest and session are the proof"
	return rec
}

func (a *Agent) downloadVerifiedArtifact(name, expectSHA string) (string, error) {
	return a.downloadVerifiedRelease(name, expectSHA, "")
}
func (a *Agent) downloadVerifiedRelease(name, expectSHA, releaseDigest string) (string, error) {
	base := "/api/v1/agent/"
	if releaseDigest != "" {
		if !tufutil.ValidReleaseID(releaseDigest) {
			return "", fmt.Errorf("invalid pinned release digest")
		}
		base += "releases/" + releaseDigest + "/"
	}

	expectedName := goruntime.GOOS + "-" + goruntime.GOARCH + "/monik-agent"
	if goruntime.GOOS == "windows" {
		expectedName += ".exe"
	}
	if name != expectedName {
		return "", fmt.Errorf("artifact must match this worker platform and component")
	}
	root, err := a.trustedRoot()
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(a.State.File.StateDir, "tuf-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	for _, meta := range []string{"timestamp.json", "snapshot.json", "targets.json"} {
		b, err := a.getBytes(base+"tuf/"+meta, releaseDigest != "")
		if err != nil {
			return "", fmt.Errorf("tuf %s: %w", meta, err)
		}
		if err := os.WriteFile(filepath.Join(tmp, meta), b, 0o600); err != nil {
			return "", err
		}
	}
	trustDir := filepath.Join(a.State.File.StateDir, "update-trust")
	prior, err := tufutil.ReadHighWater(trustDir)
	if err != nil {
		return "", err
	}
	hw, err := tufutil.VerifyRepo(root, tmp, prior, a.Clock.Now().UTC())
	if err != nil {
		return "", fmt.Errorf("tuf metadata: %w", err)
	}
	if err := tufutil.SaveHighWater(trustDir, hw); err != nil {
		return "", fmt.Errorf("persist authenticated metadata versions: %w", err)
	}
	raw, err := a.getBytes(base+"artifacts/"+name, true)
	if err != nil {
		return "", err
	}
	if err := tufutil.VerifyTarget(tmp, name, raw); err != nil {
		return "", fmt.Errorf("signed target: %w", err)
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if got != expectSHA {
		return "", fmt.Errorf("artifact sha256 mismatch")
	}
	dir := filepath.Join(a.State.File.StateDir, "updates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "incoming.bin")
	if err := secure.AtomicWrite(dst, raw, 0o700); err != nil {
		return "", err
	}
	return dst, nil
}

func (a *Agent) trustedRoot() ([]byte, error) {
	path := a.State.File.UpdateRootPath
	if path == "" {
		path = filepath.Join(a.State.File.StateDir, "tuf-root.json")
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil, fmt.Errorf("enrolled TUF root is unavailable; repair enrollment, never trust an update-supplied root")
	}
	return b, nil
}

func (a *Agent) getBytes(path string, auth bool) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(a.State.File.ControllerURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+a.Cred)
		req.Header.Set("X-Monik-Agent-Id", a.State.File.AgentID)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	limit := int64(2 << 20)
	if strings.HasPrefix(path, "/api/v1/agent/artifacts/") || strings.HasPrefix(path, "/api/v1/agent/releases/") && strings.Contains(path, "/artifacts/") {
		limit = 200 << 20
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download exceeds limit")
	}
	return b, nil
}

func (a *Agent) jobRebind(job protocol.JobEnvelope, rec protocol.JobReceipt, now time.Time) protocol.JobReceipt {
	planID, _ := job.Params["plan_id"].(string)
	failed := func(stage string, err error) protocol.JobReceipt {
		rec.Status = protocol.TargetRejected
		rec.Stage = stage
		rec.Message = err.Error()
		rec.ErrorCode = stage
		return rec
	}
	if job.Action == "rebind.prepare" {
		plan, err := planFromParams(job.Params)
		if err != nil {
			return failed("bad_plan", err)
		}
		if err := a.validatePlan(plan); err != nil {
			return failed("bad_plan", err)
		}
		if existing, err := configfile.LoadMigration(a.State.File.StateDir); err == nil && existing.PlanID != plan.PlanID && (existing.Generation >= plan.Generation || existing.ArmFallback && existing.ConfirmedAt == nil && existing.ExpiresAt.After(now)) {
			return failed("plan_conflict", fmt.Errorf("a newer or armed local plan is still active"))
		}
		if existing, e := configfile.LoadMigration(a.State.File.StateDir); e != nil || existing.PlanID != plan.PlanID {
			if plan.Generation <= a.State.File.EndpointGeneration {
				return failed("generation_reused", fmt.Errorf("a new plan requires a strictly newer endpoint generation"))
			}
		}
		plan.CurrentURL = a.State.File.ControllerURL
		if err := configfile.SaveMigration(a.State.File.StateDir, plan); err != nil {
			return failed("persist_failed", err)
		}
		rec.Status = protocol.TargetSucceeded
		rec.Stage = "rebind.prepared"
		rec.Message = "plan persisted; candidate connectivity not yet verified"
		rec.ErrorCode = ""
		rec.AppliedAt = &now
		rec.Evidence = map[string]any{"plan_id": plan.PlanID, "generation": plan.Generation}
		return rec
	}
	plan, err := configfile.LoadMigration(a.State.File.StateDir)
	if err != nil || planID == "" || plan == nil || plan.PlanID != planID {
		return failed("rebind.unprepared", fmt.Errorf("matching persisted migration plan is required"))
	}
	if job.Action == "rebind.retire" {
		if plan.ConfirmedAt == nil || a.State.File.ControllerURL != plan.CandidateURL {
			return failed("unconfirmed", fmt.Errorf("candidate has not confirmed fresh durable contact"))
		}
		if err := configfile.ClearMigration(a.State.File.StateDir); err != nil {
			return failed("persist_failed", err)
		}
		rec.Status = protocol.TargetSucceeded
		rec.Stage = "rebind.retired"
		rec.ErrorCode = ""
		rec.Message = "old endpoint retired; current confirmed endpoint retained"
		rec.AppliedAt = &now
		rec.Evidence = map[string]any{"plan_id": planID}
		return rec
	}
	if err := a.validatePlan(plan); err != nil {
		return failed("bad_plan", err)
	}
	if job.Action == "rebind.arm" {
		plan.ArmFallback = true
		if err := configfile.SaveMigration(a.State.File.StateDir, plan); err != nil {
			return failed("persist_failed", err)
		}
		rec.Status = protocol.TargetSucceeded
		rec.Stage = "rebind.armed"
		rec.ErrorCode = ""
		rec.Message = "primary-loss trigger persisted; switching is not yet confirmed"
		rec.AppliedAt = &now
		rec.Evidence = map[string]any{"plan_id": planID}
		return rec
	}
	if job.Action == "rebind.activate" {
		if err := a.beginMigration(plan, job.JobID, job.OperationID); err != nil {
			return failed("candidate_unverified", err)
		}
		rec.Status = protocol.TargetAwaitingConfirmation
		rec.Stage = "rebind.trial"
		rec.ErrorCode = ""
		rec.Message = "candidate trial started; awaiting three committed live exchanges over at least 15 seconds"
		rec.Evidence = map[string]any{"plan_id": planID, "generation": plan.Generation}
		return rec
	}
	return rec
}

func planFromParams(p map[string]any) (*protocol.MigrationPlan, error) {
	id, _ := p["plan_id"].(string)
	cand, _ := p["candidate_url"].(string)
	if id == "" || cand == "" {
		return nil, fmt.Errorf("plan_id and candidate_url required")
	}
	plan := &protocol.MigrationPlan{PlanID: id, CandidateURL: cand, Mode: "prepare"}
	if v, ok := p["generation"].(float64); ok {
		plan.Generation = int64(v)
	}
	plan.ControllerID, _ = p["controller_id"].(string)
	plan.CurrentURL, _ = p["current_url"].(string)
	plan.TrustPEM, _ = p["trust_pem"].(string)
	plan.PayloadHash, _ = p["payload_hash"].(string)
	plan.PrimaryLossSeconds = 30
	if n, ok := p["primary_loss_seconds"].(float64); ok && n >= 15 && n <= 3600 {
		plan.PrimaryLossSeconds = int(n)
	}
	if exp, _ := p["expires_at"].(string); exp != "" {
		t, _ := time.Parse(time.RFC3339, exp)
		plan.ExpiresAt = t
	}
	return plan, nil
}

func (a *Agent) verifyCandidate(plan *protocol.MigrationPlan) error {
	if err := a.validatePlan(plan); err != nil {
		return err
	}
	pem := plan.TrustPEM
	if pem == "" {
		pem = a.State.File.CACertPEM
	}
	pool, err := tlsutil.PoolFromPEM([]byte(pem))
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: 4 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			Proxy:           nil,
		},
	}
	defer client.CloseIdleConnections()
	var last error
	for i := 0; i < 1; i++ {
		req, err := http.NewRequest(http.MethodGet, strings.TrimRight(plan.CandidateURL, "/")+"/api/v1/agent/identity", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			last = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			last = fmt.Errorf("candidate identity status %d", resp.StatusCode)
			continue
		}
		var ident struct {
			ControllerID string `json:"controller_id"`
		}
		if json.Unmarshal(body, &ident) != nil || ident.ControllerID == "" {
			return fmt.Errorf("candidate identity is malformed")
		}
		want := plan.ControllerID
		if want == "" {
			want = a.State.File.ControllerID
		}
		if ident.ControllerID != want {
			return fmt.Errorf("candidate controller identity does not match the enrolled controller")
		}
		return nil
	}
	if last == nil {
		last = fmt.Errorf("candidate unreachable")
	}
	return last
}

func (a *Agent) applySwitch(in *lifecycleIntent) error {
	if _, err := netutil.ValidateControllerURL(in.CandidateURL); err != nil {
		return err
	}
	trust := in.TrustPEM
	if trust == "" {
		trust = a.State.File.CACertPEM
	}
	pool, err := tlsutil.PoolFromPEM([]byte(trust))
	if err != nil {
		return err
	}
	previous := a.State.File
	a.State.File.ControllerURL = in.CandidateURL
	a.State.File.CACertPEM = trust
	if in.Generation > a.State.File.EndpointGeneration {
		a.State.File.EndpointGeneration = in.Generation
	}
	if err := a.State.Save(); err != nil {
		a.State.File = previous
		return err
	}
	old := a.client
	next := *old
	next.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, Proxy: nil}
	a.client = &next
	old.CloseIdleConnections()
	return nil
}

// Completion requires both a different worker session and the supervisor's
// durable probation outcome. Launching the candidate alone is not success.
func (a *Agent) reconcileIntents() {
	in, err := loadIntent(a.State.File.StateDir)
	if err != nil || in == nil || in.JobID == "" || in.Kind == "rebind_switch" {
		return
	}
	if in.PreviousSession == "" || in.PreviousSession == a.Session {
		return
	}
	now := a.Clock.Now().UTC()
	rec := protocol.JobReceipt{JobID: in.JobID, OperationID: in.OperationID, Status: protocol.TargetSucceeded, AppliedAt: &now, Evidence: map[string]any{"worker_digest": a.digest, "session_id": a.Session}}
	switch in.Kind {
	case "update", "rollback":
		j, err := update.Load(a.State.File.StateDir)
		if err != nil || j.TxID == "" || j.Current != a.selfBin {
			return
		}
		if in.Kind == "update" && j.Stage == update.StageRollback && j.NewSHA == in.ExpectedDigest && a.digest == j.OldSHA {
			rec.Status = protocol.TargetRolledBack
			rec.Stage = "restored_previous_worker"
			rec.Message = "update failed; supervisor restored the previous worker"
			rec.Evidence["rollback_tx_id"] = j.TxID
		} else if in.Kind == "update" && j.Stage == update.StageConfirmed && j.NewSHA == in.ExpectedDigest && a.digest == in.ExpectedDigest {
			rec.Stage = "build_digest_session"
			rec.Message = "new worker passed supervisor probation and reports its authorized digest"
			rec.Evidence["update_tx_id"] = j.TxID
		} else if in.Kind == "rollback" && j.Stage == update.StageRollback && j.OldSHA == in.ExpectedDigest && a.digest == in.ExpectedDigest {
			rec.Stage = "build_digest_session"
			rec.Message = "previous worker restored and reports the journaled digest"
			rec.Evidence["rollback_tx_id"] = j.TxID
			rec.Evidence["previous_good_digest"] = j.OldSHA
		} else {
			return
		}
	case "restart":
		rec.Stage = "new_worker_session"
		rec.Message = "new worker session after managed restart"
	default:
		return
	}
	a.jobs[in.JobID] = rec
	if err := a.saveJobsLocked(); err == nil {
		clearIntent(a.State.File.StateDir)
	}
}
