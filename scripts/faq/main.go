// Command faq is the DFC169 phase 1 harness: a local model answering
// questions about ssql, grounded in the docs and the sample data, its
// pipelines checked in schema mode and run before they are shown.
//
//	go -C scripts/faq run . -ask 'how many employees per dept?'
//	go -C scripts/faq run . -eval cases.json -models gemma4:26b,gemma4:31b
//
// The ssql binary is the one on PATH unless -ssql names another; the sample
// data is the playground's (cmd/ssql-playground/data, samples.go).
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func main() {
	ask := flag.String("ask", "", "answer one question")
	evalFile := flag.String("eval", "", "run the eval cases in this JSON file")
	only := flag.String("only", "", "with -eval: run only the cases whose id contains this")
	models := flag.String("models", "gemma4:26b", "comma-separated Ollama models")
	host := flag.String("host", envOr("OLLAMA_HOST", "http://localhost:11434"), "Ollama server")
	ctxSize := flag.Int("ctx", 32768, "context window (num_ctx)")
	retries := flag.Int("retries", 2, "corrections after a failed pipeline")
	docsK := flag.Int("docs", 4, "documentation sections given with a question")
	ssqlBin := flag.String("ssql", "", "the ssql binary (default: the one on PATH)")
	verbose := flag.Bool("v", false, "show each case and retry")
	review := flag.Bool("review", true, "show the model a pipeline's output and the question once more, to fix its shape")
	goldens := flag.Bool("goldens", false, "with -eval: run and print every golden, with no model")
	serve := flag.Bool("serve", false, "serve the FAQ page to the local network and the tailnet")
	listen := flag.String("listen", ":8484", "with -serve: the address (tailscale:PORT binds the tailnet address only)")
	var lan cidrList
	flag.Var(&lan, "lan", "with -serve: a local network to answer besides loopback and the tailnet, such as 192.168.4.0/22 (repeatable; none by default, so a café's network is refused)")
	jail := flag.String("jail", "auto", "run pipelines in the bubblewrap jail: on, off, or auto (on when available; -serve requires it)")
	logFile := flag.String("log", defaultLog(), "with -serve: where questions are kept, without addresses ('' keeps nothing)")
	logShared := flag.Bool("log-shared", false, "with -serve: make the log and its directory readable by the service's group (the public instance's, so its owner can read it without sudo)")
	logMax := flag.Int64("log-max", 64<<20, "with -serve: past this many bytes the log moves to LOG.1, keeping one old generation")
	public := flag.Bool("public", false, "with -serve: the public instance Tailscale Funnel points at (loopback -listen only, limits per visitor and per day)")
	perHour := flag.Int("per-hour", 10, "with -public: questions one visitor may ask an hour")
	perDay := flag.Int("per-day", 200, "with -public: questions answered in total per UTC day")
	repoFlag := flag.String("repo", "", "the ssql docs tree (default: the git checkout this runs in)")
	search := flag.String("docsearch", "", "the docsearch command (default: REPO/scripts/docsearch.sh)")
	flag.Parse()

	if *public && !*serve {
		fail(fmt.Errorf("-public is a mode of -serve"))
	}
	repo := *repoFlag
	var err error
	if repo == "" {
		if repo, err = repoRoot(); err != nil {
			fail(err)
		}
	}
	jailed, err := useJail(*jail, *serve)
	if err != nil {
		fail(err)
	}
	bin := *ssqlBin
	switch {
	case bin == "" && jailed:
		// The jail runs the slim build (DFC151: no net/http, no serve, no
		// URL sources), built from this checkout so it answers for it.
		if bin, err = buildSlim(repo); err != nil {
			fail(err)
		}
	case bin == "":
		if bin, err = exec.LookPath("ssql"); err != nil {
			fail(fmt.Errorf("no ssql on PATH; build one or pass -ssql"))
		}
	}
	if bin, err = filepath.Abs(bin); err != nil {
		fail(err)
	}
	samples, err := buildSamples(repo)
	if err != nil {
		fail(err)
	}
	box := sandbox{ssql: bin, dataDir: samples, timeout: 20 * time.Second, jail: jailed}
	f, err := newFAQ(repo, box, *retries, *docsK, *verbose)
	if err != nil {
		fail(err)
	}
	f.review, f.search = *review, *search
	var llms []ollama
	for _, m := range strings.Split(*models, ",") {
		llms = append(llms, ollama{host: *host, model: strings.TrimSpace(m), ctx: *ctxSize})
	}

	switch {
	case *serve:
		addr, err := listenAddr(*listen)
		if err != nil {
			fail(err)
		}
		if *public {
			if err := loopbackListen(addr); err != nil {
				fail(err)
			}
		}
		if *logFile != "" {
			if err := shareLog(*logFile, *logShared); err != nil {
				fail(err)
			}
		}
		if err := llms[0].warm(); err != nil {
			fail(fmt.Errorf("loading %s: %w", llms[0].model, err))
		}
		s := newServer(f, llms[0], *logFile)
		s.logMax = *logMax
		s.lan = lan
		if *logShared {
			s.logPerm = 0o640
		}
		reach := "loopback and tailnet only"
		if len(lan) > 0 {
			reach = "loopback, tailnet and " + lan.String()
		}
		if *public {
			if err := s.makePublic(*perHour, *perDay); err != nil {
				fail(err)
			}
			reach = fmt.Sprintf("PUBLIC via Funnel; %d questions per visitor an hour, %d a day", *perHour, *perDay)
		}
		fmt.Fprintf(os.Stderr, "faq: %s on http://%s (%s; jail on, ssql %s; log %s)\n", llms[0].model, addr, reach, bin, orNone(*logFile))
		srv := &http.Server{Addr: addr, Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 16 << 10}
		fail(srv.ListenAndServe())
	case *ask != "":
		a := f.ask(llms[0], *ask, nil)
		printAnswer(a)
	case *evalFile != "":
		cases, err := loadCases(*evalFile)
		if err != nil {
			fail(err)
		}
		if *only != "" {
			cases = slices.DeleteFunc(cases, func(c evalCase) bool { return !strings.Contains(c.ID, *only) })
		}
		if err := checkGoldens(box, cases); err != nil {
			fail(err)
		}
		if *goldens {
			for _, c := range cases {
				if c.Kind == "pipeline" {
					r := box.run(dataFor(c.Golden))
					fmt.Printf("== %s (%d rows)\n%s", c.ID, len(canonRows(r.stdout, c.Ordered)), clip(r.stdout, 600))
				}
			}
			return
		}
		runEval(f, llms, cases, *verbose)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func printAnswer(a answer) {
	fmt.Println(strings.TrimSpace(a.Reply))
	fmt.Println()
	switch {
	case a.Setup && a.Ran:
		fmt.Printf("--- verified shell setup: ssql printed the script and bash parsed it (%d correction(s))\n", a.Retries)
	case a.Setup && a.Failure == "":
		fmt.Println("--- install commands: shown, not run")
	case a.Ran:
		fmt.Printf("--- verified: the pipeline ran (%d correction(s)); its output:\n%s", a.Retries, clip(a.Output, 2000))
	case a.Unsupported:
		fmt.Println("--- ssql cannot do this, the model says; any pipeline above was not run")
	case a.Illustrative:
		fmt.Println("--- an illustration: it names a file the sample data does not have, so it was not run")
	case a.Pipeline != "":
		fmt.Printf("--- NOT verified after %d correction(s): %s\n", a.Retries, a.Failure)
	}
	if len(a.Docs) > 0 {
		fmt.Printf("--- docs given: %s\n", strings.Join(a.Docs, ", "))
	}
	fmt.Printf("--- %.1fs, %d tokens generated\n", a.Wall.Seconds(), a.GenTokens)
}

func runEval(f *faq, llms []ollama, cases []evalCase, verbose bool) {
	type tally struct {
		pass, rows, total, retries, revised int
		byKind                              map[string][2]int
		walls                               []time.Duration
		fails                               []string
	}
	results := map[string]*tally{}
	for _, llm := range llms {
		t := &tally{byKind: map[string][2]int{}}
		results[llm.model] = t
		for _, c := range cases {
			a := f.ask(llm, c.Question, nil)
			ok, rows, why := grade(f, c, a)
			if rows {
				t.rows++
			}
			if a.Revised {
				t.revised++
			}
			k := t.byKind[c.Kind]
			k[1]++
			if ok {
				t.pass++
				k[0]++
			} else {
				t.fails = append(t.fails, fmt.Sprintf("%s: %s\n      pipeline: %s", c.ID, why, orNone(a.Pipeline)))
			}
			t.byKind[c.Kind] = k
			t.total++
			t.retries += a.Retries
			t.walls = append(t.walls, a.Wall)
			if verbose {
				mark := "PASS"
				if a.Revised {
					mark = "PASS (revised)"
				}
				if !ok {
					mark = "FAIL " + why + "\n    pipeline: " + orNone(a.Pipeline)
					if a.Revised {
						mark = "FAIL (revised) " + why + "\n    pipeline: " + orNone(a.Pipeline)
					}
				}
				fmt.Fprintf(os.Stderr, "%s %-28s %5.1fs r%d %s\n", llm.model, c.ID, a.Wall.Seconds(), a.Retries, mark)
			}
		}
	}
	// Pass is the answer as asked; rows right counts a pipeline whose rows
	// hold the golden's values with columns to spare (the shape missed).
	fmt.Printf("\n| Model | Pass | rows right | pipeline | doc | refuse | corrections | revised | p50 | p95 |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for _, llm := range llms {
		t := results[llm.model]
		slices.Sort(t.walls)
		kind := func(k string) string { v := t.byKind[k]; return fmt.Sprintf("%d/%d", v[0], v[1]) }
		fmt.Printf("| %s | %d/%d (%.0f%%) | %d/%d | %s | %s | %s | %d | %d | %.1fs | %.1fs |\n", llm.model, t.pass, t.total,
			100*float64(t.pass)/float64(max(t.total, 1)), t.rows, t.total, kind("pipeline"), kind("doc"), kind("refuse"),
			t.retries, t.revised, pct(t.walls, 0.5).Seconds(), pct(t.walls, 0.95).Seconds())
	}
	for _, llm := range llms {
		if fs := results[llm.model].fails; len(fs) > 0 {
			fmt.Printf("\n%s failures:\n  %s\n", llm.model, strings.Join(fs, "\n  "))
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	return d[min(len(d)-1, int(p*float64(len(d))))]
}

// useJail decides from -jail; the service never runs without the jail.
func useJail(mode string, serve bool) (bool, error) {
	missing := jailAvailable()
	switch mode {
	case "on":
		if missing != "" {
			return false, fmt.Errorf("-jail on: %s is not installed", missing)
		}
		return true, nil
	case "off":
		if serve {
			return false, fmt.Errorf("-serve runs pipelines only in the jail; -jail off is for the CLI")
		}
		return false, nil
	case "auto":
		if missing != "" && serve {
			return false, fmt.Errorf("-serve needs the jail, and %s is not installed", missing)
		}
		return missing == "", nil
	}
	return false, fmt.Errorf("-jail %q: want on, off or auto", mode)
}

// buildSlim builds the checkout's slim ssql into the user cache.
func buildSlim(repo string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, "ssql-faq", "ssql")
	cmd := exec.Command("go", "build", "-tags", "slim", "-ldflags", "-s -w", "-o", out, "./cmd/ssql")
	cmd.Dir, cmd.Stderr = repo, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("building the slim ssql: %w", err)
	}
	return out, nil
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("run inside the ssql checkout: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "faq:", err)
	os.Exit(1)
}
