package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerationWithEnvVar tests that SSQLGO env var works
func TestGenerationWithEnvVar(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tests := []struct {
		name    string
		cmdLine string // Full shell command line
		want    string // substring that should appear in output
	}{
		{
			name:    "from generation",
			cmdLine: "export SSQLGO=1 && /tmp/ssql_test from test.csv",
			want:    `"type":"init"`,
		},
		{
			name:    "where generation",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test where -if age gt 18`,
			want:    `"type":"stmt"`,
		},
		{
			name:    "to csv generation",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to csv out.csv`,
			want:    `"type":"final"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run command using bash -c for proper pipeline execution
			cmd := exec.Command("bash", "-c", tt.cmdLine)

			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
				// Some commands may error if files don't exist, but should still generate
			}

			outputStr := string(output)
			if !strings.Contains(outputStr, tt.want) {
				t.Errorf("Expected output to contain %q, got: %s", tt.want, outputStr)
			}
		})
	}
}

// TestGenerationFlag tests that -generate flag works
func TestGenerationFlag(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	cmd := exec.Command("/tmp/ssql_test", "from", "-generate", "test.csv")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Command output: %s", output)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, `"type":"init"`) {
		t.Errorf("Expected output to contain init fragment, got: %s", outputStr)
	}
	if !strings.Contains(outputStr, `ssql.ReadCSV`) {
		t.Errorf("Expected output to contain ReadCSV call, got: %s", outputStr)
	}
}

// TestFullPipeline tests a complete generation pipeline
func TestFullPipeline(t *testing.T) {
	// This test ensures the full pipeline works end-to-end
	// Create a temporary CSV file
	csvContent := "name,age\nAlice,30\nBob,25\n"
	tmpFile := "/tmp/test_pipeline.csv"
	if err := os.WriteFile(tmpFile, []byte(csvContent), 0644); err != nil {
		t.Fatalf("Failed to create test CSV: %v", err)
	}
	defer os.Remove(tmpFile)

	// Build the binary
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Run pipeline: from | where | generate go
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -if age gt 25 | /tmp/ssql_test generate go +O`
	cmd := exec.Command("bash", "-c", pipeline)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pipeline failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Check for expected elements in generated code
	expectations := []string{
		"package main",
		"ssql.ReadCSV",
		"ssql.Where",
		"func(r ssql.Record) bool",
		`ssql.GetOr(r, "age"`,
	}

	for _, expected := range expectations {
		if !strings.Contains(outputStr, expected) {
			t.Errorf("Generated code missing expected element: %q\nGot: %s", expected, outputStr)
		}
	}
}

// TestGeneratedCodeCompiles tests that generated code actually compiles and runs
func TestGeneratedCodeCompiles(t *testing.T) {
	// Build the binary
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Create test CSV file
	csvContent := "name,age\nAlice,30\nBob,25\n"
	tmpFile := "/tmp/test_compile.csv"
	if err := os.WriteFile(tmpFile, []byte(csvContent), 0644); err != nil {
		t.Fatalf("Failed to create test CSV: %v", err)
	}
	defer os.Remove(tmpFile)

	// Generate code
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -if age gt 25 | /tmp/ssql_test generate go +O`
	cmd := exec.Command("bash", "-c", pipeline)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pipeline failed: %v\nOutput: %s", err, output)
	}

	// Write generated code to temp file
	generatedFile := "/tmp/test_generated.go"
	if err := os.WriteFile(generatedFile, output, 0644); err != nil {
		t.Fatalf("Failed to write generated code: %v", err)
	}
	defer os.Remove(generatedFile)

	// Check that generated code includes "os" import
	generatedCode := string(output)
	if !strings.Contains(generatedCode, `"os"`) {
		t.Errorf("Generated code missing 'os' import. This is needed for error handling.\nGenerated code:\n%s", generatedCode)
	}

	// Try to compile the generated code
	compileCmd := exec.Command("go", "build", "-o", "/tmp/test_generated_binary", generatedFile)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Generated code failed to compile: %v\nCompiler output:\n%s\nGenerated code:\n%s",
			err, compileOutput, generatedCode)
	}
	defer os.Remove("/tmp/test_generated_binary")

	t.Log("Generated code compiled successfully")
}

// TestLimitOffsetSortDistinct tests generation for limit, offset, sort, distinct commands
func TestLimitOffsetSortDistinct(t *testing.T) {
	// Build the binary
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Create test CSV file
	csvContent := "name,age\nAlice,30\nBob,25\nCharlie,35\n"
	tmpFile := "/tmp/test_limit_offset_sort.csv"
	if err := os.WriteFile(tmpFile, []byte(csvContent), 0644); err != nil {
		t.Fatalf("Failed to create test CSV: %v", err)
	}
	defer os.Remove(tmpFile)

	tests := []struct {
		name    string
		cmdLine string
		want    []string // substrings that should appear in output
	}{
		{
			name:    "limit command",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test limit 5`,
			want:    []string{`"type":"stmt"`, `"var":"limited"`, `Limit[ssql.Record](*flagLimit)`, `"name":"limit"`, `"default":"5"`},
		},
		{
			name:    "offset command",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test offset 10`,
			want:    []string{`"type":"stmt"`, `"var":"skipped"`, `Offset[ssql.Record](*flagOffset)`, `"name":"offset"`, `"default":"10"`},
		},
		{
			name:    "sort command",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test sort age`,
			want:    []string{`"type":"stmt"`, `"var":"sorted"`, `SortRecords`, `age`},
		},
		{
			name:    "distinct command",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test distinct`,
			want:    []string{`"type":"stmt"`, `"var":"distinct"`, `DistinctBy`},
		},
		{
			name:    "pipeline with all commands",
			cmdLine: `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -if age gt 25 | /tmp/ssql_test limit 5 | /tmp/ssql_test offset 1 | /tmp/ssql_test sort age -desc | /tmp/ssql_test distinct | /tmp/ssql_test generate go +O`,
			want:    []string{"package main", "ssql.ReadCSV", "ssql.Where", "ssql.Limit", "ssql.Offset", "ssql.SortRecords", "ssql.DistinctBy"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)
			for _, expected := range tt.want {
				if !strings.Contains(outputStr, expected) {
					t.Errorf("Expected output to contain %q, got: %s", expected, outputStr)
				}
			}
		})
	}
}

// TestAllCommandsSupportGeneration ensures every command supports code generation
// This is a critical test to prevent losing the generation feature
func TestAllCommandsSupportGeneration(t *testing.T) {
	// Build the binary
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Create test CSV file for commands that need input files
	csvContent := "name,age,dept,salary\nAlice,30,Engineering,95000\nBob,25,Sales,75000\nCharlie,35,Engineering,105000\n"
	tmpFile := "/tmp/test_all_commands.csv"
	if err := os.WriteFile(tmpFile, []byte(csvContent), 0644); err != nil {
		t.Fatalf("Failed to create test CSV: %v", err)
	}
	defer os.Remove(tmpFile)

	// Create test XLSX file (convert CSV to XLSX using the built binary)
	xlsxFile := "/tmp/test_all_commands.xlsx"
	createXLSX := exec.Command("bash", "-c", `/tmp/ssql_test from `+tmpFile+` | /tmp/ssql_test to xlsx `+xlsxFile)
	if out, err := createXLSX.CombinedOutput(); err != nil {
		t.Logf("Failed to create test XLSX (may affect xlsx tests): %v\n%s", err, out)
	}
	defer os.Remove(xlsxFile)

	tests := []struct {
		name           string
		cmdLine        string
		expectFragment bool   // false for commands that shouldn't generate (like generate go)
		wantSubstring  string // substring to verify in generated code
	}{
		{
			name:           "from",
			cmdLine:        "SSQLGO=1 /tmp/ssql_test from " + tmpFile,
			expectFragment: true,
			wantSubstring:  `"type":"init"`,
		},
		{
			name:           "where",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test where -if age gt 25`,
			expectFragment: true,
			wantSubstring:  `ssql.Where`,
		},
		{
			name:           "limit",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test limit 10`,
			expectFragment: true,
			wantSubstring:  `ssql.Limit`,
		},
		{
			name:           "offset",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test offset 5`,
			expectFragment: true,
			wantSubstring:  `ssql.Offset`,
		},
		{
			name:           "sort",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test sort age`,
			expectFragment: true,
			wantSubstring:  `ssql.SortRecords`,
		},
		{
			name:           "distinct",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test distinct`,
			expectFragment: true,
			wantSubstring:  `ssql.DistinctBy`,
		},
		{
			name:           "group-by",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test group-by dept -count total`,
			expectFragment: true,
			wantSubstring:  `ssql.GroupByFields`,
		},
		{
			name:           "group-by-rollup",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test group-by a_kind z_kind -count count -rollup`,
			expectFragment: true,
			wantSubstring:  `ssql.Rollup`,
		},
		{
			name:           "group-by-cube",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test group-by a_kind z_kind -count count -cube`,
			expectFragment: true,
			wantSubstring:  `ssql.RollupCube`,
		},
		{
			name:           "pivot",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test pivot -row dept -col quarter -val revenue -func sum`,
			expectFragment: true,
			wantSubstring:  `ssql.Pivot`,
		},
		{
			name:           "window-row-number",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test window -row-number rn -order salary -desc`,
			expectFragment: true,
			wantSubstring:  `ssql.WRowNumber()`,
		},
		{
			name:           "window-partition-sum",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test window -sum amount total -partition dept -order date`,
			expectFragment: true,
			wantSubstring:  `ssql.WSum`,
		},
		{
			name:           "window-presorted-row-number",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test window -row-number rn -order salary -presorted`,
			expectFragment: true,
			wantSubstring:  `ssql.MustStreamWindow`,
		},
		{
			name:           "window-presorted-sum",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test window -sum amount total -partition dept -order date -presorted`,
			expectFragment: true,
			wantSubstring:  `ssql.MustStreamWindow`,
		},
		{
			name:           "window-presorted-sliding-avg",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test window -avg price ma3 -order date -preceding 2 -following 0 -presorted`,
			expectFragment: true,
			wantSubstring:  `ssql.MustStreamWindow`,
		},
		{
			name:           "window-presorted-lag",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test window -lag salary 1 prev_sal -order date -presorted`,
			expectFragment: true,
			wantSubstring:  `ssql.MustStreamWindow`,
		},
		{
			name:           "merge",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test merge sorted.jsonl -by timestamp`,
			expectFragment: true,
			wantSubstring:  `ssql.MergeSorted`,
		},
		{
			name:           "merge-multi-field",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test merge file1.jsonl file2.jsonl -by dept name -desc`,
			expectFragment: true,
			wantSubstring:  `Desc: true`,
		},
		{
			name:           "to csv",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to csv /tmp/out.csv`,
			expectFragment: true,
			wantSubstring:  `ssql.WriteCSV`,
		},
		{
			name:           "chart",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to chart -x age -y salary`,
			expectFragment: true,
			wantSubstring:  `ssql.EnhancedChart`,
		},
		{
			name:           "heatmap",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to chart -type heatmap -x age -y salary -z dept`,
			expectFragment: true,
			wantSubstring:  `ssql.HeatmapChart`,
		},
		{
			name:           "animate",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to animate -frame time -x x -y y -z val -type heatmap`,
			expectFragment: true,
			wantSubstring:  `ssql.AnimateChart`,
		},
		{
			name:           "explore",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to explore`,
			expectFragment: true,
			wantSubstring:  `ssql.DataExplore`,
		},
		{
			name:           "from xlsx",
			cmdLine:        "SSQLGO=1 /tmp/ssql_test from /tmp/test_all_commands.xlsx",
			expectFragment: true,
			wantSubstring:  `ssql.ReadXLSX`,
		},
		{
			name:           "to xlsx",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to xlsx /tmp/out.xlsx`,
			expectFragment: true,
			wantSubstring:  `ssql.WriteXLSX`,
		},
		{
			name:           "from ssh",
			cmdLine:        `SSQLGO=1 /tmp/ssql_test from ssh myhost /data/test.csv`,
			expectFragment: true,
			wantSubstring:  `sshCmd := exec.Command`,
		},
		{
			// v4.42: from-ssh-pushdown codegen embeds the .ssql script
			// as a const and inlines a small ship-and-cat-and-run helper.
			// Generated Go ssh's the script to the remote and runs `ssql
			// generate go -script -mode $mode -run` there.
			name:           "from ssh remote",
			cmdLine:        `SSQLGO=1 /tmp/ssql_test from ssh myhost /data/test.csv -- where -if age gt 25`,
			expectFragment: true,
			wantSubstring:  `remoteSSQLScript`,
		},
		{
			name:           "from ssh remote multi",
			cmdLine:        `SSQLGO=1 /tmp/ssql_test from ssh myhost /data/test.csv -- where -if age gt 25 + group-by dept -count cnt`,
			expectFragment: true,
			wantSubstring:  `ssql generate go -script`,
		},
		{
			name:           "from ssh gpu",
			cmdLine:        `SSQLGO=1 /tmp/ssql_test from ssh myhost /data/test.csv -gpu`,
			expectFragment: true,
			wantSubstring:  `ssql_gpu`,
		},
		{
			name:           "from catalog",
			cmdLine:        `SSQLGO=1 /tmp/ssql_test from catalog test-data/test-catalog.csv`,
			expectFragment: true,
			wantSubstring:  `ssql.ReadCatalog`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil && tt.expectFragment {
				// Some commands may error for other reasons, but should still generate
				t.Logf("Command had error (may be ok): %v\nOutput: %s", err, output)
			}

			outputStr := string(output)

			if tt.expectFragment {
				// Verify it generates a code fragment (JSONL output)
				if !strings.Contains(outputStr, `"type":`) {
					t.Errorf("Command %q did not generate a code fragment.\nOutput: %s", tt.name, outputStr)
				}

				// Verify expected code appears in fragment
				if !strings.Contains(outputStr, tt.wantSubstring) {
					t.Errorf("Command %q fragment missing expected substring %q.\nOutput: %s",
						tt.name, tt.wantSubstring, outputStr)
				}
			}
		})
	}
}

// TestChartGeneration specifically tests that chart generates code instead of creating HTML
func TestChartGeneration(t *testing.T) {
	// Build the binary
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Test that chart generates code when SSQLGO=1
	cmdLine := `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to chart -x z_kind -y count`
	cmd := exec.Command("bash", "-c", cmdLine)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Command output: %s", output)
	}

	outputStr := string(output)

	// Should generate a code fragment
	if !strings.Contains(outputStr, `"type":"final"`) {
		t.Errorf("Chart command did not generate a final fragment.\nOutput: %s", outputStr)
	}

	// Should contain EnhancedChart call (used for multi-series/advanced features)
	if !strings.Contains(outputStr, `ssql.EnhancedChart`) {
		t.Errorf("Chart fragment missing EnhancedChart call.\nOutput: %s", outputStr)
	}

	// Verify chart.html was NOT created (generation shouldn't execute)
	if _, err := os.Stat("chart.html"); err == nil {
		t.Error("chart.html file was created when it should only generate code")
		os.Remove("chart.html") // Clean up
	}
}

// TestToChartUnknownFieldLoud pins the fail-loudly rule for the chart
// sink: an axis field that is not in the stream must be an error, not an
// empty chart with exit 0. Found via the signal-processing guide, whose
// charts asked for `convolved` where convolve emits `value_convolved`;
// every one of them "succeeded" (DFC125 runner, 2026-09-05).
func TestToChartUnknownFieldLoud(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	dir := t.TempDir()
	csv := filepath.Join(dir, "s.csv")
	os.WriteFile(csv, []byte("sample,amplitude\n0,0.1\n1,0.2\n2,0.3\n"), 0o644)

	bad := filepath.Join(dir, "bad.html")
	out, err := exec.Command("bash", "-c",
		"/tmp/ssql_test from "+csv+" | /tmp/ssql_test to chart -x sample -y convolved -output "+bad).CombinedOutput()
	if err == nil {
		t.Fatalf("to chart with an unknown -y field exited 0; output:\n%s", out)
	}
	for _, want := range []string{"to chart", "unknown field(s): convolved", "available: amplitude, sample"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("error should mention %q; got:\n%s", want, out)
		}
	}
	if _, statErr := os.Stat(bad); statErr == nil {
		t.Errorf("an empty chart was still written for the unknown field")
	}

	good := filepath.Join(dir, "good.html")
	out, err = exec.Command("bash", "-c",
		"/tmp/ssql_test from "+csv+" | /tmp/ssql_test to chart -x sample -y amplitude -output "+good).CombinedOutput()
	if err != nil {
		t.Fatalf("to chart with valid fields failed: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(good); statErr != nil {
		t.Errorf("valid chart was not written: %v", statErr)
	}
}

// TestUpdateGeneration tests that the update command generates code correctly
func TestUpdateGeneration(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tests := []struct {
		name     string
		cmdLine  string
		wantStrs []string // substrings that should appear in output
	}{
		{
			name:    "single field update",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -set status processed`,
			wantStrs: []string{
				`"type":"stmt"`,
				`"var":"updated"`,
				`ssql.Update`,
				`mut = mut.String(\"status\", \"processed\")`,
			},
		},
		{
			name:    "multiple field update",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -set status done -set count 42`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.Update`,
				`mut = mut.String(\"status\", \"done\")`,
				`mut = mut.Int(\"count\", int64(42))`,
			},
		},
		{
			name:    "type inference - bool",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -set active true`,
			wantStrs: []string{
				`mut = mut.Bool(\"active\", true)`,
			},
		},
		{
			name:    "type inference - float",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -set price 99.99`,
			wantStrs: []string{
				`mut = mut.Float(\"price\", 99.9`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)

			for _, want := range tt.wantStrs {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
				}
			}
		})
	}
}

// TestUpdateConditionalGeneration tests that the update command generates correct code for conditional updates
func TestUpdateConditionalGeneration(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tests := []struct {
		name     string
		cmdLine  string
		wantStrs []string // substrings that should appear in output
	}{
		{
			name:    "simple conditional - single clause",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -if age gt 30 -set priority high`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.Update`,
				`frozen`,
				// The shared condition lowering emits the literal as an
				// untyped constant in a float64 comparison (Phase B). NB the
				// fragment is raw JSON, where > is encoded as >.
				`ssql.GetOr(frozen, \"age\", float64(0))`,
				`\u003e 30) {`,
			},
		},
		{
			name:    "multiple clauses - first match wins",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -if purchases gt 5000 -set tier Gold + -if purchases gt 1000 -set tier Silver + -set tier Bronze`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.Update`,
				`else if`,
				`else {`,
				`Gold`,
				`Silver`,
				`Bronze`,
			},
		},
		{
			name:    "AND logic within clause",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -if status eq active -if age gt 30 -set priority high`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.Update`,
				`frozen`,
				`status`,
				`active`,
				`age`,
			},
		},
		{
			name:    "multiple updates per clause",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -if tier eq Gold -set discount 0.2 -set priority high`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.Update`,
				`tier`,
				`Gold`,
				`mut.Float`,
				`discount`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)

			for _, want := range tt.wantStrs {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
				}
			}
		})
	}
}

// TestNegatedConditionGeneration locks the +if / +if-expr negation emissions.
// Before v4.56.1, record codegen emitted +if UN-negated (the complement rows),
// silently DROPPED +if-expr (the negated form arrives as a map, not a string),
// dropped an -if-expr that had no accompanying -if entirely, and typed codegen
// ignored negation too — while exec was correct. The optimiser round-trip
// (parseWhereArgs/buildWhereArgs in generate ssql) dropped +if/+if-expr tokens
// whenever a rewrite rule rebuilt a where. The equivalence cases
// (where_negated_if, where_negated_expr, update_negated_if,
// where_negated_survives_simplify, where_negated_expr_survives_reorder) are
// the end-to-end gate; this pins the exact emitted source.
func TestNegatedConditionGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tmpFile := "/tmp/negation_gen_test.csv"
	if err := os.WriteFile(tmpFile, []byte("name,age\nAlice,30\nBob,20\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(tmpFile)

	tests := []struct {
		name     string
		cmdLine  string
		wantStrs []string
	}{
		{
			name:     "record where +if negates",
			cmdLine:  `export SSQL_MODE=record && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where +if age gt 25 | /tmp/ssql_test generate go +O`,
			wantStrs: []string{`return !((ssql.GetOr(r, "age"`},
		},
		{
			name:     "record where +if-expr negates (not dropped)",
			cmdLine:  `export SSQL_MODE=record && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where +if-expr 'age > 25' | /tmp/ssql_test generate go +O`,
			wantStrs: []string{`return !((ssql.GetOr(r, "age", int64(0)) > 25))`},
		},
		{
			name:     "record update +if negates",
			cmdLine:  `export SSQL_MODE=record && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test update +if age gt 25 -set tag young | /tmp/ssql_test generate go +O`,
			wantStrs: []string{`if !((ssql.GetOr(frozen, "age"`},
		},
		{
			name:     "record update +if-expr negates (not dropped)",
			cmdLine:  `export SSQL_MODE=record && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test update +if-expr 'age > 25' -set tag young | /tmp/ssql_test generate go +O`,
			wantStrs: []string{`if !((ssql.GetOr(frozen, "age", int64(0)) > 25)) {`},
		},
		{
			name:     "record update -if-expr without -if keeps the condition",
			cmdLine:  `export SSQL_MODE=record && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test update -if-expr 'age > 25' -set tag old | /tmp/ssql_test generate go +O`,
			wantStrs: []string{`if (ssql.GetOr(frozen, "age", int64(0)) > 25) {`},
		},
		{
			name:     "typed where +if negates",
			cmdLine:  `export SSQL_MODE=typed && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where +if age gt 25 | /tmp/ssql_test generate go +O`,
			wantStrs: []string{`return !((r.Age > 25))`},
		},
		{
			name:     "typed update +if negates",
			cmdLine:  `export SSQL_MODE=typed && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test update +if age gt 25 -set tag young | /tmp/ssql_test generate go +O`,
			wantStrs: []string{`if !((r.Age > 25)) {`},
		},
		{
			// Optimiser round-trip: range tightening (gt 5 + ge 8) rebuilds
			// the where args; the +if must survive the rebuild.
			name:     "generate ssql keeps +if through simplification",
			cmdLine:  `export SSQL_MODE=record && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -if age gt 5 -if age ge 8 +if age lt 12 | /tmp/ssql_test generate ssql`,
			wantStrs: []string{`+if age lt 12`},
		},
		{
			// Predicate reorder (ne before gt) rebuilds too; +if-expr must
			// survive.
			// Since convergence Phase C the trivial negated expression
			// CANONICALIZES to a structured +if — negation still survives
			// the reorder rebuild, now in flag form.
			name:     "generate ssql canonicalizes +if-expr and keeps negation through reorder",
			cmdLine:  `export SSQL_MODE=record && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -if age gt 8 -if name ne Bob +if-expr 'age > 12' | /tmp/ssql_test generate ssql`,
			wantStrs: []string{`+if age gt 12`},
		},
		{
			// A float-literal expression refuses canonicalization (the
			// int-column trap), so THIS one must still survive the reorder
			// rebuild as +if-expr — the original Phase-4-era guard.
			name:     "generate ssql keeps non-canonicalizable +if-expr through reorder",
			cmdLine:  `export SSQL_MODE=record && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -if age gt 8 -if name ne Bob +if-expr 'age > 12.5' | /tmp/ssql_test generate ssql`,
			wantStrs: []string{`+if-expr`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)
			for _, want := range tt.wantStrs {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
				}
			}
		})
	}
}

// TestTierVKeepsTypedPipeline locks expr-transpiler Phase 1.5: an expression
// OUTSIDE the native subset (sha256) evaluates via the VM against a generated
// static env — and the stage STAYS typed, so downstream stages keep their
// parallel forms. Before 1.5, one such expression ejected the whole stage to
// record mode and the planner downgraded everything downstream.
func TestTierVKeepsTypedPipeline(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tmpFile := "/tmp/tierv_gen_test.csv"
	if err := os.WriteFile(tmpFile, []byte("city,pop\nOslo,31\nCairo,10\nAo,7\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(tmpFile)

	t.Run("where tier V stays parallel through group-by", func(t *testing.T) {
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=parallel && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test where -if-expr 'sha256(city) > "8"'`+
				` | /tmp/ssql_test group-by pop -count c | /tmp/ssql_test generate go +O`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate failed: %v\n%s", err, out)
		}
		src := string(out)
		for _, want := range []string{
			"exprvm.MustCompileExprFilterEnv", // the Tier V predicate var
			"exprEnv",                         // the generated static-env constructor
			"GroupByParallel",                 // downstream KEPT its parallel form
		} {
			if !strings.Contains(src, want) {
				t.Errorf("generated source missing %q:\n%s", want, src)
			}
		}
		// The pre-1.5 record ejection must be gone: no record-mode VM filter,
		// no ssql.Where over Records.
		for _, reject := range []string{"MustCompileExprFilter(", "ssql.Where("} {
			if strings.Contains(src, reject) {
				t.Errorf("generated source still contains record-mode marker %q:\n%s", reject, src)
			}
		}
	})

	t.Run("explain names the tiers", func(t *testing.T) {
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=typed && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test where -if-expr 'pop > 8'`+
				` | /tmp/ssql_test update -set-expr city 'sha256(city)'`+
				` | /tmp/ssql_test generate go +O -explain > /dev/null`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate -explain failed: %v\n%s", err, out)
		}
		stderr := string(out)
		for _, want := range []string{
			`expr "pop > 8": native`,
			`expr "sha256(city)": VM with static env`,
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("-explain output missing %q:\n%s", want, stderr)
			}
		}
	})

	t.Run("untypeable new field falls back with explain note", func(t *testing.T) {
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=typed && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test update -set-expr h 'sha256(city)'`+
				` | /tmp/ssql_test generate go +O -explain > /dev/null`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate -explain failed: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "record fallback") {
			t.Errorf("-explain output missing the record-fallback note:\n%s", out)
		}
	})
}

// TestStreamExprTypedGeneration locks the -stream-expr typed accumulator
// lowering (expr-transpiler Phase 2): state fields on the aggregator struct,
// ONE simultaneous multi-assignment in Add() (sequential assignment breaks
// {a: b, b: a} — gated by the groupby_stream_swap equivalence golden), the
// serial GroupBy form (fold state is not mergeable), and record fallback for
// shapes a typed struct can't hold.
func TestStreamExprTypedGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tmpFile := "/tmp/streamexpr_gen_test.csv"
	if err := os.WriteFile(tmpFile, []byte("dept,salary\na,100\nb,200\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(tmpFile)

	t.Run("typed accumulator emission", func(t *testing.T) {
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=typed && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test group-by dept -stream-expr '{s:0, n:0}' '{s:s+salary, n:n+1}' 's/n' avg_sal`+
				` | /tmp/ssql_test generate go +O`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate failed: %v\n%s", err, out)
		}
		src := string(out)
		for _, want := range []string{
			"se0_s int64",
			"se0_n int64",
			"a.se0_s, a.se0_n = (a.se0_s + r.Salary), (a.se0_n + 1)", // ONE multi-assign
			"AvgSal: (float64(a.se0_s) / float64(a.se0_n))",
			"typed.GroupBy(", // serial form — fold state is not mergeable
		} {
			if !strings.Contains(src, want) {
				t.Errorf("generated source missing %q:\n%s", want, src)
			}
		}
		if strings.Contains(src, "GroupByParallel") {
			t.Errorf("stream-expr must not use the parallel group-by:\n%s", src)
		}
	})

	t.Run("untypeable shape falls back with explain note", func(t *testing.T) {
		// every drops a state key — the VM legitimately shrinks the state
		// object; a struct can't. Record fallback, reason under -explain.
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=typed && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test group-by dept -stream-expr '{s:0, n:0}' '{s:s+salary}' 's' total`+
				` | /tmp/ssql_test generate go +O -explain`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate -explain failed: %v\n%s", err, out)
		}
		src := string(out)
		if !strings.Contains(src, "ssql.StreamExprAgg") {
			t.Errorf("fallback must emit the record StreamExprAgg path:\n%s", src)
		}
		if !strings.Contains(src, "record fallback") {
			t.Errorf("-explain output missing the record-fallback note:\n%s", src)
		}
	})
}

// TestExprAggTypedGeneration locks the -expr aggregation lowering
// (expr-transpiler Phase 3): accumulator terms with the element's own type,
// a Merge that adds terms and counts (mergeable — so GroupByParallel is
// KEPT, unlike -stream-expr), and record fallback for shapes with no typed
// lowering (a field bound to the VM's per-group value array).
func TestExprAggTypedGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tmpFile := "/tmp/expragg_gen_test.csv"
	if err := os.WriteFile(tmpFile, []byte("dept,salary\na,100\nb,200\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(tmpFile)

	t.Run("mergeable accumulator keeps parallel group-by", func(t *testing.T) {
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=parallel && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test group-by dept -expr 'sum(salary * 2) / count()' v`+
				` | /tmp/ssql_test generate go +O`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate failed: %v\n%s", err, out)
		}
		src := string(out)
		for _, want := range []string{
			"ea0_t0 int64", // element's own type, not blanket float64
			"ea0_cnt int64",
			"a.ea0_t0 += (r.Salary * 2)",
			"a.ea0_t0 += o.ea0_t0", // the Merge — sums add across shards
			"a.ea0_cnt += o.ea0_cnt",
			"V: (float64(a.ea0_t0) / float64(a.ea0_cnt))",
			"typed.GroupByParallel", // mergeable → parallel form KEPT
		} {
			if !strings.Contains(src, want) {
				t.Errorf("generated source missing %q:\n%s", want, src)
			}
		}
	})

	t.Run("outer value-array shape falls back with explain note", func(t *testing.T) {
		// len(salary) outside an aggregation is the VM's value array —
		// no typed lowering; record fallback preserves the behaviour.
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=typed && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test group-by dept -expr 'sum(salary) / len(salary)' v`+
				` | /tmp/ssql_test generate go +O -explain`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate -explain failed: %v\n%s", err, out)
		}
		src := string(out)
		if !strings.Contains(src, "ssql.ExprAgg") {
			t.Errorf("fallback must emit the record ExprAgg path:\n%s", src)
		}
		if !strings.Contains(src, "record fallback") {
			t.Errorf("-explain output missing the record-fallback note:\n%s", src)
		}
	})
}

// TestRecordNativeExprGeneration locks the record-mode native expression
// emission (expr-transpiler Phase 4): with advisory column types from the
// CSV source, -if-expr predicates and -set-expr assignments emit typed
// GetOr code (no VM var, no runtime type-switch); without advisory types
// (an intervening stage that doesn't propagate them) the VM path is kept —
// zero regression.
func TestRecordNativeExprGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tmpFile := "/tmp/recnative_gen_test.csv"
	if err := os.WriteFile(tmpFile, []byte("dept,salary\na,100\nb,200\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(tmpFile)

	t.Run("where predicate goes native", func(t *testing.T) {
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=record && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test where -if-expr 'salary > 150 && dept != "x"' | /tmp/ssql_test generate go +O`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate failed: %v\n%s", err, out)
		}
		src := string(out)
		if !strings.Contains(src, `(ssql.GetOr(r, "salary", int64(0)) > 150)`) {
			t.Errorf("missing native GetOr predicate:\n%s", src)
		}
		if strings.Contains(src, "MustCompileExprFilter") {
			t.Errorf("VM filter var still emitted for a native predicate:\n%s", src)
		}
	})

	t.Run("update set-expr goes native typed setter", func(t *testing.T) {
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=record && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test update -if-expr 'salary > 150' -set-expr salary 'salary * 2' | /tmp/ssql_test generate go +O`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate failed: %v\n%s", err, out)
		}
		src := string(out)
		if !strings.Contains(src, `mut = mut.Int("salary", (ssql.GetOr(frozen, "salary", int64(0)) * 2))`) {
			t.Errorf("missing native typed setter:\n%s", src)
		}
		if strings.Contains(src, "MustCompileExpr") {
			t.Errorf("VM eval var still emitted for a native set-expr:\n%s", src)
		}
		if strings.Contains(src, "switch v := result.(type)") {
			t.Errorf("runtime type-switch still emitted for a native set-expr:\n%s", src)
		}
	})

	t.Run("no advisory types keeps the VM path", func(t *testing.T) {
		// sort's fragment doesn't propagate AdvisoryTypes → downstream
		// where uses the VM exactly as before Phase 4.
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=record && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test sort salary | /tmp/ssql_test where -if-expr 'salary > 150'`+
				` | /tmp/ssql_test generate go +O -explain`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate failed: %v\n%s", err, out)
		}
		src := string(out)
		if !strings.Contains(src, "MustCompileExprFilter") {
			t.Errorf("VM filter var missing without advisory types:\n%s", src)
		}
		if !strings.Contains(src, "no advisory column types") {
			t.Errorf("-explain output missing the no-advisory note:\n%s", src)
		}
	})

	t.Run("where propagates advisory types", func(t *testing.T) {
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=record && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test where -if dept eq a | /tmp/ssql_test where -if-expr 'salary > 150'`+
				` | /tmp/ssql_test generate go +O`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate failed: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), `(ssql.GetOr(r, "salary", int64(0)) > 150)`) {
			t.Errorf("advisory types not propagated through where:\n%s", out)
		}
	})

	t.Run("update retype drops the field from advisory", func(t *testing.T) {
		// salary becomes float64 (division) — a following -if-expr on
		// salary must NOT use the stale int64 advisory type. The update
		// propagates salary: float64 (unconditional retype tracked), so
		// the where should be native with a float64 GetOr — or, if
		// dropped, VM. Either is sound; stale int64 is the bug.
		cmd := exec.Command("bash", "-c",
			`export SSQL_MODE=record && /tmp/ssql_test from `+tmpFile+
				` | /tmp/ssql_test update -set-expr salary 'salary / 2' | /tmp/ssql_test where -if-expr 'salary > 60'`+
				` | /tmp/ssql_test generate go +O`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generate failed: %v\n%s", err, out)
		}
		if strings.Contains(string(out), `ssql.GetOr(r, "salary", int64(0)) > 60`) {
			t.Errorf("stale int64 advisory used after a retyping update:\n%s", out)
		}
	})
}

// TestCatalogPredicateExtraction pins the optimizer's catalog rewrite
// semantics after the range-leak fix (2026-08-11): EXACT metadata columns
// hold for every row in a shard, so extraction REPLACES the row filter;
// RANGE (_from/_to) columns prune only conservatively — a straddling shard
// still contains non-matching rows — so the row filter is KEPT (and the
// pushdown rule then ships it shard-side). Before the fix, range
// extraction deleted the row filter and straddling shards leaked rows
// (reproduced on the LXD rig). Negated (+if) conditions now extract too.
func TestCatalogPredicateExtraction(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	catFile := "/tmp/catalog_extract_test.csv"
	if err := os.WriteFile(catFile, []byte(
		"host,path,region,date_from,date_to\n"+
			"n1,/data/s1.csv,east,2026-01-01,2026-02-01\n"+
			"n2,/data/s2.csv,west,2026-02-15,2026-03-15\n"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(catFile)

	tests := []struct {
		name     string
		where    string
		wantStrs []string
		rejects  []string
	}{
		{
			// Range column: prune AND keep the row filter (which pushdown
			// then ships into the shard-side `--` pipeline).
			name:     "range condition keeps row filter",
			where:    `where -if date ge 2026-03-01`,
			wantStrs: []string{`-if date ge 2026-03-01 -- where -if date ge 2026-03-01`},
		},
		{
			// Exact column: the metadata value holds for every row —
			// extraction fully replaces the row filter.
			name:     "exact condition fully extracted",
			where:    `where -if region eq east`,
			wantStrs: []string{`-if region eq east`},
			rejects:  []string{`-- where`},
		},
		{
			name:     "negated exact condition fully extracted",
			where:    `where +if region eq east`,
			wantStrs: []string{`+if region eq east`},
			rejects:  []string{`-- where`},
		},
		{
			name:     "negated range condition prunes AND keeps row filter",
			where:    `where +if date ge 2026-03-01`,
			wantStrs: []string{`+if date ge 2026-03-01 -- where +if date ge 2026-03-01`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c",
				`(export SSQL_MODE=record; /tmp/ssql_test from catalog `+catFile+
					` | /tmp/ssql_test `+tt.where+` | /tmp/ssql_test to jsonl) | /tmp/ssql_test generate ssql`)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("generate ssql failed: %v\n%s", err, out)
			}
			for _, want := range tt.wantStrs {
				if !strings.Contains(string(out), want) {
					t.Errorf("optimized pipeline missing %q:\n%s", want, out)
				}
			}
			for _, reject := range tt.rejects {
				if strings.Contains(string(out), reject) {
					t.Errorf("optimized pipeline should not contain %q:\n%s", reject, out)
				}
			}
		})
	}
}

// TestTableGeneration tests that the table command generates correct Go code
func TestTableGeneration(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tests := []struct {
		name     string
		cmdLine  string
		wantStrs []string
	}{
		{
			name:    "basic table generation",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to table`,
			wantStrs: []string{
				`"type":"final"`,
				`ssql.DisplayTable`,
				`records`,
				`50`,
			},
		},
		{
			name:    "table with max-width",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to table -max-width 30`,
			wantStrs: []string{
				`"type":"final"`,
				`ssql.DisplayTable`,
				`30`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)

			for _, want := range tt.wantStrs {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
				}
			}
		})
	}
}

// TestIncludeGeneration tests code generation for the include command
func TestIncludeGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tests := []struct {
		name     string
		cmdLine  string
		wantStrs []string
	}{
		{
			name:    "include basic",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test include name age`,
			wantStrs: []string{
				`"type":"stmt"`,
				`"var":"included"`,
				`ssql.Select`,
				`includedMap`,
			},
		},
		{
			name:    "include multiple fields",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test include field1 field2 field3`,
			wantStrs: []string{
				`"type":"stmt"`,
				`"var":"included"`,
				`field1`,
				`field2`,
				`field3`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)

			for _, want := range tt.wantStrs {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
				}
			}
		})
	}
}

// TestExcludeGeneration tests code generation for the exclude command
func TestExcludeGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tests := []struct {
		name     string
		cmdLine  string
		wantStrs []string
	}{
		{
			name:    "exclude basic",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test exclude salary city`,
			wantStrs: []string{
				`"type":"stmt"`,
				`"var":"excluded"`,
				`ssql.Select`,
				`Delete`,
				`salary`,
				`city`,
			},
		},
		{
			name:    "exclude multiple fields",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test exclude field1 field2 field3`,
			wantStrs: []string{
				`"type":"stmt"`,
				`"var":"excluded"`,
				`Delete`,
				`field1`,
				`field2`,
				`field3`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)

			for _, want := range tt.wantStrs {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
				}
			}
		})
	}
}

// TestRenameGeneration tests code generation for the rename command
func TestRenameGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tests := []struct {
		name     string
		cmdLine  string
		wantStrs []string
	}{
		{
			name:    "rename basic",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test rename -as name full_name -as age years`,
			wantStrs: []string{
				`"type":"stmt"`,
				`"var":"renamed"`,
				`ssql.Select`,
				`Rename`,
				`name`,
				`full_name`,
			},
		},
		{
			name:    "rename single field",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test rename -as old new`,
			wantStrs: []string{
				`"type":"stmt"`,
				`"var":"renamed"`,
				`Rename`,
				`old`,
				`new`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)

			for _, want := range tt.wantStrs {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
				}
			}
		})
	}
}

// TestReadJSONGeneration tests code generation for the from command
func TestReadJSONGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tests := []struct {
		name     string
		cmdLine  string
		wantStrs []string
	}{
		{
			name:    "from basic",
			cmdLine: `SSQLGO=1 /tmp/ssql_test from /tmp/test.json`,
			wantStrs: []string{
				`"type":"init"`,
				`"var":"records"`,
				`ssql.ReadJSONAuto`,
				`/tmp/test.json`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)

			for _, want := range tt.wantStrs {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
				}
			}
		})
	}
}

// TestWriteJSONGeneration tests code generation for the to json command
func TestWriteJSONGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tests := []struct {
		name     string
		cmdLine  string
		wantStrs []string
	}{
		{
			name:    "to json JSONL mode",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to json /tmp/output.jsonl`,
			wantStrs: []string{
				`"type":"final"`,
				`ssql.WriteJSON`,
				`/tmp/output.jsonl`,
			},
		},
		{
			name:    "to json pretty mode",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to json /tmp/output.json`,
			wantStrs: []string{
				`"type":"final"`,
				`ssql.WriteJSONPretty`,
				`/tmp/output.json`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)

			for _, want := range tt.wantStrs {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
				}
			}
		})
	}
}

// TestJoinGeneration tests code generation for the join command
// Note: join now only accepts JSONL files for secondary sources (use process substitution for CSV)
func TestJoinGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Create test JSONL files for join operations
	orders := `{"id":101,"user_id":1,"amount":50}
{"id":102,"user_id":2,"amount":75}
`
	if err := os.WriteFile("/tmp/test_orders.jsonl", []byte(orders), 0644); err != nil {
		t.Fatalf("Failed to create orders JSONL: %v", err)
	}
	defer os.Remove("/tmp/test_orders.jsonl")

	tests := []struct {
		name     string
		cmdLine  string
		wantStrs []string
	}{
		{
			name:    "join basic with -using",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join /tmp/test_orders.jsonl -using user_id`,
			wantStrs: []string{
				`"type":"func"`,
				`rightSource1`,
				`ssql.ReadJSON`,
				`/tmp/test_orders.jsonl`,
				`"type":"stmt"`,
				`"var":"joined"`,
				`ssql.InnerJoin`,
				`ssql.OnFields`,
				`user_id`,
			},
		},
		{
			name:    "join with -type left",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join -type left /tmp/test_orders.jsonl -using user_id`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.LeftJoin`,
			},
		},
		{
			name:    "join with -type right",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join -type right /tmp/test_orders.jsonl -using user_id`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.RightJoin`,
			},
		},
		{
			name:    "join with -type full",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join -type full /tmp/test_orders.jsonl -using user_id`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.FullJoin`,
			},
		},
		{
			name:    "join with -on different fields",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join /tmp/test_orders.jsonl -on id user_id`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.OnFieldPair`,
				`id`,
				`user_id`,
			},
		},
		{
			name:    "join with -as rename",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join /tmp/test_orders.jsonl -on id user_id -as amount order_amount`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.LookupJoin`,
				`ssql.Lookup`,
				`order_amount`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command output: %s", output)
			}

			outputStr := string(output)

			for _, want := range tt.wantStrs {
				if !strings.Contains(outputStr, want) {
					t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
				}
			}
		})
	}
}

// TestJoinGenerationFullPipeline tests that join works in a complete pipeline
// Note: join now only accepts JSONL files for secondary sources
func TestJoinGenerationFullPipeline(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Create test files - CSV for primary, JSONL for secondary
	users := "id,name\n1,Alice\n2,Bob\n"
	orders := `{"user_id":1,"amount":50}
{"user_id":2,"amount":75}
`

	if err := os.WriteFile("/tmp/test_users.csv", []byte(users), 0644); err != nil {
		t.Fatalf("Failed to create users CSV: %v", err)
	}
	defer os.Remove("/tmp/test_users.csv")

	if err := os.WriteFile("/tmp/test_orders.jsonl", []byte(orders), 0644); err != nil {
		t.Fatalf("Failed to create orders JSONL: %v", err)
	}
	defer os.Remove("/tmp/test_orders.jsonl")

	// Test full pipeline with join (using JSONL for right side)
	// Note: v4 uses -using for same field name, -on for different field names
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from /tmp/test_users.csv | /tmp/ssql_test join /tmp/test_orders.jsonl -on id user_id | /tmp/ssql_test generate go +O`
	cmd := exec.Command("bash", "-c", pipeline)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pipeline failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Check for expected elements in generated code
	expectations := []string{
		"package main",
		"ssql.ReadCSV",     // For the primary input (left side)
		"rightSource1",     // Function for right side
		"ssql.InnerJoin",   // Join function
		"ssql.OnFieldPair", // Different field names predicate
		"func main()",
		"ssql.ReadJSON", // For the secondary input (right side)
	}

	for _, expected := range expectations {
		if !strings.Contains(outputStr, expected) {
			t.Errorf("Generated code missing expected element: %q\nGot: %s", expected, outputStr)
		}
	}
}

// TestStreamExprGeneration tests that -stream-expr generates correct code
func TestStreamExprGeneration(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	cmdLine := `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test group-by dept -stream-expr '{s:0}' '{s:s+salary}' 's' total`
	cmd := exec.Command("bash", "-c", cmdLine)
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		t.Fatalf("Expected success, got error: %v\nOutput: %s", err, outputStr)
	}

	// Should contain StreamExprAgg call
	expected := []string{
		`"type":"stmt"`,
		`ssql.StreamExprAgg`,
		`{s:0}`,
		`{s:s+salary}`,
	}
	for _, want := range expected {
		if !strings.Contains(outputStr, want) {
			t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
		}
	}
}

// TestStreamExprFullPipeline tests stream-expr code generation in a full pipeline
func TestStreamExprFullPipeline(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Create a test CSV
	csvContent := "name,salary,dept\nAlice,90000,Engineering\nBob,75000,Sales\n"
	tmpFile := "/tmp/test_stream_expr_pipe.csv"
	if err := os.WriteFile(tmpFile, []byte(csvContent), 0644); err != nil {
		t.Fatalf("Failed to create test CSV: %v", err)
	}
	defer os.Remove(tmpFile)

	pipeline := `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test group-by dept -stream-expr '{s:0}' '{s:s+salary}' 's' total | /tmp/ssql_test generate go +O`
	cmd := exec.Command("bash", "-c", pipeline)
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		t.Fatalf("Expected success, got error: %v\nOutput: %s", err, outputStr)
	}

	// Should produce valid Go code with StreamExprAgg
	expected := []string{
		"package main",
		"ssql.StreamExprAgg",
		"ssql.GroupByFields",
		"ssql.Aggregate",
	}
	for _, want := range expected {
		if !strings.Contains(outputStr, want) {
			t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
		}
	}
}

// TestStreamExprWithBuiltinAgg tests stream-expr combined with built-in aggregations
func TestStreamExprWithBuiltinAgg(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	cmdLine := `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test group-by dept -count num -stream-expr '{s:0}' '{s:s+salary}' 's' total`
	cmd := exec.Command("bash", "-c", cmdLine)
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		t.Fatalf("Expected success, got error: %v\nOutput: %s", err, outputStr)
	}

	// Should contain both Count and StreamExprAgg
	expected := []string{
		`ssql.Count()`,
		`ssql.StreamExprAgg`,
	}
	for _, want := range expected {
		if !strings.Contains(outputStr, want) {
			t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
		}
	}
}

// TestJoinWithProcessSubstitutionGeneration tests that join correctly handles
// nested fragments from process substitution in code generation mode
func TestJoinWithProcessSubstitutionGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Create test CSV files
	users := "id,name\n1,Alice\n2,Bob\n"
	orders := "user_id,amount\n1,50\n2,75\n"

	if err := os.WriteFile("/tmp/test_users.csv", []byte(users), 0644); err != nil {
		t.Fatalf("Failed to create users CSV: %v", err)
	}
	defer os.Remove("/tmp/test_users.csv")

	if err := os.WriteFile("/tmp/test_orders.csv", []byte(orders), 0644); err != nil {
		t.Fatalf("Failed to create orders CSV: %v", err)
	}
	defer os.Remove("/tmp/test_orders.csv")

	// Test full pipeline with join using process substitution for right side
	// This tests the nested fragment merging feature
	// Note: v4 uses -on LEFT RIGHT for different field names
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from /tmp/test_users.csv | /tmp/ssql_test join <(/tmp/ssql_test from csv /tmp/test_orders.csv) -on id user_id | /tmp/ssql_test generate go +O`
	cmd := exec.Command("bash", "-c", pipeline)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pipeline failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Check for expected elements in generated code
	// With process substitution, both sides should use ssql.ReadCSV
	expectations := []string{
		"package main",
		"ssql.ReadCSV",     // Both inputs are CSV files
		"rightSource1",     // Function wrapping the subprocess
		"ssql.InnerJoin",   // Join function
		"ssql.OnFieldPair", // Different field names
		"func main()",
	}

	for _, expected := range expectations {
		if !strings.Contains(outputStr, expected) {
			t.Errorf("Generated code missing expected element: %q\nGot: %s", expected, outputStr)
		}
	}
}

// TestUpdateSchemaNewField tests that update -set with a new field updates the schema header
func TestUpdateSchemaNewField(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Input with schema header - no "tier" field
	input := `{"_schema":{"fields":["name","status"],"types":{"name":"string","status":"string"}}}
{"name":"Alice","status":"pending"}
{"name":"Bob","status":"active"}
`
	if err := os.WriteFile("/tmp/test_update_schema.jsonl", []byte(input), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	defer os.Remove("/tmp/test_update_schema.jsonl")

	cmdLine := `/tmp/ssql_test update -set tier gold < /tmp/test_update_schema.jsonl`
	cmd := exec.Command("bash", "-c", cmdLine)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Command failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)
	lines := strings.Split(strings.TrimSpace(outputStr), "\n")

	if len(lines) < 1 {
		t.Fatalf("Expected at least 1 line of output, got: %s", outputStr)
	}

	// First line should be the schema header with "tier" added
	schemaLine := lines[0]
	if !strings.Contains(schemaLine, `"_schema"`) {
		t.Fatalf("First line should be schema header, got: %s", schemaLine)
	}
	if !strings.Contains(schemaLine, `"tier"`) {
		t.Errorf("Schema header should include new field 'tier', got: %s", schemaLine)
	}

	// Data lines should contain tier field
	for i, line := range lines[1:] {
		if !strings.Contains(line, `"tier"`) {
			t.Errorf("Data line %d should contain 'tier' field, got: %s", i+1, line)
		}
		if !strings.Contains(line, `"gold"`) {
			t.Errorf("Data line %d should contain 'gold' value, got: %s", i+1, line)
		}
	}
}

// TestTopGeneration tests code generation for the top command
func TestTopGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	cmdLine := `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test top 5 -field salary`
	cmd := exec.Command("bash", "-c", cmdLine)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Command output: %s", output)
	}

	outputStr := string(output)
	// Record codegen must rank by natural type (CompareAny), NOT the old
	// float64 key that coerced strings to 0.0 — matches CLI execution + typed.
	for _, want := range []string{`"type":"stmt"`, `ssql.TopByFunc`, `ssql.CompareAny`, `salary`, `"var":"topRecords"`} {
		if !strings.Contains(outputStr, want) {
			t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
		}
	}
	if strings.Contains(outputStr, `float64 {`) {
		t.Errorf("top must not emit the numeric-only float64 key anymore, got: %s", outputStr)
	}
}

// TestTopGenerationAsc tests code generation for top -asc
func TestTopGenerationAsc(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	cmdLine := `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test top 3 -field age -asc`
	cmd := exec.Command("bash", "-c", cmdLine)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Command output: %s", output)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, `ssql.BottomByFunc`) || !strings.Contains(outputStr, `ssql.CompareAny`) {
		t.Errorf("Expected ssql.BottomByFunc + CompareAny for -asc, got: %s", outputStr)
	}
}

// TestPresortedGroupByGeneration tests code generation for group-by -presorted
func TestPresortedGroupByGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	cmdLine := `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test group-by dept -count n -presorted`
	cmd := exec.Command("bash", "-c", cmdLine)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Command output: %s", output)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, `ssql.StreamGroupByFields`) {
		t.Errorf("Expected output to contain ssql.StreamGroupByFields, got: %s", outputStr)
	}
	if !strings.Contains(outputStr, `ssql.Aggregate`) {
		t.Errorf("Expected output to contain ssql.Aggregate, got: %s", outputStr)
	}
}

// TestParameterizedCodeGeneration verifies that generated code uses flag variables
// instead of hardcoded literals, enabling reuse with different inputs.
func TestParameterizedCodeGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Create test input
	tmpFile := "/tmp/test_param_gen.csv"
	if err := os.WriteFile(tmpFile, []byte("name,age\nAlice,30\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(tmpFile)

	tests := []struct {
		name string
		cmd  string
		want []string
	}{
		{
			name: "input file parameterized",
			cmd:  `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test generate go +O`,
			want: []string{`flag.String("input"`, `"input CSV file"`, `*flagInput`, `flag.Parse()`},
		},
		{
			name: "output file parameterized",
			cmd:  `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test to csv /tmp/out.csv | /tmp/ssql_test generate go +O`,
			want: []string{`flag.String("output"`, `*flagOutput`, `flag.Parse()`},
		},
		{
			name: "limit parameterized as int",
			cmd:  `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test limit 42 | /tmp/ssql_test generate go +O`,
			want: []string{`flag.Int("limit"`, `42`, `*flagLimit`},
		},
		{
			name: "offset parameterized as int",
			cmd:  `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test offset 5 | /tmp/ssql_test generate go +O`,
			want: []string{`flag.Int("offset"`, `5`, `*flagOffset`},
		},
		{
			name: "stdin input has no flag",
			cmd:  `echo '{"name":"Alice"}' | SSQLGO=1 /tmp/ssql_test from json | /tmp/ssql_test generate go +O`,
			want: []string{`ReadJSONFromReader(os.Stdin)`},
		},
		{
			name: "flag name deduplication",
			cmd:  `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test join ` + tmpFile + ` -using name | /tmp/ssql_test generate go +O`,
			want: []string{`flag.String("input"`, `flag.String("join"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmd)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("Command error: %v\nOutput: %s", err, output)
			}
			outputStr := string(output)
			for _, w := range tt.want {
				if !strings.Contains(outputStr, w) {
					t.Errorf("Expected %q in output, got:\n%s", w, outputStr)
				}
			}
		})
	}
}

// TestParameterizedStdinNoFlags verifies that stdin-only pipelines don't emit flag declarations
func TestParameterizedStdinNoFlags(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	cmd := exec.Command("bash", "-c", `echo '{"name":"Alice"}' | SSQLGO=1 /tmp/ssql_test from json | /tmp/ssql_test to table | /tmp/ssql_test generate go +O`)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Command error: %v\nOutput: %s", err, output)
	}
	outputStr := string(output)

	// Should NOT contain flag infrastructure when everything is stdin/stdout
	if strings.Contains(outputStr, `flag.Parse()`) {
		t.Errorf("Stdin-only pipeline should not have flag.Parse(), got:\n%s", outputStr)
	}
	if strings.Contains(outputStr, `"flag"`) {
		t.Errorf("Stdin-only pipeline should not import flag, got:\n%s", outputStr)
	}
}

// TestJoinFieldCollision tests join behavior with overlapping field names.
func TestJoinFieldCollision(t *testing.T) {
	// Build binary
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_join_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_join_test")

	// Create test data
	leftCSV := "id,name,score\n1,Alice,90\n2,Bob,80\n"
	rightCSV := "id,name,grade\n1,Alice_R,A\n2,Bobby,B\n"
	rightNoCollision := "id,grade\n1,A\n2,B\n"

	os.WriteFile("/tmp/join_left.csv", []byte(leftCSV), 0644)
	os.WriteFile("/tmp/join_right.csv", []byte(rightCSV), 0644)
	os.WriteFile("/tmp/join_right_nocol.csv", []byte(rightNoCollision), 0644)
	defer os.Remove("/tmp/join_left.csv")
	defer os.Remove("/tmp/join_right.csv")
	defer os.Remove("/tmp/join_right_nocol.csv")

	ssql := "/tmp/ssql_join_test"

	tests := []struct {
		name      string
		cmd       string
		wantErr   string   // substring in stderr if error expected
		wantCols  []string // field names expected in output
		dontWant  []string // field names that should NOT appear
		wantValue string   // substring that should appear in output
	}{
		{
			name:    "collision errors by default",
			cmd:     ssql + " from csv /tmp/join_left.csv | " + ssql + " join <(" + ssql + " from csv /tmp/join_right.csv) -using id",
			wantErr: "join field collision: name",
		},
		{
			name:      "suffix renames all right fields",
			cmd:       ssql + " from csv /tmp/join_left.csv | " + ssql + " join <(" + ssql + " from csv /tmp/join_right.csv) -using id -suffix _right | " + ssql + " to table",
			wantCols:  []string{"name", "name_right", "grade_right"},
			wantValue: "Alice",
		},
		{
			name:      "suffix preserves left values",
			cmd:       ssql + " from csv /tmp/join_left.csv | " + ssql + " join <(" + ssql + " from csv /tmp/join_right.csv) -using id -suffix _right | " + ssql + " to table",
			wantValue: "Alice_R", // should appear as name_right, not as name
		},
		{
			name:     "exclude-right keeps only left fields",
			cmd:      ssql + " from csv /tmp/join_left.csv | " + ssql + " join <(" + ssql + " from csv /tmp/join_right.csv) -using id -exclude-right | " + ssql + " to table",
			wantCols: []string{"name", "score"},
			dontWant: []string{"grade"},
		},
		{
			name:      "exclude-right preserves left values",
			cmd:       ssql + " from csv /tmp/join_left.csv | " + ssql + " join <(" + ssql + " from csv /tmp/join_right.csv) -using id -exclude-right | " + ssql + " to table",
			wantValue: "Alice",
			dontWant:  []string{"Alice_R"},
		},
		{
			name:     "exclude-left keeps only right fields",
			cmd:      ssql + " from csv /tmp/join_left.csv | " + ssql + " join <(" + ssql + " from csv /tmp/join_right.csv) -using id -exclude-left | " + ssql + " to table",
			wantCols: []string{"grade"},
			dontWant: []string{"score"},
		},
		{
			name:      "as rename resolves collision",
			cmd:       ssql + " from csv /tmp/join_left.csv | " + ssql + " join <(" + ssql + " from csv /tmp/join_right.csv) -using id -as name right_name | " + ssql + " to table",
			wantCols:  []string{"name", "right_name"},
			wantValue: "Alice",
		},
		{
			name:      "no collision works without flags",
			cmd:       ssql + " from csv /tmp/join_left.csv | " + ssql + " join <(" + ssql + " from csv /tmp/join_right_nocol.csv) -using id | " + ssql + " to table",
			wantCols:  []string{"name", "score", "grade"},
			wantValue: "Alice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmd)
			output, err := cmd.CombinedOutput()
			outputStr := string(output)

			if tt.wantErr != "" {
				if err == nil {
					t.Errorf("expected error containing %q, but command succeeded with: %s", tt.wantErr, outputStr)
				} else if !strings.Contains(outputStr, tt.wantErr) {
					t.Errorf("expected error containing %q, got: %s", tt.wantErr, outputStr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v\noutput: %s", err, outputStr)
			}

			for _, col := range tt.wantCols {
				if !strings.Contains(outputStr, col) {
					t.Errorf("expected output to contain column %q, got:\n%s", col, outputStr)
				}
			}
			for _, col := range tt.dontWant {
				if strings.Contains(outputStr, col) {
					t.Errorf("expected output NOT to contain %q, got:\n%s", col, outputStr)
				}
			}
			if tt.wantValue != "" && !strings.Contains(outputStr, tt.wantValue) {
				t.Errorf("expected output to contain %q, got:\n%s", tt.wantValue, outputStr)
			}
		})
	}
}

// TestLimitZeroSkipsGeneration pins that `limit 0` / `offset 0` (the
// pass-through dial: keep a limit stage in the pipeline, set it to 0 for
// full runs) emit NO fragment — the stage must vanish from generated go,
// sql, and ssql alike. The result-equivalence side is covered by the
// limit_zero_passthrough case in TestPipelineEquivalence.
func TestLimitZeroSkipsGeneration(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tmpFile := "/tmp/lz_gen_test.csv"
	if err := os.WriteFile(tmpFile, []byte("city,pop\nOslo,31\nCairo,10\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(tmpFile)

	pipeline := `/tmp/ssql_test from ` + tmpFile +
		` | /tmp/ssql_test offset 0 | /tmp/ssql_test limit 0 | /tmp/ssql_test to jsonl`

	for _, mode := range []string{"record", "typed"} {
		for _, gen := range []struct{ format, reject string }{
			{"go", "Limit"},   // no ssql.Limit / typed.Limit / flagLimit
			{"go", "Offset"},  // no ssql.Offset / typed.Skip flagOffset
			{"sql", "LIMIT"},  // no LIMIT clause
			{"sql", "OFFSET"}, // no OFFSET clause
			{"ssql", "ssql limit"}, // stage gone from regenerated pipeline
			{"ssql", "ssql offset"},
		} {
			t.Run(mode+"_"+gen.format+"_no_"+gen.reject, func(t *testing.T) {
				cmd := exec.Command("bash", "-c",
					`export SSQL_MODE=`+mode+` && `+pipeline+` | /tmp/ssql_test generate `+gen.format)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("generate %s failed: %v\n%s", gen.format, err, out)
				}
				if strings.Contains(string(out), gen.reject) {
					t.Errorf("generate %s (mode %s): zero-valued stage leaked %q into output:\n%s",
						gen.format, mode, gen.reject, out)
				}
			})
		}
	}
}

// TestParquetSchemaMode: schema mode must answer from the FOOTER —
// real types, and never a data decode (before the branch existed it
// read the whole file to list columns).
func TestParquetSchemaMode(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	pq := dir + "/t.parquet"
	cmd := exec.Command("bash", "-c",
		fmt.Sprintf(`printf 'n,s\n1,a\n2,b\n' | %s from csv /dev/stdin | %s to parquet %s`, bin, bin, pq))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write parquet: %v\n%s", err, out)
	}
	out, err := exec.Command("bash", "-c",
		"SSQL_MODE=schema "+bin+" from "+pq).CombinedOutput()
	if err != nil {
		t.Fatalf("schema mode: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, `"n":"int"`) || !strings.Contains(s, `"s":"string"`) {
		t.Errorf("schema types missing: %s", s)
	}
	if strings.Contains(s, `{"n":1`) {
		t.Errorf("schema mode leaked data rows: %s", s)
	}
}

// TestFromRecords: the -records protocol prints one integer — the
// record count of that exact from invocation, cheapest per format —
// and refuses loudly where no cheap count exists.
func TestFromRecords(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	os.WriteFile(dir+"/d.csv", []byte("a,b\n1,2\n3,4\n5,6\n"), 0o644)
	os.WriteFile(dir+"/e.csv", []byte("a,b\n7,8\n"), 0o644)
	os.WriteFile(dir+"/d.jsonl", []byte("{\"_schema\":{\"fields\":[\"a\"],\"types\":{\"a\":\"int\"}}}\n{\"a\":1}\n{\"a\":2}\n"), 0o644)
	os.WriteFile(dir+"/arr.json", []byte("[{\"a\":1}]"), 0o644)

	run := func(args string) (string, error) {
		out, err := exec.Command("bash", "-c", "cd "+dir+" && "+bin+" "+args).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	cases := []struct {
		args, want string
	}{
		{"from csv d.csv -records", "3"},
		{"from d.csv -records", "3"},                       // bare, by extension
		{"from csv d.csv e.csv -records", "4"},             // multi-file sum
		{"from jsonl d.jsonl -records", "2"},               // _schema header excluded
		{"from csv d.csv -sample 2 -sample-seed 1 -records", "2"},
		{"from csv d.csv -sample 99 -sample-seed 1 -records", "3"}, // sample > rows
	}
	for _, c := range cases {
		got, err := run(c.args)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q err %v, want %q", c.args, got, err, c.want)
		}
	}
	if out, err := run("from arr.json -records"); err == nil || !strings.Contains(out, "JSON array") {
		t.Errorf("array: want loud refusal, got %q %v", out, err)
	}
	if out, err := run("from csv -records < d.csv"); err == nil || !strings.Contains(out, "stdin") {
		t.Errorf("stdin: want loud refusal, got %q %v", out, err)
	}
	// Parquet: write one, count via footer.
	if _, err := run("from csv d.csv | " + bin + " to parquet d.parquet"); err != nil {
		t.Fatalf("write parquet: %v", err)
	}
	if got, err := run("from parquet d.parquet -records"); err != nil || got != "3" {
		t.Errorf("parquet: got %q err %v", got, err)
	}
}

// TestFromHTTP: URL sources end-to-end through the real binary —
// streaming, parquet-over-Range with column selection, -records via
// footer, and the loud refusals.
func TestFromHTTP(t *testing.T) {
	bin := corpusBin(t)
	dir := t.TempDir()
	os.WriteFile(dir+"/d.csv", []byte("a,b\n1,x\n2,y\n3,z\n"), 0o644)
	if out, err := exec.Command("bash", "-c",
		fmt.Sprintf("cd %s && %s from csv d.csv | %s to parquet d.parquet", dir, bin, bin)).CombinedOutput(); err != nil {
		t.Fatalf("fixture parquet: %v\n%s", err, out)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	run := func(args string) (string, error) {
		out, err := exec.Command("bash", "-c", bin+" "+args).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	if out, err := run("from " + srv.URL + "/d.csv | " + bin + " count"); err != nil || out != "3" {
		t.Errorf("stream csv: %q %v", out, err)
	}
	if out, err := run("from parquet " + srv.URL + "/d.parquet -columns b | " + bin + " count"); err != nil || out != "3" {
		t.Errorf("parquet columns: %q %v", out, err)
	}
	if out, err := run("from parquet " + srv.URL + "/d.parquet -records"); err != nil || out != "3" {
		t.Errorf("records footer: %q %v", out, err)
	}
	if out, err := run("from csv " + srv.URL + "/d.csv -records"); err == nil || !strings.Contains(out, "no cheap record count over http") {
		t.Errorf("csv records refusal: %q %v", out, err)
	}
	if out, err := run("from " + srv.URL + "/d.noext"); err == nil || !strings.Contains(out, "cannot infer format") {
		t.Errorf("no-ext refusal: %q %v", out, err)
	}
	// Schema mode over a URL: header only.
	if out, err := run("from " + srv.URL + "/d.csv"); err != nil || !strings.Contains(out, `"a":`) {
		_ = out // data path covered above; schema:
	}
	out, err := exec.Command("bash", "-c", "SSQL_MODE=schema "+bin+" from "+srv.URL+"/d.csv").CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"fields":["a","b"]`) {
		t.Errorf("schema mode url: %s %v", out, err)
	}
}
