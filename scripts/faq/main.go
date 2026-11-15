// Command faq is the DFC169 phase 1 harness: a local model answering
// questions about ssql, grounded in the docs and the sample data, its
// pipelines checked in schema mode and run before they are shown.
//
//	go -C scripts/faq run . -ask 'how many employees per dept?'
//	go -C scripts/faq run . -eval cases.json -models gemma4:26b,gemma4:31b
//
// The ssql binary is the one on PATH unless -ssql names another; the sample
// data is the playground's (cmd/ssql-playground/data).
package main

import (
	"flag"
	"fmt"
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
	goldens := flag.Bool("goldens", false, "with -eval: run and print every golden, with no model")
	flag.Parse()

	repo, err := repoRoot()
	if err != nil {
		fail(err)
	}
	bin := *ssqlBin
	if bin == "" {
		if bin, err = exec.LookPath("ssql"); err != nil {
			fail(fmt.Errorf("no ssql on PATH; build one or pass -ssql"))
		}
	}
	if bin, err = filepath.Abs(bin); err != nil {
		fail(err)
	}
	box := sandbox{ssql: bin, dataDir: filepath.Join(repo, "cmd", "ssql-playground", "data"), timeout: 20 * time.Second}
	f, err := newFAQ(repo, box, *retries, *docsK, *verbose)
	if err != nil {
		fail(err)
	}
	var llms []ollama
	for _, m := range strings.Split(*models, ",") {
		llms = append(llms, ollama{host: *host, model: strings.TrimSpace(m), ctx: *ctxSize})
	}

	switch {
	case *ask != "":
		a := f.ask(llms[0], *ask)
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
		pass, total, retries int
		byKind               map[string][2]int
		walls                []time.Duration
		fails                []string
	}
	results := map[string]*tally{}
	for _, llm := range llms {
		t := &tally{byKind: map[string][2]int{}}
		results[llm.model] = t
		for _, c := range cases {
			a := f.ask(llm, c.Question)
			ok, why := grade(f, c, a)
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
				if !ok {
					mark = "FAIL " + why + "\n    pipeline: " + orNone(a.Pipeline)
				}
				fmt.Fprintf(os.Stderr, "%s %-28s %5.1fs r%d %s\n", llm.model, c.ID, a.Wall.Seconds(), a.Retries, mark)
			}
		}
	}
	fmt.Printf("\n| Model | Pass | pipeline | doc | refuse | corrections | p50 | p95 |\n|---|---|---|---|---|---|---|---|\n")
	for _, llm := range llms {
		t := results[llm.model]
		slices.Sort(t.walls)
		kind := func(k string) string { v := t.byKind[k]; return fmt.Sprintf("%d/%d", v[0], v[1]) }
		fmt.Printf("| %s | %d/%d (%.0f%%) | %s | %s | %s | %d | %.1fs | %.1fs |\n", llm.model, t.pass, t.total,
			100*float64(t.pass)/float64(max(t.total, 1)), kind("pipeline"), kind("doc"), kind("refuse"),
			t.retries, pct(t.walls, 0.5).Seconds(), pct(t.walls, 0.95).Seconds())
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
