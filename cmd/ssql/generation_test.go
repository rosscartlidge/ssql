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
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test where -where age gt 18`,
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

	// Run pipeline: from | where | generate-go
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -where age gt 25 | /tmp/ssql_test generate-go`
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
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -where age gt 25 | /tmp/ssql_test generate-go`
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
			want:    []string{`"type":"stmt"`, `"var":"limited"`, `Limit[ssql.Record](5)`},
		},
		{
			name:    "offset command",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test offset 10`,
			want:    []string{`"type":"stmt"`, `"var":"skipped"`, `Offset[ssql.Record](10)`},
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
			cmdLine: `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -where age gt 25 | /tmp/ssql_test limit 5 | /tmp/ssql_test offset 1 | /tmp/ssql_test sort age -desc | /tmp/ssql_test distinct | /tmp/ssql_test generate-go`,
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
		expectFragment bool   // false for commands that shouldn't generate (like generate-go)
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
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test where -where age gt 25`,
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
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test window -avg price ma3 -order date -rows 2,0 -presorted`,
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
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -where age gt 30 -set priority high`,
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
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -where purchases gt 5000 -set tier Gold + -where purchases gt 1000 -set tier Silver + -set tier Bronze`,
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
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -where status eq active -where age gt 30 -set priority high`,
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
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test update -where tier eq Gold -set discount 0.2 -set priority high`,
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
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to json -pretty /tmp/output.json`,
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
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from /tmp/test_users.csv | /tmp/ssql_test join /tmp/test_orders.jsonl -on id user_id | /tmp/ssql_test generate-go`
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

// TestGenerationErrorHandling tests that code generation errors prevent partial code output
func TestGenerationErrorHandling(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	tests := []struct {
		name           string
		cmdLine        string
		expectError    bool
		errorSubstring string // Expected substring in error message
		notInOutput    string // Should NOT appear in output (no partial code)
	}{
		{
			name:           "stream-expr not supported in generation",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test group-by dept -stream-expr '{s:0}' '{s:s+1}' 's' total | /tmp/ssql_test generate-go`,
			expectError:    true,
			errorSubstring: "not yet supported",
			notInOutput:    "package main", // No partial Go code should be output
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", tt.cmdLine)
			output, err := cmd.CombinedOutput()
			outputStr := string(output)

			if tt.expectError {
				// Should have an error
				if err == nil {
					t.Errorf("Expected error but got none. Output: %s", outputStr)
				}

				// Error message should contain expected substring
				if !strings.Contains(outputStr, tt.errorSubstring) {
					t.Errorf("Expected error message to contain %q, got: %s", tt.errorSubstring, outputStr)
				}

				// Should NOT contain partial code
				if strings.Contains(outputStr, tt.notInOutput) {
					t.Errorf("Output should NOT contain %q when there's an error (no partial code), got: %s", tt.notInOutput, outputStr)
				}
			}
		})
	}
}

// TestErrorFragmentPropagation tests that error fragments propagate through pipeline
func TestErrorFragmentPropagation(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Create a test CSV
	csvContent := "name,age,dept\nAlice,30,Engineering\nBob,25,Sales\n"
	tmpFile := "/tmp/test_error_prop.csv"
	if err := os.WriteFile(tmpFile, []byte(csvContent), 0644); err != nil {
		t.Fatalf("Failed to create test CSV: %v", err)
	}
	defer os.Remove(tmpFile)

	// Test that an error in middle of pipeline prevents generate-go from producing code
	// The where command would succeed, but if an error fragment was introduced,
	// generate-go should fail
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test group-by dept -stream-expr '{s:0}' '{s:s+1}' 's' total | /tmp/ssql_test where -where age gt 25 | /tmp/ssql_test generate-go 2>&1`
	cmd := exec.Command("bash", "-c", pipeline)
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	// Should fail due to unsupported -stream-expr
	if err == nil {
		t.Errorf("Expected error from unsupported feature, got none. Output: %s", outputStr)
	}

	// Should NOT produce partial code
	if strings.Contains(outputStr, "package main") {
		t.Errorf("Should not produce partial Go code when there's an error. Output: %s", outputStr)
	}

	// Should contain error message
	if !strings.Contains(outputStr, "not yet supported") {
		t.Errorf("Expected 'not yet supported' error message, got: %s", outputStr)
	}
}

// TestErrorFragmentFormat tests that error fragments have correct JSON format
func TestErrorFragmentFormat(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ssql_test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build ssql: %v", err)
	}
	defer os.Remove("/tmp/ssql_test")

	// Test that error fragments are valid JSON with expected fields
	cmdLine := `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test group-by dept -stream-expr '{s:0}' '{s:s+1}' 's' total 2>&1`
	cmd := exec.Command("bash", "-c", cmdLine)
	output, _ := cmd.CombinedOutput()
	outputStr := string(output)

	// Should contain error fragment markers
	if !strings.Contains(outputStr, `"type":"error"`) {
		t.Errorf("Expected error fragment with type 'error', got: %s", outputStr)
	}

	if !strings.Contains(outputStr, `"error":`) {
		t.Errorf("Expected error fragment with 'error' field, got: %s", outputStr)
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
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from /tmp/test_users.csv | /tmp/ssql_test join <(/tmp/ssql_test from csv /tmp/test_orders.csv) -on id user_id | /tmp/ssql_test generate-go`
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
