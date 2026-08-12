#!/bin/bash
# Documentation validation script for ssql
# Ensures documentation stays in sync with code

# Note: Don't use set -e because we want to collect all errors

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

ERRORS=0
WARNINGS=0
CHECKS=0

echo -e "${BLUE}ssql Documentation Validation${NC}"
echo "======================================"
echo ""

# Helper functions
pass() {
    echo -e "${GREEN}✓${NC} $1"
    ((CHECKS++))
}

fail() {
    echo -e "${RED}✗${NC} $1"
    ((ERRORS++))
    ((CHECKS++))
}

warn() {
    echo -e "${YELLOW}⚠${NC} $1"
    ((WARNINGS++))
}

section() {
    echo ""
    echo -e "${BLUE}$1${NC}"
    echo "----------------------------------------"
}

# Function to extract Go code blocks from markdown
# Only extracts code from <details> blocks (the complete, runnable examples)
extract_code_blocks() {
    local file=$1
    local temp_dir=$2

    awk '
        /<details>/ { in_details=1; next }
        /<\/details>/ { in_details=0; next }
        /^```go$/ && in_details { in_block=1; counter++; filename = sprintf("'$temp_dir'/example_%03d.go", counter); next }
        /^```$/ && in_block { in_block=0; next }
        in_block && filename { print > filename }
    ' "$file"
}

# Check 1: Validate file existence
section "1. Checking Documentation Files"

required_files=(
    "doc/ai-code-generation.md"
    "doc/AI-PROMPT-README.md"
    "doc/ai-human-guide.md"
    "doc/api-reference.md"
    "README.md"
)

for file in "${required_files[@]}"; do
    if [[ -f "$file" ]]; then
        pass "Found: $file"
    else
        fail "Missing: $file"
    fi
done

# Check 2: Validate that old LLM docs are deleted
section "2. Checking Old Files Removed"

old_files=(
    "doc/nl-to-code-examples.md"
    "doc/streamv3-ai-prompt.md"
    "doc/streamv3-ai-prompt-detailed.md"
    "doc/human-llm-tutorial.md"
    "doc/streamv3-ai-system.md"
)

for file in "${old_files[@]}"; do
    if [[ ! -f "$file" ]]; then
        pass "Correctly removed: $file"
    else
        fail "Old file still exists: $file"
    fi
done

# Check 3: Check for outdated API patterns in documentation
section "3. Checking for Outdated API Patterns"

# Patterns that should NOT appear in docs
bad_patterns=(
    "NewRecord().Build()"
    "ssql.Map("
    "ssql.Filter("
    "ssql.Take("
    "ssql.Skip("
    "ssql.FlatMap("
)

for file in "${required_files[@]}"; do
    if [[ ! -f "$file" ]]; then
        continue
    fi

    file_has_errors=0
    for pattern in "${bad_patterns[@]}"; do
        # Check if pattern exists but NOT in a "wrong" example section
        # Use awk to check if lines with pattern are within 5 lines after a "❌ WRONG" comment
        found_bad_usage=0

        # Extract line numbers where pattern appears
        pattern_lines=$(grep -n "$pattern" "$file" 2>/dev/null | cut -d: -f1)

        for line_num in $pattern_lines; do
            # Check if there's a "❌ WRONG" or similar marker within 5 lines before this line
            start_line=$((line_num - 5))
            if [ $start_line -lt 1 ]; then
                start_line=1
            fi

            # Check if this is in a "wrong example" section
            if ! sed -n "${start_line},${line_num}p" "$file" | grep -q "❌\|WRONG\|Wrong\|Don't\|doesn't exist\|NOT\|Avoid"; then
                # Also check if the line itself has a comment indicating it's wrong
                if ! sed -n "${line_num}p" "$file" | grep -q "//.*Filter is a type\|//.*doesn't exist\|//.*Map doesn't exist\|//.*Take doesn't exist"; then
                    found_bad_usage=1
                    break
                fi
            fi
        done

        if [ $found_bad_usage -eq 1 ]; then
            fail "Found outdated pattern '$pattern' in $file (not in a 'wrong example' section)"
            file_has_errors=1
        fi
    done

    if [[ $file_has_errors -eq 0 ]]; then
        pass "No outdated patterns in $file"
    fi
done

# Check 4: Validate Go code examples compile
section "4. Validating Go Code Examples"

# Create temporary directory for code extraction
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

files_with_code=(
    "doc/ai-code-generation.md"
    "doc/ai-human-guide.md"
    "README.md"
)

total_examples=0
compiled_examples=0

for doc_file in "${files_with_code[@]}"; do
    if [[ ! -f "$doc_file" ]]; then
        continue
    fi

    # Extract code blocks
    extract_code_blocks "$doc_file" "$TEMP_DIR"

    # Try to compile each extracted example
    for example in "$TEMP_DIR"/example_*.go; do
        if [[ ! -f "$example" ]]; then
            continue
        fi

        ((total_examples++))
        example_name=$(basename "$example")

        # Check if it's a complete program (has package main)
        if grep -q "^package main" "$example"; then
            # Try to compile it
            compiled_bin="$TEMP_DIR/${example_name%.go}"
            if go build -o "$compiled_bin" "$example" 2>/dev/null; then
                pass "Compiles: $doc_file - $example_name"
                ((compiled_examples++))

                # Try to run it (with timeout to prevent hangs)
                # Only run if it doesn't require external files
                if ! grep -q "ReadCSV\|ReadJSON\|ReadLines" "$example"; then
                    if timeout 2s "$compiled_bin" >/dev/null 2>&1; then
                        pass "  Runs successfully: $example_name"
                    else
                        warn "  Compiled but failed to run: $example_name"
                    fi
                fi
                rm -f "$compiled_bin"
            else
                # Get the error
                error_output=$(go build -o /dev/null "$example" 2>&1 || true)
                fail "Compile error in $doc_file - $example_name"
                warn "  Error: $(echo "$error_output" | head -3 | tr '\n' ' ')"
            fi
        else
            # Just check syntax for code snippets
            if gofmt -e "$example" >/dev/null 2>&1; then
                pass "Valid syntax: $doc_file - $example_name"
                ((compiled_examples++))
            else
                fail "Syntax error in $doc_file - $example_name"
            fi
        fi

        # Clean up this example
        rm "$example"
    done
done

if [[ $total_examples -eq 0 ]]; then
    warn "No Go code examples found to validate"
else
    echo ""
    echo "Validated $compiled_examples/$total_examples code examples"
fi

# Check 5: Validate markdown links
section "5. Checking Markdown Links"

for file in "${required_files[@]}"; do
    if [[ ! -f "$file" ]]; then
        continue
    fi

    file_has_broken_links=0

    # Extract relative markdown links
    while IFS= read -r link; do
        # Remove the ]( and ) parts
        target=$(echo "$link" | sed 's/.*](\([^)]*\)).*/\1/')

        # Skip external links
        if [[ "$target" =~ ^https?:// ]]; then
            continue
        fi

        # Skip anchors only
        if [[ "$target" =~ ^# ]]; then
            continue
        fi

        # Get the directory of the current file
        file_dir=$(dirname "$file")

        # Remove anchor from target
        target_file="${target%%#*}"

        # Resolve relative path
        if [[ "$target_file" == /* ]]; then
            # Absolute path from repo root
            full_path="$target_file"
        else
            # Relative path
            full_path="$file_dir/$target_file"
        fi

        # Check if target exists
        if [[ ! -f "$full_path" && ! -d "$full_path" ]]; then
            fail "Broken link in $file: $target"
            file_has_broken_links=1
        fi
    done < <(grep -o '\[.*\](.*\.md[^)]*)' "$file" 2>/dev/null || true)

    if [[ $file_has_broken_links -eq 0 ]]; then
        pass "All links valid in $file"
    fi
done

# Check 6: Verify go doc reference in LLM docs
section "6. Checking go doc References"

llm_files=(
    "doc/ai-code-generation.md"
    "doc/ai-human-guide.md"
)

for file in "${llm_files[@]}"; do
    if [[ ! -f "$file" ]]; then
        continue
    fi

    if grep -q "go doc github.com/rosscartlidge/ssql" "$file"; then
        pass "$file references go doc"
    else
        warn "$file should reference 'go doc' as source of truth"
    fi
done

# Check 7: Verify critical API patterns are documented
section "7. Checking Critical API Patterns"

critical_patterns=(
    "MakeMutableRecord"
    "Freeze()"
    "ssql.Select"
    "ssql.Where"
    "ssql.Limit"
    "ssql.ReadCSV"
    "int64(0)"
    "GetOr"
)

for file in "${llm_files[@]}"; do
    if [[ ! -f "$file" ]]; then
        continue
    fi

    file_has_all=1
    for pattern in "${critical_patterns[@]}"; do
        # For ai-human-guide.md, allow patterns without ssql. prefix
        if [[ "$file" == "doc/ai-human-guide.md" ]]; then
            # Strip ssql. prefix for human guide
            relaxed_pattern="${pattern#ssql.}"
            if ! grep -q "$relaxed_pattern" "$file"; then
                fail "Missing critical pattern '$pattern' in $file"
                file_has_all=0
            fi
        else
            if ! grep -q "$pattern" "$file"; then
                fail "Missing critical pattern '$pattern' in $file"
                file_has_all=0
            fi
        fi
    done

    if [[ $file_has_all -eq 1 ]]; then
        pass "All critical patterns present in $file"
    fi
done

# Check 8: Validate error handling in examples
section "8. Checking Error Handling in Examples"

for doc_file in "${files_with_code[@]}"; do
    if [[ ! -f "$doc_file" ]]; then
        continue
    fi

    # Extract code blocks again
    extract_code_blocks "$doc_file" "$TEMP_DIR"

    file_has_issues=0
    for example in "$TEMP_DIR"/example_*.go; do
        if [[ ! -f "$example" ]]; then
            continue
        fi

        # Check if code has ReadCSV/ReadJSON without error handling
        if grep -q "ReadCSV\|ReadJSON\|ReadLines" "$example"; then
            if ! grep -q "if err != nil" "$example"; then
                fail "Missing error handling in $doc_file - $(basename "$example")"
                file_has_issues=1
            fi
        fi

        rm "$example"
    done

    if [[ $file_has_issues -eq 0 ]]; then
        pass "Proper error handling in $doc_file"
    fi
done

# Check 9: DFC metadata on research docs (Reference/Created/Last modified,
# unique numbers, index currency). scripts/dfc.py prints the specifics.
section "9. Checking DFC Metadata (doc/research)"

if python3 scripts/dfc.py --check; then
    pass "DFC metadata and index current"
else
    fail "DFC metadata problems — run scripts/dfc.py --stamp && scripts/dfc.py --index"
fi

# Summary
section "Summary"

echo "Total checks: $CHECKS"
echo -e "Passed: ${GREEN}$((CHECKS - ERRORS))${NC}"
if [[ $WARNINGS -gt 0 ]]; then
    echo -e "Warnings: ${YELLOW}$WARNINGS${NC}"
fi
if [[ $ERRORS -gt 0 ]]; then
    echo -e "Failed: ${RED}$ERRORS${NC}"
fi

echo ""

if [[ $ERRORS -eq 0 ]]; then
    echo -e "${GREEN}✓ All documentation validation checks passed!${NC}"
    exit 0
else
    echo -e "${RED}✗ Documentation validation failed with $ERRORS error(s)${NC}"
    exit 1
fi
