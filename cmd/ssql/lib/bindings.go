package lib

import (
	"fmt"
	"go/scanner"
	"go/token"
	"strings"
)

// ResolveBindings is the assembler-side variable-binding pass (DFC123
// slice 1, DFC099 §4b): it walks the fragment chain in order and
// assigns guaranteed-unique final variable names, treating Var/Input
// as bindings rather than literal identifiers.
//
// Commands emit their readable base name ("filtered", "included", …)
// without checking the upstream stream; when two fragments pick the
// same name, this pass renames the later one ("included" →
// "included2") and rewrites every downstream reference. A reference
// always resolves to the MOST RECENT binding of that name before the
// referencing fragment — the same shadowing a reader assumes.
//
// This retires the per-process uniqueVarName heuristic (v4.50.1):
// that worked but required every command to remember to call it — 28
// call sites of manual discipline. Here collisions are structurally
// impossible regardless of what commands emit.
//
// CONTRACT on fragment Code: the fragment declares Var exactly once,
// and that declaration is the FIRST occurrence of the identifier in
// the snippet (the universal `out := F(in)` shape, closures
// included). When Var and Input share a name, that first occurrence
// is the new binding and every later occurrence is the input.
//
// Both Code and AltCodeIfSeq are rewritten: the typed planner may
// swap AltCodeIfSeq in AFTER this pass runs, so the alternative
// template must carry the same final names.
func ResolveBindings(fragments []*CodeFragment) {
	used := make(map[string]bool, len(fragments))
	env := make(map[string]string, len(fragments)) // emitted name → current final name

	// bindPlaceholder never collides with emitted names: commands
	// emit plain Go base names, and this is reserved.
	const bindPlaceholder = "__ssql_bind_tmp__"

	for _, f := range fragments {
		// The input's final name under the environment (identity when
		// the upstream binding kept its emitted name).
		inputFinal := f.Input
		if final, ok := env[f.Input]; ok {
			inputFinal = final
		}

		if f.Var != "" && f.Var == f.Input {
			// One identifier, two bindings: the first occurrence is
			// the definition (new binding), the rest reference the
			// upstream one.
			final := freshName(f.Var, used)
			f.Code = renameSharedIdent(f.Code, f.Var, final, inputFinal)
			if f.AltCodeIfSeq != "" {
				f.AltCodeIfSeq = renameSharedIdent(f.AltCodeIfSeq, f.Var, final, inputFinal)
			}
			f.Input = inputFinal
			env[f.Var] = final
			f.Var = final
			used[final] = true
			continue
		}

		// Distinct names: rename the definition to a placeholder
		// first so the input rewrite can never capture it (the
		// input's FINAL name may equal the fragment's emitted Var).
		if f.Var != "" {
			f.Code = renameIdent(f.Code, f.Var, bindPlaceholder)
			if f.AltCodeIfSeq != "" {
				f.AltCodeIfSeq = renameIdent(f.AltCodeIfSeq, f.Var, bindPlaceholder)
			}
		}
		if f.Input != "" && inputFinal != f.Input {
			f.Code = renameIdent(f.Code, f.Input, inputFinal)
			if f.AltCodeIfSeq != "" {
				f.AltCodeIfSeq = renameIdent(f.AltCodeIfSeq, f.Input, inputFinal)
			}
		}
		f.Input = inputFinal
		if f.Var != "" {
			final := freshName(f.Var, used)
			f.Code = renameIdent(f.Code, bindPlaceholder, final)
			if f.AltCodeIfSeq != "" {
				f.AltCodeIfSeq = renameIdent(f.AltCodeIfSeq, bindPlaceholder, final)
			}
			env[f.Var] = final
			f.Var = final
			used[final] = true
		}
	}
}

// freshName returns base if unused, else base2, base3, …
func freshName(base string, used map[string]bool) string {
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		if cand := fmt.Sprintf("%s%d", base, i); !used[cand] {
			return cand
		}
	}
}

// renameIdent replaces every occurrence of the identifier old with new
// in a Go code snippet, using go/scanner so string literals, comments,
// and longer identifiers that merely contain old are never touched.
// Selector fields (x.old) are also left alone — a variable rename must
// not rewrite field accesses that happen to share the name.
func renameIdent(code, old, new string) string {
	return renameIdentFunc(code, old, func(int) string { return new })
}

// renameSharedIdent handles the Var==Input shape: the first occurrence
// of name is the definition (→ defName), all later occurrences are
// references to the upstream binding (→ refName).
func renameSharedIdent(code, name, defName, refName string) string {
	return renameIdentFunc(code, name, func(occurrence int) string {
		if occurrence == 0 {
			return defName
		}
		return refName
	})
}

// renameIdentFunc is the scanner core: replacement chosen per
// occurrence index (0-based, counting only real identifier tokens —
// never selector fields, strings, or comments).
func renameIdentFunc(code, old string, replacement func(occurrence int) string) string {
	if !strings.Contains(code, old) {
		return code
	}
	src := []byte(code)
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	// nil error handler: fragments are token-valid Go; scanning is
	// best-effort and an unrecognized rune just doesn't match IDENT.
	s.Init(file, src, nil, scanner.ScanComments)

	var out strings.Builder
	last := 0
	occ := 0
	prevTok := token.ILLEGAL
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.IDENT && lit == old && prevTok != token.PERIOD {
			off := file.Offset(pos)
			out.Write(src[last:off])
			out.WriteString(replacement(occ))
			last = off + len(old)
			occ++
		}
		prevTok = tok
	}
	if occ == 0 {
		return code
	}
	out.Write(src[last:])
	return out.String()
}
