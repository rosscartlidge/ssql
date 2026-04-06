package ssql

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CatalogEntry represents one shard in a catalog.
type CatalogEntry struct {
	Host     string
	Path     string
	Format   string
	Metadata map[string]string
}

// CatalogFilter represents a partition pruning condition.
type CatalogFilter struct {
	Field    string
	Operator string
	Value    string
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
	formatIdx := -1
	for i, h := range headers {
		switch strings.TrimSpace(strings.ToLower(h)) {
		case "host":
			hostIdx = i
		case "path":
			pathIdx = i
		case "format":
			formatIdx = i
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
		for i, h := range headers {
			h = strings.TrimSpace(strings.ToLower(h))
			if h == "host" || h == "path" || h == "format" || h == "fields" {
				continue
			}
			if i < len(row) {
				entry.Metadata[h] = strings.TrimSpace(row[i])
			}
		}
		entries = append(entries, entry)
	}

	return entries, nil
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
			if !catalogRangeMatches(fromVal, toVal, f.Operator, f.Value) {
				return false
			}
			continue
		}

		if colVal, ok := entry.Metadata[f.Field]; ok {
			if !catalogStringOp(colVal, f.Operator, f.Value) {
				return false
			}
			continue
		}
	}
	return true
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
				Metadata: entry.Metadata, // shared — metadata applies to all expanded files
			}
			result = append(result, expanded)
		}
	}
	return result
}

// WriteCatalog writes catalog entries as CSV.
func WriteCatalog(w io.Writer, entries []CatalogEntry) error {
	writer := csv.NewWriter(w)

	// Collect all metadata keys
	metaKeys := make(map[string]bool)
	for _, e := range entries {
		for k := range e.Metadata {
			metaKeys[k] = true
		}
	}
	var sortedKeys []string
	for k := range metaKeys {
		sortedKeys = append(sortedKeys, k)
	}

	// Headers
	headers := []string{"host", "path"}
	if len(sortedKeys) > 0 {
		headers = append(headers, sortedKeys...)
	}
	if err := writer.Write(headers); err != nil {
		return err
	}

	// Rows
	for _, e := range entries {
		row := []string{e.Host, e.Path}
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

// ProcessCatalogShards connects to each shard via SSH (or locally for
// host "local"/"localhost") and returns a concatenated record stream.
// remoteBin is "ssql" or "ssql_gpu". pipelineArgs are push-down commands
// split on "+". shardField, if non-empty, adds a provenance field to
// each record with the value "host:path".
func ProcessCatalogShards(entries []CatalogEntry, remoteBin string, shardField string, pipelineArgs [][]string) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		for _, entry := range entries {
			remoteCmd := BuildRemoteCommand(remoteBin, entry.Path, entry.Format, pipelineArgs)

			var cmd *exec.Cmd
			if entry.Host == "local" || entry.Host == "localhost" {
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
			for r := range records {
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
// remoteBin is "ssql" or "ssql_gpu". path is the data file path.
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

// inferFormat returns the format from a file extension (e.g., ".csv" → "csv").
func inferFormat(path string) string {
	// Strip glob characters to find the extension
	clean := strings.TrimRight(path, "*?[]")
	if i := strings.LastIndex(clean, "."); i >= 0 {
		ext := strings.ToLower(clean[i+1:])
		if validFormats[ext] {
			return ext
		}
	}
	return "csv" // default
}

func BuildRemoteCommand(remoteBin, path, format string, pipelineGroups [][]string) string {
	var remoteCmd string

	if isGlob(path) {
		// Glob path: don't quote (let bash expand), use explicit format for multi-file
		if format == "" {
			format = inferFormat(path)
		}
		remoteCmd = remoteBin + " from " + format + " " + path
	} else if format != "" && format != "csv" && validFormats[format] {
		remoteCmd = remoteBin + " from " + format + " " + ShellQuote(path)
	} else {
		// Unknown formats are ignored — use bare "from path" which infers from extension
		remoteCmd = remoteBin + " from " + ShellQuote(path)
	}
	for _, group := range pipelineGroups {
		quoted := make([]string, len(group))
		for i, arg := range group {
			quoted[i] = ShellQuote(arg)
		}
		remoteCmd += " | " + remoteBin + " " + strings.Join(quoted, " ")
	}
	return remoteCmd
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
	if shardField == "" && len(entry.Metadata) == 0 {
		return r
	}

	mut := r.ToMutable()
	if shardField != "" {
		mut = mut.String(shardField, entry.Host+":"+entry.Path)
	}
	for k, v := range entry.Metadata {
		if strings.HasSuffix(k, "_from") || strings.HasSuffix(k, "_to") {
			continue
		}
		if v != "" {
			mut = mut.String(k, v)
		}
	}
	return mut.Freeze()
}
