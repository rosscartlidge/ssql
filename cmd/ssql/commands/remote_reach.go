package commands

import (
	"strings"

	ssql "github.com/rosscartlidge/ssql/v4"
)

// remoteReach names the stages that reach another host when they run:
// over ssh, or a URL fetched over the network. The language server runs
// nothing of a pipeline that reaches another host while the user types
// (Ross, 2026-11-10: no ssh connection on every pause), and says so on
// the stage instead. Files lists the source files whose code makes the
// connection: TestRemoteReachCoversSSHCallers fails on a file that builds
// a remote command or runs ssh without a row here (or a reason in
// remoteReachLocal), so a new remote command cannot slip past.
var remoteReach = []struct {
	Path  []string // the command path: "from ssh", or "merge" with a flag
	Flag  string   // the flag that makes the stage remote ("" = always)
	Why   string
	Files []string
}{
	{[]string{"from", "ssh"}, "", "reads from a remote host over ssh", []string{"from_ssh.go", "from_ssh_typed.go"}},
	{[]string{"from", "catalog"}, "", "reads catalog shards, which may live on remote hosts", []string{"from_catalog.go"}},
	{[]string{"merge"}, "-catalog", "merges catalog shards, which may live on remote hosts", []string{"merge.go"}},
}

// remoteReachLocal names the files that build a remote command line but
// run it on this host, with why.
var remoteReachLocal = map[string]string{
	"from.go": "the multi-file pushdown runs BuildRemoteCommand's pipeline on local files through bash",
}

// stageReachesRemote says why a stage's words (after `ssql`) reach
// another host, or "" when they do not. Any argument that is an http(s)
// URL is fetched over the network, whichever command takes it.
func stageReachesRemote(words []string) string {
	for _, r := range remoteReach {
		if len(words) < len(r.Path) {
			continue
		}
		match := true
		for i, p := range r.Path {
			if words[i] != p {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if r.Flag == "" {
			return r.Why
		}
		for _, w := range words[len(r.Path):] {
			if w == r.Flag {
				return r.Why
			}
		}
	}
	for _, w := range words {
		if ssql.IsHTTPURL(strings.Trim(w, `'"`)) {
			return "names a URL, which a command may fetch over the network"
		}
	}
	return ""
}

// textReachesRemote is stageReachesRemote over every stage of shell
// text, nested pipelines included: the first stage that reaches another
// host, with its span in text (top-level stages only; a nested stage
// reports the stage holding it) and why.
func textReachesRemote(text string) (span [2]int, why string) {
	for _, sp := range topLevelStageSpans(text) {
		seg := text[sp[0]:sp[1]]
		toks := tokenizeStage(strings.TrimSpace(seg))
		if len(toks) > 1 {
			if w := stageReachesRemote(toks[1:]); w != "" {
				return sp, w
			}
		}
		for _, tok := range toks {
			if strings.HasPrefix(tok, "<(") && strings.HasSuffix(tok, ")") {
				if _, w := textReachesRemote(tok[2 : len(tok)-1]); w != "" {
					return sp, w
				}
			}
		}
	}
	return [2]int{}, ""
}
