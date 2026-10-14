package service

import (
	"context"
	"encoding/json"
	"github.com/kurotch321/slackadm/internal/contract"
	"github.com/kurotch321/slackadm/internal/slackapi"
	"net/url"
	"reflect"
	"time"
)

type Item struct {
	Operation Operation      `json:"operation"`
	Before    map[string]any `json:"before"`
}
type Plan struct {
	SchemaVersion string    `json:"schema_version"`
	TeamID        string    `json:"team_id"`
	UserID        string    `json:"user_id"`
	CreatedAt     time.Time `json:"created_at"`
	Items         []Item    `json:"items"`
}
type ItemResult struct {
	Index  int             `json:"index"`
	Status string          `json:"status"`
	Data   any             `json:"data,omitempty"`
	Error  *contract.Error `json:"error,omitempty"`
}

func (s *Service) Snapshot(ctx context.Context, op Operation) (map[string]any, error) {
	before := map[string]any{}
	if op.Command == "channels create" {
		rows, _, e := s.API.Pages(ctx, "conversations.list", "channels", url.Values{"types": {"public_channel,private_channel"}, "exclude_archived": {"false"}}, 0, "")
		if e != nil {
			return nil, e
		}
		exists := false
		for _, r := range rows {
			m, _ := r.(map[string]any)
			if slackapi.String(m, "name") == op.Args["name"] {
				exists = true
			}
		}
		before["visible_name_exists"] = exists
		return before, nil
	}
	data, e := s.API.Call(ctx, "conversations.info", url.Values{"channel": {op.Args["channel"]}}, false)
	if e != nil {
		return nil, e
	}
	ch := slackapi.Map(data, "channel")
	if ch == nil {
		return nil, contract.Input("Channel state unavailable.")
	}
	before["id"] = slackapi.String(ch, "id")
	before["is_private"] = slackapi.Bool(ch, "is_private")
	before["is_archived"] = slackapi.Bool(ch, "is_archived")
	switch op.Command {
	case "channels rename":
		before["name"] = slackapi.String(ch, "name")
	case "channels topic":
		before["topic"] = slackapi.String(slackapi.Map(ch, "topic"), "value")
	case "channels purpose":
		before["purpose"] = slackapi.String(slackapi.Map(ch, "purpose"), "value")
	case "channels join", "channels leave":
		before["is_member"] = slackapi.Bool(ch, "is_member")
	case "channels members add", "channels members remove":
		rows, _, e := s.API.Pages(ctx, "conversations.members", "members", url.Values{"channel": {op.Args["channel"]}}, 0, "")
		if e != nil {
			return nil, e
		}
		found := false
		for _, v := range rows {
			if v == op.Args["user"] {
				found = true
			}
		}
		before["user_is_member"] = found
		before["user_id"] = op.Args["user"]
	}
	return before, nil
}
func (s *Service) MakePlan(ctx context.Context, ops []Operation) (*Plan, error) {
	if len(ops) == 0 || len(ops) > 1000 {
		return nil, contract.Input("Batch must contain 1-1000 operations.")
	}
	for _, op := range ops {
		if _, e := Validate(op, true); e != nil {
			return nil, e
		}
	}
	identity, e := s.Identity(ctx)
	if e != nil {
		return nil, e
	}
	p := &Plan{SchemaVersion: "1", TeamID: slackapi.String(identity, "team_id"), UserID: slackapi.String(identity, "user_id"), CreatedAt: time.Now().UTC(), Items: []Item{}}
	for _, op := range ops {
		resolved, e := s.ResolveOperation(ctx, op)
		if e != nil {
			return nil, e
		}
		b, e := s.Snapshot(ctx, resolved)
		if e != nil {
			return nil, e
		}
		p.Items = append(p.Items, Item{resolved, b})
	}
	return p, nil
}
func (p *Plan) Validate() error {
	if p.SchemaVersion != "1" || p.TeamID == "" || p.UserID == "" || len(p.Items) == 0 || len(p.Items) > 1000 {
		return contract.Input("Invalid batch plan header.")
	}
	for _, item := range p.Items {
		if _, e := Validate(item.Operation, true); e != nil {
			return e
		}
		if item.Before == nil {
			return contract.Input("Plan is missing before state.")
		}
		for k, v := range item.Operation.Args {
			if k == "channel" && !channelID.MatchString(v) || k == "user" && !userID.MatchString(v) {
				return contract.Input("Plan targets must be IDs.")
			}
		}
	}
	return nil
}
func (s *Service) Apply(ctx context.Context, p *Plan, keepGoing, dry bool) ([]ItemResult, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	identity, e := s.Identity(ctx)
	if e != nil {
		return nil, e
	}
	if p.TeamID != slackapi.String(identity, "team_id") || p.UserID != slackapi.String(identity, "user_id") {
		return nil, contract.New("plan_identity_mismatch", "Plan workspace or user differs from this authentication.", "Use the matching profile or generate a new plan.", 2)
	}
	// Preflight every item before any write. Later changes are tracked by this process.
	results := make([]ItemResult, len(p.Items))
	for i := range results {
		results[i] = ItemResult{Index: i, Status: "skipped"}
	}
	for _, item := range p.Items {
		b, e := s.Snapshot(ctx, item.Operation)
		if e != nil {
			return results, e
		}
		if !reflect.DeepEqual(b, item.Before) {
			return results, contract.New("plan_stale", "A target changed after planning.", "Review Slack and create a new plan.", 2)
		}
	}
	// Compare each item's relevant state just before sending. Refresh expected state
	// for later operations on the same channel only after our own successful write.
	expected := make([]map[string]any, len(p.Items))
	for i, it := range p.Items {
		expected[i] = it.Before
	}
	failed := false
	for i, item := range p.Items {
		b, err := s.Snapshot(ctx, item.Operation)
		if err == nil && !reflect.DeepEqual(b, expected[i]) {
			err = contract.New("plan_stale", "Target changed before execution.", "Generate a new plan.", 2)
		}
		var data any
		if err == nil && !dry {
			data, _, err = s.Run(ctx, item.Operation, Options{})
		}
		if err != nil {
			failed = true
			results[i] = ItemResult{Index: i, Status: "failed", Error: contract.As(err)}
			if !keepGoing {
				break
			}
			continue
		}
		status := "applied"
		if dry {
			status = "dry_run"
		}
		results[i] = ItemResult{Index: i, Status: status, Data: data}
		if !dry {
			for j := i + 1; j < len(p.Items); j++ {
				if item.Operation.Args["channel"] != "" && item.Operation.Args["channel"] == p.Items[j].Operation.Args["channel"] {
					expected[j] = project(expected[j], item.Operation)
				}
			}
		}
	}
	if failed {
		return results, contract.New("partial_failure", "One or more batch items failed; no rollback was attempted.", "Review item results and Slack state before planning a retry.", 6)
	}
	return results, nil
}

// Update only fields changed by our operation; do not absorb unrelated external changes.
func project(before map[string]any, op Operation) map[string]any {
	b := map[string]any{}
	raw, _ := json.Marshal(before)
	_ = json.Unmarshal(raw, &b)
	key := ""
	var value any
	switch op.Command {
	case "channels archive":
		key = "is_archived"
		value = true
	case "channels unarchive":
		key = "is_archived"
		value = false
	case "channels join":
		key = "is_member"
		value = true
	case "channels leave":
		key = "is_member"
		value = false
	case "channels rename":
		key = "name"
		value = op.Args["name"]
	case "channels topic":
		key = "topic"
		value = op.Args["topic"]
	case "channels purpose":
		key = "purpose"
		value = op.Args["purpose"]
	case "channels members add", "channels members remove":
		if b["user_id"] == op.Args["user"] {
			key = "user_is_member"
			value = op.Command == "channels members add"
		}
	}
	if _, ok := b[key]; ok {
		b[key] = value
	}
	return b
}
