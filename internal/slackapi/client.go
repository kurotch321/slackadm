package slackapi

import (
	"context"
	"encoding/json"
	"github.com/kurotch321/slackadm/internal/contract"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Object = map[string]any
type Client struct {
	Token       string
	BaseURL     string
	HTTP        *http.Client
	Scopes      []string
	ScopesKnown bool
	MaxRetries  int
}

func New(token string) *Client {
	return &Client{Token: token, BaseURL: "https://slack.com/api/", HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, MaxRetries: 3}
}
func String(m Object, k string) string { v, _ := m[k].(string); return v }
func Map(m Object, k string) Object    { v, _ := m[k].(map[string]any); return v }
func Bool(m Object, k string) bool     { v, _ := m[k].(bool); return v }
func (c *Client) Call(ctx context.Context, method string, args url.Values, write bool) (Object, error) {
	for attempt := 0; ; attempt++ {
		var sent atomic.Bool
		trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { sent.Store(true) }}
		httpMethod, target := "POST", c.BaseURL+method
		var requestBody io.Reader = strings.NewReader(args.Encode())
		if !write {
			httpMethod = "GET"
			requestBody = nil
			if len(args) > 0 {
				target += "?" + args.Encode()
			}
		}
		req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), httpMethod, target, requestBody)
		if err != nil {
			return nil, contract.Input("Invalid API endpoint.")
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		res, err := c.HTTP.Do(req)
		if err != nil {
			if write && sent.Load() {
				return nil, unknown()
			}
			return nil, contract.New("network_error", "Slack connection failed.", "Check connectivity and retry.", 5)
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		res.Body.Close()
		if scopes, ok := res.Header["X-Oauth-Scopes"]; ok {
			c.ScopesKnown = true
			c.Scopes = nil
			for _, s := range strings.Split(strings.Join(scopes, ","), ",") {
				if s = strings.TrimSpace(s); s != "" {
					c.Scopes = append(c.Scopes, s)
				}
			}
		}
		if res.StatusCode == 429 {
			seconds, e := strconv.Atoi(res.Header.Get("Retry-After"))
			if e != nil || seconds < 0 || seconds > 60 || attempt >= c.MaxRetries {
				return nil, contract.New("rate_limited", "Slack rate limit reached.", "Wait for Retry-After before retrying (maximum automatic wait: 60 seconds).", 5)
			}
			timer := time.NewTimer(time.Duration(seconds) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, contract.New("cancelled", "Request cancelled.", "Retry when ready.", 5)
			case <-timer.C:
			}
			continue
		}
		if readErr != nil || res.StatusCode < 200 || res.StatusCode >= 300 {
			if write {
				return nil, unknown()
			}
			return nil, contract.New("api_error", "Slack returned an invalid HTTP response.", "Check Slack status and retry.", 5)
		}
		var data Object
		if json.Unmarshal(body, &data) != nil {
			if write {
				return nil, unknown()
			}
			return nil, contract.New("invalid_response", "Slack returned invalid JSON.", "Retry later.", 5)
		}
		if !Bool(data, "ok") {
			code := String(data, "error")
			e := contract.New("slack_error", "Slack rejected the operation.", "Check the Slack error and command scopes.", 5)
			switch code {
			case "invalid_auth", "not_authed", "token_revoked", "token_expired", "account_inactive", "invalid_refresh_token":
				e.Code = "authentication_failed"
				e.Exit = 3
				e.Remedy = "Run login again or replace SLACKADM_TOKEN; no credential fallback is attempted."
			case "missing_scope", "no_permission", "restricted_action", "not_allowed_token_type", "access_denied":
				e.Code = "permission_denied"
				e.Exit = 4
				e.Remedy = "Check user privileges and reinstall the app with required user scopes."
			case "internal_error", "fatal_error":
				if write {
					return nil, unknown()
				}
			}
			// Only retain machine codes, never arbitrary response bodies or tokens.
			valid := len(code) <= 100
			for _, r := range code {
				if !(r >= 'a' && r <= 'z' || r == '_' || r >= '0' && r <= '9') {
					valid = false
				}
			}
			if valid {
				e.SlackCode = code
			}
			return nil, e
		}
		return data, nil
	}
}
func unknown() *contract.Error {
	return contract.New("result_unknown", "The write may have succeeded; it was not retried.", "Inspect the current Slack state before retrying.", 5)
}

func (c *Client) Pages(ctx context.Context, method, key string, args url.Values, limit int, cursor string) ([]any, string, error) {
	rows := []any{}
	seen := map[string]bool{}
	for {
		if seen[cursor] {
			return nil, "", contract.New("invalid_cursor", "Slack repeated a pagination cursor.", "Retry the listing.", 5)
		}
		seen[cursor] = true
		size := 200
		if limit > 0 && limit-len(rows) < size {
			size = limit - len(rows)
		}
		args.Set("limit", strconv.Itoa(size))
		args.Set("cursor", cursor)
		data, err := c.Call(ctx, method, args, false)
		if err != nil {
			return nil, "", err
		}
		page, _ := data[key].([]any)
		rows = append(rows, page...)
		cursor = String(Map(data, "response_metadata"), "next_cursor")
		if cursor == "" || (limit > 0 && len(rows) >= limit) {
			return rows, cursor, nil
		}
	}
}
