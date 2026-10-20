// Package probe performs bounded, one-shot service availability checks.
package probe

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Result deliberately excludes URLs and raw transport errors: both can contain secrets.
type Result struct {
	Healthy    bool   `json:"healthy"`
	StatusCode int    `json:"status_code,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// HTTP accepts only absolute HTTP(S) URLs without credentials or fragments.
// It checks response headers, does not download bodies or follow redirects,
// and considers only 2xx responses healthy. timeout bounds the whole request.
func HTTP(ctx context.Context, target string, timeout time.Duration) (Result, error) {
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return Result{}, fmt.Errorf("target must be an absolute HTTP(S) URL without credentials or fragment")
	}
	if timeout <= 0 || timeout > time.Minute {
		return Result{}, fmt.Errorf("timeout must be greater than zero and at most 1m")
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("invalid HTTP request")
	}
	// Dedicated transport avoids shared mutable state and inherited proxy settings.
	transport := &http.Transport{MaxResponseHeaderBytes: 64 << 10, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := time.Now()
	resp, err := client.Do(req)
	result := Result{DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		result.Error = "request failed"
		if requestCtx.Err() != nil {
			result.Error = requestCtx.Err().Error()
		}
		return result, nil
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	result.Healthy = resp.StatusCode >= 200 && resp.StatusCode < 300
	return result, nil
}
