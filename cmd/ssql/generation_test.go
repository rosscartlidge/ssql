package main

import (
	"os"
	"os/exec"
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
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -if age gt 25 | /tmp/ssql_test generate go`
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
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -if age gt 25 | /tmp/ssql_test generate go`
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
			cmdLine: `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -if age gt 25 | /tmp/ssql_test limit 5 | /tmp/ssql_test offset 1 | /tmp/ssql_test sort age -desc | /tmp/ssql_test distinct | /tmp/ssql_test generate go`,
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
				`ssql.GetOr`,
				`float64(30)`,
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
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from /tmp/test_users.csv | /tmp/ssql_test join /tmp/test_orders.jsonl -on id user_id | /tmp/ssql_test generate go`
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

	pipeline := `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test group-by dept -stream-expr '{s:0}' '{s:s+salary}' 's' total | /tmp/ssql_test generate go`
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
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from /tmp/test_users.csv | /tmp/ssql_test join <(/tmp/ssql_test from csv /tmp/test_orders.csv) -on id user_id | /tmp/ssql_test generate go`
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
	for _, want := range []string{`"type":"stmt"`, `ssql.TopBy`, `salary`, `"var":"topRecords"`} {
		if !strings.Contains(outputStr, want) {
			t.Errorf("Expected output to contain %q, got: %s", want, outputStr)
		}
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
	if !strings.Contains(outputStr, `ssql.BottomBy`) {
		t.Errorf("Expected output to contain ssql.BottomBy for -asc, got: %s", outputStr)
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
			cmd:  `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test generate go`,
			want: []string{`flag.String("input"`, `"input CSV file"`, `*flagInput`, `flag.Parse()`},
		},
		{
			name: "output file parameterized",
			cmd:  `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test to csv /tmp/out.csv | /tmp/ssql_test generate go`,
			want: []string{`flag.String("output"`, `*flagOutput`, `flag.Parse()`},
		},
		{
			name: "limit parameterized as int",
			cmd:  `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test limit 42 | /tmp/ssql_test generate go`,
			want: []string{`flag.Int("limit"`, `42`, `*flagLimit`},
		},
		{
			name: "offset parameterized as int",
			cmd:  `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test offset 5 | /tmp/ssql_test generate go`,
			want: []string{`flag.Int("offset"`, `5`, `*flagOffset`},
		},
		{
			name: "stdin input has no flag",
			cmd:  `echo '{"name":"Alice"}' | SSQLGO=1 /tmp/ssql_test from json | /tmp/ssql_test generate go`,
			want: []string{`ReadJSONFromReader(os.Stdin)`},
		},
		{
			name: "flag name deduplication",
			cmd:  `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test join ` + tmpFile + ` -using name | /tmp/ssql_test generate go`,
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

	cmd := exec.Command("bash", "-c", `echo '{"name":"Alice"}' | SSQLGO=1 /tmp/ssql_test from json | /tmp/ssql_test to table | /tmp/ssql_test generate go`)
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
