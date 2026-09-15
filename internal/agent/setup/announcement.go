package setup

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PrepareDiscovery performs no network request and grants no remote authority.
// A trusted CA profile and stable local random identity are required first.
func PrepareDiscovery(p *Profile) (*configfile.State, error) {
	if p == nil {
		return nil, fmt.Errorf("trusted discovery profile required")
	}
	if p.EnrollmentCode != "" {
		return nil, fmt.Errorf("choose automatic discovery OR a one-use enrollment code")
	}
	if p.ControllerURL == "" {
		p.ControllerURL = protocol.DefaultBootstrapURL
	}
	if _, err := netutil.ValidateControllerURL(p.ControllerURL); err != nil {
		return nil, err
	}
	p.ControllerURL = strings.TrimRight(p.ControllerURL, "/")
	if _, err := tlsutil.PoolFromPEM([]byte(p.CACertPEM)); err != nil {
		return nil, fmt.Errorf("import trusted controller CA before discovery: %w", err)
	}
	if p.StateDir == "" {
		p.StateDir = defaultStateDir()
	}
	if err := os.MkdirAll(p.StateDir, 0700); err != nil {
		return nil, err
	}
	unlock, err := acquireSetupLock(filepath.Join(p.StateDir, "setup.lock"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	cfg := p.ConfigPath
	if cfg == "" {
		cfg = filepath.Join(p.StateDir, "agent.json")
	}
	// One state directory belongs to one identity. A different config filename
	// must not bypass the existing-config check and overwrite its credential.
	canonical, err := filepath.Abs(filepath.Join(p.StateDir, "agent.json"))
	if err != nil {
		return nil, err
	}
	actual, err := filepath.Abs(cfg)
	if err != nil {
		return nil, err
	}
	if actual != canonical && !(runtime.GOOS == "windows" && strings.EqualFold(actual, canonical)) {
		return nil, fmt.Errorf("automatic discovery requires agent.json in its own state directory; choose a separate state directory")
	}
	if _, err := os.Stat(filepath.Join(p.StateDir, "agent.credential")); err == nil {
		return nil, fmt.Errorf("state directory already contains a code-enrollment credential; use its existing configuration or explicit recovery")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := os.Stat(cfg); err == nil {
		return nil, fmt.Errorf("agent config already exists; run the installed agent instead of creating a new identity")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	// An interrupted ordinary enrollment must never be silently converted.
	if _, err := os.Stat(filepath.Join(p.StateDir, "enrollment-intent.json")); err == nil {
		return nil, fmt.Errorf("unfinished code enrollment exists; retry its original profile")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	cred, err := idgen.Secret(32)
	if err != nil {
		return nil, err
	}
	// Write credentials first; losing the first setup before publishing config has
	// not announced this identity. Existing published configurations are untouched.
	credPath := filepath.Join(p.StateDir, "discovery.credential")
	if err = configfile.WriteCredential(credPath, cred); err != nil {
		return nil, err
	}
	f := configfile.File{SchemaVersion: protocol.SchemaVersion, AgentID: idgen.New(), DisplayName: p.DisplayName, ControllerURL: p.ControllerURL, CACertPEM: p.CACertPEM, CredentialPath: credPath, StateDir: p.StateDir, BootstrapURL: protocol.DefaultBootstrapURL, PendingRegistration: true}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = secure.AtomicWrite(cfg, raw, 0600); err != nil {
		return nil, err
	}
	return configfile.Load(cfg)
}

// AnnounceOnce is restartable and never accepts an identity/credential change
// from the network. Server approval still needs a committed local transition.
func AnnounceOnce(ctx context.Context, st *configfile.State) (string, error) {
	if !st.File.PendingRegistration {
		return "approved", nil
	}
	if _, err := netutil.ValidateControllerURL(st.File.ControllerURL); err != nil {
		return "", err
	}
	pool, err := tlsutil.PoolFromPEM([]byte(st.File.CACertPEM))
	if err != nil {
		return "", err
	}
	cred, err := configfile.ReadCredential(st.File.CredentialPath)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	hostname, _ := os.Hostname()
	body, _ := json.Marshal(protocol.Announcement{AgentID: st.File.AgentID, Credential: cred, Hostname: hostname, DisplayName: st.File.DisplayName, OS: runtime.GOOS, Arch: runtime.GOARCH, Version: version.Version})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, st.File.ControllerURL+"/api/v1/agent/announce", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("invalid controller request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("controller unreachable or TLS could not be verified")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("announcement rejected (HTTP %d); no registration confirmed", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return "", fmt.Errorf("invalid bounded announcement response")
	}
	var out protocol.AnnouncementResponse
	if jsonutil.Unmarshal(raw, &out) != nil || out.Fingerprint != protocol.RegistrationFingerprint(st.File.AgentID, cred) {
		return "", fmt.Errorf("announcement proof mismatch")
	}
	switch out.State {
	case "pending", "rejected", "admission_closed":
		return out.State, nil
	case "approved":
		e := out.Enrollment
		if e == nil || e.AgentID != st.File.AgentID || e.Credential != cred || e.ControllerID == "" || e.EndpointGeneration < 1 {
			return "", fmt.Errorf("controller did not confirm this enrollment")
		}
		next := st.File
		next.ControllerID = e.ControllerID
		next.EndpointGeneration = e.EndpointGeneration
		next.PendingRegistration = false
		if e.UpdateRootJSON != "" {
			path := filepath.Join(next.StateDir, "tuf-root.json")
			if err = secure.AtomicWrite(path, []byte(e.UpdateRootJSON), 0600); err != nil {
				return "", err
			}
			next.UpdateRootPath = path
		}
		old := st.File
		st.File = next
		if err = st.Save(); err != nil {
			st.File = old
			return "", err
		}
		return "approved", nil
	default:
		return "", fmt.Errorf("unknown registration state")
	}
}

// Before enrollment only this bounded beacon loop runs. There are no collectors,
// local port probes, job polls, secret requests, or remote commands in quarantine.
func WaitForApproval(ctx context.Context, path string, out io.Writer) error {
	st, err := configfile.Load(path)
	if err != nil {
		return err
	}
	if !st.File.PendingRegistration {
		return nil
	}
	cred, err := configfile.ReadCredential(st.File.CredentialPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Pending owner approval. agent_id=%s fingerprint=%s\n", st.File.AgentID, protocol.RegistrationFingerprint(st.File.AgentID, cred))
	last := ""
	delay := 30 * time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		state, err := AnnounceOnce(ctx, st)
		message := state
		if err != nil {
			message = err.Error()
			delay = 60 * time.Second
		} else {
			delay = 30 * time.Second
		}
		if message != last {
			fmt.Fprintln(out, "Registration:", message)
			last = message
		}
		if state == "approved" {
			return nil
		}
		if state == "rejected" {
			delay = 10 * time.Minute
		}
		// Identity-based jitter prevents a rebooted fleet from waking in lockstep.
		if len(st.File.AgentID) > 0 {
			delay += time.Duration(st.File.AgentID[0]%6) * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
