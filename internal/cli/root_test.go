package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/kurotch321/slackadm/internal/auth"
	"github.com/kurotch321/slackadm/internal/slackapi"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIContract(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth.test":
			fmt.Fprint(w, `{"ok":true,"team_id":"TFAKE","user_id":"UFAKE"}`)
		case "/conversations.info":
			fmt.Fprint(w, `{"ok":true,"channel":{"id":"CFAKE","is_archived":false}}`)
		case "/conversations.archive":
			writes++
			fmt.Fprint(w, `{"ok":true}`)
		default:
			t.Errorf("unexpected: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	for _, tt := range []struct {
		args         []string
		code, writes int
	}{{[]string{"channels", "archive", "CFAKE"}, 7, 0}, {[]string{"channels", "archive", "CFAKE", "--dry-run"}, 0, 0}, {[]string{"channels", "archive", "CFAKE", "--yes"}, 0, 1}, {[]string{"channels", "archive"}, 2, 1}, {[]string{"--output", "bad", "schema"}, 2, 1}, {[]string{"schema"}, 0, 1}} {
		var out, stderr bytes.Buffer
		m := auth.New()
		m.Env = func(string) (string, bool) { return "fake-sensitive-token", true }
		a := &App{In: strings.NewReader("yes\n"), Out: &out, Err: &stderr, Auth: m, Client: func(token string) *slackapi.Client { c := slackapi.New(token); c.BaseURL = server.URL + "/"; return c }}
		code := a.Execute(context.Background(), tt.args, "test")
		if code != tt.code || writes != tt.writes {
			t.Fatalf("%v: code %d writes %d: %s", tt.args, code, writes, out.String())
		}
		var env map[string]any
		if json.Unmarshal(out.Bytes(), &env) != nil || env["schema_version"] != "1" {
			t.Fatalf("invalid envelope %s", out.String())
		}
		if strings.Contains(out.String()+stderr.String(), "fake-sensitive-token") {
			t.Fatal("secret leaked")
		}
	}
}
func TestE2EBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("binary build")
	}
	bin := filepath.Join(t.TempDir(), "slackadm")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "../../cmd/slackadm")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, out)
	}
	for _, tc := range []struct {
		args []string
		code int
	}{{[]string{"schema"}, 0}, {[]string{"channels", "info"}, 2}, {[]string{"auth", "status"}, 3}, {[]string{"completion", "bash"}, 0}} {
		cmd := exec.Command(bin, tc.args...)
		cmd.Env = append(os.Environ(), "SLACKADM_TOKEN=")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			code = err.(*exec.ExitError).ExitCode()
		}
		if code != tc.code {
			t.Fatalf("%v: %d %s", tc.args, code, stderr.String())
		}
		if tc.args[0] != "completion" {
			var e map[string]any
			if json.Unmarshal(stdout.Bytes(), &e) != nil {
				t.Fatal(stdout.String())
			}
		}
	}
}
