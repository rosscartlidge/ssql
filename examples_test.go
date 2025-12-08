//go:build examples
// +build examples

package ssql_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExamples verifies that all example programs can build and run without errors
func TestExamples(t *testing.T) {
	examplesDir := "examples"

	// Get all .go files in examples directory
	files, err := filepath.Glob(filepath.Join(examplesDir, "*.go"))
	if err != nil {
		t.Fatalf("Failed to list examples: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("No example files found")
	}

	// Track statistics
	totalExamples := 0
	successfulExamples := 0
	skippedExamples := 0

	for _, file := range files {
		filename := filepath.Base(file)

		// Skip test files if any exist
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}

		totalExamples++
		exampleName := strings.TrimSuffix(filename, ".go")

		t.Run(exampleName, func(t *testing.T) {
			// Check if this example should be skipped
			if shouldSkipExample(exampleName) {
				t.Skipf("Example %s requires interactive input or external dependencies", exampleName)
				skippedExamples++
				return
			}

			// Build the example
			buildCmd := exec.Command("go", "build", "-o", "/dev/null", file)
			buildCmd.Env = os.Environ()
			buildOutput, err := buildCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Failed to build %s:\n%s\nError: %v", filename, buildOutput, err)
			}

			// Run the example with timeout
			runCmd := exec.Command("go", "run", file)
			runCmd.Env = os.Environ()

			// Set timeout to prevent hanging examples
			done := make(chan error, 1)
			go func() {
				output, err := runCmd.CombinedOutput()
				if err != nil {
					done <- err
					t.Logf("Output from %s:\n%s", filename, output)
				} else {
					done <- nil
				}
			}()

			select {
			case err := <-done:
				if err != nil {
					// Check if it's an exit error (non-zero exit code)
					if exitErr, ok := err.(*exec.ExitError); ok {
						t.Errorf("Example %s exited with code %d", filename, exitErr.ExitCode())
					} else {
						t.Errorf("Example %s failed to run: %v", filename, err)
					}
				} else {
					successfulExamples++
					t.Logf("✅ Example %s ran successfully", exampleName)
				}
			case <-time.After(10 * time.Second):
				// Kill the process if it's still running
				if runCmd.Process != nil {
					runCmd.Process.Kill()
				}
				t.Errorf("Example %s timed out after 10 seconds", filename)
			}
		})
	}

	// Log summary
	t.Logf("\n📊 Examples Test Summary:")
	t.Logf("   Total examples: %d", totalExamples)
	t.Logf("   Successful: %d", successfulExamples)
	t.Logf("   Skipped: %d", skippedExamples)
	t.Logf("   Failed: %d", totalExamples-successfulExamples-skippedExamples)
}

// shouldSkipExample returns true for examples that require special handling
func shouldSkipExample(exampleName string) bool {
	// Examples that require interactive input, external services, or specific setup
	skipList := []string{
		// Add examples that need to be skipped here
		// For example:
		// "interactive_example",
		// "requires_database_example",
	}

	for _, skip := range skipList {
		if exampleName == skip {
			return true
		}
	}

	return false
}

// TestSpecificExamples runs specific high-priority examples with detailed validation
func TestSpecificExamples(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		contains []string // Expected output strings
	}{
		{
			name: "safe_unsafe_mixing",
			file: "examples/safe_unsafe_mixing_example.go",
			contains: []string{
				"Pattern 1: Mixed Pipeline",
				"Pattern 2: I/O with Safe",
				"Pattern 3: Fail-Fast",
				"Pattern 4: Best-Effort",
				"Successfully processed transactions",
				"Valid records",
			},
		},
		{
			name: "tee_methods",
			file: "examples/tee_methods_example.go",
			contains: []string{
				"ssql Tee Methods Comparison",
				"Method 1: Using standalone Tee function",
				"Method 2: Using Stream.Tee() method",
			},
		},
		{
			name: "tee_real_world",
			file: "examples/tee_real_world_example.go",
			contains: []string{
				"Real-World Tee Example",
				"Revenue Analytics",
				"Customer Segmentation",
				"Product Performance",
				"Geographic Distribution",
			},
		},
		{
			name: "infinite_stream_strategies",
			file: "examples/infinite_stream_strategies_comprehensive.go",
			contains: []string{
				"Infinite Stream Strategies",
				"Strategy Integration",
				"Windowing & Chunking",
				"LazyTee Broadcasting",
				"Streaming Aggregations",
				"Early Termination",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run the example and capture output
			cmd := exec.Command("go", "run", tt.file)
			cmd.Env = os.Environ()

			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Example failed to run: %v\nOutput:\n%s", err, output)
			}

			outputStr := string(output)

			// Verify expected strings are in output
			for _, expected := range tt.contains {
				if !strings.Contains(outputStr, expected) {
					t.Errorf("Expected output to contain %q, but it didn't.\nFull output:\n%s", expected, outputStr)
				}
			}

			t.Logf("✅ Example %s produced expected output", tt.name)
		})
	}
}

// TestExamplesBuild verifies all examples can at least compile
func TestExamplesBuild(t *testing.T) {
	examplesDir := "examples"

	files, err := filepath.Glob(filepath.Join(examplesDir, "*.go"))
	if err != nil {
		t.Fatalf("Failed to list examples: %v", err)
	}

	buildFailed := 0
	for _, file := range files {
		filename := filepath.Base(file)

		if strings.HasSuffix(filename, "_test.go") {
			continue
		}

		t.Run("build_"+strings.TrimSuffix(filename, ".go"), func(t *testing.T) {
			cmd := exec.Command("go", "build", "-o", "/dev/null", file)
			cmd.Env = os.Environ()
			output, err := cmd.CombinedOutput()
			if err != nil {
				buildFailed++
				t.Errorf("Failed to build %s:\n%s\nError: %v", filename, output, err)
			}
		})
	}

	if buildFailed > 0 {
		t.Logf("❌ %d examples failed to build", buildFailed)
	} else {
		t.Logf("✅ All examples built successfully")
	}
}
