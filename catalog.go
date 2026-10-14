package ssql

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"iter"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// CatalogEntry represents one shard in a catalog.
type CatalogEntry struct {
	Host     string
	Path     string
	Format   string
	Bin      string // optional absolute path of ssql on this shard's host ("bin" column); "" = resolve there
	Metadata map[string]string
	// Columns: the metadata keys in the order the records carry them —
	// the catalog's header order, then the hive keys (DeriveHiveMetadata
	// appends). nil for an entry built by hand: catalogMetaColumns then
	// sorts the keys. Until 2026-10-14 enrichment ranged the map, so the
	// column order differed from record to record.
	Columns []string
	// HiveKeys: the metadata keys derived from the path's key=value
	// directories (DeriveHiveMetadata), in path order; nil for a row whose
	// metadata is all from the catalog. A shard column of the same name is
	// an error when its records arrive.
	HiveKeys []string
}

// CatalogFilter represents a partition pruning condition.
type CatalogFilter struct {
	Field    string
	Operator string
	Value    string
	// Negated is the +if form: keep shards that may contain rows NOT
	// matching the condition. For exact metadata columns that is the
	// complement; for range (_from/_to) columns a shard is skipped only
	// when its ENTIRE range satisfies the positive condition (no row
	// could survive the negation) — pruning stays conservative.
	Negated bool
}

// ReadCatalog parses a catalog CSV file. The CSV must have "host" and "path"
// columns. An optional "format" column specifies the data format (defaults to
// CSV). All other columns are stored as metadata for pruning and enrichment.
func ReadCatalog(filename string) ([]CatalogEntry, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening catalog: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("reading catalog headers: %w", err)
	}

	hostIdx, pathIdx := -1, -1
	formatIdx, binIdx := -1, -1
	for i, h := range headers {
		switch strings.TrimSpace(strings.ToLower(h)) {
		case "host":
			hostIdx = i
		case "path":
			pathIdx = i
		case "format":
			formatIdx = i
		case "bin":
			binIdx = i
		}
	}
	if hostIdx < 0 || pathIdx < 0 {
		return nil, fmt.Errorf("catalog must have 'host' and 'path' columns (found: %v)", headers)
	}

	var entries []CatalogEntry
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading catalog row: %w", err)
		}

		entry := CatalogEntry{
			Host:     strings.TrimSpace(row[hostIdx]),
			Path:     strings.TrimSpace(row[pathIdx]),
			Metadata: make(map[string]string),
		}
		if formatIdx >= 0 && formatIdx < len(row) {
			entry.Format = strings.TrimSpace(row[formatIdx])
		}
		if binIdx >= 0 && binIdx < len(row) {
			entry.Bin = strings.TrimSpace(row[binIdx])
			if entry.Bin != "" && !strings.HasPrefix(entry.Bin, "/") {
				return nil, fmt.Errorf("catalog row %s:%s: bin must be an absolute path, got %q", entry.Host, entry.Path, entry.Bin)
			}
		}
		for i, h := range headers {
			h = strings.TrimSpace(strings.ToLower(h))
			if isCatalogReservedColumn(h) {
				continue
			}
			entry.Columns = append(entry.Columns, h)
			if i < len(row) {
				entry.Metadata[h] = strings.TrimSpace(row[i])
			}
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// isCatalogReservedColumn: the catalog columns that locate and read a
// shard, as opposed to describing its data. One list — ReadCatalog and
// CatalogColumns both consult it (the optimiser kept its own copy, which
// lacked "bin").
func isCatalogReservedColumn(h string) bool {
	switch h {
	case "host", "path", "format", "bin", "fields":
		return true
	}
	return false
}

// CatalogColumns reads a catalog's header and returns its metadata
// columns in header order: an exact column by its name, a range pair by
// its base name once (`date_from`,`date_to` → `date`, also in ranges).
// Exact columns become string fields of every record from the shard;
// range columns prune only. This is what a consumer learns about a
// catalog's data without opening a shard (schema mode, the optimiser's
// predicate extraction).
func CatalogColumns(filename string) (cols []string, ranges map[string]bool, err error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("opening catalog: %w", err)
	}
	defer f.Close()
	headers, err := csv.NewReader(f).Read()
	if err != nil {
		return nil, nil, fmt.Errorf("reading catalog headers: %w", err)
	}
	ranges = map[string]bool{}
	seen := map[string]bool{}
	for _, h := range headers {
		h = strings.TrimSpace(strings.ToLower(h))
		if isCatalogReservedColumn(h) {
			continue
		}
		base := h
		if strings.HasSuffix(h, "_from") {
			base = strings.TrimSuffix(h, "_from")
			ranges[base] = true
		} else if strings.HasSuffix(h, "_to") {
			base = strings.TrimSuffix(h, "_to")
			ranges[base] = true
		}
		if !seen[base] {
			seen[base] = true
			cols = append(cols, base)
		}
	}
	return cols, ranges, nil
}

// PruneCatalog filters catalog entries based on pruning conditions.
// For range columns (field_from/field_to), uses interval overlap logic.
// For exact-value columns, matches directly.
// Filters referencing columns not in the catalog are ignored (they may
// apply to the data itself, not the catalog).
func PruneCatalog(entries []CatalogEntry, filters []CatalogFilter) []CatalogEntry {
	if len(filters) == 0 {
		return entries
	}

	var result []CatalogEntry
	for _, entry := range entries {
		if catalogEntryMatches(entry, filters) {
			result = append(result, entry)
		}
	}
	return result
}

func catalogEntryMatches(entry CatalogEntry, filters []CatalogFilter) bool {
	for _, f := range filters {
		fromKey := f.Field + "_from"
		toKey := f.Field + "_to"
		fromVal, hasFrom := entry.Metadata[fromKey]
		toVal, hasTo := entry.Metadata[toKey]

		if hasFrom || hasTo {
			if f.Negated {
				// +if: skip only when the WHOLE range satisfies the
				// positive condition — then no row survives the negation.
				if catalogRangeEntirely(fromVal, toVal, f.Operator, f.Value) {
					return false
				}
				continue
			}
			if !catalogRangeMatches(fromVal, toVal, f.Operator, f.Value) {
				return false
			}
			continue
		}

		if colVal, ok := entry.Metadata[f.Field]; ok {
			match := catalogStringOp(colVal, f.Operator, f.Value)
			if f.Negated {
				// Exact metadata: the column value holds for every row in
				// the shard, so negation is the exact complement.
				match = !match
			}
			if !match {
				return false
			}
			continue
		}
	}
	return true
}

// catalogRangeEntirely reports whether EVERY value in [from, to] satisfies
// the operator against value — the condition under which a negated (+if)
// filter can prune the shard. Missing bounds answer false (conservative:
// keep the shard).
func catalogRangeEntirely(from, to, operator, value string) bool {
	switch operator {
	case "ge":
		return from != "" && from >= value
	case "gt":
		return from != "" && from > value
	case "le":
		return to != "" && to <= value
	case "lt":
		return to != "" && to < value
	case "eq":
		return from != "" && to != "" && from == value && to == value
	case "ne":
		// Entire range ≠ value ⇔ value lies outside [from, to].
		return (from != "" && value < from) || (to != "" && value > to)
	}
	return false
}

func catalogRangeMatches(from, to, operator, value string) bool {
	switch operator {
	case "ge", "gt":
		if to != "" && to < value {
			return false
		}
	case "le", "lt":
		if from != "" && from > value {
			return false
		}
	case "eq":
		if from != "" && value < from {
			return false
		}
		if to != "" && value > to {
			return false
		}
	case "ne":
		// Can't prune on not-equal for ranges
	}
	return true
}

func catalogStringOp(colVal, operator, filterVal string) bool {
	switch operator {
	case "eq":
		return colVal == filterVal
	case "ne":
		return colVal != filterVal
	case "gt":
		return colVal > filterVal
	case "ge":
		return colVal >= filterVal
	case "lt":
		return colVal < filterVal
	case "le":
		return colVal <= filterVal
	}
	return true
}

// ExpandCatalogGlobs expands entries whose paths contain glob characters into
// one entry per matching file. Local entries use filepath.Glob; remote entries
// use `ssh host echo <glob>` to expand on the remote host. Non-glob entries
// are passed through unchanged. Metadata is inherited by expanded entries.
func ExpandCatalogGlobs(entries []CatalogEntry) []CatalogEntry {
	var result []CatalogEntry
	for _, entry := range entries {
		if !isGlob(entry.Path) {
			result = append(result, entry)
			continue
		}

		var paths []string
		if entry.Host == "local" || entry.Host == "localhost" || entry.Host == "" {
			matches, err := filepath.Glob(entry.Path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "glob %s: %v\n", entry.Path, err)
				continue
			}
			paths = matches
		} else {
			// Remote: ssh host echo <glob> — shell expands the glob
			out, err := exec.Command("ssh", entry.Host, "echo", entry.Path).Output()
			if err != nil {
				fmt.Fprintf(os.Stderr, "glob %s:%s: %v\n", entry.Host, entry.Path, err)
				continue
			}
			for _, p := range strings.Fields(strings.TrimSpace(string(out))) {
				if p != "" {
					paths = append(paths, p)
				}
			}
		}

		for _, p := range paths {
			expanded := CatalogEntry{
				Host:     entry.Host,
				Path:     p,
				Format:   entry.Format,
				Bin:      entry.Bin,
				Metadata: maps.Clone(entry.Metadata), // the row's metadata for every file; a copy, so -hive can add per file
				Columns:  slices.Clone(entry.Columns),
			}
			result = append(result, expanded)
		}
	}
	return result
}

// IsGlobPath reports whether a path has glob characters (*, ?, [).
func IsGlobPath(path string) bool { return isGlob(path) }

// LoadCatalog is the one way a catalog becomes its shard entries — for
// exec, schema mode, generated programs and the optimiser alike, so they
// agree on what a catalog means. file is a catalog CSV, or, under hive, a
// glob of data files (a synthesised local row; the files' format is
// inferred by the shard's own `from`). Rows are pruned on the catalog's
// columns, globs expanded, then under hive each file's key=value
// directories become its metadata (DeriveHiveMetadata) and the pruning
// runs again on those (DFC149).
func LoadCatalog(file string, hive bool, filters []CatalogFilter) ([]CatalogEntry, error) {
	var entries []CatalogEntry
	if hive && isGlob(file) {
		entries = []CatalogEntry{{Host: "local", Path: file, Metadata: map[string]string{}}}
	} else {
		var err error
		if entries, err = ReadCatalog(file); err != nil {
			if hive {
				return nil, fmt.Errorf("%w (with -hive, FILE is a catalog CSV or a glob of data files such as 'data/day=*/*.parquet')", err)
			}
			return nil, err
		}
	}
	entries = ExpandCatalogGlobs(PruneCatalog(entries, filters))
	if hive {
		var err error
		if entries, err = DeriveHiveMetadata(entries); err != nil {
			return nil, err
		}
		entries = PruneCatalog(entries, filters)
	}
	return entries, nil
}

// DeriveHiveMetadata reads each entry's key=value directory segments
// (`…/year=2023/region=north/data.parquet` → year, region; the file name
// is never one; values are percent-decoded, as DuckDB and Spark write
// them) into that entry's metadata — string columns on every record from
// the shard, exact-value columns for pruning — the way a catalog row's
// own columns are. Rules, each an error naming the file: every entry
// must yield the same keys in the same order (files read together share
// one layout); a key that is also a catalog column is a disagreement
// between the author and the layout; a path with no key=value directory
// has nothing to derive.
func DeriveHiveMetadata(entries []CatalogEntry) ([]CatalogEntry, error) {
	var first []string
	for i := range entries {
		e := &entries[i]
		keys, values := hivePathMetadata(e.Path)
		if len(keys) == 0 {
			return nil, fmt.Errorf("%s:%s: no key=value directory in the path (-hive derives columns from directories such as day=2023-05-15/)", e.Host, e.Path)
		}
		if i == 0 {
			first = keys
		} else if !slices.Equal(first, keys) {
			return nil, fmt.Errorf("%s:%s has partition keys %v where %s:%s has %v — files read together must share one layout", e.Host, e.Path, keys, entries[0].Host, entries[0].Path, first)
		}
		// The catalog's own columns come first, read BEFORE the keys join
		// the map: an entry without Columns (the glob form) orders its
		// metadata by sorting the map, and with the keys already in it
		// each key was listed twice, so every record carried the derived
		// column twice (shipped in the fast path, caught by the SQL lane's
		// literal form 2026-10-14).
		cols := append([]string(nil), catalogMetaColumns(*e)...)
		md := maps.Clone(e.Metadata)
		if md == nil {
			md = map[string]string{}
		}
		for j, k := range keys {
			if _, clash := md[k]; clash {
				return nil, fmt.Errorf("%s:%s: partition key %q is also a catalog column — drop one of them", e.Host, e.Path, k)
			}
			md[k] = values[j]
		}
		e.Metadata = md
		e.Columns = append(cols, keys...)
		e.HiveKeys = keys
	}
	return entries, nil
}

// catalogMetaColumns is the order an entry's metadata columns take on
// its records: Columns when the catalog set it, else the keys sorted.
func catalogMetaColumns(e CatalogEntry) []string {
	if e.Columns != nil {
		return e.Columns
	}
	return slices.Sorted(maps.Keys(e.Metadata))
}

// CatalogRecordMetadata is the metadata an entry's records carry, in
// the order it is attached: the shard field (host:path) when named, then
// the metadata columns in catalogMetaColumns order, skipping range
// (_from/_to) columns, which prune and are not fields, and empty values.
// Every path that attaches metadata (the subprocess shard reader, the
// in-process fast path, the SQL lane's literals) takes it from here.
func CatalogRecordMetadata(e CatalogEntry, shardField string) (names, values []string) {
	if shardField != "" {
		names = append(names, shardField)
		values = append(values, e.Host+":"+e.Path)
	}
	for _, k := range catalogMetaColumns(e) {
		if v := e.Metadata[k]; v != "" && !isCatalogRangeColumn(k) {
			names = append(names, k)
			values = append(values, v)
		}
	}
	return names, values
}

// CatalogEntryFormat is the format a shard is read as: the catalog's
// format column, else the path's extension (what a bare `from PATH`
// infers on the shard).
func CatalogEntryFormat(e CatalogEntry) string {
	if e.Format != "" {
		return e.Format
	}
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(e.Path)), ".")
}

// hivePathMetadata returns the key=value directory segments of a path,
// in order. Paths are /-separated on every host ssql reads from.
func hivePathMetadata(path string) (keys, values []string) {
	segs := strings.Split(path, "/")
	for _, seg := range segs[:len(segs)-1] { // the file name is not a segment
		i := strings.IndexByte(seg, '=')
		if i <= 0 {
			continue
		}
		v, err := url.PathUnescape(seg[i+1:])
		if err != nil {
			v = seg[i+1:]
		}
		keys = append(keys, seg[:i])
		values = append(values, v)
	}
	return keys, values
}

// hiveClash names the first of the entry's derived keys that the shard's
// own records already carry, or "". Checked on a shard's first record:
// a layout key that is also a file column would silently shadow it.
func hiveClash(r Record, entry CatalogEntry) error {
	for _, k := range entry.HiveKeys {
		if r.Has(k) {
			return fmt.Errorf("partition key %q is also a column of the file — rename one of them", k)
		}
	}
	return nil
}

// WriteCatalog writes catalog entries as CSV.
func WriteCatalog(w io.Writer, entries []CatalogEntry) error {
	writer := csv.NewWriter(w)

	// The metadata columns, in the order the entries carry them (a
	// catalog's header order, then hive keys); the map order it was until
	// 2026-10-14 shuffled -catalog-used's columns from run to run.
	seen := make(map[string]bool)
	var sortedKeys []string
	for _, e := range entries {
		for _, k := range catalogMetaColumns(e) {
			if !seen[k] {
				seen[k] = true
				sortedKeys = append(sortedKeys, k)
			}
		}
	}

	anyBin := false
	for _, e := range entries {
		if e.Bin != "" {
			anyBin = true
		}
	}

	// Headers
	headers := []string{"host", "path"}
	if anyBin {
		headers = append(headers, "bin")
	}
	if len(sortedKeys) > 0 {
		headers = append(headers, sortedKeys...)
	}
	if err := writer.Write(headers); err != nil {
		return err
	}

	// Rows
	for _, e := range entries {
		row := []string{e.Host, e.Path}
		if anyBin {
			row = append(row, e.Bin)
		}
		for _, k := range sortedKeys {
			row = append(row, e.Metadata[k])
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	writer.Flush()
	return writer.Error()
}

// shardBin picks the ssql binary for one catalog row: the row's own bin
// column, else the running ssql for a local row, else remoteBin.
// ShardBin is the ssql to run for a shard: the row's own bin column,
// else remoteBin (a bare name resolved on the host, or -remote-bin's
// path), else SelfBin for a local row.
func ShardBin(entry CatalogEntry, remoteBin string) string { return shardBin(entry, remoteBin) }

func shardBin(entry CatalogEntry, remoteBin string) string {
	if entry.Bin != "" {
		return entry.Bin
	}
	if IsLocalHost(entry.Host) {
		return SelfBin(remoteBin)
	}
	return remoteBin
}

// IsLocalHost returns true when host refers to the machine running this
// process: the literal "local"/"localhost" sentinels, or a value
// matching os.Hostname() (so a catalog with host=derra is portable —
// the row runs locally on derra and via ssh from anywhere else).
//
// The hostname lookup is cached on first call.
func IsLocalHost(host string) bool {
	if host == "local" || host == "localhost" {
		return true
	}
	return host == cachedHostname()
}

var (
	cachedHostnameOnce sync.Once
	cachedHostnameVal  string
)

func cachedHostname() string {
	cachedHostnameOnce.Do(func() {
		if h, err := os.Hostname(); err == nil {
			cachedHostnameVal = h
		}
	})
	return cachedHostnameVal
}

// SelfBin is the ssql binary to run for shard rows that resolve to this
// machine (IsLocalHost): a `go install` user has no /usr/bin/ssql, and
// until v4.93.0 every local shard failed with "bash: /usr/bin/ssql: No
// such file or directory". Resolution: the running executable when it IS
// ssql (ssql, ssql_gpu, a test build named ssql_*), else — inside a
// generated program, whose executable is the program itself — the ssql
// on PATH, else fallback (a bare name, which BuildRemoteCommand resolves
// with the same candidate list the remote side uses). The result is an
// unquoted path; the command builders quote it.
func SelfBin(fallback string) string {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	return selfBinFor(exe, generatedProgram, exec.LookPath, fallback)
}

// generatedProgram is set by MarkGeneratedProgram: the running
// executable is a program `generate go` wrote, not ssql.
var generatedProgram bool

// MarkGeneratedProgram is the first statement of every generated
// program's main: whatever the executable is called, it is not ssql, so
// a local shard runs the ssql on PATH. Before this (v4.93.0–v4.115.0)
// SelfBin took any executable named ssql* for ssql, and `generate go
// -run` names its program ssqlgen — each local shard of a catalog ran
// the whole program again, which ran its shards, …: a fork bomb.
func MarkGeneratedProgram() { generatedProgram = true }

// selfBinFor is SelfBin's rule, testable: the executable itself only when
// the process is ssql — not a generated program, and named ssql,
// ssql_gpu or a test build ssql_* (never ssqlgen or ssql-report).
func selfBinFor(exe string, generated bool, lookPath func(string) (string, error), fallback string) string {
	if !generated && exe != "" {
		base := filepath.Base(exe)
		if base == "ssql" || base == "ssql_gpu" || strings.HasPrefix(base, "ssql_") {
			return exe
		}
	}
	if p, err := lookPath("ssql"); err == nil {
		return p
	}
	return fallback
}

// remoteBinDirs is where each install method puts ssql: the .deb
// (/usr/bin), a release tarball (/usr/local/bin), `go install`
// ($HOME/go/bin), and a per-user bin. Absolute, so nothing on the
// remote depends on that shell's PATH (the shell-injection rule).
var remoteBinDirs = []string{"/usr/bin", "/usr/local/bin", "$HOME/go/bin", "$HOME/.local/bin"}

// RemoteBinPrologue returns the shell text that sets $SSQL on the remote
// before a remote ssql command runs. bin is either a bare binary name
// ("ssql" or "ssql_gpu") — resolved there as the first executable among
// remoteBinDirs, with a one-line error naming the host, the paths tried
// and the remedies (exit 127) when none exists — or an absolute path
// (`-remote-bin`, a catalog `bin` column), used as given. Until v4.94.0
// the remote command was hard-wired to /usr/bin/ssql, which a
// go-install user does not have.
func RemoteBinPrologue(bin string) string {
	if strings.HasPrefix(bin, "/") {
		return "SSQL=" + ShellQuote(bin) + "; "
	}
	var quoted, plain []string
	for _, d := range remoteBinDirs {
		quoted = append(quoted, `"`+d+"/"+bin+`"`)
		plain = append(plain, d+"/"+bin)
	}
	return fmt.Sprintf(`SSQL=; for b in %s; do if [ -x "$b" ]; then SSQL="$b"; break; fi; done; `+
		`if [ -z "$SSQL" ]; then echo "%s: not found on $(hostname): tried %s; install it there with: go install github.com/rosscartlidge/ssql/v4/cmd/ssql@latest, or give its path with -remote-bin PATH (catalog: a bin column)" >&2; exit 127; fi; `,
		strings.Join(quoted, " "), bin, strings.Join(plain, " "))
}

// remoteBinWord returns the prologue and the command word for bin: an
// absolute path is quoted and needs no prologue; a bare name resolves
// through RemoteBinPrologue and runs as "$SSQL".
func remoteBinWord(bin string) (prologue, word string) {
	if strings.HasPrefix(bin, "/") {
		return "", ShellQuote(bin)
	}
	return RemoteBinPrologue(bin), `"$SSQL"`
}

// RemoteScriptCommand is the remote side of ship-and-run: receive a
// .ssql script on stdin at remotePath, run it with `generate go -script
// -mode MODE -run`, remove it on exit. bin as for RemoteBinPrologue.
func RemoteScriptCommand(bin, remotePath, mode string) string {
	prologue, word := remoteBinWord(bin)
	return prologue + fmt.Sprintf("trap 'rm -f %s' EXIT; cat > %s && %s generate go -script %s -mode %s -run",
		remotePath, remotePath, word, remotePath, mode)
}

// ProcessCatalogShards connects to each shard via SSH (or locally for
// hosts that resolve to this machine — see IsLocalHost) and returns a
// concatenated record stream. remoteBin is a bare name ("ssql" or
// "ssql_gpu"), resolved on each host by RemoteBinPrologue, or an
// absolute path (-remote-bin); a row's own bin column wins, and local
// rows without one run SelfBin.
// pipelineArgs are push-down commands split on "+". shardField, if
// non-empty, adds a provenance field to each record with the value
// "host:path".
func ProcessCatalogShards(entries []CatalogEntry, remoteBin string, shardField string, pipelineArgs [][]string) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		for _, entry := range entries {
			if catalogLocalParquet(entry, pipelineArgs) {
				// DFC149 §4.4: a local Parquet shard is read in this
				// process; an error is the shard's, reported by the
				// caller's ssql.Run.
				stopped, err := readLocalParquetShard(entry, shardField, yield)
				if err != nil {
					panic(fmt.Errorf("shard %s:%s: %w", entry.Host, entry.Path, err))
				}
				if stopped {
					return
				}
				continue
			}
			remoteCmd := BuildRemoteCommand(shardBin(entry, remoteBin), entry.Path, entry.Format, pipelineArgs)

			var cmd *exec.Cmd
			if IsLocalHost(entry.Host) {
				cmd = exec.Command("bash", "-c", remoteCmd)
			} else {
				cmd = exec.Command("ssh", entry.Host, remoteCmd)
			}
			cmd.Stderr = os.Stderr

			stdout, err := cmd.StdoutPipe()
			if err != nil {
				fmt.Fprintf(os.Stderr, "shard %s:%s pipe: %v\n", entry.Host, entry.Path, err)
				continue
			}
			if err := cmd.Start(); err != nil {
				fmt.Fprintf(os.Stderr, "shard %s:%s start: %v\n", entry.Host, entry.Path, err)
				continue
			}

			records := readCatalogJSONL(stdout)
			stopped := false
			checked := false
			for r := range records {
				if !checked {
					checked = true
					if err := hiveClash(r, entry); err != nil {
						panic(fmt.Errorf("shard %s:%s: %w", entry.Host, entry.Path, err)) // the caller's ssql.Run reports it
					}
				}
				r = enrichCatalogRecord(r, entry, shardField)
				if !yield(r) {
					stopped = true
					break
				}
			}

			if err := cmd.Wait(); err != nil {
				fmt.Fprintf(os.Stderr, "shard %s:%s: %v\n", entry.Host, entry.Path, err)
			}

			if stopped {
				return
			}
		}
	}
}

// ShellQuote quotes a string for safe use in shell commands.
// Returns the string unquoted if it contains only safe characters,
// otherwise wraps in single quotes (using double-quote escaping for
// strings that contain single quotes).
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	needsQuoting := false
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == '/' || c == ':') {
			needsQuoting = true
			break
		}
	}
	if !needsQuoting {
		return s
	}
	if strings.Contains(s, "'") {
		escaped := strings.ReplaceAll(s, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		escaped = strings.ReplaceAll(escaped, `$`, `\$`)
		escaped = strings.ReplaceAll(escaped, "`", "\\`")
		return `"` + escaped + `"`
	}
	return "'" + s + "'"
}

// SplitOnPlus splits a slice of strings on "+" delimiter tokens.
// For example: ["where", "-if", "age", "gt", "25", "+", "group-by", "dept"]
// becomes: [["where", "-if", "age", "gt", "25"], ["group-by", "dept"]]
func SplitOnPlus(args []string) [][]string {
	var groups [][]string
	var current []string
	for _, arg := range args {
		if arg == "+" {
			if len(current) > 0 {
				groups = append(groups, current)
				current = nil
			}
		} else {
			current = append(current, arg)
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// BuildRemoteCommand constructs a remote ssql command string.
// remoteBin is a bare name ("ssql" or "ssql_gpu"), resolved on the
// remote by RemoteBinPrologue, or an absolute path used as given. path
// is the data file path.
// format is the data format (empty or "csv" uses bare "from path",
// others use "from FORMAT path"). pipelineGroups are push-down
// commands (each group becomes "| remoteBin group...").
// validFormats is the set of allowed format subcommands for remote execution.
var validFormats = map[string]bool{
	"csv": true, "tsv": true, "json": true, "jsonl": true,
	"arrow": true, "wav": true, "xlsx": true,
}

// isGlob returns true if the path contains shell glob characters.
func isGlob(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func BuildRemoteCommand(remoteBin, path, format string, pipelineGroups [][]string) string {
	prologue, bin := remoteBinWord(remoteBin)
	remoteBin = bin
	var remoteCmd string

	if format != "" && format != "csv" && validFormats[format] {
		remoteCmd = remoteBin + " from " + format + " " + ShellQuote(path)
	} else {
		// Unknown formats are ignored — use bare "from path" which infers from extension
		remoteCmd = remoteBin + " from " + ShellQuote(path)
	}
	// The local pipeline's zone, not the remote machine's (DFC147 §5.7).
	if z := StreamZoneName(); z != "" {
		remoteCmd += " -tz " + ShellQuote(z)
	}
	for _, group := range pipelineGroups {
		quoted := make([]string, len(group))
		for i, arg := range group {
			quoted[i] = ShellQuote(arg)
		}
		remoteCmd += " | " + remoteBin + " " + strings.Join(quoted, " ")
	}
	return prologue + remoteCmd
}

// readCatalogJSONL reads JSONL from a reader, skipping _schema header lines.
func readCatalogJSONL(r io.Reader) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			// Skip schema header lines
			if len(line) > 10 && strings.Contains(string(line[:20]), `"_schema"`) {
				continue
			}

			record, err := ParseJSONLine(line)
			if err != nil {
				continue
			}
			if !yield(record.Freeze()) {
				return
			}
		}
	}
}

func enrichCatalogRecord(r Record, entry CatalogEntry, shardField string) Record {
	names, values := CatalogRecordMetadata(entry, shardField)
	if len(names) == 0 {
		return r
	}
	mut := r.ToMutable()
	for i, k := range names {
		mut = mut.String(k, values[i])
	}
	return mut.Freeze()
}

// isCatalogRangeColumn: a _from/_to column prunes and is not a field.
func isCatalogRangeColumn(k string) bool {
	return strings.HasSuffix(k, "_from") || strings.HasSuffix(k, "_to")
}
