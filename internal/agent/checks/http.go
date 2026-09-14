package checks

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func Run(ctx context.Context, def protocol.CheckDefinition, locals []net.IP, headerName, headerVal string) protocol.CheckObservation {
	return RunRequest(ctx, def, locals, headerName, headerVal, "")
}

// RunRequest accepts resolved secret values only in memory, never in the definition/result.
func RunRequest(ctx context.Context, def protocol.CheckDefinition, locals []net.IP, headerName, headerVal, bodySecret string) (obs protocol.CheckObservation) {
	now := time.Now().UTC()
	obs = protocol.CheckObservation{
		ServiceID: def.ServiceID, CheckID: def.ID, ObservedAt: now, Vantage: "agent/local",
		URL: sanitizedURL(def.URL), DialTarget: def.DialTarget, Quality: protocol.QualityOK, ConfigRev: 0, IntervalSeconds: def.IntervalSeconds, RequestVersion: def.RequestVersion, Purpose: def.Purpose,
	}
	// Completion time and full bounded exchange duration, not request-start time/header RTT.
	defer func() {
		obs.ObservedAt = time.Now().UTC()
		switch {
		case obs.Transport == "blocked":
			obs.FailureLayer = "configuration"
		case obs.Transport == "tls_error":
			obs.FailureLayer = "tls"
		case obs.Transport != "ok" && obs.Transport != "paused":
			obs.FailureLayer = "connection"
		case obs.AppResult == "fail":
			obs.FailureLayer = "expectation"
		case obs.HTTPStatus != nil && *obs.HTTPStatus >= 400:
			obs.FailureLayer = "http"
		}
	}()
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
	if err := protocol.ValidateCheckRequest(def); err != nil {
		obs.Quality = protocol.QualityError
		obs.Transport = "blocked"
		obs.AppReason = err.Error()
		return obs
	}
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
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
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
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions && !(method == http.MethodPost && def.AllowPOST && def.RequestVersion == 1) {
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
		path, _ := url.ParseRequestURI(def.Path)
		requestURL.Path = path.Path
		requestURL.RawPath = path.RawPath
		requestURL.RawQuery = path.RawQuery
		requestURL.ForceQuery = path.ForceQuery
	}
	// Keep the actual path in observations; query values may contain private context.
	observedURL := requestURL
	observedURL.RawQuery = ""
	observedURL.ForceQuery = false
	obs.URL = observedURL.String()
	bodyValue := def.Body
	if def.BodySecretID != "" {
		if bodySecret == "" {
			obs.Quality = protocol.QualityError
			obs.Transport = "blocked"
			obs.AppReason = "configured request-body secret is unavailable"
			return obs
		}
		bodyValue = bodySecret
	}
	if len(bodyValue) > protocol.MaxRequestBody {
		obs.Transport = "blocked"
		obs.Quality = protocol.QualityError
		obs.AppReason = "request body exceeds 16 KiB"
		return obs
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), strings.NewReader(bodyValue))
	if err != nil {
		obs.Transport = "blocked"
		obs.Quality = protocol.QualityError
		obs.AppReason = "request could not be constructed"
		return obs
	}
	for k, v := range def.Headers {
		req.Header.Set(k, v)
	}
	if headerName != "" && (!protocol.HeaderNameAllowed(headerName, true) || strings.ContainsAny(headerVal, "\r\n") || len(headerVal) > 8192) {
		obs.Transport = "blocked"
		obs.Quality = protocol.QualityError
		obs.AppReason = "invalid resolved secret header"
		return obs
	}
	if def.HostHeader != "" {
		req.Host = def.HostHeader
	}
	if headerName != "" {
		req.Header.Set(headerName, headerVal)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "monik-check/0.4")
	}
	if req.Header.Get("Cache-Control") == "" {
		req.Header.Set("Cache-Control", "no-cache")
	}
	req.Header.Set("Accept-Encoding", "identity")
	obs.Feedback = &protocol.ResponseFeedback{Method: method, BodyState: "unavailable"}
	start := time.Now()
	defer func() { lat := float64(time.Since(start).Microseconds()) / 1000; obs.LatencyMS = &lat }()
	resp, err := client.Do(req)
	lat := float64(time.Since(start).Microseconds()) / 1000.0
	obs.LatencyMS = &lat
	if err != nil {
		es := err.Error()
		obs.Transport = classifyTransport(es)
		if errors.Is(err, context.DeadlineExceeded) {
			obs.Transport = "timeout"
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			obs.Transport = "refused"
		}
		obs.AppReason = TransportExplanation(obs.Transport)
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
		return RunRequest(ctx, def, locals, headerName, headerVal, bodySecret)
	}
	if readErr == nil && len(body) <= 64*1024 && method != http.MethodHead {
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
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if decoder.Decode(&v) != nil || decoder.Decode(new(any)) != io.EOF {
			obs.AppResult = "fail"
			obs.AppReason = "response is not json"
		} else if !matchJSON(v, def.ExpectJSONPath, def.ExpectJSONValue, def.ExpectJSONType) {
			obs.AppResult = "fail"
			obs.AppReason = "json field mismatch"
		}
	}
	if def.ExpectHealth && (obs.Feedback == nil || !PositiveHealth(obs.Feedback.Health)) {
		obs.AppResult = "fail"
		obs.AppReason = "expected an unambiguous healthy response, received absent or negative health evidence"
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

func jsonHas(v any, path, expect string) bool { return matchJSON(v, path, expect, "") }
func lookupJSON(v any, path string) (any, bool) {
	var parts []string
	if strings.HasPrefix(path, "/") {
		parts = strings.Split(path[1:], "/")
		for i, p := range parts {
			p = strings.ReplaceAll(p, "~1", "/")
			parts[i] = strings.ReplaceAll(p, "~0", "~")
		}
	} else {
		parts = strings.Split(path, ".")
	}
	cur := v
	for _, p := range parts {
		if p == "" && !strings.HasPrefix(path, "/") {
			return nil, false
		}
		switch x := cur.(type) {
		case map[string]any:
			var ok bool
			cur, ok = x[p]
			if !ok {
				return nil, false
			}
		case []any:
			n, e := strconv.Atoi(p)
			if e != nil || n < 0 || n >= len(x) {
				return nil, false
			}
			cur = x[n]
		default:
			return nil, false
		}
	}
	return cur, true
}
func matchJSON(v any, path, expect, kind string) bool {
	cur, exists := lookupJSON(v, path)
	if !exists {
		return false
	}
	switch kind {
	case "exists":
		return true
	case "null":
		return cur == nil
	case "string":
		s, ok := cur.(string)
		return ok && s == expect
	case "boolean":
		b, ok := cur.(bool)
		return ok && strconv.FormatBool(b) == expect
	case "number":
		n, ok := cur.(json.Number)
		if !ok {
			return false
		}
		if !boundedNumber(string(n)) || !boundedNumber(expect) {
			return false
		}
		a, aok := new(big.Rat).SetString(string(n))
		b, bok := new(big.Rat).SetString(expect)
		return aok && bok && a.Cmp(b) == 0
	default:
		if expect == "" {
			return true
		}
		return fmt.Sprint(cur) == expect
	}
}
func PositiveHealth(s string) bool {
	switch strings.ToLower(s) {
	case "ok", "up", "healthy", "ready", "pass", "true":
		return true
	}
	return false
}
func NegativeHealth(s string) bool { return s != "" && !PositiveHealth(s) }
func TransportExplanation(s string) string {
	switch s {
	case "refused":
		return "connection refused before HTTP: verify listener, bind address, container port and service process"
	case "timeout":
		return "connection or response deadline exceeded; no conclusion about application health"
	case "tls_error":
		return "TLS validation/handshake failed; configure the correct scheme, server name or trusted certificate"
	default:
		return "connection failed before a complete HTTP response"
	}
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
				case "false", "fail", "down", "unhealthy", "not_ready", "degraded", "warn", "starting":
					return v, name
				}
				if result == "" {
					result, source = v, name
				}
			}
		}
		return result, source
	} else if contentType == "text/plain" && len(body) <= 64 {
		switch strings.TrimSpace(string(body)) {
		case "Prometheus Server is Ready.":
			return "ready", "text/prometheus"
		case "Prometheus Server is Healthy.":
			return "healthy", "text/prometheus"
		}
		if v := token(string(body)); v != "" {
			return v, "text"
		}
	}
	return "", ""
}

func boundedNumber(s string) bool {
	if len(s) > 256 {
		return false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		n, e := strconv.Atoi(s[i+1:])
		if e != nil || n > 1000 || n < -1000 {
			return false
		}
	}
	return true
}

func sanitizedURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return "invalid URL"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}
