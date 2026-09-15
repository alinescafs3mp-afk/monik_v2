package setup

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

type enrollmentIntent struct {
	AgentID     string `json:"agent_id"`
	Credential  string `json:"credential"`
	ProfileHash string `json:"profile_hash"`
}

func Enroll(p *Profile) (*configfile.State, error) {
	if p != nil && p.AutoDiscover {
		return PrepareDiscovery(p)
	}
	if p == nil {
		return nil, fmt.Errorf("enrollment profile required")
	}
	if p.ControllerURL == "" {
		p.ControllerURL = protocol.DefaultBootstrapURL
	}
	if _, err := netutil.ValidateControllerURL(p.ControllerURL); err != nil {
		return nil, err
	}
	p.ControllerURL = strings.TrimRight(p.ControllerURL, "/")
	if p.EnrollmentCode == "" {
		return nil, fmt.Errorf("enrollment code required")
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
	configPath := p.ConfigPath
	if configPath == "" {
		configPath = filepath.Join(p.StateDir, "agent.json")
	}
	// Re-running setup must not overwrite a working enrollment or reset its URL.
	if _, err := os.Stat(configPath); err == nil {
		return nil, fmt.Errorf("agent configuration already exists; use the existing enrollment or explicit recovery")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(p.StateDir, "discovery.credential")); err == nil {
		return nil, fmt.Errorf("state directory already belongs to automatic registration; use its existing configuration or explicit recovery")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if p.CACertPEM != "" {
		pool, err := tlsutil.PoolFromPEM([]byte(p.CACertPEM))
		if err != nil {
			return nil, err
		}
		tlsCfg.RootCAs = pool
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsCfg, Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	intentPath := filepath.Join(p.StateDir, "enrollment-intent.json")
	profileHash := secure.SHA256Bytes([]byte(p.ControllerURL + "\x00" + p.EnrollmentCode + "\x00" + p.CACertPEM + "\x00" + configPath))
	var intent enrollmentIntent
	raw, err := os.ReadFile(intentPath)
	if err == nil {
		if json.Unmarshal(raw, &intent) != nil || intent.ProfileHash != profileHash || intent.AgentID == "" || len(intent.Credential) != 64 {
			return nil, fmt.Errorf("pending enrollment differs from this profile; retry the original profile or explicitly recover after checking controller registration")
		}
	} else if os.IsNotExist(err) {
		credential, err := idgen.Secret(32)
		if err != nil {
			return nil, err
		}
		intent = enrollmentIntent{AgentID: idgen.New(), Credential: credential, ProfileHash: profileHash}
		raw, err = json.Marshal(intent)
		if err != nil {
			return nil, err
		}
		if err = secure.AtomicWrite(intentPath, raw, 0600); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	hn, _ := os.Hostname()
	body, _ := json.Marshal(protocol.EnrollRequest{Code: p.EnrollmentCode, Credential: intent.Credential, AgentID: intent.AgentID, DisplayName: p.DisplayName, Hostname: hn, OS: runtime.GOOS, Arch: runtime.GOARCH, Version: version.Version})
	resp, err := client.Post(p.ControllerURL+"/api/v1/agent/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("enrollment exchange failed; saved identity can be retried with the same profile: %w", err)
	}
	defer resp.Body.Close()
	slurp, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(slurp) > 1<<20 {
		return nil, fmt.Errorf("incomplete or oversized enrollment response; retry with the same profile")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enrollment rejected (HTTP %d); check the code and server version; redirects are not followed", resp.StatusCode)
	}
	var er protocol.EnrollResponse
	if err = jsonutil.Unmarshal(slurp, &er); err != nil {
		return nil, fmt.Errorf("invalid enrollment response")
	}
	if er.AgentID != intent.AgentID || er.Credential != intent.Credential || er.ControllerID == "" || er.EndpointGeneration < 1 {
		return nil, fmt.Errorf("controller did not confirm the persisted enrollment proof; upgrade the server before enrolling this agent and review any legacy registration")
	}
	ca := p.CACertPEM
	if ca == "" {
		ca = er.CACertPEM
	}
	if _, err = tlsutil.PoolFromPEM([]byte(ca)); err != nil {
		return nil, fmt.Errorf("agent-scoped controller trust required: %w", err)
	}
	credPath := filepath.Join(p.StateDir, "agent.credential")
	if err = configfile.WriteCredential(credPath, intent.Credential); err != nil {
		return nil, err
	}
	f := configfile.File{SchemaVersion: protocol.SchemaVersion, AgentID: intent.AgentID, DisplayName: p.DisplayName, ControllerURL: p.ControllerURL, ControllerID: er.ControllerID, CACertPEM: ca, CredentialPath: credPath, StateDir: p.StateDir, EndpointGeneration: er.EndpointGeneration, BootstrapURL: protocol.DefaultBootstrapURL}
	// A config has not been applied just because enrollment returned its revision.
	if er.UpdateRootJSON != "" {
		rootPath := filepath.Join(p.StateDir, "tuf-root.json")
		if err = secure.AtomicWrite(rootPath, []byte(er.UpdateRootJSON), 0600); err != nil {
			return nil, err
		}
		f.UpdateRootPath = rootPath
	}
	raw, err = json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = secure.AtomicWrite(configPath, raw, 0600); err != nil {
		return nil, err
	}
	// A crash before removing this non-authoritative intent is harmless: config exists.
	_ = os.Remove(intentPath)
	return configfile.Load(configPath)
}
