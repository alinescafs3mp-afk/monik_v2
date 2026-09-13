package discovery

import (
	"crypto/tls"
	"net/http"
	"time"
)

func headOrGet(raw string, timeout time.Duration, allowInsecure bool) (bool, int) {
	tr := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: allowInsecure, MinVersion: tls.VersionTLS12},
	}
	client := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequest(http.MethodHead, raw, nil)
	if err != nil {
		return false, 0
	}
	req.Header.Set("User-Agent", "monik-discovery/0.1")
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		return true, resp.StatusCode
	}
	req, err = http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return false, 0
	}
	req.Header.Set("User-Agent", "monik-discovery/0.1")
	resp, err = client.Do(req)
	if err != nil {
		if allowInsecure {
			return false, 0
		}
		return false, 0
	}
	defer resp.Body.Close()
	return true, resp.StatusCode
}
