package service

import (
	"context"
	"fmt"
	"github.com/kurotch321/slackadm/internal/contract"
	"github.com/kurotch321/slackadm/internal/slackapi"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBatchGuardsAndPartialFailure(t *testing.T) {
	for _, mode := range []string{"success", "mismatch", "stale", "stop", "continue", "dry"} {
		t.Run(mode, func(t *testing.T) {
			archived := false
			writes := 0
			team := "TFAKE"
			name := "example"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/auth.test":
					fmt.Fprintf(w, `{"ok":true,"team_id":%q,"user_id":"UFAKE"}`, team)
				case "/conversations.info":
					fmt.Fprintf(w, `{"ok":true,"channel":{"id":"CFAKE","name":%q,"is_private":false,"is_archived":%t}}`, name, archived)
				case "/conversations.archive":
					writes++
					if mode == "stop" || mode == "continue" {
						fmt.Fprint(w, `{"ok":false,"error":"no_permission"}`)
					} else {
						archived = true
						fmt.Fprint(w, `{"ok":true}`)
					}
				case "/conversations.unarchive":
					writes++
					archived = false
					fmt.Fprint(w, `{"ok":true}`)
				default:
					t.Errorf("unexpected %s", r.URL.Path)
				}
			}))
			defer server.Close()
			c := slackapi.New("fake")
			c.BaseURL = server.URL + "/"
			s := &Service{API: c}
			ctx := context.Background()
			p, e := s.MakePlan(ctx, []Operation{{"channels archive", map[string]string{"channel": "CFAKE"}}, {"channels unarchive", map[string]string{"channel": "CFAKE"}}})
			if e != nil {
				t.Fatal(e)
			}
			if mode == "mismatch" {
				team = "TOTHER"
			}
			if mode == "stale" {
				archived = true
			}
			result, e := s.Apply(ctx, p, mode == "continue", mode == "dry")
			switch mode {
			case "success":
				if e != nil || writes != 2 || archived {
					t.Fatal(e, writes)
				}
			case "mismatch", "stale":
				if e == nil || writes != 0 {
					t.Fatal(e, writes)
				}
			case "stop":
				if contract.As(e).Exit != 6 || writes != 1 || result[1].Status != "skipped" {
					t.Fatal(e, writes, result)
				}
			case "continue":
				if contract.As(e).Exit != 6 || writes != 2 || result[1].Status != "applied" {
					t.Fatal(e, writes, result)
				}
			case "dry":
				if e != nil || writes != 0 || result[0].Status != "dry_run" {
					t.Fatal(e, writes, result)
				}
			}
		})
	}
}
func TestMembershipProjection(t *testing.T) {
	b := map[string]any{"user_id": "U1", "user_is_member": false}
	op := Operation{"channels members add", map[string]string{"user": "U1"}}
	if project(b, op)["user_is_member"] != true {
		t.Fatal("not projected")
	}
	op.Args["user"] = "U2"
	if project(b, op)["user_is_member"] != false {
		t.Fatal("other user changed")
	}
}
func TestStrictOperationValidation(t *testing.T) {
	for _, op := range []Operation{{"users list", nil}, {"channels archive", map[string]string{"channel": "C1", "token": "fake"}}, {"channels create", map[string]string{"name": "example", "is_private": "maybe"}}} {
		if _, e := Validate(op, true); e == nil {
			t.Fatal(op)
		}
	}
}
func TestExactResolution(t *testing.T) {
	s := &Service{}
	if _, e := s.Resolve(context.Background(), "user", "Display Name"); e == nil {
		t.Fatal("accepted ambiguous display name")
	}
}
