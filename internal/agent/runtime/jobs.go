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
)

type storedSecret struct {
	ID      string
	Header  string
	Value   string
	Version int
}

type lifecycleIntent struct {
	Kind           string `json:"kind"`
	JobID          string `json:"job_id"`
	OperationID    string `json:"operation_id"`
	ExpectedDigest string `json:"expected_digest,omitempty"`
	CandidateURL   string `json:"candidate_url,omitempty"`
	TrustPEM       string `json:"trust_pem,omitempty"`
	Generation     int64  `json:"generation,omitempty"`
	PlanID         string `json:"plan_id,omitempty"`
	ControllerID   string `json:"controller_id,omitempty"`
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
	tmp := intentPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, intentPath(dir))
}

func loadIntent(dir string) (*lifecycleIntent, error) {
	b, err := os.ReadFile(intentPath(dir))
	if err != nil {
		return nil, err
	}
	in := &lifecycleIntent{}
	if err := json.Unmarshal(b, in); err != nil {
		return nil, err
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	obs := checks.Run(ctx, def, locals, hdr, val)
	rec.Status = protocol.TargetSucceeded
	rec.Stage = "redacted_trial_result"
	rec.ErrorCode = ""
	rec.Message = "trial completed; definition not saved"
	rec.AppliedAt = &now
	rec.Evidence = map[string]any{
		"trial": true, "vantage": "agent/local", "transport": obs.Transport,
		"http_status": obs.HTTPStatus, "latency_ms": obs.LatencyMS,
		"app_result": obs.AppResult, "app_reason": obs.AppReason, "quality": obs.Quality,
	}
	return rec
}

func (a *Agent) jobRotate(job protocol.JobEnvelope, rec protocol.JobReceipt, now time.Time) protocol.JobReceipt {
	nextPath := filepath.Join(a.State.File.StateDir, "next.credential")
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
	_ = configfile.WriteCredential(prevPath, a.Cred)
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

func (a *Agent) currentTrustPEM() []byte {
	if b, err := os.ReadFile(trustBundlePath(a.State.File.StateDir)); err == nil && len(b) > 0 {
		return b
	}
	return []byte(a.State.File.CACertPEM)
}

func (a *Agent) applyTrustPool() error {
	pool, err := tlsutil.PoolFromPEM(a.currentTrustPEM())
	if err != nil {
		return err
	}
	a.client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		Proxy:           nil,
	}
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
	if err := os.WriteFile(trustBundlePath(a.State.File.StateDir), encodeCerts(merged), 0o600); err != nil {
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
	if err := os.WriteFile(trustBundlePath(a.State.File.StateDir), encodeCerts(kept), 0o600); err != nil {
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
	if err := saveIntent(a.State.File.StateDir, &lifecycleIntent{Kind: "restart", JobID: job.JobID, OperationID: job.OperationID}); err != nil {
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
		if err := saveIntent(a.State.File.StateDir, &lifecycleIntent{Kind: "rollback", JobID: job.JobID, OperationID: job.OperationID, ExpectedDigest: expect}); err != nil {
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
	incoming, err := a.downloadVerifiedArtifact(name, sha)
	if err != nil {
		rec.Status = protocol.TargetFailed
		rec.Stage = "verify_failed"
		rec.Message = err.Error()
		rec.ErrorCode = "tuf"
		rec.Retryable = true
		return rec
	}
	if err := saveIntent(a.State.File.StateDir, &lifecycleIntent{Kind: "update", JobID: job.JobID, OperationID: job.OperationID, ExpectedDigest: sha}); err != nil {
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
	root, err := a.trustedRoot()
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(a.State.File.StateDir, "tuf-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	for _, meta := range []string{"root.json", "timestamp.json", "snapshot.json", "targets.json"} {
		b, err := a.getBytes("/api/v1/agent/tuf/"+meta, false)
		if err != nil {
			return "", fmt.Errorf("tuf %s: %w", meta, err)
		}
		if err := os.WriteFile(filepath.Join(tmp, meta), b, 0o600); err != nil {
			return "", err
		}
	}
	if _, err := tufutil.VerifyRepo(root, tmp, tufutil.HighWater{}, a.Clock.Now().UTC()); err != nil {
		return "", fmt.Errorf("tuf metadata: %w", err)
	}
	raw, err := a.getBytes("/api/v1/agent/artifacts/"+name, true)
	if err != nil {
		return "", err
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
	if err := os.WriteFile(dst, raw, 0o700); err != nil {
		return "", err
	}
	return dst, nil
}

func (a *Agent) trustedRoot() ([]byte, error) {
	path := a.State.File.UpdateRootPath
	if path == "" {
		path = filepath.Join(a.State.File.StateDir, "tuf-root.json")
	}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return b, nil
	}
	b, err := a.getBytes("/api/v1/agent/update-root", true)
	if err != nil {
		return nil, fmt.Errorf("no enrolled TUF root: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err == nil {
		a.State.File.UpdateRootPath = path
		_ = a.State.Save()
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
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

func (a *Agent) jobRebind(job protocol.JobEnvelope, rec protocol.JobReceipt, now time.Time) protocol.JobReceipt {
	planID, _ := job.Params["plan_id"].(string)
	switch job.Action {
	case "rebind.prepare":
		plan, err := planFromParams(job.Params)
		if err != nil {
			rec.Status = protocol.TargetRejected
			rec.Message = err.Error()
			rec.ErrorCode = "bad_plan"
			return rec
		}
		if err := configfile.SaveMigration(a.State.File.StateDir, plan); err != nil {
			rec.Status = protocol.TargetFailed
			rec.Message = "could not persist migration plan"
			return rec
		}
		rec.Status = protocol.TargetSucceeded
		rec.Stage = "rebind.prepared"
		rec.ErrorCode = ""
		rec.Message = "plan stored; controller URL unchanged"
		rec.AppliedAt = &now
		rec.Evidence = map[string]any{"plan_id": plan.PlanID, "generation": plan.Generation}
		return rec
	case "rebind.arm":
		plan, err := configfile.LoadMigration(a.State.File.StateDir)
		if err != nil || plan == nil || (planID != "" && plan.PlanID != planID) {
			rec.Status = protocol.TargetRejected
			rec.Stage = "rebind.unprepared"
			rec.Message = "no prepared plan to arm"
			rec.ErrorCode = "unprepared"
			return rec
		}
		plan.ArmFallback = true
		_ = configfile.SaveMigration(a.State.File.StateDir, plan)
		rec.Status = protocol.TargetSucceeded
		rec.Stage = "rebind.armed"
		rec.ErrorCode = ""
		rec.Message = "fallback armed"
		rec.AppliedAt = &now
		rec.Evidence = map[string]any{"plan_id": plan.PlanID}
		return rec
	case "rebind.activate":
		plan, err := configfile.LoadMigration(a.State.File.StateDir)
		if err != nil || plan == nil || (planID != "" && plan.PlanID != planID) {
			rec.Status = protocol.TargetRejected
			rec.Stage = "rebind.unprepared"
			rec.Message = "no prepared plan; refusing to discover an unknown replacement address"
			rec.ErrorCode = "unprepared"
			return rec
		}
		if err := a.verifyCandidate(plan); err != nil {
			rec.Status = protocol.TargetFailed
			rec.Stage = "candidate_unverified"
			rec.Message = err.Error()
			rec.ErrorCode = "identity"
			rec.Retryable = true
			return rec
		}
		if err := saveIntent(a.State.File.StateDir, &lifecycleIntent{
			Kind: "rebind_switch", JobID: job.JobID, OperationID: job.OperationID,
			CandidateURL: plan.CandidateURL, TrustPEM: plan.TrustPEM, Generation: plan.Generation,
			PlanID: plan.PlanID, ControllerID: plan.ControllerID,
		}); err != nil {
			rec.Status = protocol.TargetFailed
			rec.Message = "could not persist switch intent"
			return rec
		}
		a.State.File.EndpointGeneration = plan.Generation
		_ = a.State.Save()
		rec.Status = protocol.TargetSucceeded
		rec.Stage = "rebind.activating"
		rec.ErrorCode = ""
		rec.Message = "candidate verified; switch waits for receipt acknowledgement"
		rec.AppliedAt = &now
		rec.Evidence = map[string]any{"plan_id": plan.PlanID, "generation": plan.Generation}
		return rec
	case "rebind.retire":
		_ = configfile.ClearMigration(a.State.File.StateDir)
		clearIntent(a.State.File.StateDir)
		rec.Status = protocol.TargetSucceeded
		rec.Stage = "rebind.retired"
		rec.ErrorCode = ""
		rec.Message = "prepared endpoint retired"
		rec.AppliedAt = &now
		rec.Evidence = map[string]any{"plan_id": planID}
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
	if exp, _ := p["expires_at"].(string); exp != "" {
		t, _ := time.Parse(time.RFC3339, exp)
		plan.ExpiresAt = t
	}
	return plan, nil
}

func (a *Agent) verifyCandidate(plan *protocol.MigrationPlan) error {
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
	var last error
	for i := 0; i < 3; i++ {
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
	if in.CandidateURL == "" {
		return fmt.Errorf("missing candidate")
	}
	a.State.File.ControllerURL = in.CandidateURL
	if in.TrustPEM != "" {
		a.State.File.CACertPEM = in.TrustPEM
		if pool, err := tlsutil.PoolFromPEM([]byte(in.TrustPEM)); err == nil {
			a.client.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
				Proxy:           nil,
			}
		}
	}
	if in.Generation > 0 {
		a.State.File.EndpointGeneration = in.Generation
	}
	return a.State.Save()
}

func (a *Agent) reconcileIntents() {
	in, err := loadIntent(a.State.File.StateDir)
	if err != nil || in == nil || in.JobID == "" {
		return
	}
	now := a.Clock.Now().UTC()
	switch in.Kind {
	case "update", "rollback":
		if in.ExpectedDigest != "" && a.digest == in.ExpectedDigest {
			a.jobs[in.JobID] = protocol.JobReceipt{
				JobID: in.JobID, OperationID: in.OperationID, Status: protocol.TargetSucceeded,
				Stage: "build_digest_session", Message: "new worker digest matches authorized build",
				AppliedAt: &now, Evidence: map[string]any{"worker_digest": a.digest, "session_id": a.Session},
			}
			clearIntent(a.State.File.StateDir)
		}
	case "restart":
		a.jobs[in.JobID] = protocol.JobReceipt{
			JobID: in.JobID, OperationID: in.OperationID, Status: protocol.TargetSucceeded,
			Stage: "new_worker_session", Message: "new worker session after managed restart",
			AppliedAt: &now, Evidence: map[string]any{"session_id": a.Session},
		}
		clearIntent(a.State.File.StateDir)
	case "rebind_switch":
		if a.State.File.ControllerURL == in.CandidateURL {
			clearIntent(a.State.File.StateDir)
		}
	}
	_ = a.saveJobsLocked()
}
