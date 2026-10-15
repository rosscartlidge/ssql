package commands

// The stage chain `ssql run` and `serve` share: moved out of
// serve_http.go (DFC151) so the slim build, which has no serve and no
// net/http, still runs pipeline documents.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
)

// stageChain is a started pipeline of self-exec stages with per-stage
// stderr capture (a shared buffer would be a data race).
type stageChain struct {
	cmds    []*exec.Cmd
	stderrs []bytes.Buffer
	out     io.ReadCloser // last stage's stdout
}

// startStageChain wires and starts `self stage[0] | self stage[1] | …`
// in dir under ctx, capturing each stage's stderr and exposing the last
// stage's stdout as ch.out. The pipe discipline lives in startChain,
// which the pipeline-document runner (`ssql run`) shares.
func startStageChain(ctx context.Context, self, dir string, stages [][]string, extraEnv ...string) (*stageChain, error) {
	return startChain(ctx, self, stages, chainOptions{Dir: dir, Env: extraEnv})
}

// wait waits for every stage, with shell status semantics: the LAST
// stage's error wins; an upstream stage killed by a signal (SIGPIPE
// after its consumer exited early — `limit`, `head`-like flows) is
// normal termination, not failure. Upstream real exit codes still
// count when the last stage succeeded.
// killedByBrokenPipe reports whether a stage died of SIGPIPE: it wrote
// after its reader had gone, which in a pipeline means a consumer
// downstream finished early (`… | limit 3`) — normal, as in a shell.
// Only SIGPIPE: both wait loops excused death by ANY signal until
// 2026-10-11, so a stage the OOM killer took (SIGKILL) passed silently.
func killedByBrokenPipe(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	return exitedBySIGPIPE(ee)
}

func (ch *stageChain) wait() error {
	var lastErr, upstreamErr error
	for i, c := range ch.cmds {
		err := c.Wait()
		if err == nil {
			continue
		}
		if i == len(ch.cmds)-1 {
			lastErr = err
			continue
		}
		if killedByBrokenPipe(err) {
			continue // downstream closed the pipe early
		}
		if upstreamErr == nil {
			upstreamErr = err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return upstreamErr
}
