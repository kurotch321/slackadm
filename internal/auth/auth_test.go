package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"github.com/kurotch321/slackadm/internal/contract"
	"github.com/kurotch321/slackadm/internal/slackapi"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryStore struct {
	mu    sync.Mutex
	c     Credential
	reads int
}

func (s *memoryStore) Get(string) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return s.c, nil
}
func (s *memoryStore) Set(_ string, c Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c = c
	return nil
}
func (s *memoryStore) Delete(string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c = Credential{}
	return nil
}
func manager(t *testing.T) *Manager {
	return &Manager{Store: &memoryStore{}, API: slackapi.New(""), LockDir: t.TempDir(), Env: func(string) (string, bool) { return "", false }}
}
func TestEnvironmentPriority(t *testing.T) {
	m := manager(t)
	m.Env = func(string) (string, bool) { return "invalid-fake-token", true }
	token, source, e := m.Token(context.Background(), "default")
	if e != nil || source != "environment" || token != "invalid-fake-token" || m.Store.(*memoryStore).reads != 0 {
		t.Fatal(token, source, e)
	}
	m.Env = func(string) (string, bool) { return "", true }
	_, _, e = m.Token(context.Background(), "default")
	if contract.As(e).Code != "empty_token" {
		t.Fatal(e)
	}
}
func TestRefreshConcurrency(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "fake-refresh" || r.Form.Get("client_secret") != "" {
			t.Error("invalid refresh")
		}
		fmt.Fprint(w, `{"ok":true,"token_type":"user","access_token":"fake-new","refresh_token":"fake-next","expires_in":3600}`)
	}))
	defer server.Close()
	store := &memoryStore{c: Credential{Token: "fake-old", Refresh: "fake-refresh", ClientID: "123", Expires: time.Now().Add(-time.Minute)}}
	dir := t.TempDir()
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			m := manager(t)
			m.Store = store
			m.LockDir = dir
			m.API.BaseURL = server.URL + "/"
			token, _, e := m.Token(context.Background(), "default")
			if e != nil || token != "fake-new" {
				t.Error(e, token)
			}
		})
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal(count.Load())
	}
}
func TestCallbackStateAndOneShot(t *testing.T) {
	cb := &Callback{State: "expected", Result: make(chan string, 1)}
	w := httptest.NewRecorder()
	cb.ServeHTTP(w, httptest.NewRequest("GET", "/callback?state=wrong&code=bad", nil))
	if w.Code != 400 || len(cb.Result) != 0 {
		t.Fatal(w.Code)
	}
	for i := 0; i < 2; i++ {
		w = httptest.NewRecorder()
		cb.ServeHTTP(w, httptest.NewRequest("GET", "/callback?state=expected&code=good", nil))
		if i == 0 && w.Code != 200 || i == 1 && w.Code != 409 {
			t.Fatal(w.Code)
		}
	}
	if <-cb.Result != "good" {
		t.Fatal("wrong code")
	}
}
func TestLoginPKCE(t *testing.T) {
	for _, mode := range []string{"success", "denied", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			m := manager(t)
			var challenge string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge || r.Form.Get("client_secret") != "" {
					t.Error("PKCE mismatch")
				}
				fmt.Fprint(w, `{"ok":true,"authed_user":{"id":"UFAKE","access_token":"fake-access","token_type":"user"}}`)
			}))
			defer server.Close()
			m.API.BaseURL = server.URL + "/"
			l, e := net.Listen("tcp4", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			port := l.Addr().(*net.TCPAddr).Port
			l.Close()
			timeout := 2 * time.Second
			if mode == "timeout" {
				timeout = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			e = m.Login(ctx, "default", "123", port, io.Discard, func(target string) error {
				u, _ := url.Parse(target)
				q := u.Query()
				challenge = q.Get("code_challenge")
				if q.Get("scope") != "" || q.Get("code_challenge_method") != "S256" {
					t.Error("wrong authorization")
				}
				if mode == "timeout" {
					return nil
				}
				callback := "http://127.0.0.1:" + strconv.Itoa(port) + "/callback?state=" + q.Get("state")
				if mode == "denied" {
					callback += "&error=access_denied"
				} else {
					callback += "&code=fake-code"
				}
				res, err := http.Get(callback)
				if err != nil {
					return err
				}
				res.Body.Close()
				return nil
			})
			if mode == "success" {
				if e != nil {
					t.Fatal(e)
				}
				c, _ := m.Store.Get("default")
				if c.Token != "fake-access" {
					t.Fatal("not saved")
				}
			} else if e == nil {
				t.Fatal("expected OAuth failure")
			}
		})
	}
}
