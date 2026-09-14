package checks

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func Run(ctx context.Context, def protocol.CheckDefinition, locals []net.IP, headerName, headerVal string) (obs protocol.CheckObservation) {
	now := time.Now().UTC()
	obs = protocol.CheckObservation{
		ServiceID: def.ServiceID, CheckID: def.ID, ObservedAt: now, Vantage: "agent/local",
		URL: def.URL, DialTarget: def.DialTarget, Quality: protocol.QualityOK, ConfigRev: 0,
	}
	// Completion time and full bounded exchange duration, not request-start time/header RTT.
	defer func() { obs.ObservedAt = time.Now().UTC() }()
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
		if u.Port() == "" {
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
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tr := &http.Transport{
		MaxResponseHeaderBytes: 32 * 1024,
		DisableKeepAlives:      true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, "tcp", dial)
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: def.TLSServerName, InsecureSkipVerify: def.InsecureTLS},
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	method := def.Method
	if method == "" {
		method = http.MethodGet // Bounded read enables generic JSON/text health feedback.
	}
	if method != http.MethodGet && method != http.MethodHead {
		obs.Quality = protocol.QualityError
		obs.Transport = "blocked"
		obs.AppReason = "only GET/HEAD health probes are allowed"
		return obs
	}
	if def.SecretID != "" && (headerName == "" || headerVal == "" || !strings.EqualFold(headerName, def.SecretHeader)) {
		obs.Quality = protocol.QualityError
		obs.Transport = "blocked"
		obs.AppReason = "configured secret is unavailable or header does not match"
		return obs
	}
	requestURL := *u
	if def.Path != "" {
		if !strings.HasPrefix(def.Path, "/") || strings.ContainsAny(def.Path, "?#\r\n") {
			obs.Quality = protocol.QualityError
			obs.Transport = "blocked"
			obs.AppReason = "check path must be an absolute path without query or fragment"
			return obs
		}
		requestURL.Path = def.Path
		requestURL.RawPath = ""
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), nil)
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
	obs.Feedback = &protocol.ResponseFeedback{Method: method, BodyState: "unavailable"}
	start := time.Now()
	defer func() { lat := float64(time.Since(start).Microseconds()) / 1000; obs.LatencyMS = &lat }()
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
			obs.TLSReason = "TLS certificate or handshake validation failed"
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
	obs.Feedback.StatusText = http.StatusText(code)
	if ct, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && len(ct) <= 100 {
		obs.Feedback.ContentType = ct
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	obs.Feedback.SampledBytes = len(body)
	obs.Feedback.BodyState = "sampled"
	if method == http.MethodHead {
		obs.Feedback.BodyState = "not_requested"
	} else if len(body) == 0 {
		obs.Feedback.BodyState = "empty"
	}
	if len(body) > 64*1024 {
		obs.Feedback.BodyState = "limited"
	}
	if readErr != nil {
		obs.Feedback.BodyState = "read_error"
	}
	if readErr != nil || (len(body) > 64*1024 && def.Kind != "baseline_http" && def.Kind != "") {
		obs.Quality = protocol.QualityError
		obs.AppResult = "fail"
		obs.AppReason = "response body failed or exceeded limit"
		return obs
	}
	if method == http.MethodHead && (code == 405 || code == 501) {
		def.Method = http.MethodGet
		return Run(ctx, def, locals, headerName, headerVal)
	}
	if readErr == nil && len(body) <= 64*1024 && method == http.MethodGet {
		obs.Feedback.Health, obs.Feedback.HealthSource = healthToken(body, obs.Feedback.ContentType)
	}
	if def.Kind == "baseline_http" || def.Kind == "" {
		obs.AppResult = "not_configured"
		return obs
	}
	obs.AppResult = "pass"
	if len(def.ExpectedStatus) == 0 {
		obs.AppResult = "fail"
		obs.AppReason = "application check has no expected status"
	}
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
	if def.LatencyMS != nil && float64(time.Since(start).Microseconds())/1000 > float64(*def.LatencyMS) {
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

// Interpret a deliberately tiny vocabulary. Never copy arbitrary JSON messages/titles,
// error traces, request identifiers or text excerpts into fleet telemetry.
func healthToken(body []byte, contentType string) (string, string) {
	token := func(v any) string {
		switch x := v.(type) {
		case bool:
			if x {
				return "true"
			}
			return "false"
		case string:
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "ok", "pass", "fail", "warn", "up", "down", "healthy", "unhealthy", "ready", "not_ready", "degraded", "starting":
				return strings.ToLower(strings.TrimSpace(x))
			}
		}
		return ""
	}
	if contentType == "application/json" || strings.HasSuffix(contentType, "+json") || (contentType == "" && strings.HasPrefix(strings.TrimSpace(string(body)), "{")) {
		var fields map[string]any
		if json.Unmarshal(body, &fields) != nil {
			return "", ""
		}
		var result, source string
		for _, name := range []string{"status", "health", "state", "ready", "ok", "success"} {
			if v := token(fields[name]); v != "" {
				switch v {
				case "false", "fail", "down", "unhealthy", "not_ready", "degraded", "warn":
					return v, name
				}
				if result == "" {
					result, source = v, name
				}
			}
		}
		return result, source
	} else if contentType == "text/plain" && len(body) <= 64 {
		if v := token(string(body)); v != "" {
			return v, "text"
		}
	}
	return "", ""
}
