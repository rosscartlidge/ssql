package commands

// The pipeline's time zone (DFC147 §5.7): chosen once, where the stream
// starts, and carried in the `_schema` header. A source command takes
// `-tz ZONE` (else TZ, else /etc/localtime — ssql.ResolveZone), pins it
// for the process, and writes it into its header; under SSQL_MODE it
// also materialises the resolved name into its fragment — `-tz NAME` in
// the stage's argv, so generate json/ssql/sql see it, and a statement
// in the generated program — so every serialisation of the pipeline
// names the zone it was resolved under. Downstream commands adopt the
// header's zone when they read it (lib.ReadJSONLWithSchema) and never
// read their own environment.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// zoneFlag adds -tz ZONE to a source command.
func zoneFlag(b *cf.SubcommandBuilder) *cf.SubcommandBuilder {
	return b.
		Flag("-tz").
		String().
		Global().
		Default("").
		Completer(cf.CompletionFunc(completeZones)).
		Help("Time zone the pipeline reads calendars in (hour(ts), date_trunc, strftime): an IANA name such as Australia/Sydney or UTC. Default: TZ, then /etc/localtime. Carried in the stream's _schema header").
		Done()
}

// resolveFromZone pins the pipeline zone from -tz, or resolves it from
// the environment, failing loudly when no IANA name can be found.
func resolveFromZone(ctx *cf.Context) error {
	flag, _ := ctx.GlobalFlags["-tz"].(string)
	name, _, err := ssql.ResolveZone(flag)
	if err != nil {
		return err
	}
	if flag != "" {
		return ssql.PinStreamZone(name)
	}
	// Not pinned: a header the source then reads (from jsonl on a tee'd
	// file) may still name the zone; the environment is the fallback the
	// lazy StreamZone resolution applies.
	return nil
}

// fromInitFragment is lib.NewInitFragment for a source: the fragment
// carries the resolved zone in its argv (so every generate target sees
// `-tz NAME` and the Go assemblers set it in the program) and as a plan
// note for -explain.
func fromInitFragment(varName, code string, imports []string) *lib.CodeFragment {
	frag := lib.NewInitFragment(varName, code, imports, getCommandString())
	name := ssql.StreamZoneName()
	if name == "" {
		return frag
	}
	if frag.Op != nil && !argvHasFlag(frag.Op.Argv, "-tz") {
		frag.Op.Argv = append(frag.Op.Argv, "-tz", name)
		frag.Command += " -tz " + ssql.ShellQuote(name)
	}
	// The generated program's zone statement is the assemblers' (they
	// read `-tz` from the first source's argv — lib.PipelineZone — so
	// every template of a source, parallel or serial, is covered).
	frag.PlanNotes = append(frag.PlanNotes, "zone: "+name)
	return frag
}

func argvHasFlag(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag {
			return true
		}
	}
	return false
}

// peekHeaderZone adopts the zone named by a local JSONL file's `_schema`
// header without reading the data — the generate path of a JSON source
// does not open the file, but the serialised pipeline must still carry
// the zone a tee'd stream was written under.
func peekHeaderZone(filename string) {
	if filename == "" || ssql.IsHTTPURL(filename) {
		return
	}
	f, err := os.Open(filename)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	n, _ := f.Read(buf)
	line := buf[:n]
	if i := strings.IndexByte(string(line), '\n'); i >= 0 {
		line = line[:i]
	}
	if schema, ok := lib.ParseSchemaHeaderFromBytes(line); ok {
		ssql.AdoptStreamZone(schema.TZ)
	}
}

// completeZones lists IANA zone names from the system zoneinfo tree
// (ZONEINFO, then /usr/share/zoneinfo) matching the partial; UTC first.
func completeZones(ctx cf.CompletionContext) ([]string, error) {
	roots := []string{os.Getenv("ZONEINFO"), "/usr/share/zoneinfo", "/usr/lib/zoneinfo", "/usr/share/lib/zoneinfo"}
	var out []string
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" {
			continue
		}
		if _, err := os.Stat(root); err != nil {
			continue
		}
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if d.IsDir() {
				if rel != "." && (rel == "posix" || rel == "right" || rel == "Etc" || strings.HasPrefix(rel, ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			// Zone names start with a capital (Australia/Sydney, UTC); the
			// rest of the tree is data files (zone.tab, leapseconds, …).
			if rel == "" || rel[0] < 'A' || rel[0] > 'Z' || strings.Contains(rel, ".") || rel == "Factory" {
				return nil
			}
			if strings.HasPrefix(rel, ctx.Partial) && !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
			return nil
		})
		break
	}
	sort.Strings(out)
	if strings.HasPrefix("UTC", ctx.Partial) && !seen["UTC"] {
		out = append([]string{"UTC"}, out...)
	}
	return out, nil
}
