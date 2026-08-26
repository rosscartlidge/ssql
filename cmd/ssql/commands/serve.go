//go:build !slim

package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rosscartlidge/autocli/shell"
	autossh "github.com/rosscartlidge/autocli/ssh"
	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
)

// RegisterServe adds the `serve` subcommand — an SSH-accessible
// operator console for an in-memory dataset. Behind the !slim build
// tag because it pulls in autocli/shell + autocli/ssh (and their
// crypto/ssh + readline deps).
//
// Operators connect with their existing SSH keys and run commands
// against the loaded data with zero per-query startup cost.
//
//	ssql serve data.csv -listen :2222 -authorized-keys ./keys
//
// Operator workflow:
//
//	$ ssh -p 2222 alice@host
//	> sta<TAB>tus
//	> status
//	uptime: 3h17m
//	path:   /data/main.csv
//	rows:   87123421
//	> schema
//	... field/type list ...
//	> head -n 5 -t
//	... 5 rows as a table ...
func RegisterServe(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("serve").
		Description("Run an SSH-accessible operator console serving an in-memory dataset").
		ClauseDescription("Loads PATH at startup, then accepts SSH connections that run a small ssql CLI against the loaded data with no per-query startup cost").
		Example("ssql serve data.csv -listen :2222 -authorized-keys ./keys", "Default serve loopback-open").
		Example("ssql serve -listen-http 127.0.0.1:8080 -dir /data", "HTTP protocol: per-request pipelines over /data").
		Example("ssql serve -listen-http 0.0.0.0:8080 -dir /data -token SECRET", "HTTP on all interfaces (token required)").
		Example("ssql serve data.csv -listen 127.0.0.1:2222 -host-key ./host_key -authorized-keys ./keys", "Bind loopback-only").
		Flag("PATH").
		String().
		Completer(&cf.FileCompleter{Pattern: "*"}).
		Global().
		Help("Dataset to load into memory for the SSH protocol (CSV / JSON / JSONL — autodetected by extension). Optional when -listen-http is given").
		Done().
		Flag("-listen").
		String().
		Global().
		Default(":2222").
		Help("SSH listen address (default :2222)").
		Done().
		Flag("-host-key").
		String().
		Global().
		Default("./ssql_serve_host_key").
		Help("SSH host key file — generated on first run if absent (0600, ed25519)").
		Done().
		Flag("-authorized-keys").
		String().
		Global().
		Default("./ssql_serve_authorized_keys").
		Completer(&cf.FileCompleter{Pattern: "*"}).
		Help("OpenSSH authorized_keys file — required, refuses to start if missing").
		Done().
		Flag("-welcome").
		String().
		Global().
		Default("").
		Help("Welcome banner shown on session connect").
		Done().
		Flag("-listen-http").
		String().
		Global().
		Default("").
		Help("HTTP listen address (e.g. 127.0.0.1:8080) — enables the REST protocol (execute/cursor/files/health)").
		Done().
		Flag("-dir").
		String().
		Global().
		Default(".").
		Help("Working directory for HTTP pipelines and /api/files (NOT a sandbox — see -token)").
		Done().
		Flag("-token").
		String().
		Global().
		Default("").
		Help("Bearer token for the HTTP protocol; REQUIRED when -listen-http binds a non-loopback address").
		Done().
		Flag("-readonly").
		Bool().
		Global().
		Default(false).
		Help("Reject HTTP pipelines that write files (tee, to FMT FILE, generate -run/-build) — recommended with tokenless tailnet binds").
		Done().
		Flag("-http-timeout").
		Int().
		Global().
		Default(300).
		Help("Per-request wall-clock limit in seconds for HTTP pipeline execution").
		Done().
		Flag("-session-dir").
		String().
		Global().
		Default("").
		Completer(&cf.FileCompleter{Pattern: "*"}).
		Help("Parent directory for per-user shell state (history + :set vi/emacs prefs). Empty = no persistence.").
		Done().
		Handler(func(ctx *cf.Context) error {
			path, _ := ctx.GlobalFlags["PATH"].(string)
			listen, _ := ctx.GlobalFlags["-listen"].(string)
			hostKey, _ := ctx.GlobalFlags["-host-key"].(string)
			authKeys, _ := ctx.GlobalFlags["-authorized-keys"].(string)
			welcome, _ := ctx.GlobalFlags["-welcome"].(string)
			sessionDir, _ := ctx.GlobalFlags["-session-dir"].(string)
			listenHTTP, _ := ctx.GlobalFlags["-listen-http"].(string)
			httpDir, _ := ctx.GlobalFlags["-dir"].(string)
			token, _ := ctx.GlobalFlags["-token"].(string)
			httpTimeout, _ := ctx.GlobalFlags["-http-timeout"].(int)
			readonly, _ := ctx.GlobalFlags["-readonly"].(bool)

			if path == "" && listenHTTP == "" {
				return fmt.Errorf("ssql serve: need a dataset PATH (SSH protocol), -listen-http ADDR (HTTP protocol), or both")
			}

			rootCtx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Honour Ctrl-C / SIGTERM for graceful shutdown.
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigs
				fmt.Fprintln(ctx.Stderr(), "ssql serve: shutting down…")
				cancel()
			}()

			var httpDone <-chan error
			if listenHTTP != "" {
				var err error
				_, httpDone, err = startServeHTTP(rootCtx, serveHTTPOptions{
					Addr:     listenHTTP,
					Dir:      httpDir,
					Token:    token,
					Timeout:  time.Duration(httpTimeout) * time.Second,
					Readonly: readonly,
					Stderr:   ctx.Stderr(),
				})
				if err != nil {
					return err
				}
			}

			if path == "" {
				// HTTP-only mode: block until shutdown or server error.
				select {
				case err := <-httpDone:
					if rootCtx.Err() != nil {
						return nil // graceful shutdown
					}
					return err
				case <-rootCtx.Done():
					return nil
				}
			}

			srv, err := loadServerDataset(path)
			if err != nil {
				return fmt.Errorf("ssql serve: load %s: %w", path, err)
			}

			fmt.Fprintf(ctx.Stderr(), "ssql serve: loaded %d rows from %s in %v\n",
				len(srv.records), srv.path, srv.loadTime)
			if welcome == "" {
				welcome = fmt.Sprintf("ssql serve — %d rows from %s — :help for built-ins, :exit to quit",
					len(srv.records), srv.path)
			}

			cli := buildServeCLI()

			return autossh.Serve(rootCtx, cli, autossh.Options{
				Addr:           listen,
				HostKeyPath:    hostKey,
				AuthorizedKeys: authKeys,
				State:          srv,
				Welcome:        welcome,
				HistoryDir:     sessionDir,
				Settings:       buildServeSettings(srv),
				SchemaWalk:     serveSchemaWalk,
			})
		}).
		Done()
	return cmd
}

// serveState is the per-instance value passed to every session via
// ctx.State.  All handlers below type-assert to this and read
// (never write — the dataset is immutable for the lifetime of the
// process).
type serveState struct {
	records  []ssql.Record
	schema   []string
	path     string
	started  time.Time
	loadTime time.Duration

	// headDefault is the default row count for `head` when the
	// operator hasn't passed -n explicitly. Tunable at runtime via
	// `:set head-default-rows N` — the Setting's Get/Set close over
	// this field via atomic load/store, so concurrent sessions
	// stay consistent.
	headDefault atomic.Int64
}

// loadServerDataset reads the entire dataset into memory.  Supports
// CSV/TSV/JSON/JSONL by extension; other formats error.  Drains the
// iterator and caches the schema (field names from the first record,
// or empty if the dataset is empty).
func loadServerDataset(path string) (*serveState, error) {
	t0 := time.Now()
	dsFmt := ""
	if fi, ok := formatForPath(path); ok {
		dsFmt = fi.Name
	}
	var records []ssql.Record
	switch dsFmt {
	case "csv":
		seq, err := ssql.ReadCSV(path)
		if err != nil {
			return nil, err
		}
		records = slices.Collect(seq)
	case "tsv":
		seq, err := ssql.ReadCSV(path, ssql.CSVConfig{Delimiter: '\t'})
		if err != nil {
			return nil, err
		}
		records = slices.Collect(seq)
	case "json", "jsonl":
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		records = slices.Collect(ssql.ReadJSONLFromReader(f))
	default:
		return nil, fmt.Errorf("unsupported format (use .csv, .tsv, .json, or .jsonl): %s", path)
	}

	// Schema = field names in first-record order, falls back to empty.
	var schema []string
	if len(records) > 0 {
		for k := range records[0].KeysIter() {
			schema = append(schema, k)
		}
	}

	s := &serveState{
		records:  records,
		schema:   schema,
		path:     path,
		started:  time.Now(),
		loadTime: time.Since(t0),
	}
	s.headDefault.Store(10)
	return s, nil
}

// buildServeSettings constructs the runtime-tunable settings exposed
// at the SSH prompt via `:set`. Each Setting closes over srv so all
// sessions on the same server see the same underlying values; the
// atomics make concurrent reads/writes safe.
func buildServeSettings(srv *serveState) []shell.Setting {
	return []shell.Setting{
		{
			Name:        "head-default-rows",
			Description: "default row count for `head` when -n isn't passed",
			Get: func() string {
				return strconv.FormatInt(srv.headDefault.Load(), 10)
			},
			Set: func(v string) error {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return fmt.Errorf("must be an integer, got %q", v)
				}
				if n <= 0 {
					return fmt.Errorf("must be > 0, got %d", n)
				}
				srv.headDefault.Store(n)
				return nil
			},
		},
	}
}
