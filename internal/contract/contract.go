package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	SlackCode string `json:"slack_code,omitempty"`
	Remedy    string `json:"remedy"`
	Exit      int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func New(code, message, remedy string, exit int) *Error {
	return &Error{Code: code, Message: message, Remedy: remedy, Exit: exit}
}
func Input(message string) *Error {
	return New("invalid_input", message, "Run slackadm --help or slackadm schema.", 2)
}
func As(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return New("internal_error", "Operation failed.", "Retry after checking local configuration.", 5)
}

type Envelope struct {
	SchemaVersion string         `json:"schema_version"`
	OK            bool           `json:"ok"`
	Data          any            `json:"data"`
	Error         *Error         `json:"error"`
	Meta          map[string]any `json:"meta"`
}

func Write(w io.Writer, format string, data any, err error, meta map[string]any) error {
	e := Envelope{"1", err == nil, data, As(err), meta}
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(e)
	}
	if err != nil {
		_, x := fmt.Fprintf(w, "ERROR\t%s\t%s\n", e.Error.Code, e.Error.Message)
		return x
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	raw, _ := json.Marshal(data)
	var obj any
	_ = json.Unmarshal(raw, &obj)
	switch v := obj.(type) {
	case []any:
		columns := []string{"id", "name", "real_name", "is_private", "is_archived", "deleted", "is_admin", "status", "index"}
		present := []string{}
		for _, key := range columns {
			for _, row := range v {
				if m, ok := row.(map[string]any); ok {
					if _, exists := m[key]; exists {
						present = append(present, key)
						break
					}
				}
			}
		}
		if len(present) == 0 {
			fmt.Fprintln(tw, "VALUE")
			for _, row := range v {
				fmt.Fprintln(tw, cell(row))
			}
		} else {
			fmt.Fprintln(tw, strings.Join(present, "\t"))
			for _, row := range v {
				m, _ := row.(map[string]any)
				cells := []string{}
				for _, key := range present {
					cells = append(cells, cell(m[key]))
				}
				fmt.Fprintln(tw, strings.Join(cells, "\t"))
			}
		}
	case map[string]any:
		fmt.Fprintln(tw, "FIELD\tVALUE")
		keys := []string{}
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(tw, "%s\t%s\n", k, cell(v[k]))
		}
	default:
		fmt.Fprintln(tw, "VALUE")
		fmt.Fprintf(tw, "result\t%s\n", raw)
	}
	if len(meta) > 0 {
		b, _ := json.Marshal(meta)
		fmt.Fprintf(tw, "meta\t%s\n", b)
	}
	return tw.Flush()
}

func cell(v any) string {
	if v == nil {
		return "-"
	}
	b, _ := json.Marshal(v)
	return string(b)
}
