package service

import (
	"context"
	"github.com/kurotch321/slackadm/internal/contract"
	"github.com/kurotch321/slackadm/internal/slackapi"
	"net/url"
	"regexp"
	"strings"
)

type Service struct{ API *slackapi.Client }
type Options struct {
	Limit                                                    int
	Cursor, Name, Email, Visibility, Archived, Active, Admin string
}

const Visibility = "Only information visible to the authenticated user is returned; inaccessible private channels and hidden emails are not inferred."

var channelID = regexp.MustCompile(`^[CG][A-Z0-9]+$`)
var userID = regexp.MustCompile(`^[UW][A-Z0-9]+$`)

func (s *Service) Identity(ctx context.Context) (slackapi.Object, error) {
	d, e := s.API.Call(ctx, "auth.test", nil, false)
	if e != nil {
		return nil, e
	}
	if slackapi.String(d, "bot_id") != "" || slackapi.String(d, "user_id") == "" || slackapi.String(d, "team_id") == "" {
		return nil, contract.New("user_token_required", "A workspace user token is required.", "Use login or provide a User OAuth Token.", 3)
	}
	return d, nil
}
func (s *Service) Resolve(ctx context.Context, kind, value string) (string, error) {
	if kind == "channel" && channelID.MatchString(value) || kind == "user" && userID.MatchString(value) {
		return value, nil
	}
	if kind == "user" {
		if !strings.Contains(value, "@") {
			return "", contract.Input("Use a user ID or exact email, not a display name.")
		}
		d, e := s.API.Call(ctx, "users.lookupByEmail", url.Values{"email": {value}}, false)
		if e != nil {
			return "", e
		}
		id := slackapi.String(slackapi.Map(d, "user"), "id")
		if !userID.MatchString(id) {
			return "", contract.Input("No exact user match.")
		}
		return id, nil
	}
	rows, _, e := s.API.Pages(ctx, "conversations.list", "channels", url.Values{"types": {"public_channel,private_channel"}, "exclude_archived": {"false"}}, 0, "")
	if e != nil {
		return "", e
	}
	name := strings.TrimPrefix(value, "#")
	var ids []string
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if slackapi.String(m, "name") == name {
			ids = append(ids, slackapi.String(m, "id"))
		}
	}
	if len(ids) != 1 {
		return "", contract.Input("Channel name must match exactly one visible channel; use its ID.")
	}
	return ids[0], nil
}
func (s *Service) ResolveOperation(ctx context.Context, op Operation) (Operation, error) {
	if _, e := Validate(op, false); e != nil {
		return op, e
	}
	resolved := Operation{Command: op.Command, Args: map[string]string{}}
	for k, v := range op.Args {
		if k == "channel" || k == "user" {
			id, e := s.Resolve(ctx, k, v)
			if e != nil {
				return op, e
			}
			v = id
		}
		resolved.Args[k] = v
	}
	return resolved, nil
}
func (s *Service) Run(ctx context.Context, op Operation, opts Options) (any, map[string]any, error) {
	d, e := Validate(op, false)
	if e != nil {
		return nil, nil, e
	}
	op, e = s.ResolveOperation(ctx, op)
	if e != nil {
		return nil, nil, e
	}
	args := url.Values{}
	for k, v := range op.Args {
		if k != "query" {
			args.Set(k, v)
		}
	}
	if op.Command == "channels members add" {
		args.Set("users", args.Get("user"))
		args.Del("user")
	}
	meta := map[string]any{"visibility": Visibility}
	if d.Method == "users.conversations" {
		meta["visibility"] = "Slack users.conversations only returns channels shared by the authenticated user and the requested user; this is not a complete workspace membership inventory."
	}
	if d.Method == "conversations.list" || d.Method == "users.conversations" {
		args.Set("types", "public_channel,private_channel")
		args.Set("exclude_archived", "false")
	}
	if d.Result == "channels" || d.Result == "members" {
		rows, cursor, e := s.API.Pages(ctx, d.Method, d.Result, args, opts.Limit, opts.Cursor)
		if e != nil {
			return nil, meta, e
		}
		meta["next_cursor"] = cursor
		meta["truncated"] = cursor != ""
		filtered := []any{}
		for _, row := range rows {
			m, ok := row.(map[string]any)
			if !ok || matches(m, opts, op.Args["query"]) {
				filtered = append(filtered, row)
			}
		}
		meta["scanned"] = len(rows)
		return filtered, meta, nil
	}
	result, e := s.API.Call(ctx, d.Method, args, d.Write)
	return result, meta, e
}
func matches(m slackapi.Object, o Options, query string) bool {
	name := strings.ToLower(slackapi.String(m, "name") + " " + slackapi.String(m, "real_name") + " " + slackapi.String(slackapi.Map(m, "profile"), "display_name"))
	email := strings.ToLower(slackapi.String(slackapi.Map(m, "profile"), "email"))
	if !strings.Contains(name, strings.ToLower(o.Name)) || !strings.Contains(email, strings.ToLower(o.Email)) || !strings.Contains(name+" "+email, strings.ToLower(query)) {
		return false
	}
	for _, p := range []struct {
		value  string
		actual bool
	}{{o.Archived, slackapi.Bool(m, "is_archived")}, {o.Active, !slackapi.Bool(m, "deleted")}, {o.Admin, slackapi.Bool(m, "is_admin")}} {
		if p.value != "all" && p.value != "" && (p.value == "true") != p.actual {
			return false
		}
	}
	return o.Visibility == "" || o.Visibility == "all" || (o.Visibility == "private") == slackapi.Bool(m, "is_private")
}
