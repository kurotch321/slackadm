package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gofrs/flock"
	"github.com/kurotch321/slackadm/internal/contract"
	"github.com/kurotch321/slackadm/internal/slackapi"
	"github.com/slack-go/slack"
	"github.com/zalando/go-keyring"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const Scopes = "channels:read,channels:write,groups:read,groups:write,users:read,users:read.email"

type Credential struct {
	Token    string    `json:"token"`
	Refresh  string    `json:"refresh,omitempty"`
	ClientID string    `json:"client_id"`
	Expires  time.Time `json:"expires"`
}
type Store interface {
	Get(string) (Credential, error)
	Set(string, Credential) error
	Delete(string) error
}
type Keyring struct{}

func (Keyring) Get(p string) (Credential, error) {
	var c Credential
	s, e := keyring.Get("slackadm", p)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal([]byte(s), &c)
	return c, e
}
func (Keyring) Set(p string, c Credential) error {
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	return keyring.Set("slackadm", p, string(b))
}
func (Keyring) Delete(p string) error {
	e := keyring.Delete("slackadm", p)
	if errors.Is(e, keyring.ErrNotFound) {
		return nil
	}
	return e
}

type Manager struct {
	Store   Store
	API     *slackapi.Client
	LockDir string
	Env     func(string) (string, bool)
}

func New() *Manager { return &Manager{Store: Keyring{}, API: slackapi.New(""), Env: os.LookupEnv} }
func ValidateProfile(p string) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(p) {
		return contract.Input("Profile must contain 1-64 letters, digits, underscores or hyphens.")
	}
	return nil
}
func unavailable() error {
	return contract.New("credential_store_unavailable", "OS credential storage is unavailable or no login exists.", "Run login with an unlocked OS keyring, or set SLACKADM_TOKEN to a user token.", 3)
}
func (m *Manager) locked(ctx context.Context, p string, fn func() error) error {
	if e := ValidateProfile(p); e != nil {
		return e
	}
	dir := m.LockDir
	if dir == "" {
		d, e := os.UserConfigDir()
		if e != nil {
			return unavailable()
		}
		dir = filepath.Join(d, "slackadm", "locks")
	}
	if os.MkdirAll(dir, 0700) != nil {
		return unavailable()
	}
	lock := flock.New(filepath.Join(dir, p+".lock"))
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ok, e := lock.TryLockContext(wait, 50*time.Millisecond)
	if e != nil || !ok {
		return contract.New("credential_busy", "Credential lock is busy.", "Retry when the other process finishes.", 3)
	}
	defer lock.Unlock()
	return fn()
}
func (m *Manager) Token(ctx context.Context, p string) (string, string, error) {
	if e := ValidateProfile(p); e != nil {
		return "", "", e
	}
	if v, ok := m.Env("SLACKADM_TOKEN"); ok {
		if strings.TrimSpace(v) == "" {
			return "", "environment", contract.New("empty_token", "SLACKADM_TOKEN is empty.", "Set a user token or unset the variable.", 3)
		}
		return v, "environment", nil
	}
	var token string
	err := m.locked(ctx, p, func() error {
		c, e := m.Store.Get(p)
		if e != nil || c.Token == "" {
			return unavailable()
		}
		if !c.Expires.IsZero() && time.Until(c.Expires) < 2*time.Minute {
			if c.Refresh == "" {
				return contract.New("token_expired", "Saved token expired.", "Run login again.", 3)
			}
			data, e := m.API.Call(ctx, "oauth.v2.access", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.Refresh}, "client_id": {c.ClientID}}, true)
			if e != nil {
				return e
			}
			next, e := credential(data, c.ClientID)
			if e != nil {
				return e
			}
			if m.Store.Set(p, next) != nil {
				return unavailable()
			}
			c = next
		}
		token = c.Token
		return nil
	})
	return token, "keyring", err
}
func credential(data slackapi.Object, client string) (Credential, error) {
	b, _ := json.Marshal(data)
	var r slack.OAuthV2Response
	if json.Unmarshal(b, &r) != nil {
		return Credential{}, unavailable()
	}
	token, refresh, expires := r.AuthedUser.AccessToken, r.AuthedUser.RefreshToken, r.AuthedUser.ExpiresIn
	if token == "" && r.TokenType == "user" {
		token, refresh, expires = r.AccessToken, r.RefreshToken, r.ExpiresIn
	}
	if token == "" {
		return Credential{}, contract.New("user_token_required", "OAuth did not return a user token.", "Configure only user scopes and enable PKCE.", 3)
	}
	c := Credential{Token: token, Refresh: refresh, ClientID: client}
	if expires > 0 {
		c.Expires = time.Now().Add(time.Duration(expires) * time.Second)
	}
	return c, nil
}
func (m *Manager) Logout(ctx context.Context, p string) error {
	return m.locked(ctx, p, func() error {
		if m.Store.Delete(p) != nil {
			return unavailable()
		}
		return nil
	})
}
func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

type Callback struct {
	State  string
	Result chan string
	once   sync.Once
}

func (c *Callback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.URL.Path != "/callback" || r.Method != "GET" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(c.State)) != 1 {
		http.Error(w, "Invalid OAuth state", 400)
		return
	}
	used := false
	c.once.Do(func() {
		used = true
		code := r.URL.Query().Get("code")
		if r.URL.Query().Get("error") != "" {
			code = ""
		}
		c.Result <- code
	})
	if !used {
		http.Error(w, "Callback already used", 409)
		return
	}
	fmt.Fprintln(w, "Authentication response received. You may close this window.")
}
func (m *Manager) Login(ctx context.Context, p, client string, port int, out io.Writer, open func(string) error) error {
	if client == "" || port < 1 || port > 65535 {
		return contract.Input("--client-id and a valid --port are required.")
	}
	return m.locked(ctx, p, func() error {
		// Keep existing credentials untouched if login fails.
		listener, e := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if e != nil {
			return contract.New("callback_unavailable", "Cannot bind the loopback callback port.", "Free the port or select --port and update the app redirect URL.", 3)
		}
		defer listener.Close()
		cb := &Callback{State: random(), Result: make(chan string, 1)}
		server := &http.Server{Handler: cb, ReadHeaderTimeout: 5 * time.Second}
		defer server.Close()
		go func() { _ = server.Serve(listener) }()
		verifier := random()
		hash := sha256.Sum256([]byte(verifier))
		redirect := fmt.Sprintf("http://localhost:%d/callback", port)
		q := url.Values{"client_id": {client}, "user_scope": {Scopes}, "redirect_uri": {redirect}, "state": {cb.State}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}}
		target := "https://slack.com/oauth/v2/authorize?" + q.Encode()
		fmt.Fprintln(out, "Open this URL to authorize:\n"+target)
		_ = open(target)
		select {
		case <-ctx.Done():
			return contract.New("oauth_timeout", "OAuth was cancelled or timed out.", "Run login again.", 3)
		case code := <-cb.Result:
			if code == "" {
				return contract.New("oauth_denied", "OAuth authorization was denied or missing a code.", "Run login again and approve user scopes.", 3)
			}
			data, e := m.API.Call(ctx, "oauth.v2.access", url.Values{"client_id": {client}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}}, true)
			if e != nil {
				return e
			}
			c, e := credential(data, client)
			if e != nil {
				return e
			}
			if m.Store.Set(p, c) != nil {
				return unavailable()
			}
			return nil
		}
	})
}
func OpenBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if e := cmd.Start(); e != nil {
		return e
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
