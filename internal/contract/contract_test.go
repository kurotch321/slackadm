package contract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopeAndTable(t *testing.T) {
	for _, format := range []string{"json", "table"} {
		var out bytes.Buffer
		data := []any{map[string]any{"id": "CFAKE", "name": "example\x1b[31m\nline"}}
		if e := Write(&out, format, data, nil, map[string]any{"truncated": true, "next_cursor": "next"}); e != nil {
			t.Fatal(e)
		}
		if strings.ContainsRune(out.String(), '\x1b') {
			t.Fatal("terminal control injected")
		}
		if !strings.Contains(out.String(), "next_cursor") {
			t.Fatal("missing pagination metadata")
		}
		if format == "json" {
			var e Envelope
			if json.Unmarshal(out.Bytes(), &e) != nil || !e.OK || e.SchemaVersion != "1" {
				t.Fatal(out.String())
			}
		}
	}
	var out bytes.Buffer
	e := New("permission_denied", "Denied", "Reinstall.", 4)
	if Write(&out, "json", nil, e, map[string]any{}) != nil {
		t.Fatal("write")
	}
	var env Envelope
	if json.Unmarshal(out.Bytes(), &env) != nil || env.OK || env.Error.Code != "permission_denied" {
		t.Fatal(out.String())
	}
}
