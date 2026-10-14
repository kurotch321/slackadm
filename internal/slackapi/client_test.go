package slackapi

import (
	"context"
	"fmt"
	"github.com/kurotch321/slackadm/internal/contract"
	"github.com/slack-go/slack/slacktest"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func clientFor(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	c := New("fake-secret")
	c.BaseURL = server.URL + "/"
	return c
}
func TestSlacktestIntegration(t *testing.T) {
	server := slacktest.NewTestServer(func(c slacktest.Customize) {
		c.Handle("/auth.test", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer fake-secret" {
				t.Error("missing auth")
			}
			fmt.Fprint(w, `{"ok":true,"team_id":"TEXAMPLE","user_id":"UEXAMPLE"}`)
		})
	})
	server.Start()
	defer server.Stop()
	c := New("fake-secret")
	c.BaseURL = server.GetAPIURL()
	d, e := c.Call(context.Background(), "auth.test", nil, false)
	if e != nil || String(d, "team_id") != "TEXAMPLE" {
		t.Fatalf("%v %v", d, e)
	}
}
func TestPaginationAndLimit(t *testing.T) {
	for _, limit := range []int{0, 1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			calls := 0
			c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				_ = r.ParseForm()
				if r.Form.Get("cursor") == "" {
					fmt.Fprint(w, `{"ok":true,"channels":[{"id":"C1"}],"response_metadata":{"next_cursor":"next"}}`)
				} else {
					fmt.Fprint(w, `{"ok":true,"channels":[{"id":"C2"}],"response_metadata":{"next_cursor":""}}`)
				}
			})
			rows, cursor, e := c.Pages(context.Background(), "conversations.list", "channels", url.Values{}, limit, "")
			if e != nil {
				t.Fatal(e)
			}
			if limit == 0 && (len(rows) != 2 || cursor != "" || calls != 2) {
				t.Fatal(rows, cursor, calls)
			}
			if limit == 1 && (len(rows) != 1 || cursor != "next" || calls != 1) {
				t.Fatal(rows, cursor, calls)
			}
		})
	}
}
func TestRateLimit(t *testing.T) {
	calls := 0
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	})
	if _, e := c.Call(context.Background(), "conversations.archive", nil, true); e != nil || calls != 3 {
		t.Fatal(e, calls)
	}
	c.MaxRetries = 1
	calls = 0
	if _, e := c.Call(context.Background(), "auth.test", nil, false); contract.As(e).Code != "rate_limited" || calls != 2 {
		t.Fatal(e, calls)
	}
}
func TestWriteUnknownNeverRetried(t *testing.T) {
	var calls atomic.Int32
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	})
	_, e := c.Call(context.Background(), "conversations.archive", nil, true)
	if contract.As(e).Code != "result_unknown" || calls.Load() != 1 {
		t.Fatal(e, calls.Load())
	}
}
func TestErrorsRedactSecrets(t *testing.T) {
	for _, code := range []string{"invalid_auth", "missing_scope", "fake-secret!"} {
		c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"ok":false,"error":%q,"needed":"fake-secret"}`, code)
		})
		_, e := c.Call(context.Background(), "auth.test", nil, false)
		ce := contract.As(e)
		if strings.Contains(fmt.Sprintf("%+v", ce), "fake-secret") {
			t.Fatal("secret leaked")
		}
		if code == "invalid_auth" && ce.Exit != 3 || code == "missing_scope" && ce.Exit != 4 {
			t.Fatal(ce)
		}
	}
}
func TestMalformedWriteResponse(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "broken") })
	_, e := c.Call(context.Background(), "conversations.create", nil, true)
	if contract.As(e).Code != "result_unknown" {
		t.Fatal(e)
	}
}
