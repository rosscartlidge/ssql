package commands

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	cf "github.com/rosscartlidge/autocli/v4"
	"github.com/rosscartlidge/ssql/v4"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/lib"
	"github.com/rosscartlidge/ssql/v4/cmd/ssql/version"
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
			generate, _ := ctx.GlobalFlags["-generate"].(bool)

			if host == "" || path == "" {
				return fmt.Errorf("usage: ssql from ssh HOST PATH [-- <remote-pipeline>]")
			}

			// If RemainingArgs present (after --), it's a push-down pipeline.
			// In codegen mode (SSQLGO set), the generated Go ships the
			// .ssql script to the remote and runs ssql generate go -script
			// -run there — so local and remote run the same mode. In CLI
			// mode, it's the v4.27 baseline: ssql … | ssql … chain on the
			// remote.
			if len(ctx.RemainingArgs) > 0 {
				if shouldGenerate(generate) {
					return generateFromSSHRemoteCode(host, path, gpu, ctx.RemainingArgs)
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
	// `# require: vX.Y.Z` lets the remote `ssql generate go -script`
	// pre-flight check refuse the script if the remote ssql is older
	// than the local one that produced it. Without this, version skew
	// surfaces as a confusing mid-pipeline failure (a stage uses a
	// flag the remote doesn't know). With it the remote errors at
	// load time with a clear "this ssql (vA) needs vB or newer".
	sb.WriteString("# require: v")
	sb.WriteString(version.Version)
	sb.WriteString("\n")
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

// generateFromSSHRemoteCode emits an init fragment that runs the
// pushdown pipeline on the remote host. v4.42 codegen-symmetric
// design (see remote-go-execution-proposal.md rev 4):
//
// The generated Go embeds the .ssql script as a `const` string and
// inlines a small ssh-and-cat-and-run helper. Whatever mode SSQLGO
// is at codegen time (record/typed/parallel) is propagated to the
// remote via `ssql generate go -script -mode $mode -run`. So:
//
//   (SSQLGO=record …) | ssql generate go -run   →  remote runs record-mode Go
//   (SSQLGO=typed  …) | ssql generate go -run   →  remote runs typed-parallel Go
//
// Local-side, the fragment produces an `iter.Seq[ssql.Record]` from
// the remote's JSONL output (parsed via ssql.ReadJSONFromReader,
// which strips the `{"_schema":…}` header the remote emits via the
// v4.41.2 schema-aware JSONL fallback). Downstream local stages
// see the same wire shape they would in the v4.27 baseline path.
//
// Self-contained: the .ssql script is baked into the generated
// source as a const, so the resulting binary is a single artifact
// — no sibling files to ship.
func generateFromSSHRemoteCode(host, path string, gpu bool, pipelineArgs []string) error {
	_ = gpu // -gpu is unused in the codegen path — the remote runs
	// `ssql generate go` regardless. ssql_gpu vs ssql is a runtime
	// choice on the remote, not something the generator picks.

	pipelineGroups := ssql.SplitOnPlus(pipelineArgs)
	script := buildRemoteSSQLScript(path, pipelineGroups)
	remoteMode := pipelineModeFromEnv()

	params := []lib.CodeParam{
		{Name: "host", Default: host, Help: "SSH host", VarName: "flagHost"},
		// path stays embedded in the script (which is a const), but
		// we still expose -input so users can override at runtime
		// (e.g. when the binary is pointed at a different remote
		// file with the same schema).
	}

	// Indent each line of the .ssql script for the Go raw-string
	// literal. We use a backtick-delimited string for clarity.
	scriptLiteral := "`" + strings.TrimRight(script, "\n") + "`"

	// The remote command: `trap 'rm -f X' EXIT; cat > X && ssql
	// generate go -script X -mode $mode -run`. Built at runtime so
	// the temp path is unique per invocation.
	code := fmt.Sprintf(`const remoteSSQLScript = %s
	const remoteSSQLMode = %q
	remoteSSQLPath := fmt.Sprintf("/tmp/ssql-remote-%%d-%%d.ssql", os.Getpid(), time.Now().UnixNano())
	remoteSSQLCmd := fmt.Sprintf("trap 'rm -f %%s' EXIT; cat > %%s && /usr/bin/ssql generate go -script %%s -mode %%s -run",
		remoteSSQLPath, remoteSSQLPath, remoteSSQLPath, remoteSSQLMode)
	sshCmd := exec.Command("ssh", "-o", "BatchMode=yes", *flagHost, remoteSSQLCmd)
	sshCmd.Stdin = strings.NewReader(remoteSSQLScript)
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
	records := ssql.ReadJSONLFromReader(sshStdout)`, scriptLiteral, remoteMode)

	imports := []string{"fmt", "os", "os/exec", "strings", "time"}
	frag := lib.NewInitFragment("records", code, imports, getCommandString())
	frag.Params = params
	return lib.WriteCodeFragment(frag)
}

// pipelineModeFromEnv returns the canonical mode name to pass to the
// remote `ssql generate go -mode …` based on the local mode env var
// (SSQL_MODE, or its deprecated SSQLGO alias — see modeEnv).
// 1/true/record → "record"; typed/parallel → "typed".
// We're called only from the codegen path so the mode is non-empty.
func pipelineModeFromEnv() string {
	switch modeEnv() {
	case "typed", "parallel":
		return "typed"
	default:
		return "record"
	}
}
