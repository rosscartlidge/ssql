#!/bin/bash
# Ralph Wiggum loop for AI prompt improvement
#
# Iteratively tests AI prompts against structured test cases,
# validates output, and feeds failures back to Claude Code for
# prompt improvement.
#
# Usage:
#   ./scripts/test-ai-prompts.sh [go|cli|all] [--max-iterations N] [--dry-run] [--apply-fixes]
#
# Modes:
#   go          Run Go code generation tests only
#   cli         Run CLI pipeline tests only
#   all         Run both (default)
#
# Options:
#   --max-iterations N  Maximum improvement iterations (default: 5)
#   --dry-run           Show test cases without running them
#   --apply-fixes       After collecting failures, run Claude Code to fix prompts
#
# Requirements:
#   - claude CLI (claude -p for non-interactive mode)
#   - go compiler (for Go code validation)
#   - ssql binary built (for CLI pattern reference)

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TEST_CASES="$PROJECT_DIR/doc/ai-test-cases.md"
GO_PROMPT="$PROJECT_DIR/doc/ai-code-generation.md"
CLI_PROMPT="$PROJECT_DIR/doc/ai-cli-generation.md"
RESULTS_DIR="/tmp/ssql-ai-test-results"
RESULTS_FILE="$PROJECT_DIR/doc/ai-test-results.md"
FIX_REQUEST_FILE="$PROJECT_DIR/doc/ai-fix-request.md"

MAX_ITERATIONS=5
DRY_RUN=false
TEST_MODE="all"  # go, cli, or all
APPLY_FIXES=false
LLM_COMMAND="claude"  # LLM CLI to use (claude, gemini, etc.)
ALL_LLMS=false  # Test with all supported LLMs
SUPPORTED_LLMS=("claude" "gemini")

# Detailed failure tracking for fix requests
declare -a DETAILED_FAILURES=()

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        go|cli|all)
            TEST_MODE="$1"
            shift
            ;;
        --max-iterations)
            MAX_ITERATIONS="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --apply-fixes)
            APPLY_FIXES=true
            shift
            ;;
        --llm)
            LLM_COMMAND="$2"
            shift 2
            ;;
        --all-llms)
            ALL_LLMS=true
            shift
            ;;
        --help|-h)
            echo "Usage: $0 [go|cli|all] [--max-iterations N] [--dry-run] [--apply-fixes] [--llm CMD] [--all-llms]"
            echo ""
            echo "Modes:"
            echo "  go   - Test Go code generation prompt only"
            echo "  cli  - Test CLI pipeline generation prompt only"
            echo "  all  - Test both prompts (default)"
            echo ""
            echo "Options:"
            echo "  --max-iterations N  Maximum Ralph Wiggum iterations (default: 5)"
            echo "  --dry-run           Parse test cases and show what would be tested"
            echo "  --apply-fixes       Run Claude Code interactively to fix prompt failures"
            echo "  --llm CMD           LLM command to use (default: claude)"
            echo "  --all-llms          Test with all supported LLMs (claude, gemini)"
            exit 0
            ;;
        *)
            echo "Unknown argument: $1"
            exit 1
            ;;
    esac
done

# Ensure results directory exists
mkdir -p "$RESULTS_DIR"

# Check prerequisites
check_prerequisites() {
    local missing=0

    if ! command -v "$LLM_COMMAND" &>/dev/null; then
        echo -e "${RED}Error: '$LLM_COMMAND' CLI not found${NC}"
        missing=1
    fi

    if ! command -v go &>/dev/null; then
        echo -e "${RED}Error: 'go' compiler not found${NC}"
        missing=1
    fi

    if [ ! -f "$TEST_CASES" ]; then
        echo -e "${RED}Error: Test cases file not found: $TEST_CASES${NC}"
        missing=1
    fi

    if [ "$TEST_MODE" = "go" ] || [ "$TEST_MODE" = "all" ]; then
        if [ ! -f "$GO_PROMPT" ]; then
            echo -e "${RED}Error: Go prompt file not found: $GO_PROMPT${NC}"
            missing=1
        fi
    fi

    if [ "$TEST_MODE" = "cli" ] || [ "$TEST_MODE" = "all" ]; then
        if [ ! -f "$CLI_PROMPT" ]; then
            echo -e "${RED}Error: CLI prompt file not found: $CLI_PROMPT${NC}"
            missing=1
        fi
    fi

    if [ $missing -ne 0 ]; then
        exit 1
    fi
}

# Parse test cases from markdown
# Extracts: ID, prompt, expected patterns, negative patterns, validation type
# Searches the entire file for test cases matching the mode prefix (GO- or CLI-)
parse_test_cases() {
    local mode="$1"  # "go" or "cli"
    local in_test=false
    local current_id=""
    local current_prompt=""
    local current_expected=()
    local current_negative=()
    local current_validation=""
    local reading_field=""

    # Determine the ID prefix to look for
    local id_prefix
    if [ "$mode" = "go" ]; then
        id_prefix="GO"
    else
        id_prefix="CLI"
    fi

    while IFS= read -r line; do
        # Detect test case start (### GO-01 or ### CLI-01)
        if [[ "$line" =~ ^###\ (GO|CLI)-([0-9]+): ]]; then
            local test_type="${BASH_REMATCH[1]}"
            local test_num="${BASH_REMATCH[2]}"

            # Emit previous test if it matches our mode
            if [ -n "$current_id" ] && [[ "$current_id" == "${id_prefix}-"* ]]; then
                emit_test "$current_id" "$current_prompt" "$current_validation" \
                    "$(printf '%s\n' "${current_expected[@]}")" \
                    "$(printf '%s\n' "${current_negative[@]}")"
            fi

            # Start new test
            current_id="${test_type}-${test_num}"
            current_prompt=""
            current_expected=()
            current_negative=()
            current_validation=""
            reading_field=""
            in_test=true
            continue
        fi

        if ! $in_test; then
            continue
        fi

        # Parse fields
        if [[ "$line" == "**Prompt"*":"* ]]; then
            reading_field="prompt"
            # Extract prompt text after "**Prompt**: "
            local after_colon="${line#***: }"
            if [ "$after_colon" != "$line" ] && [ -n "$after_colon" ]; then
                current_prompt="$after_colon"
            fi
            continue
        elif [[ "$line" == "**Expected patterns"* ]]; then
            reading_field="expected"
            continue
        elif [[ "$line" == "**Negative patterns"* ]]; then
            reading_field="negative"
            continue
        elif [[ "$line" == "**Validation"*":"* ]]; then
            reading_field=""
            if [[ "$line" == *"compile"* ]]; then
                current_validation="compile"
            else
                current_validation="parse"
            fi
            continue
        elif [[ "$line" == "---" ]]; then
            reading_field=""
            continue
        fi

        # Collect field values
        case "$reading_field" in
            prompt)
                if [ -n "$line" ] && [ -z "$current_prompt" ]; then
                    current_prompt="$line"
                fi
                ;;
            expected)
                if [[ "$line" =~ ^-\ \`(.+)\` ]]; then
                    current_expected+=("${BASH_REMATCH[1]}")
                fi
                ;;
            negative)
                if [[ "$line" =~ ^-\ \`(.+)\` ]]; then
                    current_negative+=("${BASH_REMATCH[1]}")
                fi
                ;;
        esac

    done < "$TEST_CASES"

    # Emit final test if it matches our mode
    if [ -n "$current_id" ] && [[ "$current_id" == "${id_prefix}-"* ]]; then
        emit_test "$current_id" "$current_prompt" "$current_validation" \
            "$(printf '%s\n' "${current_expected[@]}")" \
            "$(printf '%s\n' "${current_negative[@]}")"
    fi
}

# Emit a test case as a structured record
emit_test() {
    local id="$1"
    local prompt="$2"
    local validation="$3"
    local expected="$4"
    local negative="$5"

    echo "TEST_ID=$id"
    echo "TEST_PROMPT=$prompt"
    echo "TEST_VALIDATION=$validation"
    echo "TEST_EXPECTED<<EXPECTED_EOF"
    echo "$expected"
    echo "EXPECTED_EOF"
    echo "TEST_NEGATIVE<<NEGATIVE_EOF"
    echo "$negative"
    echo "NEGATIVE_EOF"
    echo "---TEST_END---"
}

# Record a detailed failure for fix request generation
# Arguments: mode, test_id, prompt, failures_list, output_file
record_detailed_failure() {
    local mode="$1"
    local test_id="$2"
    local prompt="$3"
    local failures="$4"
    local output_file="$5"

    local output_content=""
    if [ -f "$output_file" ]; then
        output_content=$(cat "$output_file")
    fi

    # Store as a structured entry (using ||| as field separator)
    DETAILED_FAILURES+=("${mode}|||${test_id}|||${prompt}|||${failures}|||${output_content}")
}

# Generate a fix request markdown file from collected failures
generate_fix_request() {
    local mode="$1"
    local prompt_file
    if [ "$mode" = "go" ]; then
        prompt_file="$GO_PROMPT"
    else
        prompt_file="$CLI_PROMPT"
    fi

    cat > "$FIX_REQUEST_FILE" << EOF
# AI Prompt Fix Request

**Generated**: $(date '+%Y-%m-%d %H:%M')
**Mode**: $mode
**Prompt file**: $prompt_file

## Summary

The following test cases failed. Please update the prompt file to fix these issues.

**Rules:**
- Do NOT modify the test cases in \`doc/ai-test-cases.md\`
- Only modify the prompt file: \`$prompt_file\`
- Focus on adding missing patterns, clarifying instructions, or adding examples
- Keep changes minimal and targeted to fix the specific failures

---

EOF

    local count=0
    for entry in "${DETAILED_FAILURES[@]}"; do
        # Parse the entry
        IFS='|||' read -r entry_mode entry_id entry_prompt entry_failures entry_output <<< "$entry"

        # Only include failures for the current mode
        if [ "$entry_mode" != "$mode" ]; then
            continue
        fi

        ((count++))

        # Determine file extension and code language
        local ext="sh"
        local lang="bash"
        if [ "$mode" = "go" ]; then
            ext="go"
            lang="go"
        fi

        cat >> "$FIX_REQUEST_FILE" << EOF
## Failure $count: $entry_id

**Prompt**: $entry_prompt

**Issues found**:
\`\`\`
$entry_failures
\`\`\`

**Generated output** (\`$RESULTS_DIR/${entry_id}.${ext}\`):
\`\`\`${lang}
$entry_output
\`\`\`

---

EOF
    done

    if [ $count -eq 0 ]; then
        echo "No failures to fix!" >> "$FIX_REQUEST_FILE"
    fi

    echo -e "${BLUE}Fix request written to: $FIX_REQUEST_FILE${NC}"
}

# Apply fixes using interactive Claude Code
apply_fixes() {
    local mode="$1"
    local prompt_file
    if [ "$mode" = "go" ]; then
        prompt_file="$GO_PROMPT"
    else
        prompt_file="$CLI_PROMPT"
    fi

    if [ ! -f "$FIX_REQUEST_FILE" ]; then
        echo -e "${RED}No fix request file found. Run tests first.${NC}"
        return 1
    fi

    echo -e "${YELLOW}Launching Claude Code to apply fixes...${NC}"
    echo ""

    # Use claude (interactive) with a prompt that references the fix request
    claude "Please read the fix request file at $FIX_REQUEST_FILE and apply the necessary changes to $prompt_file to fix the test failures. Follow the rules in the fix request - only modify the prompt file, not the test cases."
}

# Run a single Go test case
run_go_test() {
    local id="$1"
    local prompt="$2"
    local expected="$3"
    local negative="$4"
    local output_file="$RESULTS_DIR/${id}.go"

    echo -ne "  ${CYAN}$id${NC}: "

    # Generate code using claude
    local system_prompt
    system_prompt=$(cat "$GO_PROMPT")

    local full_prompt="$system_prompt

---

Generate a complete Go program for this task. Output ONLY the Go code, no explanations:

$prompt"

    local generated
    # Note: < /dev/null prevents claude from consuming stdin meant for the test loop
    generated=$($LLM_COMMAND -p "$full_prompt" < /dev/null 2>/dev/null || echo "ERROR: $LLM_COMMAND failed")

    # Extract Go code from markdown code blocks if present
    if echo "$generated" | grep -q '```go'; then
        generated=$(echo "$generated" | sed -n '/```go/,/```/p' | sed '1d;$d')
    elif echo "$generated" | grep -q '```'; then
        generated=$(echo "$generated" | sed -n '/```/,/```/p' | sed '1d;$d')
    fi

    echo "$generated" > "$output_file"

    # Validate patterns
    local failures=()

    # Check expected patterns (handles "or" alternatives like `patternA` or `patternB`)
    while IFS= read -r pattern; do
        [ -z "$pattern" ] && continue
        local found=false
        # Split on "` or `" to handle alternatives
        IFS='|' read -ra alternatives <<< "${pattern//\` or \`/|}"
        for alt in "${alternatives[@]}"; do
            if grep -qF -- "$alt" "$output_file" 2>/dev/null; then
                found=true
                break
            fi
        done
        if ! $found; then
            failures+=("missing: $pattern")
        fi
    done <<< "$expected"

    # Check negative patterns
    while IFS= read -r pattern; do
        [ -z "$pattern" ] && continue
        if grep -qF -- "$pattern" "$output_file" 2>/dev/null; then
            failures+=("found forbidden: $pattern")
        fi
    done <<< "$negative"

    # Try to compile
    local compile_result=""
    if [ ${#failures[@]} -eq 0 ]; then
        local compile_dir="/tmp/ssql-ai-compile-${id}"
        mkdir -p "$compile_dir"
        cp "$output_file" "$compile_dir/main.go"

        # Create a go.mod that uses local source (no network needed)
        cat > "$compile_dir/go.mod" << GOMOD
module ssql-ai-test

go 1.24.8

require github.com/rosscartlidge/ssql/v4 v4.11.0

replace github.com/rosscartlidge/ssql/v4 => ${PROJECT_DIR}
GOMOD
        # Copy go.sum from project for dependency resolution
        cp "$PROJECT_DIR/go.sum" "$compile_dir/go.sum" 2>/dev/null || true

        if ! (cd "$compile_dir" && go mod tidy 2>/dev/null && go build -o /dev/null . 2>"$RESULTS_DIR/${id}.compile_err"); then
            compile_result=$(cat "$RESULTS_DIR/${id}.compile_err" 2>/dev/null | head -5)
            failures+=("compile error: $compile_result")
        fi
        rm -rf "$compile_dir"
    fi

    if [ ${#failures[@]} -eq 0 ]; then
        echo -e "${GREEN}PASS${NC}"
        return 0
    else
        echo -e "${RED}FAIL${NC}"
        for f in "${failures[@]}"; do
            echo -e "    ${RED}- $f${NC}"
        done
        # Record detailed failure for fix request
        local failures_str=""
        for f in "${failures[@]}"; do
            failures_str="${failures_str}${f}"$'\n'
        done
        record_detailed_failure "go" "$id" "$prompt" "$failures_str" "$output_file"
        return 1
    fi
}

# Run a single CLI test case
run_cli_test() {
    local id="$1"
    local prompt="$2"
    local expected="$3"
    local negative="$4"
    local output_file="$RESULTS_DIR/${id}.sh"

    echo -ne "  ${CYAN}$id${NC}: "

    # Generate pipeline using claude
    local system_prompt
    system_prompt=$(cat "$CLI_PROMPT")

    local full_prompt="$system_prompt

---

Generate an ssql CLI pipeline for this task. Output ONLY the pipeline command(s), no explanations:

$prompt"

    local generated
    # Note: < /dev/null prevents claude from consuming stdin meant for the test loop
    generated=$($LLM_COMMAND -p "$full_prompt" < /dev/null 2>/dev/null || echo "ERROR: $LLM_COMMAND failed")

    # Extract shell code from markdown code blocks if present
    if echo "$generated" | grep -q '```bash'; then
        generated=$(echo "$generated" | sed -n '/```bash/,/```/p' | sed '1d;$d')
    elif echo "$generated" | grep -q '```sh'; then
        generated=$(echo "$generated" | sed -n '/```sh/,/```/p' | sed '1d;$d')
    elif echo "$generated" | grep -q '```'; then
        generated=$(echo "$generated" | sed -n '/```/,/```/p' | sed '1d;$d')
    fi

    echo "$generated" > "$output_file"

    # Validate patterns
    local failures=()

    # Check expected patterns (handles "or" alternatives like `patternA` or `patternB`)
    while IFS= read -r pattern; do
        [ -z "$pattern" ] && continue
        local found=false
        # Split on "` or `" to handle alternatives
        IFS='|' read -ra alternatives <<< "${pattern//\` or \`/|}"
        for alt in "${alternatives[@]}"; do
            if grep -qF -- "$alt" "$output_file" 2>/dev/null; then
                found=true
                break
            fi
        done
        if ! $found; then
            failures+=("missing: $pattern")
        fi
    done <<< "$expected"

    # Check negative patterns
    while IFS= read -r pattern; do
        [ -z "$pattern" ] && continue
        if grep -qF -- "$pattern" "$output_file" 2>/dev/null; then
            failures+=("found forbidden: $pattern")
        fi
    done <<< "$negative"

    if [ ${#failures[@]} -eq 0 ]; then
        echo -e "${GREEN}PASS${NC}"
        return 0
    else
        echo -e "${RED}FAIL${NC}"
        for f in "${failures[@]}"; do
            echo -e "    ${RED}- $f${NC}"
        done
        # Record detailed failure for fix request
        local failures_str=""
        for f in "${failures[@]}"; do
            failures_str="${failures_str}${f}"$'\n'
        done
        record_detailed_failure "cli" "$id" "$prompt" "$failures_str" "$output_file"
        return 1
    fi
}

# Run all tests of a given type
run_tests() {
    local mode="$1"  # "go" or "cli"
    local total=0
    local passed=0
    local failed_cases=()

    echo -e "${BLUE}Running $mode tests...${NC}"

    local test_id=""
    local test_prompt=""
    local test_validation=""
    local test_expected=""
    local test_negative=""
    local reading=""

    while IFS= read -r line; do
        if [[ "$line" == "TEST_ID="* ]]; then
            test_id="${line#TEST_ID=}"
        elif [[ "$line" == "TEST_PROMPT="* ]]; then
            test_prompt="${line#TEST_PROMPT=}"
        elif [[ "$line" == "TEST_VALIDATION="* ]]; then
            test_validation="${line#TEST_VALIDATION=}"
        elif [[ "$line" == "TEST_EXPECTED<<EXPECTED_EOF" ]]; then
            reading="expected"
            test_expected=""
        elif [[ "$line" == "EXPECTED_EOF" ]]; then
            reading=""
        elif [[ "$line" == "TEST_NEGATIVE<<NEGATIVE_EOF" ]]; then
            reading="negative"
            test_negative=""
        elif [[ "$line" == "NEGATIVE_EOF" ]]; then
            reading=""
        elif [[ "$line" == "---TEST_END---" ]]; then
            # Execute test
            ((total++))

            if $DRY_RUN; then
                echo -e "  ${CYAN}$test_id${NC}: $test_prompt"
                echo "    validation: $test_validation"
                echo "    expected patterns: $(echo "$test_expected" | grep -c . || echo 0)"
                echo "    negative patterns: $(echo "$test_negative" | grep -c . || echo 0)"
                ((passed++))
            else
                local result=0
                if [ "$mode" = "go" ]; then
                    run_go_test "$test_id" "$test_prompt" "$test_expected" "$test_negative" || result=1
                else
                    run_cli_test "$test_id" "$test_prompt" "$test_expected" "$test_negative" || result=1
                fi

                if [ $result -eq 0 ]; then
                    ((passed++))
                else
                    failed_cases+=("$test_id: $test_prompt")
                fi
            fi
        elif [ "$reading" = "expected" ]; then
            if [ -n "$test_expected" ]; then
                test_expected="$test_expected
$line"
            else
                test_expected="$line"
            fi
        elif [ "$reading" = "negative" ]; then
            if [ -n "$test_negative" ]; then
                test_negative="$test_negative
$line"
            else
                test_negative="$line"
            fi
        fi
    done < <(parse_test_cases "$mode")

    echo ""
    echo -e "${BLUE}$mode results: $passed/$total passed${NC}"

    if [ ${#failed_cases[@]} -gt 0 ]; then
        echo -e "${RED}Failed cases:${NC}"
        for fc in "${failed_cases[@]}"; do
            echo -e "  ${RED}- $fc${NC}"
        done
    fi

    # Return failure info for Ralph Wiggum loop
    LAST_TOTAL=$total
    LAST_PASSED=$passed
    LAST_FAILED_CASES=("${failed_cases[@]}")

    return $(( total - passed ))
}

# Ralph Wiggum improvement loop
ralph_wiggum_loop() {
    local mode="$1"  # "go" or "cli"
    local prompt_file
    if [ "$mode" = "go" ]; then
        prompt_file="$GO_PROMPT"
    else
        prompt_file="$CLI_PROMPT"
    fi

    local iteration=0
    local failures=1  # Start with assumed failures

    echo -e "${BLUE}=== Ralph Wiggum Loop: $mode (max $MAX_ITERATIONS iterations) ===${NC}"
    echo ""

    while [ $iteration -lt "$MAX_ITERATIONS" ] && [ $failures -gt 0 ]; do
        echo -e "${YELLOW}--- Iteration $((iteration + 1))/$MAX_ITERATIONS ---${NC}"
        echo ""

        # Clear previous detailed failures for this iteration
        DETAILED_FAILURES=()

        run_tests "$mode" || true
        failures=$(( LAST_TOTAL - LAST_PASSED ))

        if [ $failures -gt 0 ]; then
            echo ""

            # Generate fix request file with detailed failure info
            generate_fix_request "$mode"

            if [ $iteration -lt $((MAX_ITERATIONS - 1)) ]; then
                if $APPLY_FIXES; then
                    echo ""
                    echo -e "${YELLOW}Applying fixes using Claude Code...${NC}"
                    apply_fixes "$mode"
                    echo ""
                    echo -e "${YELLOW}Fixes applied. Re-running tests...${NC}"
                    echo ""
                else
                    echo ""
                    echo -e "${YELLOW}To apply fixes interactively, run:${NC}"
                    echo -e "  ${CYAN}claude 'Read $FIX_REQUEST_FILE and fix the issues in $prompt_file'${NC}"
                    echo ""
                    echo -e "${YELLOW}Or re-run with --apply-fixes to auto-apply:${NC}"
                    echo -e "  ${CYAN}$0 $mode --apply-fixes${NC}"
                    # Without --apply-fixes, we exit after first iteration to let user fix manually
                    break
                fi
            fi
        fi

        ((iteration++))
    done

    echo ""
    if [ $failures -eq 0 ]; then
        echo -e "${GREEN}=== All $mode tests passing after $iteration iteration(s) ===${NC}"
    else
        echo -e "${RED}=== $failures $mode test(s) still failing after $iteration iteration(s) ===${NC}"
    fi

    return $failures
}

# Write results summary
write_results() {
    local go_total="${1:-0}"
    local go_passed="${2:-0}"
    local cli_total="${3:-0}"
    local cli_passed="${4:-0}"
    local iterations="${5:-1}"

    cat > "$RESULTS_FILE" << EOF
# AI Prompt Test Results

**Date**: $(date '+%Y-%m-%d %H:%M')
**Mode**: $TEST_MODE
**Iterations**: $iterations / $MAX_ITERATIONS

## Summary

| Category | Passed | Total | Rate |
|----------|--------|-------|------|
| Go Code | $go_passed | $go_total | $(( go_total > 0 ? go_passed * 100 / go_total : 0 ))% |
| CLI Pipeline | $cli_passed | $cli_total | $(( cli_total > 0 ? cli_passed * 100 / cli_total : 0 ))% |
| **Total** | **$(( go_passed + cli_passed ))** | **$(( go_total + cli_total ))** | **$(( (go_total + cli_total) > 0 ? (go_passed + cli_passed) * 100 / (go_total + cli_total) : 0 ))%** |

## Output Files

Test outputs are in \`/tmp/ssql-ai-test-results/\`:
- \`GO-*.go\` - Generated Go programs
- \`CLI-*.sh\` - Generated CLI pipelines
- \`*.compile_err\` - Compilation errors (if any)
EOF

    echo ""
    echo -e "${BLUE}Results written to: $RESULTS_FILE${NC}"
}

# Run tests for a single LLM
# Sets: LLM_GO_TOTAL, LLM_GO_PASSED, LLM_CLI_TOTAL, LLM_CLI_PASSED, LLM_FAILURES
run_single_llm_tests() {
    LLM_GO_TOTAL=0
    LLM_GO_PASSED=0
    LLM_CLI_TOTAL=0
    LLM_CLI_PASSED=0
    LLM_FAILURES=0

    if [ "$TEST_MODE" = "go" ] || [ "$TEST_MODE" = "all" ]; then
        if $DRY_RUN; then
            run_tests "go" || true
            LLM_GO_TOTAL=$LAST_TOTAL
            LLM_GO_PASSED=$LAST_PASSED
        else
            ralph_wiggum_loop "go" || true
            LLM_GO_TOTAL=$LAST_TOTAL
            LLM_GO_PASSED=$LAST_PASSED
            LLM_FAILURES=$(( LLM_FAILURES + LAST_TOTAL - LAST_PASSED ))
        fi
        echo ""
    fi

    if [ "$TEST_MODE" = "cli" ] || [ "$TEST_MODE" = "all" ]; then
        if $DRY_RUN; then
            run_tests "cli" || true
            LLM_CLI_TOTAL=$LAST_TOTAL
            LLM_CLI_PASSED=$LAST_PASSED
        else
            ralph_wiggum_loop "cli" || true
            LLM_CLI_TOTAL=$LAST_TOTAL
            LLM_CLI_PASSED=$LAST_PASSED
            LLM_FAILURES=$(( LLM_FAILURES + LAST_TOTAL - LAST_PASSED ))
        fi
        echo ""
    fi
}

# Main execution
main() {
    echo -e "${BLUE}ssql AI Prompt Test Runner (Ralph Wiggum Loop)${NC}"
    echo -e "${BLUE}===============================================${NC}"

    if $DRY_RUN; then
        echo -e "${YELLOW}DRY RUN - showing test cases without executing${NC}"
        echo ""
        # For dry run, just show test cases once
        LLM_COMMAND="claude"
        check_prerequisites
        run_single_llm_tests
        exit 0
    fi

    # Determine which LLMs to test
    local llms_to_test=()
    if $ALL_LLMS; then
        for llm in "${SUPPORTED_LLMS[@]}"; do
            if command -v "$llm" &>/dev/null; then
                llms_to_test+=("$llm")
            else
                echo -e "${YELLOW}Warning: $llm not found, skipping${NC}"
            fi
        done
        if [ ${#llms_to_test[@]} -eq 0 ]; then
            echo -e "${RED}Error: No supported LLMs found${NC}"
            exit 1
        fi
    else
        llms_to_test=("$LLM_COMMAND")
    fi

    # Track results per LLM
    declare -A llm_go_passed llm_go_total llm_cli_passed llm_cli_total llm_failures
    local grand_total_failures=0

    for llm in "${llms_to_test[@]}"; do
        LLM_COMMAND="$llm"
        echo ""
        echo -e "${BLUE}╔═══════════════════════════════════════════════╗${NC}"
        echo -e "${BLUE}║  Testing with: ${CYAN}$LLM_COMMAND${BLUE}$(printf '%*s' $((28 - ${#LLM_COMMAND})) '')║${NC}"
        echo -e "${BLUE}╚═══════════════════════════════════════════════╝${NC}"
        echo ""

        check_prerequisites

        # Clear results directory for this LLM
        rm -rf "$RESULTS_DIR"
        mkdir -p "$RESULTS_DIR"

        # Clear detailed failures
        DETAILED_FAILURES=()

        run_single_llm_tests

        llm_go_passed[$llm]=$LLM_GO_PASSED
        llm_go_total[$llm]=$LLM_GO_TOTAL
        llm_cli_passed[$llm]=$LLM_CLI_PASSED
        llm_cli_total[$llm]=$LLM_CLI_TOTAL
        llm_failures[$llm]=$LLM_FAILURES
        grand_total_failures=$(( grand_total_failures + LLM_FAILURES ))
    done

    # Final summary
    echo ""
    echo -e "${BLUE}╔═══════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║           FINAL SUMMARY (All LLMs)            ║${NC}"
    echo -e "${BLUE}╚═══════════════════════════════════════════════╝${NC}"
    echo ""
    printf "%-12s %10s %10s %10s\n" "LLM" "Go" "CLI" "Total"
    printf "%-12s %10s %10s %10s\n" "---" "--" "---" "-----"

    for llm in "${llms_to_test[@]}"; do
        local go_result="${llm_go_passed[$llm]}/${llm_go_total[$llm]}"
        local cli_result="${llm_cli_passed[$llm]}/${llm_cli_total[$llm]}"
        local total_passed=$(( llm_go_passed[$llm] + llm_cli_passed[$llm] ))
        local total_tests=$(( llm_go_total[$llm] + llm_cli_total[$llm] ))
        local total_result="$total_passed/$total_tests"

        if [ "${llm_failures[$llm]}" -eq 0 ]; then
            printf "${GREEN}%-12s %10s %10s %10s${NC}\n" "$llm" "$go_result" "$cli_result" "$total_result"
        else
            printf "${RED}%-12s %10s %10s %10s${NC}\n" "$llm" "$go_result" "$cli_result" "$total_result"
        fi
    done

    echo ""
    if [ $grand_total_failures -eq 0 ]; then
        echo -e "${GREEN}All tests passed on all LLMs!${NC}"
        exit 0
    else
        echo -e "${RED}$grand_total_failures total test failure(s) across all LLMs${NC}"
        exit 1
    fi
}

main
