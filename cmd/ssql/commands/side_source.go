package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
)

// sideCtx is what a two-input command's emitters know about the main
// input when generateSideCode calls them.
type sideCtx struct {
	inputVar, funcName string
	fragments          []*lib.CodeFragment // the main input's fragments, already passed through
	leftSchema         *lib.TypedSchema
	leftFields         []string
}

// leftType is the main input's advisory wire type of a field ("" when
// the last fragment advertises none).
func (c sideCtx) leftType(f string) string {
	if len(c.fragments) > 0 && c.fragments[len(c.fragments)-1].AdvisoryTypes != nil {
		return advisoryToSchemaType(c.fragments[len(c.fragments)-1].AdvisoryTypes[f])
	}
	return ""
}

// sideSource is a command that reads a second input from a file or a
// procsub — join and traverse. generateSideCode finds the side (code
// fragments from a procsub, or a file to read), wraps it in a
// `rightSourceN` func fragment, and hands the stage to the command's
// emitters.
type sideSource struct {
	name string // the command, for messages and the generated program's flag
	file string
	// A key that is a dotted path into a nested value has no typed form:
	// the stage falls back to record mode (DFC144 Level 2).
	leftKeys, rightKeys []string
	// typed emits the typed form, func fragment included; handled false
	// (nothing written) falls back to record.
	typed func(c sideCtx, rightSchema *lib.TypedSchema, rightFragments []*lib.CodeFragment, subCommandStr string) (handled bool, err error)
	// record emits the statement over funcName's records, after the func
	// fragment; rightFields is nil when the side's fields are unknown.
	record func(c sideCtx, rightFields []string, rightType func(string) string) error
}

// generateSideCode generates Go code for a sideSource. Two scenarios:
//  1. a regular file: a func that reads it (typed mode samples it);
//  2. a process substitution (/dev/fd/N): its code fragments, wrapped
//     into a func.
func generateSideCode(s sideSource) error {
	// Read all previous code fragments from stdin (if any)
	fragments, err := lib.ReadAllCodeFragments()
	if err != nil {
		return fmt.Errorf("reading code fragments: %w", err)
	}

	// Pass through all previous fragments
	for _, frag := range fragments {
		if err := lib.WriteCodeFragment(frag); err != nil {
			return fmt.Errorf("writing previous fragment: %w", err)
		}
	}

	c := sideCtx{inputVar: "records", fragments: fragments}
	if len(fragments) > 0 {
		c.inputVar = fragments[len(fragments)-1].Var
		c.leftSchema = fragments[len(fragments)-1].OutputTypedSchema
	}

	// Generate unique function name for the right source
	// Count existing func fragments to ensure unique naming across the pipeline
	funcCount := 1
	for _, frag := range fragments {
		if frag.Type == "func" {
			funcCount++
		}
	}
	c.funcName = fmt.Sprintf("rightSource%d", funcCount)

	pathKey := nestedPathIn(c.leftSchema, s.leftKeys...)
	// A right side whose last stage is record-shaped joins in record mode
	// too (the func returns records; it exited as "no typed schema" before).
	typedSide := func(rightSchema *lib.TypedSchema) bool {
		return typedMode() && rightSchema != nil && !pathKey && !nestedPathIn(rightSchema, s.rightKeys...)
	}

	// The fields each source kind lets generation see (typed: both
	// schemas, in the typed emitters).
	c.leftFields = fragmentFields(fragments)
	if c.leftSchema != nil {
		c.leftFields = typedFieldNames(c.leftSchema)
	}

	// No side input (traverse with -start values only): the emitters
	// run over the main input alone.
	if s.file == "" {
		if typedMode() && !pathKey {
			if c.leftSchema == nil {
				return lib.WriteErrorAndExit(getCommandString(),
					fmt.Errorf("ssql generate go -typed: '%s' must follow a typed-mode source", s.name))
			}
			if handled, err := s.typed(c, nil, nil, ""); handled || err != nil {
				return err
			}
		}
		return s.record(c, nil, noType)
	}

	// Check if the file is a non-regular file (e.g., /dev/fd/N, named pipe)
	// In generation mode, these contain code fragments from the inner command
	fileInfo, statErr := os.Stat(s.file)
	if statErr == nil && !fileInfo.Mode().IsRegular() {
		rightFragments, err := lib.ReadCodeFragmentsFromFile(s.file)
		if err == nil && len(rightFragments) > 0 {
			// Build command string from subprocess fragments
			var subCommands []string
			for _, frag := range rightFragments {
				if frag.Command != "" {
					subCommands = append(subCommands, frag.Command)
				}
			}
			subCommandStr := strings.Join(subCommands, " | ")

			if rightSchema := findOutputSchema(rightFragments); typedSide(rightSchema) {
				if c.leftSchema == nil {
					return lib.WriteErrorAndExit(getCommandString(),
						fmt.Errorf("ssql generate go -typed: '%s' must follow a typed-mode source", s.name))
				}
				if handled, err := s.typed(c, rightSchema, rightFragments, subCommandStr); handled || err != nil {
					return err
				}
				// no typed form: the record form below
			}

			// The record form reads records: a right side that ends typed
			// (typed mode, the stage itself in record mode for a path key)
			// gets the typed→record adapters appended.
			rightFields := fragmentFields(rightFragments)
			rightFragments = lib.EndInRecords(rightFragments)
			funcFrag := lib.NewFuncFragment(c.funcName, rightFragments, subCommandStr)
			if err := lib.WriteCodeFragment(funcFrag); err != nil {
				return fmt.Errorf("writing func fragment: %w", err)
			}
			return s.record(c, rightFields, noType)
		}
		// If reading fragments failed, fall through to normal file handling
	}

	// Typed-mode regular-file path: sample the right-side file ourselves
	// (a CSV/TSV right side has no nested values; only a left path key
	// sends the stage to record mode).
	if typedMode() && !pathKey {
		if c.leftSchema == nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: '%s' must follow a typed-mode source", s.name))
		}
		rightSchema, rightStructDef, readExpr, readImports, err := sideFileTypedRead(s.file, "")
		if err != nil {
			return lib.WriteErrorAndExit(getCommandString(),
				fmt.Errorf("ssql generate go -typed: '%s' on %w", s.name, err))
		}
		readCode := fmt.Sprintf("right%s := %s", rightSchema.TypeName, readExpr)
		// Build a synthesized init fragment for the right side and an
		// inline func fragment that wraps it.
		rightInit := lib.NewInitFragment(
			fmt.Sprintf("right%s", rightSchema.TypeName),
			readCode,
			readImports,
			fmt.Sprintf("ssql from %s", s.file),
		)
		rightInit.OutputTypedSchema = rightSchema
		rightInit.StructDefs = []string{rightStructDef}
		if handled, err := s.typed(c, rightSchema, []*lib.CodeFragment{rightInit}, fmt.Sprintf("ssql from %s", s.file)); handled || err != nil {
			return err
		}
		// no typed form: fall through to the record form below
	}

	// For regular files, create a simple func fragment that reads the file
	// Build init fragment for the file read - detect file type by extension
	params := []lib.CodeParam{
		{Name: s.name, Default: s.file, Help: s.name + " file", VarName: "flagJoin"},
	}
	// A right-hand file that cannot be opened is a fatal error, exactly
	// as in exec mode — until v4.92.0 this read `return nil`, so a
	// missing file was an empty right side (an inner join emitted
	// nothing, a left join matched nothing) with exit status 0.
	// .jsonl goes through the schema-aware reader: a `_schema` header
	// line (ssql tee output) is not a record.
	imports := []string{"fmt", "os"}
	lower := strings.ToLower(s.file)
	initCode, delimited, err := sideDelimReadCode("records", "*flagJoin", s.file)
	if err != nil {
		return lib.WriteErrorAndExit(getCommandString(), fmt.Errorf("%s: %w", s.name, err))
	}
	switch {
	case delimited:
	case strings.HasSuffix(lower, ".jsonl"):
		initCode = `joinHandle, err := os.Open(*flagJoin)
	if err != nil {
		return fmt.Errorf("opening %s: %w", *flagJoin, err)
	}
	// closed when the reader is done, NOT when this function returns: the
	// reader is lazy (a deferred Close here truncated large side files)
	records := ssql.CloseWhenDone(lib.ReadJSONLWithSchema(joinHandle).Records, joinHandle)`
		imports = append(imports, "github.com/rosscartlidge/ssql/v4/cmd/ssql/lib", "github.com/rosscartlidge/ssql/v4")
	case strings.HasSuffix(lower, ".json"):
		initCode = joinReadTemplate("ReadJSON")
	default:
		initCode = joinReadTemplate("ReadCSV")
	}
	initFrag := lib.NewInitFragment("records", initCode, imports, fmt.Sprintf("ssql from %s", s.file))
	initFrag.Params = params

	// Create func fragment with just the init
	funcFrag := lib.NewFuncFragment(c.funcName, []*lib.CodeFragment{initFrag}, fmt.Sprintf("ssql from %s", s.file))
	if err := lib.WriteCodeFragment(funcFrag); err != nil {
		return fmt.Errorf("writing func fragment: %w", err)
	}

	rightType := noType
	var rightFields []string
	if _, schema, err := readAuxInput(s.file); err == nil && schema != nil && len(schema.Fields) > 0 {
		rightFields, rightType = schema.Fields, schema.TypeOf
	}
	return s.record(c, rightFields, rightType)
}
