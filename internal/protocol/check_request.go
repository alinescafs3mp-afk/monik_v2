package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
)

const CustomRequestVersion = 1
const MaxRequestBody = 16384

func DecodeCheck(data []byte) (CheckDefinition, error) {
	var d CheckDefinition
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, fmt.Errorf("invalid check fields: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return d, fmt.Errorf("unexpected trailing check data")
	}
	return d, nil
}
func HeaderNameAllowed(k string, secret bool) bool {
	if k == "" || len(k) > 128 {
		return false
	}
	for _, c := range k {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	l := strings.ToLower(k)
	switch l {
	case "host", "content-length", "transfer-encoding", "connection", "upgrade", "trailer", "te", "proxy-authorization", "proxy-connection", "expect", "accept-encoding":
		return false
	}
	if strings.HasPrefix(l, "proxy-") || strings.HasPrefix(l, "sec-") {
		return false
	}
	if !secret && SensitiveName(l) {
		return false
	}
	return true
}
func SensitiveName(s string) bool {
	s = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "-", ""), "_", ""))
	for _, word := range []string{"authorization", "cookie", "password", "passwd", "token", "apikey", "secret", "credential"} {
		if strings.Contains(s, word) {
			return true
		}
	}
	return s == "auth" || s == "key"
}
func badControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\t' }) >= 0
}
func ValidateCheck(d CheckDefinition) error {
	if err := ValidateCheckRequest(d); err != nil {
		return err
	}
	if d.IntervalSeconds < 5 || d.IntervalSeconds > 3600 || d.IntervalSeconds%5 != 0 {
		return fmt.Errorf("check interval must be 5..3600 seconds in five-second steps")
	}
	if d.TimeoutSeconds < 1 || d.TimeoutSeconds > 30 || d.TimeoutSeconds >= d.IntervalSeconds {
		return fmt.Errorf("timeout must be 1..30 seconds and less than the check interval")
	}
	return nil
}

// ValidateCheckRequest is also run on the worker immediately before a dial.
func ValidateCheckRequest(d CheckDefinition) error {
	if len(d.ID) > 128 || len(d.ServiceID) > 128 || len(d.Path) > 4096 || len(d.Origin) > 32 {
		return fmt.Errorf("check identity/path metadata exceeds limit")
	}
	switch d.Origin {
	case "", "user", "auto_detected", "suggested":
	default:
		return fmt.Errorf("invalid check origin")
	}

	u, err := netutil.ParseURL(d.URL)
	if err != nil {
		return err
	}
	if len(d.URL) > 4096 || u.Fragment != "" || u.Opaque != "" || badControl(d.URL) {
		return fmt.Errorf("invalid or oversized check URL")
	}
	if u.Port() != "" {
		p, e := strconv.Atoi(u.Port())
		if e != nil || p < 1 || p > 65535 {
			return fmt.Errorf("invalid URL port")
		}
	}
	if d.RequestVersion < 0 || d.RequestVersion > CustomRequestVersion {
		return fmt.Errorf("unsupported request version")
	}
	method := d.Method
	if method == "" {
		method = "GET"
	}
	switch method {
	case "GET", "HEAD", "OPTIONS":
	case "POST":
		if !d.AllowPOST || d.RequestVersion != 1 {
			return fmt.Errorf("POST requires explicit read-only monitoring consent and request version 1")
		}
	default:
		return fmt.Errorf("supported monitoring methods: GET, HEAD, OPTIONS and explicitly authorized POST")
	}
	if method != "POST" && (d.Body != "" || d.BodySecretID != "") {
		return fmt.Errorf("request body is supported for explicitly authorized POST only")
	}
	if len(d.Body) > MaxRequestBody {
		return fmt.Errorf("request body exceeds 16 KiB")
	}
	if d.Body != "" && d.BodySecretID != "" {
		return fmt.Errorf("choose an inline body or a body secret, not both")
	}
	if len(d.Headers) > 16 {
		return fmt.Errorf("at most 16 request headers")
	}
	seen := map[string]bool{}
	total := 0
	for k, v := range d.Headers {
		l := strings.ToLower(k)
		total += len(k) + len(v)
		if seen[l] || !HeaderNameAllowed(k, false) || len(v) > 2048 || badControl(v) {
			return fmt.Errorf("invalid, duplicated or sensitive header; use a secret reference for credentials")
		}
		seen[l] = true
	}
	if total > 8192 {
		return fmt.Errorf("request headers exceed 8 KiB")
	}
	if (d.SecretID == "") != (d.SecretHeader == "") {
		return fmt.Errorf("secret_id and secret_header must be set together")
	}
	if d.SecretID != "" && (!HeaderNameAllowed(d.SecretHeader, true) || seen[strings.ToLower(d.SecretHeader)]) {
		return fmt.Errorf("invalid or duplicate secret header")
	}
	if len(d.SecretID) > 128 || len(d.BodySecretID) > 128 {
		return fmt.Errorf("invalid secret identifier")
	}
	if badControl(d.HostHeader+d.TLSServerName) || strings.ContainsAny(d.HostHeader, " /\\?#@\t") || strings.ContainsAny(d.TLSServerName, " /\\?#@:\t") {
		return fmt.Errorf("invalid Host or TLS server name")
	}
	if len(d.HostHeader) > 255 || len(d.TLSServerName) > 253 {
		return fmt.Errorf("Host or TLS server name too long")
	}
	if d.DialTarget != "" {
		h, p, e := net.SplitHostPort(d.DialTarget)
		n, pe := strconv.Atoi(p)
		if e != nil || pe != nil || n < 1 || n > 65535 || net.ParseIP(h) == nil {
			return fmt.Errorf("dial_target must be a numeric local IP:port")
		}
	}
	requestURL := *u
	if d.Path != "" {
		path, e := url.ParseRequestURI(d.Path)
		if e != nil || !strings.HasPrefix(d.Path, "/") || strings.HasPrefix(d.Path, "//") || strings.ContainsAny(d.Path, "#\r\n\\") || path.Host != "" || path.IsAbs() {
			return fmt.Errorf("path must be a same-origin absolute path, optionally with query")
		}
		requestURL.Path = path.Path
		requestURL.RawPath = path.RawPath
		requestURL.RawQuery = path.RawQuery
	}
	q, e := url.ParseQuery(requestURL.RawQuery)
	if e != nil {
		return fmt.Errorf("invalid query encoding")
	}
	for k := range q {
		if SensitiveName(k) {
			return fmt.Errorf("credentials in query strings are not supported; use a header or body secret")
		}
	}
	// Public templates are visible as configuration. Sensitive JSON/form fields must be a secret body.
	if d.Body != "" {
		var data any
		if json.Unmarshal([]byte(d.Body), &data) == nil && hasSensitiveKey(data, 0) {
			return fmt.Errorf("sensitive body fields require body_secret_id")
		}
		contentType := ""
		for k, v := range d.Headers {
			if strings.EqualFold(k, "Content-Type") {
				contentType = v
			}
		}
		if strings.HasPrefix(strings.ToLower(contentType), "application/x-www-form-urlencoded") {
			fields, e := url.ParseQuery(d.Body)
			if e != nil {
				return fmt.Errorf("invalid form encoding")
			}
			for k := range fields {
				if SensitiveName(k) {
					return fmt.Errorf("sensitive form body requires body_secret_id")
				}
			}
		}
	}
	if d.Kind != "" && d.Kind != "baseline_http" && d.Kind != "configured_http" && d.Kind != "http_health" {
		return fmt.Errorf("unsupported check kind")
	}
	baseline := d.Kind == "" || d.Kind == "baseline_http"
	if !baseline && len(d.ExpectedStatus) == 0 {
		return fmt.Errorf("application checks require expected HTTP status codes")
	}
	if len(d.ExpectedStatus) > 32 {
		return fmt.Errorf("too many expected statuses")
	}
	for _, status := range d.ExpectedStatus {
		if status < 100 || status > 599 {
			return fmt.Errorf("invalid expected HTTP status")
		}
	}
	if strings.HasPrefix(d.ExpectJSONPath, "/") {
		for i := 0; i < len(d.ExpectJSONPath); i++ {
			if d.ExpectJSONPath[i] == '~' {
				if i+1 >= len(d.ExpectJSONPath) || (d.ExpectJSONPath[i+1] != '0' && d.ExpectJSONPath[i+1] != '1') {
					return fmt.Errorf("invalid JSON Pointer escape")
				}
				i++
			}
		}
	}
	if len(d.ExpectText) > 4096 || len(d.ExpectJSONPath) > 256 || len(d.ExpectJSONValue) > 4096 {
		return fmt.Errorf("check expectation exceeds limit")
	}
	if method == "HEAD" && (d.ExpectText != "" || d.ExpectJSONPath != "" || d.ExpectHealth) {
		return fmt.Errorf("body expectations cannot use HEAD")
	}
	if baseline && (d.ExpectText != "" || d.ExpectJSONPath != "" || d.ExpectHealth || d.LatencyMS != nil) {
		return fmt.Errorf("body/latency expectations require an application check")
	}
	if d.ExpectJSONType != "" && d.ExpectJSONPath == "" {
		return fmt.Errorf("JSON type requires a field path")
	}
	switch d.ExpectJSONType {
	case "", "string", "exists", "null":
	case "boolean":
		if d.ExpectJSONValue != "true" && d.ExpectJSONValue != "false" {
			return fmt.Errorf("boolean expectation must be true or false")
		}
	case "number":
		if len(d.ExpectJSONValue) > 256 || strings.ContainsAny(d.ExpectJSONValue, "\"/") {
			return fmt.Errorf("invalid or oversized numeric expectation")
		}
		var n json.Number
		if e := json.Unmarshal([]byte(d.ExpectJSONValue), &n); e != nil {
			return fmt.Errorf("invalid numeric expectation")
		}
	default:
		return fmt.Errorf("unsupported JSON expectation type")
	}
	if d.LatencyMS != nil && (*d.LatencyMS < 1 || *d.LatencyMS > 30000) {
		return fmt.Errorf("latency threshold outside 1..30000 ms")
	}
	switch d.Purpose {
	case "", "responsiveness", "readiness", "liveness", "application":
	default:
		return fmt.Errorf("invalid check purpose")
	}
	if d.RequestVersion == 0 && (len(d.Headers) > 0 || d.Body != "" || d.BodySecretID != "" || d.ExpectJSONType != "" || d.ExpectHealth || d.AllowPOST) {
		return fmt.Errorf("custom fields require request_version 1")
	}
	return nil
}
func hasSensitiveKey(v any, depth int) bool {
	if depth > 32 {
		return true
	}
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if SensitiveName(k) || hasSensitiveKey(v, depth+1) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if hasSensitiveKey(v, depth+1) {
				return true
			}
		}
	}
	return false
}
func RequiresCustomRequest(d CheckDefinition) bool {
	return d.RequestVersion > 0 || d.IntervalSeconds != 5 || d.TimeoutSeconds != 2 || strings.Contains(d.Path, "?") || d.Method == http.MethodOptions || d.Method == http.MethodPost
}

// CheckFreshness reflects the actual scheduled interval, not the report interval.
func CheckFreshness(seconds int) time.Duration {
	if seconds < 5 || seconds > 3600 {
		seconds = 5
	}
	if seconds*3 < 20 {
		return 20 * time.Second
	}
	return time.Duration(seconds*3) * time.Second
}
