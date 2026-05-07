package ssql

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CatalogShardOpts controls ProcessCatalogShardsRemoteGo.
type CatalogShardOpts struct {
	// Concurrency: 0 = uncapped (default — one ssh per shard, fanned
	// out at once). N > 0 = bounded by semaphore.
	Concurrency int

	// Order: "" or "completion" (default) — shards' records emit in
	// finish order. "catalog" — buffer per shard, flush in catalog
	// order at the end (peak memory ~total output).
	Order string

	// KeepGoing: false (default) = fail-fast (cancel remaining shards
	// on first error). true = run all shards to completion, return
	// the first error.
	KeepGoing bool
}

// ProcessCatalogShardsRemoteGo orchestrates ship-and-run across catalog
// shards, the v4.43 codegen-symmetric pushdown counterpart of
// ProcessCatalogShards. For each entry it builds a per-shard .ssql
// script (`# require: vX.Y.Z` header + `ssql from PATH` + pushdown
// stages), ships it via ssh stdin, and runs it on the remote with
// `ssql generate go -script -mode $mode -run`. Whatever mode the local
// pipeline runs in, each shard runs in too — record-mode Go from a
// SSQLGO=record local, typed-parallel Go from SSQLGO=typed.
//
// Returns iter.Seq[Record] of merged JSONL output. The remote emits
// schema-aware JSONL (v4.42 format); we strip the per-shard schema
// header — downstream stages see clean Records.
//
// shardField, if non-empty, adds a "host:path" provenance field to
// each record (matches v4.27 -shard-field behaviour).
func ProcessCatalogShardsRemoteGo(
	entries []CatalogEntry,
	requireVersion string,
	pushdownGroups [][]string,
	mode string,
	shardField string,
	opts CatalogShardOpts,
) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		if len(entries) == 0 {
			return
		}
		if opts.Order == "" {
			opts.Order = "completion"
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var sem chan struct{}
		if opts.Concurrency > 0 {
			sem = make(chan struct{}, opts.Concurrency)
		}

		var firstErr error
		var firstErrOnce sync.Once
		recordErr := func(err error) {
			firstErrOnce.Do(func() { firstErr = err })
			if !opts.KeepGoing {
				cancel()
			}
		}

		if opts.Order == "catalog" {
			runCatalogOrder(ctx, entries, requireVersion, pushdownGroups, mode, shardField, sem, recordErr, yield)
			if firstErr != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", firstErr)
			}
			return
		}

		// Default: completion order — fan-in via channel.
		ch := make(chan Record, 64)
		var wg sync.WaitGroup
		for _, entry := range entries {
			wg.Add(1)
			go func(e CatalogEntry) {
				defer wg.Done()
				if sem != nil {
					select {
					case sem <- struct{}{}:
					case <-ctx.Done():
						return
					}
					defer func() { <-sem }()
				}
				if ctx.Err() != nil {
					return
				}
				if err := runShardStream(ctx, e, requireVersion, pushdownGroups, mode, shardField, ch); err != nil {
					recordErr(fmt.Errorf("shard %s:%s: %w", e.Host, e.Path, err))
				}
			}(entry)
		}
		go func() {
			wg.Wait()
			close(ch)
		}()
		for r := range ch {
			if !yield(r) {
				cancel()
				for range ch {
				}
				return
			}
		}
		if firstErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", firstErr)
		}
	}
}

// runCatalogOrder runs all shards concurrently but buffers output per
// shard, flushing in catalog-defined order once every shard finishes.
// Trade: peak memory ~total output for deterministic ordering.
func runCatalogOrder(
	ctx context.Context,
	entries []CatalogEntry,
	requireVersion string,
	pushdownGroups [][]string,
	mode string,
	shardField string,
	sem chan struct{},
	recordErr func(error),
	yield func(Record) bool,
) {
	results := make([][]Record, len(entries))
	var wg sync.WaitGroup
	for i, entry := range entries {
		wg.Add(1)
		go func(i int, e CatalogEntry) {
			defer wg.Done()
			if sem != nil {
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
			}
			if ctx.Err() != nil {
				return
			}
			rs, err := runShardCollect(ctx, e, requireVersion, pushdownGroups, mode, shardField)
			if err != nil {
				recordErr(fmt.Errorf("shard %s:%s: %w", e.Host, e.Path, err))
				return
			}
			results[i] = rs
		}(i, entry)
	}
	wg.Wait()
	for _, rs := range results {
		for _, r := range rs {
			if !yield(r) {
				return
			}
		}
	}
}

// runShardStream runs one shard, parses its JSONL output, and forwards
// records into ch under context cancellation.
func runShardStream(
	ctx context.Context,
	entry CatalogEntry,
	requireVersion string,
	groups [][]string,
	mode, shardField string,
	ch chan<- Record,
) error {
	stdout, wait, err := startShardSSH(ctx, entry, requireVersion, groups, mode)
	if err != nil {
		return err
	}
	defer stdout.Close()

	for r := range readShardJSONL(stdout) {
		if ctx.Err() != nil {
			break
		}
		r = enrichCatalogRecord(r, entry, shardField)
		select {
		case ch <- r:
		case <-ctx.Done():
		}
	}
	return wait()
}

// runShardCollect runs one shard, accumulates all records, returns them
// when the shard completes.
func runShardCollect(
	ctx context.Context,
	entry CatalogEntry,
	requireVersion string,
	groups [][]string,
	mode, shardField string,
) ([]Record, error) {
	stdout, wait, err := startShardSSH(ctx, entry, requireVersion, groups, mode)
	if err != nil {
		return nil, err
	}
	defer stdout.Close()

	var out []Record
	for r := range readShardJSONL(stdout) {
		if ctx.Err() != nil {
			break
		}
		out = append(out, enrichCatalogRecord(r, entry, shardField))
	}
	if err := wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// startShardSSH builds the per-shard script, opens an ssh process to
// entry.Host (or bash for "local"/"localhost"), and returns the stdout
// pipe + wait-for-completion func.
func startShardSSH(
	ctx context.Context,
	entry CatalogEntry,
	requireVersion string,
	groups [][]string,
	mode string,
) (io.ReadCloser, func() error, error) {
	script := buildShardScript(entry, requireVersion, groups)
	remotePath := fmt.Sprintf("/tmp/ssql-remote-%d-%d.ssql", os.Getpid(), time.Now().UnixNano())
	remoteCmd := fmt.Sprintf(
		"trap 'rm -f %s' EXIT; cat > %s && /usr/bin/ssql generate go -script %s -mode %s -run",
		remotePath, remotePath, remotePath, mode,
	)
	var cmd *exec.Cmd
	if IsLocalHost(entry.Host) {
		cmd = exec.CommandContext(ctx, "bash", "-c", remoteCmd)
	} else {
		cmd = exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", entry.Host, remoteCmd)
	}
	cmd.Stdin = strings.NewReader(script)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return stdout, cmd.Wait, nil
}

// buildShardScript renders one shard's .ssql script: `# require:`
// header, `ssql from [FORMAT] PATH`, then the user's pushdown stages
// joined with `| ssql ...`. Format empty/csv → bare `from PATH` (auto-
// detection); other formats → `from FORMAT PATH`.
func buildShardScript(entry CatalogEntry, requireVersion string, groups [][]string) string {
	var sb strings.Builder
	if requireVersion != "" {
		sb.WriteString("# require: v")
		sb.WriteString(requireVersion)
		sb.WriteString("\n")
	}
	sb.WriteString("ssql from ")
	if entry.Format != "" && entry.Format != "csv" {
		sb.WriteString(entry.Format)
		sb.WriteString(" ")
	}
	sb.WriteString(ShellQuote(entry.Path))
	sb.WriteString("\n")
	for _, group := range groups {
		sb.WriteString("| ssql ")
		for i, arg := range group {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(ShellQuote(arg))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// readShardJSONL parses a remote shard's stdout (schema-aware JSONL,
// v4.42 wire format). Strips `{"_schema":…}` headers; downstream sees
// only data records.
func readShardJSONL(r io.Reader) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var schema *Schema
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			if schema == nil {
				if s, ok := parseSchemaHeaderLine(line); ok {
					schema = s
					continue
				}
			} else if bytes.Contains(line[:min(len(line), 20)], []byte(`"_schema"`)) {
				// Subsequent shards' headers — skip silently.
				continue
			}

			var record Record
			var err error
			if schema != nil {
				record, err = ParseJSONLineWithSchema(line, schema)
			} else {
				var mut MutableRecord
				mut, err = ParseJSONLine(line)
				if err == nil {
					record = mut.Freeze()
				}
			}
			if err != nil {
				continue
			}
			if !yield(record) {
				return
			}
		}
	}
}
