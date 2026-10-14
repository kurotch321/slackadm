package service

import (
	"github.com/kurotch321/slackadm/internal/contract"
	"strings"
)

type Definition struct {
	Command string   `json:"command"`
	Method  string   `json:"method"`
	Args    []string `json:"arguments"`
	Scopes  []string `json:"scopes"`
	Write   bool     `json:"write"`
	Confirm bool     `json:"confirmation_required"`
	Summary string   `json:"description"`
	Result  string   `json:"result"`
}

var Definitions = []Definition{
	{"channels list", "conversations.list", nil, []string{"channels:read", "groups:read"}, false, false, "List visible public and private channels", "channels"},
	{"channels info", "conversations.info", []string{"channel"}, []string{"channels:read", "groups:read"}, false, false, "Get a channel by ID or exact name", "channel"},
	{"channels members list", "conversations.members", []string{"channel"}, []string{"channels:read", "groups:read"}, false, false, "List channel member IDs", "members"},
	{"channels create", "conversations.create", []string{"name"}, []string{"channels:write", "groups:write"}, true, false, "Create a channel (--private for private)", "channel"},
	{"channels rename", "conversations.rename", []string{"channel", "name"}, []string{"channels:write", "groups:write"}, true, false, "Rename a channel", "channel"},
	{"channels topic", "conversations.setTopic", []string{"channel", "topic"}, []string{"channels:write", "groups:write"}, true, false, "Set a channel topic", "topic"},
	{"channels purpose", "conversations.setPurpose", []string{"channel", "purpose"}, []string{"channels:write", "groups:write"}, true, false, "Set a channel purpose", "purpose"},
	{"channels archive", "conversations.archive", []string{"channel"}, []string{"channels:write", "groups:write"}, true, true, "Archive a channel", "ok"},
	{"channels unarchive", "conversations.unarchive", []string{"channel"}, []string{"channels:write", "groups:write"}, true, false, "Unarchive a channel", "ok"},
	{"channels join", "conversations.join", []string{"channel"}, []string{"channels:write"}, true, false, "Join a public channel as the authenticated user", "channel"},
	{"channels leave", "conversations.leave", []string{"channel"}, []string{"channels:write", "groups:write"}, true, true, "Leave a channel as the authenticated user", "ok"},
	{"channels members add", "conversations.invite", []string{"channel", "user"}, []string{"channels:write", "groups:write"}, true, false, "Add an existing user to a channel", "channel"},
	{"channels members remove", "conversations.kick", []string{"channel", "user"}, []string{"channels:write", "groups:write"}, true, true, "Remove a channel member", "ok"},
	{"users list", "users.list", nil, []string{"users:read"}, false, false, "List workspace users", "members"},
	{"users search", "users.list", []string{"query"}, []string{"users:read", "users:read.email"}, false, false, "Search visible names and email addresses", "members"},
	{"users info", "users.info", []string{"user"}, []string{"users:read", "users:read.email"}, false, false, "Get a user by ID or exact email", "user"},
	{"users channels", "users.conversations", []string{"user"}, []string{"channels:read", "groups:read"}, false, false, "List visible channels for a user", "channels"},
}

func Lookup(command string) (Definition, bool) {
	for _, d := range Definitions {
		if d.Command == command {
			return d, true
		}
	}
	return Definition{}, false
}

type Operation struct {
	Command string            `json:"command"`
	Args    map[string]string `json:"args"`
}

func Validate(op Operation, writeOnly bool) (Definition, error) {
	d, ok := Lookup(op.Command)
	if !ok || (writeOnly && !d.Write) {
		return d, contract.Input("Unsupported operation.")
	}
	allowed := map[string]bool{}
	for _, k := range d.Args {
		allowed[k] = true
		if strings.TrimSpace(op.Args[k]) == "" && k != "topic" && k != "purpose" {
			return d, contract.Input("Missing argument: " + k)
		}
		if _, ok := op.Args[k]; !ok {
			return d, contract.Input("Missing argument: " + k)
		}
	}
	if op.Command == "channels create" {
		allowed["is_private"] = true
		if v := op.Args["is_private"]; v != "" && v != "true" && v != "false" {
			return d, contract.Input("is_private must be true or false.")
		}
	}
	for k := range op.Args {
		if !allowed[k] {
			return d, contract.Input("Unknown operation argument.")
		}
	}
	return d, nil
}
