package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/kurotch321/slackadm/internal/auth"
	"github.com/kurotch321/slackadm/internal/contract"
	"github.com/kurotch321/slackadm/internal/service"
	"github.com/kurotch321/slackadm/internal/slackapi"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
	"io"
	"os"
	"strings"
	"time"
)

type App struct {
	In              io.Reader
	Out, Err        io.Writer
	Auth            *auth.Manager
	Client          func(string) *slackapi.Client
	TTY, InputTTY   bool
	profile, format string
	yes, dry        bool
	data            any
	meta            map[string]any
	rendered        bool
}

func Execute(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer, version string) int {
	a := &App{In: in, Out: out, Err: stderr, Auth: auth.New(), Client: slackapi.New}
	if f, ok := out.(*os.File); ok {
		a.TTY = term.IsTerminal(int(f.Fd()))
	}
	if f, ok := in.(*os.File); ok {
		a.InputTTY = term.IsTerminal(int(f.Fd()))
	}
	return a.Execute(ctx, args, version)
}
func (a *App) Execute(ctx context.Context, args []string, version string) int {
	a.meta = map[string]any{}
	root := a.root(version)
	root.SetArgs(args)
	root.SetIn(a.In)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	err := root.ExecuteContext(ctx)
	if err != nil {
		if _, ok := err.(*contract.Error); !ok {
			err = contract.Input("Invalid command or arguments. Run slackadm --help.")
		}
	}
	if !a.rendered && (a.data != nil || err != nil) {
		format := a.format
		if format != "table" && format != "json" {
			format = "json"
		}
		if e := contract.Write(a.Out, format, a.data, err, a.meta); e != nil {
			return 5
		}
	}
	if err != nil {
		e := contract.As(err)
		fmt.Fprintln(a.Err, e.Message+" "+e.Remedy)
		return e.Exit
	}
	return 0
}
func (a *App) connected(ctx context.Context) (*service.Service, error) {
	token, source, e := a.Auth.Token(ctx, a.profile)
	if e != nil {
		return nil, e
	}
	a.meta["credential_source"] = source
	a.meta["profile"] = a.profile
	s := &service.Service{API: a.Client(token)}
	if _, e = s.Identity(ctx); e != nil {
		return nil, e
	}
	return s, nil
}
func (a *App) confirm(label string) error {
	if a.yes || a.dry {
		return nil
	}
	required := contract.New("confirmation_required", "Confirmation is required.", "Review --dry-run, then pass --yes to apply.", 7)
	if !a.InputTTY || !a.TTY {
		return required
	}
	fmt.Fprintf(a.Err, "%s? Type yes: ", label)
	scanner := bufio.NewScanner(a.In)
	if !scanner.Scan() || scanner.Text() != "yes" {
		return required
	}
	return nil
}
func (a *App) root(version string) *cobra.Command {
	a.format = "json"
	if a.TTY {
		a.format = "table"
	}
	root := &cobra.Command{Use: "slackadm", Short: "Manage Slack channels with your own user permissions", Version: version, SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().StringVar(&a.profile, "profile", "default", "Named OS credential profile")
	root.PersistentFlags().StringVar(&a.format, "output", a.format, "Output format: table|json")
	root.PersistentFlags().BoolVar(&a.yes, "yes", false, "Approve changes without prompting")
	root.PersistentFlags().BoolVar(&a.dry, "dry-run", false, "Resolve and inspect without changing Slack or local credentials")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if a.format != "json" && a.format != "table" {
			return contract.Input("--output must be table or json.")
		}
		return auth.ValidateProfile(a.profile)
	}
	groups := map[string]*cobra.Command{"": root}
	for _, d := range service.Definitions {
		parts := strings.Split(d.Command, " ")
		parent := ""
		for _, part := range parts[:len(parts)-1] {
			key := strings.TrimSpace(parent + " " + part)
			if groups[key] == nil {
				c := &cobra.Command{Use: part, Short: "Manage " + key}
				groups[parent].AddCommand(c)
				groups[key] = c
			}
			parent = key
		}
		def := d
		opts := service.Options{}
		private := false
		use := parts[len(parts)-1]
		for _, arg := range d.Args {
			use += " <" + arg + ">"
		}
		c := &cobra.Command{Use: use, Short: d.Summary + " (scopes: " + strings.Join(d.Scopes, ", ") + ")", Args: cobra.ExactArgs(len(d.Args))}
		if d.Command == "channels create" {
			c.Flags().BoolVar(&private, "private", false, "Create a private channel")
		}
		if d.Result == "channels" || d.Result == "members" {
			c.Flags().IntVar(&opts.Limit, "limit", 0, "Maximum records scanned; 0 fetches all pages")
			c.Flags().StringVar(&opts.Cursor, "cursor", "", "Resume from meta.next_cursor")
			if d.Command != "channels members list" {
				c.Flags().StringVar(&opts.Name, "name", "", "Filter name substring")
				if strings.HasPrefix(d.Command, "users") && d.Command != "users channels" {
					c.Flags().StringVar(&opts.Email, "email", "", "Filter visible email substring")
					c.Flags().StringVar(&opts.Active, "active", "all", "all|true|false")
					c.Flags().StringVar(&opts.Admin, "admin", "all", "all|true|false")
				} else {
					c.Flags().StringVar(&opts.Visibility, "visibility", "all", "all|public|private")
					c.Flags().StringVar(&opts.Archived, "archived", "all", "all|true|false")
				}
			}
		}
		c.RunE = func(cmd *cobra.Command, args []string) error {
			if opts.Limit < 0 {
				return contract.Input("--limit must be nonnegative.")
			}
			for _, v := range []string{opts.Active, opts.Admin, opts.Archived} {
				if v != "" && v != "all" && v != "true" && v != "false" {
					return contract.Input("Boolean filters must be all, true or false.")
				}
			}
			if v := opts.Visibility; v != "" && v != "all" && v != "public" && v != "private" {
				return contract.Input("--visibility must be all, public or private.")
			}
			op := service.Operation{Command: def.Command, Args: map[string]string{}}
			for i, k := range def.Args {
				op.Args[k] = args[i]
			}
			if def.Command == "channels create" {
				op.Args["is_private"] = fmt.Sprint(private)
			}
			if _, e := service.Validate(op, false); e != nil {
				return e
			}
			s, e := a.connected(cmd.Context())
			if e != nil {
				return e
			}
			if def.Write {
				resolved, e := s.ResolveOperation(cmd.Context(), op)
				if e != nil {
					return e
				}
				op = resolved
				if a.dry {
					before, e := s.Snapshot(cmd.Context(), op)
					a.data = service.Item{Operation: op, Before: before}
					a.meta["dry_run"] = true
					return e
				}
				if def.Confirm {
					if e := a.confirm(def.Command + " " + op.Args["channel"]); e != nil {
						return e
					}
				}
			}
			data, meta, e := s.Run(cmd.Context(), op, opts)
			a.data = data
			for k, v := range meta {
				a.meta[k] = v
			}
			return e
		}
		groups[parent].AddCommand(c)
	}
	authCmd := &cobra.Command{Use: "auth", Short: "Inspect or remove local authentication"}
	root.AddCommand(authCmd)
	for _, name := range []string{"status", "logout"} {
		n := name
		c := &cobra.Command{Use: n, Args: cobra.NoArgs, Short: n + " authentication", RunE: func(cmd *cobra.Command, _ []string) error {
			if n == "logout" {
				if !a.dry {
					if e := a.Auth.Logout(cmd.Context(), a.profile); e != nil {
						return e
					}
				}
				a.data = map[string]any{"profile": a.profile, "local_only": true, "dry_run": a.dry, "environment_token_unchanged": true}
				return nil
			}
			s, e := a.connected(cmd.Context())
			if e != nil {
				return e
			}
			a.data, e = s.Identity(cmd.Context())
			return e
		}}
		authCmd.AddCommand(c)
	}
	var clientID string
	var port int
	var noBrowser bool
	login := &cobra.Command{Use: "login", Short: "Authorize a user token using OAuth PKCE", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if clientID == "" {
			return contract.Input("--client-id is required.")
		}
		if a.dry {
			a.data = map[string]any{"profile": a.profile, "client_id": clientID, "dry_run": true}
			return nil
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Minute)
		defer cancel()
		open := auth.OpenBrowser
		if noBrowser {
			open = func(string) error { return nil }
		}
		if e := a.Auth.Login(ctx, a.profile, clientID, port, a.Err, open); e != nil {
			return e
		}
		a.data = map[string]any{"profile": a.profile, "saved": true}
		return nil
	}}
	login.Flags().StringVar(&clientID, "client-id", "", "Slack app Client ID (not a secret)")
	login.Flags().IntVar(&port, "port", 17839, "Loopback callback port")
	login.Flags().BoolVar(&noBrowser, "no-browser", false, "Print authorization URL without opening a browser")
	root.AddCommand(login)
	root.AddCommand(&cobra.Command{Use: "doctor", Short: "Diagnose identity, scopes and visibility", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, e := a.connected(cmd.Context())
		if e != nil {
			return e
		}
		identity, e := s.Identity(cmd.Context())
		if e != nil {
			return e
		}
		missing := []string{}
		have := map[string]bool{}
		for _, x := range s.API.Scopes {
			have[x] = true
		}
		if s.API.ScopesKnown {
			for _, x := range strings.Split(auth.Scopes, ",") {
				if !have[x] {
					missing = append(missing, x)
				}
			}
		}
		a.data = map[string]any{"identity": identity, "endpoint": s.API.BaseURL, "scopes_known": s.API.ScopesKnown, "scopes": s.API.Scopes, "missing_scopes": missing, "visibility": service.Visibility, "note": "Scopes do not override user privileges or workspace policy."}
		return nil
	}})
	batch := &cobra.Command{Use: "batch", Short: "Plan, inspect and apply JSON operations"}
	root.AddCommand(batch)
	for _, name := range []string{"plan", "show", "apply"} {
		n := name
		var file, save string
		var keep bool
		c := &cobra.Command{Use: n, Short: n + " a batch", Args: cobra.NoArgs}
		c.Flags().StringVar(&file, "file", "", "Input JSON file")
		if n == "plan" {
			c.Flags().StringVar(&save, "save", "", "Save a plan (refuses to overwrite an existing file)")
		}
		if n == "apply" {
			c.Flags().BoolVar(&keep, "continue-on-error", false, "Continue after item failure; never roll back")
		}
		c.RunE = func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return contract.Input("--file is required.")
			}
			if n == "plan" {
				var ops []service.Operation
				if e := readJSON(file, &ops); e != nil {
					return e
				}
				s, e := a.connected(cmd.Context())
				if e != nil {
					return e
				}
				p, e := s.MakePlan(cmd.Context(), ops)
				if e != nil {
					return e
				}
				a.data = p
				if save != "" && !a.dry {
					return saveJSON(save, p)
				}
				return nil
			}
			var p service.Plan
			if e := readJSON(file, &p); e != nil {
				return e
			}
			if e := p.Validate(); e != nil {
				return e
			}
			if n == "show" {
				a.data = p
				return nil
			}
			if e := a.confirm(fmt.Sprintf("Apply %d operations in %s as %s", len(p.Items), p.TeamID, p.UserID)); e != nil {
				return e
			}
			s, e := a.connected(cmd.Context())
			if e != nil {
				return e
			}
			results, e := s.Apply(cmd.Context(), &p, keep, a.dry)
			a.data = results
			a.meta["dry_run"] = a.dry
			return e
		}
		batch.AddCommand(c)
	}
	root.AddCommand(&cobra.Command{Use: "schema", Short: "Print the machine-readable command contract", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		a.format = "json"
		commands := []any{}
		var walk func(*cobra.Command)
		walk = func(c *cobra.Command) {
			flags := []any{}
			c.Flags().VisitAll(func(f *pflag.Flag) {
				flags = append(flags, map[string]any{"name": f.Name, "type": f.Value.Type(), "default": f.DefValue, "description": f.Usage})
			})
			commands = append(commands, map[string]any{"command": c.CommandPath(), "usage": c.Use, "description": c.Short, "flags": flags})
			for _, child := range c.Commands() {
				walk(child)
			}
		}
		walk(root)
		a.data = map[string]any{"commands": commands, "operations": service.Definitions, "envelope": []string{"schema_version", "ok", "data", "error", "meta"}, "exit_codes": map[string]int{"success": 0, "input": 2, "authentication": 3, "permission": 4, "api": 5, "partial": 6, "confirmation": 7}, "scope_semantics": "channel scopes depend on public/private target; exact email resolution additionally requires users:read.email", "batch_input": "array of {command, args}; only write operations", "visibility": service.Visibility}
		return nil
	}})
	return root
}
func readJSON(path string, dest any) error {
	f, e := os.Open(path)
	if e != nil {
		return contract.Input("Cannot open input JSON file.")
	}
	defer f.Close()
	info, statErr := f.Stat()
	if statErr != nil || info.Size() > 8<<20 {
		return contract.Input("Input JSON file must be at most 8 MiB.")
	}
	d := json.NewDecoder(io.LimitReader(f, 8<<20))
	d.DisallowUnknownFields()
	if d.Decode(dest) != nil {
		return contract.Input("Invalid JSON input or unknown fields.")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return contract.Input("Input must contain exactly one JSON value.")
	}
	return nil
}
func saveJSON(path string, v any) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return contract.Input("Cannot create plan file; destination must not already exist.")
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	e = enc.Encode(v)
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return contract.New("file_write_failed", "Cannot save plan.", "Check disk space and permissions.", 5)
	}
	return nil
}
