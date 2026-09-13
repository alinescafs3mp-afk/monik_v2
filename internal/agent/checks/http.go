package checks

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func Run(ctx context.Context, def protocol.CheckDefinition, locals []net.IP, headerName, headerVal string) protocol.CheckObservation {
	now := time.Now().UTC()
	obs := protocol.CheckObservation{
		ServiceID: def.ServiceID, CheckID: def.ID, ObservedAt: now, Vantage: "agent/local",
		URL: def.URL, DialTarget: def.DialTarget, Quality: protocol.QualityOK, ConfigRev: 0,
	}
	if def.Paused || def.Ignored {
		obs.Quality = protocol.QualityPaused
		obs.Transport = "paused"
		return obs
	}
	u, err := netutil.ParseURL(def.URL)
	if err != nil {
		obs.Quality = protocol.QualityError
		obs.Transport = "blocked"
		obs.AppReason = err.Error()
		return obs
	}
	dial := def.DialTarget
	if dial == "" {
		dial = u.Host
		if !strings.Contains(dial, ":") {
			if u.Scheme == "https" {
				dial = net.JoinHostPort(u.Hostname(), "443")
			} else {
				dial = net.JoinHostPort(u.Hostname(), "80")
			}
		}
	}
	obs.DialTarget = dial
	policy := netutil.DefaultPolicy()
	if _, err := netutil.AllowedDial(dial, policy, locals); err != nil {
		obs.Quality = protocol.QualityError
		obs.Transport = "blocked"
		obs.AppReason = err.Error()
		return obs
	}
	timeout := time.Duration(def.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = protocol.HTTPProbeTimeout
	}
	tr := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, "tcp", dial)
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: def.TLSServerName, InsecureSkipVerify: def.InsecureTLS},
	}
	client := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	method := def.Method
	if method == "" {
		method = http.MethodHead
	}
	req, err := http.NewRequestWithContext(ctx, method, def.URL, nil)
	if err != nil {
		obs.Transport = "error"
		obs.Quality = protocol.QualityError
		return obs
	}
	if def.HostHeader != "" {
		req.Host = def.HostHeader
	}
	if headerName != "" {
		req.Header.Set(headerName, headerVal)
	}
	req.Header.Set("User-Agent", "monik-check/0.1")
	start := time.Now()
	resp, err := client.Do(req)
	lat := float64(time.Since(start).Microseconds()) / 1000.0
	obs.LatencyMS = &lat
	if err != nil {
		es := err.Error()
		obs.Transport = classifyTransport(es)
		if def.InsecureTLS {
			obs.TLSValid = boolPtr(false)
		}
		if strings.Contains(es, "certificate") || strings.Contains(es, "tls") {
			obs.TLSValid = boolPtr(false)
			obs.TLSReason = es
		}
		return obs
	}
	defer resp.Body.Close()
	obs.Transport = "ok"
	code := resp.StatusCode
	obs.HTTPStatus = &code
	if u.Scheme == "https" {
		ok := !def.InsecureTLS
		obs.TLSValid = &ok
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if method == http.MethodHead && (code == 405 || code == 501) {
		def.Method = http.MethodGet
		return Run(ctx, def, locals, headerName, headerVal)
	}
	if def.Kind == "baseline_http" || def.Kind == "" {
		obs.AppResult = "not_configured"
		return obs
	}
	obs.AppResult = "pass"
	if len(def.ExpectedStatus) > 0 {
		ok := false
		for _, exp := range def.ExpectedStatus {
			if code == exp {
				ok = true
			}
		}
		if !ok {
			obs.AppResult = "fail"
			obs.AppReason = fmt.Sprintf("status %d not in expected", code)
		}
	}
	if def.ExpectText != "" && !strings.Contains(string(body), def.ExpectText) {
		obs.AppResult = "fail"
		obs.AppReason = "expected text missing"
	}
	if def.ExpectJSONPath != "" {
		var v any
		if json.Unmarshal(body, &v) != nil {
			obs.AppResult = "fail"
			obs.AppReason = "response is not json"
		} else if !jsonHas(v, def.ExpectJSONPath, def.ExpectJSONValue) {
			obs.AppResult = "fail"
			obs.AppReason = "json field mismatch"
		}
	}
	if def.LatencyMS != nil && lat > float64(*def.LatencyMS) {
		obs.AppResult = "fail"
		obs.AppReason = "latency threshold"
	}
	return obs
}

func boolPtr(b bool) *bool { return &b }

func classifyTransport(es string) string {
	switch {
	case strings.Contains(es, "timeout"):
		return "timeout"
	case strings.Contains(es, "refused"):
		return "refused"
	case strings.Contains(es, "certificate") || strings.Contains(es, "tls"):
		return "tls_error"
	default:
		return "error"
	}
}

func jsonHas(v any, path, expect string) bool {
	cur := v
	for _, p := range strings.Split(path, ".") {
		if p == "" {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = m[p]
		if !ok {
			return false
		}
	}
	if expect == "" {
		return true
	}
	return fmt.Sprint(cur) == expect
}
