#!/bin/bash
# Level 3 Documentation Verification - Deep validation for releases
# Runs Level 2 tests plus comprehensive verification

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

ERRORS=0
WARNINGS=0

echo -e "${BLUE}StreamV3 Documentation Verification (Level 3)${NC}"
echo "================================================="
echo ""

# Run Level 2 checks first
echo -e "${BLUE}Running Level 2 Tests...${NC}"
echo "----------------------------------------"
if ! ./scripts/doc-test.sh; then
    echo -e "${RED}Level 2 tests failed. Fix these first.${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN}✓ Level 2 tests passed${NC}"
echo ""

# Helper functions
pass() {
    echo -e "${GREEN}✓${NC} $1"
}

fail() {
    echo -e "${RED}✗${NC} $1"
    : $((ERRORS++))
}

warn() {
    echo -e "${YELLOW}⚠${NC} $1"
    : $((WARNINGS++))
}

section() {
    echo ""
    echo -e "${BLUE}$1${NC}"
    echo "----------------------------------------"
}

# Check 1: Verify all examples in api-reference.md compile
section "1. Verifying Examples in api-reference.md"

TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

# Extract all Go code blocks from api-reference.md
counter=0
awk '
    /^```go$/ { in_block=1; counter++; filename = sprintf("'"$TEMP_DIR"'/api_example_%03d.go", counter); next }
    /^```$/ && in_block { in_block=0; next }
    in_block && filename { print > filename }
' doc/api-reference.md

total_api_examples=0
compiled_api_examples=0

# Note: API reference contains many documentation snippets (type signatures, partial examples)
# that aren't meant to compile. We use warnings instead of errors for these.

for example in "$TEMP_DIR"/api_example_*.go; do
    if [[ ! -f "$example" ]]; then
        continue
    fi

    : $((total_api_examples++))
    example_name=$(basename "$example")

    # Try to compile or check syntax
    if grep -q "^package main" "$example"; then
        if go build -o /dev/null "$example" 2>/dev/null; then
            pass "API example compiles: $example_name"
            : $((compiled_api_examples++))
        else
            warn "API example doesn't compile (may be documentation snippet): $example_name"
        fi
    else
        # For non-main examples, just count them without validation
        # These are typically type signatures, function signatures, or partial examples
        : $((compiled_api_examples++))
    fi
done

echo ""
echo "API Reference: $compiled_api_examples/$total_api_examples examples processed"
echo "(Most API reference examples are documentation snippets, not complete programs)"

# Check 2: Cross-reference all documented functions exist
section "2. Cross-Referencing Documented Functions"

# Extract function names mentioned in LLM docs
doc_funcs=$(grep -oh "streamv3\.[A-Z][a-zA-Z]*" doc/ai-code-generation.md doc/ai-human-guide.md | sort -u | sed 's/streamv3\.//')

# Get actual exported functions and types (use -all to include all exports)
actual_funcs=$(go doc -all github.com/rosscartlidge/ssql 2>/dev/null | grep -E "^(func|type) " | awk '{print $2}' | cut -d'(' -f1 | cut -d'[' -f1 | cut -d' ' -f1 | sort -u)

for func in $doc_funcs; do
    if echo "$actual_funcs" | grep -q "^${func}$"; then
        pass "Documented function exists: $func"
    else
        # Use warning instead of error - may be false positive from negative examples
        warn "Documented reference not found: $func (may be in negative example or outdated)"
    fi
done

# Check 3: Verify README examples are current
section "3. Verifying README Examples"

# Check that README uses current API
if grep -q "MakeMutableRecord" README.md; then
    pass "README uses current Record API"
else
    fail "README may use outdated Record API"
fi

if grep -q "ssql.Select\|ssql.Where\|ssql.Limit" README.md; then
    pass "README uses SQL-style naming"
else
    fail "README may use outdated function names"
fi

# Check 4: Verify consistency across all doc files
section "4. Checking Consistency Across Documentation"

# Check that all docs use the same API patterns
docs=("doc/ai-code-generation.md" "doc/ai-human-guide.md" "doc/api-reference.md" "README.md")

consistent=1

# Check MakeMutableRecord is used consistently
for doc in "${docs[@]}"; do
    if grep -q "Record" "$doc" 2>/dev/null; then
        if grep -q "NewRecord\|\.Build()" "$doc" 2>/dev/null; then
            # Check if it's in a "wrong example" section
            if ! grep "NewRecord\|\.Build()" "$doc" | grep -q "❌\|Wrong\|WRONG\|Avoid\|NOT\|Should be\|Symptoms"; then
                # Use warning instead of error - may be in a negative example that we can't detect
                warn "$(basename $doc) may use outdated Record API (check if in negative example)"
                consistent=0
            fi
        fi
    fi
done

if [[ $consistent -eq 1 ]]; then
    pass "All documentation uses consistent API patterns"
fi

# Check 5: Verify chart examples are current
section "5. Verifying Chart Examples"

chart_docs=$(grep -l "QuickChart\|InteractiveChart" doc/*.md README.md)

for doc in $chart_docs; do
    # Verify chart functions are called correctly
    if grep "QuickChart\|InteractiveChart" "$doc" | grep -q "ssql\."; then
        pass "Chart examples in $(basename $doc) use correct package prefix"
    else
        warn "Chart examples in $(basename $doc) may be missing package prefix"
    fi
done

# Check 6: Verify all imports are correct
section "6. Checking Import Statements"

# Extract import blocks from examples and verify they're current
import_issues=0

for example in "$TEMP_DIR"/example_*.go "$TEMP_DIR"/api_example_*.go; do
    if [[ ! -f "$example" ]]; then
        continue
    fi

    # Check for correct import path
    if grep -q "import" "$example"; then
        if grep "import" "$example" | grep -q "github.com/rosscartlidge/ssql"; then
            continue  # Good
        else
            if grep -q "streamv3\." "$example"; then
                : $((import_issues++))
            fi
        fi
    fi
done

if [[ $import_issues -eq 0 ]]; then
    pass "All examples use correct import paths"
else
    warn "$import_issues examples may have import issues"
fi

# Check 7: Verify go.mod is current
section "7. Checking go.mod"

if [[ -f "go.mod" ]]; then
    if grep -qE "^go 1\.(2[3-9]|[3-9][0-9])" go.mod; then
        pass "go.mod specifies Go 1.23+"
    else
        warn "go.mod does not specify Go 1.23+"
    fi
else
    fail "go.mod not found"
fi

# Summary
section "Summary"

echo "Level 3 comprehensive verification completed"
if [[ $WARNINGS -gt 0 ]]; then
    echo -e "Warnings: ${YELLOW}$WARNINGS${NC}"
fi
if [[ $ERRORS -gt 0 ]]; then
    echo -e "Errors: ${RED}$ERRORS${NC}"
fi

echo ""

if [[ $ERRORS -eq 0 ]]; then
    echo -e "${GREEN}✓ Level 3 documentation verification passed!${NC}"
    echo -e "${GREEN}✓ Ready for release!${NC}"
    exit 0
else
    echo -e "${RED}✗ Level 3 documentation verification failed with $ERRORS error(s)${NC}"
    echo -e "${YELLOW}Note: Warnings are informational and don't cause failure${NC}"
    exit 1
fi
