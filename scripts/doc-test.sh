#!/bin/bash
# Level 2 Documentation Testing - Verify godoc matches exports
# Runs Level 1 checks plus additional verification

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

ERRORS=0
WARNINGS=0

echo -e "${BLUE}StreamV3 Documentation Testing (Level 2)${NC}"
echo "=============================================="
echo ""

# Run Level 1 checks first
echo -e "${BLUE}Running Level 1 Checks...${NC}"
echo "----------------------------------------"
if ! ./scripts/validate-docs.sh; then
    echo -e "${RED}Level 1 checks failed. Fix these first.${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN}✓ Level 1 checks passed${NC}"
echo ""

# Helper functions
pass() {
    echo -e "${GREEN}✓${NC} $1"
}

fail() {
    echo -e "${RED}✗${NC} $1"
    ERRORS=$((ERRORS + 1))
}

warn() {
    echo -e "${YELLOW}⚠${NC} $1"
    WARNINGS=$((WARNINGS + 1))
}

section() {
    echo ""
    echo -e "${BLUE}$1${NC}"
    echo "----------------------------------------"
}

# Check 1: Verify exported functions are documented
section "1. Verifying Exported Functions are Documented"

# Get all exported functions from the package
exported_funcs=$(go doc github.com/rosscartlidge/ssql/v4 | grep "^func " | awk '{print $2}' | cut -d'(' -f1 | sort -u)

# Check if each is mentioned in LLM docs
llm_files="doc/ai-code-generation.md doc/ai-human-guide.md"

for func in $exported_funcs; do
    # Skip generic type parameters
    if [[ "$func" =~ \[ ]]; then
        continue
    fi

    found=0
    for doc_file in $llm_files; do
        if grep -q "ssql\.$func" "$doc_file" 2>/dev/null; then
            found=1
            break
        fi
    done

    if [[ $found -eq 1 ]]; then
        pass "Function documented: $func"
    else
        warn "Function not documented in LLM guides: $func"
    fi
done

# Check 2: Verify types are documented
section "2. Verifying Exported Types are Documented"

exported_types=$(go doc github.com/rosscartlidge/ssql/v4 | grep "^type " | awk '{print $2}' | sort -u)

for type in $exported_types; do
    # Skip generic type parameters
    if [[ "$type" =~ \[ ]]; then
        continue
    fi

    found=0
    for doc_file in $llm_files; do
        if grep -q "\b$type\b" "$doc_file" 2>/dev/null; then
            found=1
            break
        fi
    done

    if [[ $found -eq 1 ]]; then
        pass "Type documented: $type"
    else
        warn "Type not documented in LLM guides: $type"
    fi
done

# Check 3: Verify critical functions have examples in godoc
section "3. Verifying Critical Functions Have godoc Examples"

critical_funcs=(
    "Select"
    "Where"
    "Limit"
    "GroupByFields"
    "Aggregate"
    "ReadCSV"
    "MakeMutableRecord"
    "InnerJoin"
)

for func in "${critical_funcs[@]}"; do
    # Get the godoc for this function
    doc_output=$(go doc "github.com/rosscartlidge/ssql/v4.$func" 2>/dev/null || echo "")

    if echo "$doc_output" | grep -q "Example:"; then
        pass "godoc has example: $func"
    else
        fail "godoc missing example: $func"
    fi
done

# Check 4: Verify function signatures match between godoc and LLM docs
section "4. Checking Function Signature Consistency"

# Sample check for a few critical functions
check_funcs=("Select" "Where" "GroupByFields" "Aggregate")

for func in "${check_funcs[@]}"; do
    # Get signature from godoc
    godoc_sig=$(go doc "github.com/rosscartlidge/ssql/v4.$func" 2>/dev/null | grep "^func $func" | head -1)

    if [[ -z "$godoc_sig" ]]; then
        warn "Could not find godoc signature for $func"
        continue
    fi

    # Check if LLM docs mention the function with similar signature
    # This is a basic check - just verify the function is mentioned
    if grep -q "ssql\.$func" doc/ai-code-generation.md; then
        pass "Function signature documented: $func"
    else
        warn "Function signature may be missing: $func"
    fi
done

# Check 5: Verify API patterns are current
section "5. Verifying Current API Patterns in Examples"

current_patterns=(
    "MakeMutableRecord"
    "Freeze()"
    ", err :="
    "if err != nil"
)

for doc_file in $llm_files; do
    file_ok=1
    for pattern in "${current_patterns[@]}"; do
        if ! grep -q "$pattern" "$doc_file" 2>/dev/null; then
            fail "Missing current pattern '$pattern' in $doc_file"
            file_ok=0
        fi
    done

    if [[ $file_ok -eq 1 ]]; then
        pass "All current patterns present in $(basename $doc_file)"
    fi
done

# Summary
section "Summary"

echo "Level 2 additional checks completed"
if [[ $WARNINGS -gt 0 ]]; then
    echo -e "Warnings: ${YELLOW}$WARNINGS${NC}"
fi
if [[ $ERRORS -gt 0 ]]; then
    echo -e "Errors: ${RED}$ERRORS${NC}"
fi

echo ""

if [[ $ERRORS -eq 0 ]]; then
    echo -e "${GREEN}✓ Level 2 documentation testing passed!${NC}"
    exit 0
else
    echo -e "${RED}✗ Level 2 documentation testing failed with $ERRORS error(s)${NC}"
    echo -e "${YELLOW}Note: Warnings are informational and don't cause failure${NC}"
    exit 1
fi
