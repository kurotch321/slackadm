package service

import (
	"context"
	"fmt"
	"github.com/kurotch321/slackadm/internal/slackapi"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEveryOperationMapping(t *testing.T) {
	for _, d := range Definitions {
		t.Run(d.Command, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/"+d.Method {
					t.Error(r.URL.Path)
				}
				_ = r.ParseForm()
				if d.Write && r.Method != "POST" || !d.Write && r.Method != "GET" {
					t.Error("wrong HTTP method")
				}
				if d.Command == "channels members add" && (r.Form.Get("users") != "UFAKE" || r.Form.Get("user") != "") {
					t.Error("wrong invite args")
				}
				fmt.Fprint(w, `{"ok":true,"channel":{"id":"CFAKE"},"user":{"id":"UFAKE"},"members":[],"channels":[]}`)
			}))
			defer server.Close()
			c := slackapi.New("fake")
			c.BaseURL = server.URL + "/"
			s := Service{API: c}
			op := Operation{Command: d.Command, Args: map[string]string{}}
			for _, a := range d.Args {
				op.Args[a] = "example"
				if a == "channel" {
					op.Args[a] = "CFAKE"
				}
				if a == "user" {
					op.Args[a] = "UFAKE"
				}
			}
			_, _, e := s.Run(context.Background(), op, Options{})
			if e != nil || calls != 1 {
				t.Fatal(e, calls)
			}
		})
	}
}
func TestNameResolutionAndFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"channels":[{"id":"C1","name":"example"},{"id":"C2","name":"example-other"}]}`)
	}))
	defer server.Close()
	c := slackapi.New("fake")
	c.BaseURL = server.URL + "/"
	s := Service{API: c}
	id, e := s.Resolve(context.Background(), "channel", "#example")
	if e != nil || id != "C1" {
		t.Fatal(id, e)
	}
	if _, e = s.Resolve(context.Background(), "channel", "exam"); e == nil {
		t.Fatal("substring resolved")
	}
	m := slackapi.Object{"name": "example", "is_private": true, "deleted": false, "is_admin": true, "profile": map[string]any{"email": "person@example.invalid"}}
	if !matches(m, Options{Name: "EXAMPLE", Email: "person", Active: "true", Admin: "true", Visibility: "private"}, "person") {
		t.Fatal("filter mismatch")
	}
	if matches(m, Options{Visibility: "public"}, "") || matches(m, Options{Admin: "false"}, "") {
		t.Fatal("filter ignored")
	}
}
func TestRegistryScopesMatchManifest(t *testing.T) {
	for _, d := range Definitions {
		if d.Command == "" || d.Result == "" || !strings.Contains(d.Method, ".") {
			t.Fatal(d)
		}
		if d.Write && len(d.Scopes) == 0 {
			t.Fatal("missing scopes")
		}
	}
}
