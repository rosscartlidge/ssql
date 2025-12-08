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
			want:    []string{`"type":"stmt"`, `"var":"sorted"`, `SortBy`, `age`},
		},
		{
			name:    "distinct command",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test distinct`,
			want:    []string{`"type":"stmt"`, `"var":"distinct"`, `DistinctBy`},
		},
		{
			name:    "pipeline with all commands",
			cmdLine: `export SSQLGO=1 && /tmp/ssql_test from ` + tmpFile + ` | /tmp/ssql_test where -where age gt 25 | /tmp/ssql_test limit 5 | /tmp/ssql_test offset 1 | /tmp/ssql_test sort age -desc | /tmp/ssql_test distinct | /tmp/ssql_test generate-go`,
			want:    []string{"package main", "ssql.ReadCSV", "ssql.Where", "ssql.Limit", "ssql.Offset", "ssql.SortBy", "ssql.DistinctBy"},
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
			wantSubstring:  `ssql.SortBy`,
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
			name:           "to csv",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to csv /tmp/out.csv`,
			expectFragment: true,
			wantSubstring:  `ssql.WriteCSV`,
		},
		{
			name:           "chart",
			cmdLine:        `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test to chart -x age -y salary`,
			expectFragment: true,
			wantSubstring:  `ssql.QuickChart`,
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

	// Should contain QuickChart call
	if !strings.Contains(outputStr, `ssql.QuickChart`) {
		t.Errorf("Chart fragment missing QuickChart call.\nOutput: %s", outputStr)
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
			name:    "join basic with -on",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join /tmp/test_orders.jsonl -on user_id`,
			wantStrs: []string{
				`"type":"init"`,
				`rightRecords`,
				`lib.ReadJSONL`,
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
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join -type left /tmp/test_orders.jsonl -on user_id`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.LeftJoin`,
			},
		},
		{
			name:    "join with -type right",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join -type right /tmp/test_orders.jsonl -on user_id`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.RightJoin`,
			},
		},
		{
			name:    "join with -type full",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join -type full /tmp/test_orders.jsonl -on user_id`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.FullJoin`,
			},
		},
		{
			name:    "join with multiple -on fields",
			cmdLine: `echo '{"type":"init","var":"records"}' | SSQLGO=1 /tmp/ssql_test join /tmp/test_orders.jsonl -on field1 -on field2`,
			wantStrs: []string{
				`"type":"stmt"`,
				`ssql.OnFields`,
				`field1`,
				`field2`,
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
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from /tmp/test_users.csv | /tmp/ssql_test join /tmp/test_orders.jsonl -on user_id | /tmp/ssql_test generate-go`
	cmd := exec.Command("bash", "-c", pipeline)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pipeline failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Check for expected elements in generated code
	expectations := []string{
		"package main",
		"ssql.ReadCSV", // For the primary input (left side)
		"rightRecords",
		"ssql.InnerJoin",
		"func main()",
		"lib.ReadJSONL", // For the secondary input (right side)
	}

	for _, expected := range expectations {
		if !strings.Contains(outputStr, expected) {
			t.Errorf("Generated code missing expected element: %q\nGot: %s", expected, outputStr)
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
	pipeline := `export SSQLGO=1 && /tmp/ssql_test from /tmp/test_users.csv | /tmp/ssql_test join <(/tmp/ssql_test from csv /tmp/test_orders.csv) -on user_id | /tmp/ssql_test generate-go`
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
		"ssql.ReadCSV", // Both inputs are CSV files
		"rightRecords",
		"ssql.InnerJoin",
		"func main()",
	}

	for _, expected := range expectations {
		if !strings.Contains(outputStr, expected) {
			t.Errorf("Generated code missing expected element: %q\nGot: %s", expected, outputStr)
		}
	}

	// The generated code should NOT have lib.ReadJSONL for the orders file
	// since we're using process substitution which incorporates the inner command's fragments
	// Note: This verifies fragments are being merged correctly
	if strings.Contains(outputStr, "lib.ReadJSONL") && strings.Contains(outputStr, "/tmp/test_orders.csv") {
		t.Error("With process substitution, should not generate JSONL reading code for secondary source")
	}
}
