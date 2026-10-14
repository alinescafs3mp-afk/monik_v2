package spark

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ValidateLoopbackHealthURL implements the intentionally narrow starter policy.
// Container bridge addresses require a separately reviewed explicit local policy.
// Never take this URL directly from a browser or a server command.
func ValidateLoopbackHealthURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil {
		return fmt.Errorf("invalid health URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid scheme")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("URL credentials/query/fragment forbidden")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("literal loopback IP required")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return fmt.Errorf("explicit valid port required")
	}
	if u.Path != "/health" && u.Path != "/v1/health" {
		return fmt.Errorf("unapproved passive health path")
	}
	if u.RawPath != "" {
		return fmt.Errorf("encoded path forbidden")
	}
	return nil
}

type HealthResult struct {
	Status int
	Body   []byte
}

// PassiveHealth performs one bounded GET. No environment proxy, redirects,
// decompression, inference, token generation, TLS bypass or status inference.
// Status/body interpretation and service-identity binding belong to the engine adapter.
func PassiveHealth(ctx context.Context, raw string, maxBytes int64) (HealthResult, error) {
	if e := ValidateLoopbackHealthURL(raw); e != nil {
		return HealthResult{}, e
	}
	if maxBytes < 1 || maxBytes > 65536 {
		return HealthResult{}, fmt.Errorf("invalid body limit")
	}
	t := &http.Transport{Proxy: nil, DisableCompression: true, MaxResponseHeaderBytes: 8192,
		DialContext:         (&net.Dialer{Timeout: time.Second}).DialContext,
		TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: time.Second, DisableKeepAlives: true}
	defer t.CloseIdleConnections()
	client := &http.Client{Transport: t, Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return HealthResult{}, fmt.Errorf("invalid request")
	}
	res, e := client.Do(req)
	if e != nil {
		return HealthResult{}, fmt.Errorf("health request failed: %w", e)
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if e != nil {
		return HealthResult{}, fmt.Errorf("health read failed")
	}
	if int64(len(b)) > maxBytes {
		return HealthResult{}, fmt.Errorf("health response too large")
	}
	return HealthResult{Status: res.StatusCode, Body: b}, nil
}
