//go:build !slim

package commands

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
)

// RegisterLsp registers `ssql lsp`, a language server for the ssql
// pipelines in shell files (DFC167). It speaks the Language Server
// Protocol over stdio and knows no command grammar of its own: every
// answer comes from a protocol the commands already serve to the bash
// bindings, `serve` and the playground — autocli completion, the Ctrl-O
// upstream schema and value sample, the Alt-h help, `run -check`'s
// stage-by-stage validation, `generate pretty`. What is new is the map
// from a position in multi-line shell text to the one-line text those
// protocols take (lsp_text.go).
func RegisterLsp(cmd *cf.CommandBuilder) *cf.CommandBuilder {
	cmd.Subcommand("lsp").
		Description("Serve the Language Server Protocol on stdio: completion, help, checks and layout for the ssql pipelines in shell files").
		ClauseDescription("An editor starts it. Completion offers commands, flags, and the fields and values flowing in from upstream; hover shows a flag's help; a stage that would be refused is marked while you type; formatting lays a pipeline out one stage to a line").
		Example("mkdir -p ~/.config/nvim && ssql lsp -nvim-config >> ~/.config/nvim/init.lua", "Start it for shell files in Neovim 0.11 or later").
		Example("VISUAL=nvim bash", "With `eval \"$(ssql -edit-keybinding)\"`, v (or Ctrl-X Ctrl-E) opens the pipeline in Neovim with the server attached").
		Example("ssql lsp", "Run the server; an editor starts it and speaks JSON-RPC on stdin and stdout").

		Flag("-nvim-config").
			Bool().
			Global().
			Help("Print the Neovim configuration that starts the server for sh and bash buffers, and exit").
			Done().

		Handler(func(ctx *cf.Context) error {
			if v, _ := ctx.GlobalFlags["-nvim-config"].(bool); v {
				fmt.Fprint(ctx.Stdout(), lspNvimConfig)
				return nil
			}
			return serveLSP(ctx.Command, os.Stdin, os.Stdout)
		}).
		Done()
	return cmd
}

// lspNvimConfig starts the server for shell buffers; bash's editor temp
// file (bash-fc-…) is one, so the editor key's buffer gets it too.
const lspNvimConfig = `-- ssql language server (ssql lsp -nvim-config): completion, hover (K),
-- checks and formatting (gq) for the ssql pipelines in shell files.
vim.lsp.config('ssql', {
  cmd = { 'ssql', 'lsp' },
  filetypes = { 'sh', 'bash' },
  root_dir = function(_, on_dir) on_dir(vim.fn.getcwd()) end,
})
vim.lsp.enable('ssql')
`

// lspDiagnosticDelay is how long the server waits after the last change
// before checking a document.
const lspDiagnosticDelay = 500 * time.Millisecond

// lspRunTimeout bounds every pipeline the server runs to answer a request
// (an upstream's schema, a value sample, a check).
const lspRunTimeout = 5 * time.Second

type lspServer struct {
	root  *cf.Command
	self  string
	width int

	wmu sync.Mutex // one message at a time on out
	out io.Writer

	dmu  sync.Mutex
	docs map[string]*lspDoc
}

type lspDoc struct {
	text    string
	version int
	timer   *time.Timer
}

type lspPos struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPos `json:"start"`
	End   lspPos `json:"end"`
}

type lspMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type lspTextDocument struct {
	URI     string `json:"uri"`
	Text    string `json:"text"`
	Version int    `json:"version"`
}

type lspPositionParams struct {
	TextDocument lspTextDocument `json:"textDocument"`
	Position     lspPos          `json:"position"`
	Range        *lspRange       `json:"range,omitempty"`
}

// serveLSP answers requests from in until `exit`. Requests are handled in
// order on this goroutine; checks run after a quiet spell on their own.
// Anything a command writes to stdout while answering would corrupt the
// protocol stream, so os.Stdout points at stderr while the server runs.
func serveLSP(root *cf.Command, in io.Reader, out *os.File) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("ssql lsp: %w", err)
	}
	s := &lspServer{root: root, self: self, width: PrettyLineWidth, out: out, docs: map[string]*lspDoc{}}
	saved := os.Stdout
	os.Stdout = os.Stderr
	defer func() { os.Stdout = saved }()
	r := bufio.NewReader(in)
	for {
		body, err := readLSPMessage(r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("ssql lsp: %w", err)
		}
		var m lspMessage
		if err := json.Unmarshal(body, &m); err != nil {
			s.reply(nil, nil, &lspError{-32700, "parse error: " + err.Error()})
			continue
		}
		if m.Method == "exit" {
			return nil
		}
		result, rerr := s.handle(m)
		if len(m.ID) > 0 {
			s.reply(m.ID, result, rerr)
		}
	}
}

type lspError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func readLSPMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" && length < 0 {
				return nil, io.EOF
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			if length, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
				return nil, fmt.Errorf("bad Content-Length %q", v)
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("a message without Content-Length")
	}
	body := make([]byte, length)
	_, err := io.ReadFull(r, body)
	return body, err
}

func (s *lspServer) send(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n%s", len(data), data)
}

func (s *lspServer) reply(id json.RawMessage, result any, rerr *lspError) {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if id == nil {
		msg["id"] = nil
	}
	if rerr != nil {
		msg["error"] = rerr
	} else {
		msg["result"] = result
	}
	s.send(msg)
}

func (s *lspServer) notify(method string, params any) {
	s.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *lspServer) handle(m lspMessage) (any, *lspError) {
	switch m.Method {
	case "initialize":
		var p struct {
			RootURI               string `json:"rootUri"`
			InitializationOptions struct {
				Textwidth int `json:"textwidth"`
			} `json:"initializationOptions"`
		}
		_ = json.Unmarshal(m.Params, &p)
		// Relative file names in a pipeline are the shell's working
		// directory's; the editor's root is that directory.
		if dir := uriPath(p.RootURI); dir != "" {
			_ = os.Chdir(dir)
		}
		if w := p.InitializationOptions.Textwidth; w >= 40 {
			s.width = w
		}
		return map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":                1, // the whole document on every change
				"completionProvider":              map[string]any{"triggerCharacters": []string{"-"}},
				"hoverProvider":                   true,
				"documentFormattingProvider":      true,
				"documentRangeFormattingProvider": true,
			},
			"serverInfo": map[string]any{"name": "ssql", "version": strings.TrimSpace(version.Version)},
		}, nil
	case "initialized", "$/cancelRequest", "$/setTrace", "workspace/didChangeConfiguration", "textDocument/didSave":
		return nil, nil
	case "shutdown":
		return nil, nil
	case "textDocument/didOpen", "textDocument/didChange":
		var p struct {
			TextDocument   lspTextDocument `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, nil
		}
		text := p.TextDocument.Text
		if m.Method == "textDocument/didChange" {
			if len(p.ContentChanges) == 0 {
				return nil, nil
			}
			text = p.ContentChanges[len(p.ContentChanges)-1].Text
		}
		s.update(p.TextDocument.URI, text, p.TextDocument.Version)
		return nil, nil
	case "textDocument/didClose":
		var p lspPositionParams
		if json.Unmarshal(m.Params, &p) == nil {
			s.dmu.Lock()
			if d := s.docs[p.TextDocument.URI]; d != nil && d.timer != nil {
				d.timer.Stop()
			}
			delete(s.docs, p.TextDocument.URI)
			s.dmu.Unlock()
			s.notify("textDocument/publishDiagnostics", map[string]any{"uri": p.TextDocument.URI, "diagnostics": []any{}})
		}
		return nil, nil
	case "textDocument/completion", "textDocument/hover", "textDocument/formatting", "textDocument/rangeFormatting":
		var p lspPositionParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, &lspError{-32602, err.Error()}
		}
		text, ok := s.text(p.TextDocument.URI)
		if !ok {
			return nil, nil
		}
		switch m.Method {
		case "textDocument/completion":
			return s.completion(text, lspOffset(text, p.Position.Line, p.Position.Character)), nil
		case "textDocument/hover":
			return s.hover(text, lspOffset(text, p.Position.Line, p.Position.Character)), nil
		default:
			from, to := 0, len(text)
			if p.Range != nil {
				from = lspOffset(text, p.Range.Start.Line, p.Range.Start.Character)
				to = lspOffset(text, p.Range.End.Line, p.Range.End.Character)
			}
			return s.format(text, from, to), nil
		}
	}
	if len(m.ID) > 0 {
		return nil, &lspError{-32601, "method not found: " + m.Method}
	}
	return nil, nil
}

func uriPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return u.Path
}

func (s *lspServer) text(uri string) (string, bool) {
	s.dmu.Lock()
	defer s.dmu.Unlock()
	d, ok := s.docs[uri]
	if !ok {
		return "", false
	}
	return d.text, true
}

// update stores a document's new text and checks it once changes pause.
func (s *lspServer) update(uri, text string, version int) {
	s.dmu.Lock()
	defer s.dmu.Unlock()
	d := s.docs[uri]
	if d == nil {
		d = &lspDoc{}
		s.docs[uri] = d
	}
	d.text, d.version = text, version
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(lspDiagnosticDelay, func() { s.publishDiagnostics(uri, version) })
}

func (s *lspServer) rangeOf(text string, from, to int) lspRange {
	sl, sc := lspPosition(text, from)
	el, ec := lspPosition(text, to)
	return lspRange{lspPos{sl, sc}, lspPos{el, ec}}
}

// ---- completion -----------------------------------------------------

// needsShellQuote: a value the shell would split or expand, which a
// completion inserts single-quoted (the Ctrl-O rule).
var needsShellQuote = regexp.MustCompile(`[^A-Za-z0-9._/:@%+=,-]`)

// completion answers at doc offset off the way Tab and Ctrl-O answer on
// the joined line: autocli's own candidates; for a field slot, the
// fields of the schema flowing in from upstream; for a value slot,
// values sampled from the upstream; in an expression, upstream fields
// and functions.
func (s *lspServer) completion(text string, off int) any {
	l, ok := logicalAt(logicalCommands(text), off)
	if !ok {
		return []any{}
	}
	j := l.joinedAt(off)
	before := l.joined[:j]
	stage := CursorTopLevelStage(before)
	toks, open := tokenizeStageOpen(stage)
	if items := s.stageStart(text, l, off, j, toks, open); items != nil {
		return items
	}
	if len(toks) == 0 || (toks[0] != "ssql" && toks[0] != "ssql_gpu") {
		return []any{}
	}
	args := toks[1:]
	if !open && (strings.HasSuffix(before, " ") || strings.HasSuffix(before, "\t")) {
		args = append(args, "")
	}
	if len(args) == 0 {
		return []any{}
	}
	pos := len(args)
	partial := args[pos-1]
	edit := s.rangeOf(text, l.docAt(j-len(partial)), off)

	items := []any{}
	add := func(label, insert string, kind int, detail string) {
		item := map[string]any{
			"label":    label,
			"kind":     kind,
			"textEdit": map[string]any{"range": edit, "newText": insert},
		}
		if detail != "" {
			item["documentation"] = map[string]any{"kind": "plaintext", "value": detail}
		}
		items = append(items, item)
	}

	// In an expression the word being typed is the identifier at the end
	// of the quoted text: upstream fields and functions complete it.
	if ExprArgAtCursor(s.root, args, pos) {
		k := len(partial)
		for k > 0 && isExprIdent(partial[k-1]) {
			k--
		}
		ident := partial[k:]
		edit = s.rangeOf(text, l.docAt(j-len(ident)), off)
		for _, f := range s.upstreamFields(before, l.joined) {
			if strings.HasPrefix(f, ident) {
				add(f, f, 5, "")
			}
		}
		for _, f := range catalogFunctionNames() {
			if strings.HasPrefix(f, ident) && ident != "" {
				entry, _ := FunctionEntry(f)
				add(f, f+"(", 3, entry)
			}
		}
		return items
	}

	cands := s.autocliComplete(args, pos, "")
	switch {
	case len(cands) == 1 && cands[0] == FieldHintToken:
		for _, f := range s.upstreamFields(before, l.joined) {
			if strings.HasPrefix(f, partial) {
				add(f, f, 5, "")
			}
		}
		return items
	case len(cands) == 1 && cands[0] == ValueHintToken:
		vpartial := strings.TrimPrefix(partial, "'")
		for _, v := range s.upstreamValues(before, l.joined, args, pos) {
			if strings.HasPrefix(v, vpartial) {
				insert := v
				if needsShellQuote.MatchString(v) {
					insert = "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
				}
				add(v, insert, 12, "")
			}
		}
		return items
	}
	for _, c := range cands {
		if c == "" || c == FieldHintToken || c == ValueHintToken || (strings.HasPrefix(c, "<") && strings.HasSuffix(c, ">")) {
			continue
		}
		kind, detail := 12, ""
		if strings.HasPrefix(c, "-") || strings.HasPrefix(c, "+") {
			kind = 14
			flagArgs := append(append([]string{}, args[:pos-1]...), c)
			if help, err := s.root.HelpAt(flagArgs, pos); err == nil {
				detail = strings.TrimSpace(help)
			}
		}
		add(c, c, kind, detail)
	}
	return items
}

// stageStart offers `ssql` where a stage starts: after a `|` or a `<(`,
// at the start of a command, or on a word that `ssql` begins with. At the
// end of a line ending in ` |` it is the next stage's first word, with
// the blank it needs. nil when the cursor is not at a stage's start.
func (s *lspServer) stageStart(text string, l shellLogical, off, j int, toks []string, open bool) []any {
	before := l.joined[:j]
	partial := ""
	switch {
	case len(toks) == 0:
	case len(toks) == 1 && !open && toks[0] != "ssql" && strings.HasPrefix("ssql", toks[0]) && strings.HasSuffix(before, toks[0]):
		partial = toks[0]
	default:
		return nil
	}
	lead := strings.TrimRight(before[:len(before)-len(partial)], " \t")
	if lead != "" && !strings.HasSuffix(lead, "|") && !strings.HasSuffix(lead, "<(") && !strings.HasSuffix(lead, "(") {
		return nil
	}
	insert := "ssql "
	if partial == "" && lead != "" && !strings.HasSuffix(before, " ") && !strings.HasSuffix(before, "(") {
		insert = " ssql "
	}
	return []any{map[string]any{
		"label":    "ssql",
		"kind":     14,
		"textEdit": map[string]any{"range": s.rangeOf(text, off-len(partial), off), "newText": insert},
	}}
}

func isExprIdent(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// autocliComplete is Tab's completion of args at pos, sampling values
// from cacheFile when one is given (the Ctrl-O protocol); requests are
// answered one at a time, so the environment is the server's to set.
func (s *lspServer) autocliComplete(args []string, pos int, cacheFile string) []string {
	prev, had := os.LookupEnv("AUTOCLI_CACHE_FILE")
	os.Setenv("AUTOCLI_CACHE_FILE", cacheFile)
	defer func() {
		if had {
			os.Setenv("AUTOCLI_CACHE_FILE", prev)
		} else {
			os.Unsetenv("AUTOCLI_CACHE_FILE")
		}
	}()
	c, err := s.root.Complete(args, pos)
	if err != nil {
		return nil
	}
	return c
}

// runsWhileTyping reports whether the server may run line's pipeline to
// answer a request: ssql commands alone, with no substitution,
// redirection or second command, and nothing that reaches another host
// (Ross, 2026-11-10: no ssh connection while typing).
func runsWhileTyping(line string) bool {
	if _, why := textReachesRemote(line); why != "" {
		return false
	}
	_, segment, _, err := SplitPipeline(line)
	return err == nil && pipelineSafeToDescribe(segment)
}

// upstreamFields is Ctrl-O's field list: the schema of the command that
// feeds the cursor (CompleteSource), run under SSQL_MODE=schema and read
// by `generate schema`.
func (s *lspServer) upstreamFields(before, line string) []string {
	src := CompleteSource(before, s.root)
	if src == "" || !runsWhileTyping(line) || !pipelineSafeToDescribe(src) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), lspRunTimeout)
	defer cancel()
	schema, err := describePipeline(ctx, src, "schema", "lsp", io.Discard)
	if err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, s.self, "generate", "schema")
	cmd.Stdin = bytes.NewReader(schema)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var fields []string
	for _, f := range strings.Split(string(out), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}

// upstreamValues is Ctrl-O's value list: the slot's completion with the
// upstream's sample (ValueSample) as the cache file.
func (s *lspServer) upstreamValues(before, line string, args []string, pos int) []string {
	if !runsWhileTyping(line) {
		return nil
	}
	src := ValueSample(before, s.root)
	if src == "" {
		return nil
	}
	var vals []string
	for _, v := range s.autocliComplete(args, pos, src) {
		if v == "" || v == FieldHintToken || v == ValueHintToken || strings.HasPrefix(v, "{") || (strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">")) {
			continue
		}
		vals = append(vals, v)
	}
	return vals
}

var (
	catalogNamesOnce sync.Once
	catalogNames     []string
	catalogNameRe    = regexp.MustCompile(`^  ([A-Za-z_][A-Za-z0-9_]*)\(`)
)

// catalogFunctionNames are the expression functions `ssql functions`
// documents, in its order.
func catalogFunctionNames() []string {
	catalogNamesOnce.Do(func() {
		seen := map[string]bool{}
		for _, l := range strings.Split(functionCatalog(), "\n") {
			if m := catalogNameRe.FindStringSubmatch(l); m != nil && !seen[m[1]] {
				seen[m[1]] = true
				catalogNames = append(catalogNames, m[1])
			}
		}
	})
	return catalogNames
}

// ---- hover ----------------------------------------------------------

// hover is Alt-h's help for the word under the cursor: the line up to the
// end of that word, through -help-at-cursor.
func (s *lspServer) hover(text string, off int) any {
	l, ok := logicalAt(logicalCommands(text), off)
	if !ok {
		return nil
	}
	j := l.joinedAt(off)
	start, end := j, j
	for end < len(l.joined) && !strings.ContainsRune(" \t|()", rune(l.joined[end])) {
		end++
	}
	for start > 0 && !strings.ContainsRune(" \t|()", rune(l.joined[start-1])) {
		start--
	}
	if start == end {
		return nil
	}
	upto := l.joined[:end]
	// On a command's name, the command's own help: Alt-h's answer there
	// is the list of every command.
	if toks := tokenizeStage(CursorTopLevelStage(upto)); len(toks) == 2 || (len(toks) == 3 && s.isSubcommandPath(toks[1], toks[2])) {
		upto += " "
	}
	help, _, code, handled := HandleCursorProtocol([]string{"-help-at-cursor", upto}, func() *cf.Command { return s.root })
	if !handled || code != 0 || strings.TrimSpace(help) == "" {
		return nil
	}
	return map[string]any{
		"contents": map[string]any{"kind": "plaintext", "value": strings.TrimRight(help, "\n")},
		"range":    s.rangeOf(text, l.docAt(start), l.docAt(end)),
	}
}

func (s *lspServer) isSubcommandPath(cmd, sub string) bool {
	sc := s.root.GetSubcommand(cmd)
	return sc != nil && sc.Subcommands[sub] != nil
}

// ---- formatting -----------------------------------------------------

// format lays out each ssql pipeline the range touches with PrettyLine,
// leaving every other line as it is. A command holding a comment is
// left alone: its joined text has no comment to put back.
func (s *lspServer) format(text string, from, to int) []any {
	edits := []any{}
	for _, l := range logicalCommands(text) {
		if l.end < from || l.start > to || l.comment {
			continue
		}
		if _, _, _, err := SplitPipeline(l.joined); err != nil {
			continue
		}
		pretty, err := PrettyLine(l.joined, s.root, s.width)
		if err != nil || pretty == text[l.start:l.end] {
			continue
		}
		edits = append(edits, map[string]any{"range": s.rangeOf(text, l.start, l.end), "newText": pretty})
	}
	return edits
}

// ---- diagnostics ----------------------------------------------------

// publishDiagnostics checks each ssql pipeline of the document, if it is
// still at version, the way `ssql run -check` checks a document: autocli's
// Check on every stage's argv (nothing runs), then each stage's field
// references against the schema flowing in (checkPipelineFields, which
// runs each command on a header alone). A pipeline whose words depend on
// the shell ($x, a glob) is not checked; one that reaches another host
// gets a hint instead of a check.
func (s *lspServer) publishDiagnostics(uri string, version int) {
	s.dmu.Lock()
	d := s.docs[uri]
	if d == nil || d.version != version {
		s.dmu.Unlock()
		return
	}
	text := d.text
	s.dmu.Unlock()

	diags := []any{}
	for _, l := range logicalCommands(text) {
		diags = append(diags, s.checkLogical(text, l)...)
	}
	s.dmu.Lock()
	current := s.docs[uri] != nil && s.docs[uri].version == version
	s.dmu.Unlock()
	if current {
		s.notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "version": version, "diagnostics": diags})
	}
}

var lspStageRe = regexp.MustCompile(`^stage (\d+) \(`)

func (s *lspServer) checkLogical(text string, l shellLogical) []any {
	spans := topLevelStageSpans(l.joined)
	var ssqlSpans [][2]int
	for _, sp := range spans {
		toks := tokenizeStage(strings.TrimSpace(l.joined[sp[0]:sp[1]]))
		if len(toks) > 0 && (toks[0] == "ssql" || toks[0] == "ssql_gpu") {
			ssqlSpans = append(ssqlSpans, sp)
		}
	}
	if len(ssqlSpans) == 0 {
		return nil
	}
	stageRange := func(sp [2]int) lspRange {
		a, b := sp[0], sp[1]
		for a < b && (l.joined[a] == ' ' || l.joined[a] == '\t') {
			a++
		}
		for b > a && (l.joined[b-1] == ' ' || l.joined[b-1] == '\t') {
			b--
		}
		return s.rangeOf(text, l.docAt(a), l.docAt(b))
	}
	diag := func(r lspRange, severity int, msg string) map[string]any {
		return map[string]any{"range": r, "severity": severity, "source": "ssql", "message": msg}
	}
	prefix, segment, _, err := SplitPipeline(l.joined)
	if err != nil {
		return []any{diag(s.rangeOf(text, l.start, l.end), 2, err.Error())}
	}
	stages, ok := docStagesFromText(segment)
	if !ok {
		return nil
	}
	// Syntax: autocli's own Check, stage by stage; nothing runs.
	for i, st := range stages {
		if i >= len(ssqlSpans) {
			break
		}
		if err := checkPipelineDoc(s.root, []docStage{st}, ""); err != nil {
			return []any{diag(stageRange(ssqlSpans[i]), 1, lspStageMessage(err.Error()))}
		}
	}
	if sp, why := textReachesRemote(l.joined); why != "" {
		return []any{diag(stageRange(sp), 4, "fields are not checked while you type: this stage "+why)}
	}
	for _, st := range stages {
		if n := st.name(s.root); n == "generate" || strings.HasPrefix(n, "generate ") || n == "serve" {
			return nil
		}
	}
	// Records from stdin (a command before the pipeline, or `-`) are not
	// here to give the source a schema: the fields cannot be checked.
	if prefix != "" || slices.ContainsFunc(stages[0], func(a docArg) bool { return a.Nested == nil && a.Text == "-" }) {
		return nil
	}
	// Fields: each command answers for the fields it reads.
	ctx, cancel := context.WithTimeout(context.Background(), lspRunTimeout)
	defer cancel()
	err = checkPipelineFields(ctx, s.root, s.self, stages, "")
	if err == nil {
		return nil
	}
	r := s.rangeOf(text, l.start, l.end)
	if m := lspStageRe.FindStringSubmatch(err.Error()); m != nil {
		if n, _ := strconv.Atoi(m[1]); n >= 1 && n <= len(ssqlSpans) {
			r = stageRange(ssqlSpans[n-1])
		}
	}
	return []any{diag(r, 1, lspStageMessage(err.Error()))}
}

// lspStageMessage reads a check's "stage N (cmd): …" as "ssql cmd: …",
// the way the stage refuses on the command line; the range already says
// which stage.
func lspStageMessage(msg string) string {
	if m := lspStagePrefixRe.FindStringSubmatchIndex(msg); m != nil {
		return "ssql " + msg[m[2]:m[3]] + ": " + msg[m[1]:]
	}
	return msg
}

var lspStagePrefixRe = regexp.MustCompile(`^stage \d+ \(([^)]*)\): `)
