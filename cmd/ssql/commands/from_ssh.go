package commands

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib/sshgo"
)

func registerFromSSH(cmd *cf.SubcommandBuilder) {
	cmd.Subcommand("ssh").
		Description("Read from a remote file via SSH. Tab-complete the path to enable field completion in downstream commands.").
		Example("ssql from ssh server /data/logs.csv | ssql to table", "Read remote CSV").
		Example("ssql from ssh server /data/logs.csv -- where -if status eq error", "Push filter to remote").
		Example("ssql from ssh server /data/logs.csv -- where -if age gt 25 + group-by -field dept -count", "Push multi-step pipeline to remote").

		Flag("HOST").
			String().
			CompleterFunc(completeSSHHost).
			Global().
			Help("SSH host (from ~/.ssh/config or user@host)").
			Done().

		Flag("PATH").
			String().
			CompleterFunc(completeSSHPath).
			Global().
			Help("Remote file path").
			Done().

		Flag("-gpu").
			Bool().
			Global().
			Default(false).
			Help("Use ssql_gpu on the remote machine").
			Done().

		Flag("-remote").
			Bool().
			Global().
			Default(false).
			Help("Run the pipeline as typed-Go on the remote (Mode A: requires ssql installed). Falls back to standard SSH streaming if the remote has no ssql.").
			Done().

		Flag("-generate", "-g").
			Bool().
			Global().
			Default(false).
			Help("Generate Go code instead of executing").
			Done().

		Handler(func(ctx *cf.Context) error {
			host, _ := ctx.GlobalFlags["HOST"].(string)
			path, _ := ctx.GlobalFlags["PATH"].(string)
			gpu, _ := ctx.GlobalFlags["-gpu"].(bool)
			remote, _ := ctx.GlobalFlags["-remote"].(bool)
			generate, _ := ctx.GlobalFlags["-generate"].(bool)

			if host == "" || path == "" {
				return fmt.Errorf("usage: ssql from ssh HOST PATH [-- <remote-pipeline>]")
			}

			// If RemainingArgs present (after --), it's a push-down pipeline
			if len(ctx.RemainingArgs) > 0 {
				if shouldGenerate(generate) {
					return generateFromSSHRemoteCode(host, path, gpu, ctx.RemainingArgs)
				}
				if remote {
					return executeFromSSHRemoteGo(host, path, ctx.RemainingArgs)
				}
				return executeFromSSHRemote(host, path, gpu, ctx.RemainingArgs)
			}

			// Simple remote read
			if shouldGenerate(generate) {
				return generateFromSSHCode(host, path, gpu)
			}
			return executeFromSSH(host, path, gpu)
		}).
		Done()
}

// completeSSHHost completes SSH host names from ~/.ssh/config and warms
// the connection when a single host is matched.
func completeSSHHost(ctx cf.CompletionContext) ([]string, error) {
	hosts := parseSSHConfigHosts()
	if len(hosts) == 0 {
		return []string{"<host>"}, nil
	}

	var matches []string
	partial := strings.ToLower(ctx.Partial)
	for _, h := range hosts {
		if strings.HasPrefix(strings.ToLower(h), partial) {
			matches = append(matches, h)
		}
	}

	// Single match — warm the SSH connection in the background
	if len(matches) == 1 {
		exec.Command("ssh", "-o", "ConnectTimeout=3", "-N", "-f", matches[0]).Start()
	}

	if len(matches) == 0 {
		return []string{"<host>"}, nil
	}
	return matches, nil
}

// parseSSHConfigHosts reads Host entries from ~/.ssh/config.
func parseSSHConfigHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	f, err := os.Open(home + "/.ssh/config")
	if err != nil {
		return nil
	}
	defer f.Close()

	var hosts []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(strings.ToLower(line), "host ") {
			for _, h := range strings.Fields(line)[1:] {
				// Skip wildcards and patterns
				if strings.ContainsAny(h, "*?") {
					continue
				}
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}

// completeSSHPath completes the PATH arg for `from ssh` and emits a field_cache
// directive by SSH-fetching the first line of the remote file.
func completeSSHPath(ctx cf.CompletionContext) ([]string, error) {
	host, _ := ctx.GlobalFlags["HOST"].(string)
	if host == "" || ctx.Partial == "" {
		return []string{"<remote-path>"}, nil
	}

	// Try to fetch the CSV header from the remote file
	cmd := exec.Command("ssh", "-o", "ConnectTimeout=2", "-o", "BatchMode=yes", host,
		"/usr/bin/head -1 "+ssql.ShellQuote(ctx.Partial))
	out, err := cmd.Output()
	if err != nil {
		// SSH failed (host down, file not found, etc.) — just return the partial
		return []string{ctx.Partial}, nil
	}

	header := strings.TrimSpace(string(out))
	if header == "" {
		return []string{ctx.Partial}, nil
	}

	// Parse as CSV header
	reader := csv.NewReader(strings.NewReader(header))
	fields, err := reader.Read()
	if err != nil || len(fields) == 0 {
		return []string{ctx.Partial}, nil
	}
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}

	// Emit field_cache directive for downstream commands
	directive := cf.CompletionDirective{
		Type:   "field_cache",
		Fields: fields,
	}
	directiveJSON, err := json.Marshal(directive)
	if err != nil {
		return []string{ctx.Partial}, nil
	}
	return []string{string(directiveJSON), ctx.Partial}, nil
}

// executeFromSSH runs a simple remote read via SSH.
func executeFromSSH(host, path string, gpu bool) error {
	remoteBin := sshRemoteBin(gpu)
	remoteCmd := ssql.BuildRemoteCommand(remoteBin, path, "", nil)
	return runSSHAndStreamJSONL(host, remoteCmd)
}

// executeFromSSHRemote runs a remote pipeline via SSH with push-down.
func executeFromSSHRemote(host, path string, gpu bool, pipelineArgs []string) error {
	remoteBin := sshRemoteBin(gpu)
	remoteCmd := ssql.BuildRemoteCommand(remoteBin, path, "", ssql.SplitOnPlus(pipelineArgs))
	return runSSHAndStreamJSONL(host, remoteCmd)
}

// executeFromSSHRemoteGo runs the push-down pipeline on the remote
// host as typed-Go (Mode A from remote-go-execution-proposal.md):
// builds a .ssql script, ships it via stdin, runs `ssql generate go
// -script -run`. The remote does typed-parallel codegen + go-run;
// stdout (JSONL with schema-inferred header) streams back through
// the same readJSONSchemaAware + writeWithInferredSchema layer the
// non-remote path uses, so downstream local stages see the same
// wire format either way.
//
// If the remote doesn't have ssql installed (probe says no), falls
// back to the standard executeFromSSHRemote path with a stderr note
// — pipeline still works, just without acceleration.
func executeFromSSHRemoteGo(host, path string, pipelineArgs []string) error {
	caps, err := sshgo.Probe(host)
	if err != nil {
		return fmt.Errorf("ssql from ssh -remote: probe %s: %w", host, err)
	}
	if !caps.HasSSQL {
		fmt.Fprintf(os.Stderr,
			"%s has no ssql installed; falling back to standard SSH streaming "+
				"(install ssql on %s to enable -remote acceleration)\n",
			host, host)
		return executeFromSSHRemote(host, path, false, pipelineArgs)
	}
	script := buildRemoteSSQLScript(path, ssql.SplitOnPlus(pipelineArgs))

	// Pipe the remote's stdout through the same schema-aware
	// reader/writer the non-remote path uses. This:
	//   - prepends a `{"_schema":...}` header to the output (so
	//     downstream local stages can preserve column types)
	//   - normalises field names if the remote emitted CamelCase
	//     (typed-mode JSONL fallback) — readJSONSchemaAware uses
	//     the first row's keys as schema fields verbatim, then
	//     writeWithInferredSchema emits a consistent header
	//   - matches the wire format the standard from-ssh path uses
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- sshgo.RunRemote(host, []byte(script), pw)
		pw.Close()
	}()

	records := readJSONSchemaAware(pr)
	writeErr := writeWithInferredSchema(records, writeWithInferredSchemaOptions{})
	runErr := <-errCh
	if writeErr != nil {
		return writeErr
	}
	return runErr
}

// buildRemoteSSQLScript turns a from-ssh-pushdown invocation into a
// multi-line .ssql script:
//
//	ssql from PATH
//	| ssql STAGE1
//	| ssql STAGE2
//	...
//
// Path and per-stage args are quoted with ssql.ShellQuote — the
// script is exec'd by bash on the remote, so the same quoting rules
// the existing BuildRemoteCommand uses apply.
//
// As of v4.41.2 the JSONL fallback in the typed assembler emits a
// {"_schema":...} header inferred from the typed output type and
// uses lowercase CSV-style field names — same wire format the
// baseline `--` pushdown produces — so we no longer need to
// auto-append `| ssql to jsonl`. The script ends naturally at the
// user's last stage.
func buildRemoteSSQLScript(path string, pipelineGroups [][]string) string {
	var sb strings.Builder
	sb.WriteString("ssql from ")
	sb.WriteString(ssql.ShellQuote(path))
	sb.WriteString("\n")
	for _, group := range pipelineGroups {
		sb.WriteString("| ssql ")
		for i, arg := range group {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(ssql.ShellQuote(arg))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// runSSHAndStreamJSONL executes an SSH command and streams JSONL output.
func runSSHAndStreamJSONL(host, remoteCmd string) error {
	cmd := exec.Command("ssh", host, remoteCmd)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ssh pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ssh start: %w", err)
	}

	records := readJSONSchemaAware(stdout)
	writeErr := writeWithInferredSchema(records, writeWithInferredSchemaOptions{})

	waitErr := cmd.Wait()
	if writeErr != nil {
		return writeErr
	}
	if waitErr != nil {
		return fmt.Errorf("ssh: %w", waitErr)
	}
	return nil
}

// sshRemoteBin returns the absolute path to the remote binary.
// Uses full path to prevent PATH manipulation attacks on remote machines.
func sshRemoteBin(gpu bool) string {
	if gpu {
		return "/usr/bin/ssql_gpu"
	}
	return "/usr/bin/ssql"
}

// generateFromSSHCode generates Go code for SSH remote read.
func generateFromSSHCode(host, path string, gpu bool) error {
	remoteBin := sshRemoteBin(gpu)

	params := []lib.CodeParam{
		{Name: "host", Default: host, Help: "SSH host", VarName: "flagHost"},
		{Name: "path", Default: path, Help: "remote file path", VarName: "flagPath"},
	}

	code := fmt.Sprintf(`remoteCmd := ssql.BuildRemoteCommand(%q, *flagPath, "", nil)
	sshCmd := exec.Command("ssh", *flagHost, remoteCmd)
	sshCmd.Stderr = os.Stderr
	sshStdout, err := sshCmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}
	if err := sshCmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}
	defer sshCmd.Wait()
	records := ssql.ReadJSONFromReader(sshStdout)`, remoteBin)

	imports := []string{"fmt", "os", "os/exec"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

func generateFromSSHRemoteCode(host, path string, gpu bool, pipelineArgs []string) error {
	remoteBin := sshRemoteBin(gpu)
	pipelineGroups := ssql.SplitOnPlus(pipelineArgs)

	params := []lib.CodeParam{
		{Name: "host", Default: host, Help: "SSH host", VarName: "flagHost"},
		{Name: "path", Default: path, Help: "remote file path", VarName: "flagPath"},
	}

	// Build pipeline groups code
	var pipelineCode string
	if len(pipelineGroups) > 0 {
		var groupStrs []string
		for _, group := range pipelineGroups {
			var quotedArgs []string
			for _, arg := range group {
				quotedArgs = append(quotedArgs, fmt.Sprintf("%q", arg))
			}
			groupStrs = append(groupStrs, fmt.Sprintf("{%s}", strings.Join(quotedArgs, ", ")))
		}
		pipelineCode = fmt.Sprintf("[][]string{%s}", strings.Join(groupStrs, ", "))
	} else {
		pipelineCode = "nil"
	}

	code := fmt.Sprintf(`remoteCmd := ssql.BuildRemoteCommand(%q, *flagPath, "", %s)
	sshCmd := exec.Command("ssh", *flagHost, remoteCmd)
	sshCmd.Stderr = os.Stderr
	sshStdout, err := sshCmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}
	if err := sshCmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %%v\n", err)
		os.Exit(1)
	}
	defer sshCmd.Wait()
	records := ssql.ReadJSONFromReader(sshStdout)`, remoteBin, pipelineCode)

	imports := []string{"fmt", "os", "os/exec"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}
