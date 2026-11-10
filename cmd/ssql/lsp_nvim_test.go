package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// lspNvimScript runs inside Neovim: it waits for the ssql server to
// attach to the buffer, asks for completion at a field slot and for
// hover on a flag through the built-in client, formats the buffer, and
// writes what it got as JSON. The configuration is the one
// `ssql lsp -nvim-config` prints, with the test's binary.
const lspNvimScript = `
local bin, out = vim.env.SSQL_LSP_BIN, vim.env.SSQL_LSP_OUT
local ok = vim.wait(10000, function() return #vim.lsp.get_clients({ bufnr = 0, name = 'ssql' }) > 0 end, 50)
local result = { filetype = vim.bo.filetype, attached = ok }
if ok then
  local client = vim.lsp.get_clients({ bufnr = 0, name = 'ssql' })[1]
  local function at(line, char)
    return { textDocument = vim.lsp.util.make_text_document_params(0), position = { line = line, character = char } }
  end
  -- line 1 is "  ssql where -if " : the cursor after "-if "
  local resp = client:request_sync('textDocument/completion', at(1, 17), 10000, 0)
  result.completion = {}
  for _, item in ipairs((resp and resp.result) or {}) do table.insert(result.completion, item.label) end
  local hov = client:request_sync('textDocument/hover', at(1, 15), 10000, 0)
  result.hover = hov and hov.result and hov.result.contents.value or ''
  vim.lsp.buf.format({ bufnr = 0, async = false, timeout_ms = 10000 })
  result.formatted = table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), '\n')
end
local f = io.open(out, 'w')
f:write(vim.json.encode(result))
f:close()
vim.cmd('qa!')
`

// TestLSPInNeovim: the server through Neovim's built-in client, on a file
// named as bash's editor key names its temp file (bash-fc-…), so the
// filetype that starts the server is the one the key's buffer gets.
// Skipped when nvim is not installed (or is older than 0.11).
func TestLSPInNeovim(t *testing.T) {
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("nvim not installed")
	}
	bin := corpusBin(t)
	dir := t.TempDir()
	for name, body := range prettyFixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := exec.Command(bin, "lsp", "-nvim-config").Output()
	if err != nil {
		t.Fatal(err)
	}
	initLua := strings.Replace(string(cfg), "cmd = { 'ssql', 'lsp' }", "cmd = { vim.env.SSQL_LSP_BIN, 'lsp' }", 1)
	if initLua == string(cfg) {
		t.Fatal("the -nvim-config snippet no longer starts the server with cmd = { 'ssql', 'lsp' }")
	}
	for name, body := range map[string]string{"init.lua": initLua, "check.lua": lspNvimScript} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(dir, "bash-fc-12345")
	if err := os.WriteFile(file, []byte("ssql from csv emp.csv |\n  ssql where -if \nssql from csv emp.csv | ssql where -if dept eq eng | ssql include name dept | ssql to csv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "result.json")
	cmd := exec.Command(nvim, "--headless", "--clean", "-u", filepath.Join(dir, "init.lua"), file, "-c", "luafile "+filepath.Join(dir, "check.lua"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SSQL_LSP_BIN="+bin, "SSQL_LSP_OUT="+out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nvim: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("nvim wrote no result: %v", err)
	}
	var r struct {
		Filetype   string   `json:"filetype"`
		Attached   bool     `json:"attached"`
		Completion []string `json:"completion"`
		Hover      string   `json:"hover"`
		Formatted  string   `json:"formatted"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	if !r.Attached {
		t.Fatalf("the server did not attach to a %q buffer (Neovim 0.11 or later is needed)", r.Filetype)
	}
	for _, f := range []string{"id", "name", "dept", "salary"} {
		if !slices.Contains(r.Completion, f) {
			t.Errorf("completion after -if: %q missing from %v", f, r.Completion)
		}
	}
	if !strings.Contains(r.Hover, "-if") {
		t.Errorf("hover on -if: %q", r.Hover)
	}
	want := "ssql from csv emp.csv |\n  ssql where -if dept eq eng |\n  ssql include name dept |\n  ssql to csv"
	if !strings.Contains(r.Formatted, want) {
		t.Errorf("formatted buffer:\n%s\nwant it to hold\n%s", r.Formatted, want)
	}
}
