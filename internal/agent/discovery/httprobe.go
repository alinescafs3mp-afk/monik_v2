package discovery

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"
)

// Identification sends no credentials. Unverified TLS proves only protocol presence.
func headOrGet(raw string, timeout time.Duration, allowInsecure bool) (bool, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tr := &http.Transport{DisableKeepAlives: true, MaxResponseHeaderBytes: 16384, TLSClientConfig: &tls.Config{InsecureSkipVerify: allowInsecure, MinVersion: tls.VersionTLS12}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, method := range []string{"HEAD", "GET"} {
		req, err := http.NewRequestWithContext(ctx, method, raw, nil)
		if err != nil {
			return false, 0
		}
		req.Header.Set("User-Agent", "monik-discovery/0.4")
		resp, err := client.Do(req)
		if err != nil {
			return false, 0
		}
		code := resp.StatusCode
		resp.Body.Close()
		if method == "HEAD" && (code == 405 || code == 501) {
			continue
		}
		return true, code
	}
	return false, 0
}
