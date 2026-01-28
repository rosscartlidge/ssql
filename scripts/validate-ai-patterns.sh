#!/bin/bash
# Validates that generated Go code follows ssql best practices and API patterns
# Usage: ./scripts/validate-ai-patterns.sh <go-file>

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

if [ $# -ne 1 ]; then
    echo "Usage: $0 <go-file>"
    exit 1
fi

FILE="$1"

if [ ! -f "$FILE" ]; then
    echo -e "${RED}Error: File $FILE does not exist${NC}"
    exit 1
fi

echo -e "${BLUE}Validating ssql patterns in: $FILE${NC}"
echo ""

ERRORS=0
WARNINGS=0

# Check 1: Correct import path (v4)
echo -n "Checking import path... "
if grep -q '"github.com/rosscartlidge/ssql/v4"' "$FILE"; then
    echo -e "${GREEN}✓${NC}"
else
    if grep -q '"github.com/rosscartlidge/ssql"' "$FILE"; then
        echo -e "${RED}✗${NC}"
        echo "  Error: Import path missing /v4 suffix (found ssql without version)"
        ((ERRORS++))
    elif grep -q '"github.com/rosscartlidge/ssql/v3"' "$FILE"; then
        echo -e "${RED}✗${NC}"
        echo "  Error: Using old v3 import path (should be /v4)"
        ((ERRORS++))
    else
        echo -e "${RED}✗${NC}"
        echo "  Error: Must import github.com/rosscartlidge/ssql/v4"
        ((ERRORS++))
    fi
fi

# Check 2: No wrong import paths
echo -n "Checking for wrong imports... "
WRONG_IMPORTS=0
if grep -q '"github.com/rocketlaunchr/' "$FILE"; then
    echo -e "${RED}✗${NC}"
    echo "  Error: Found wrong import path (rocketlaunchr instead of rosscartlidge)"
    ((ERRORS++))
    WRONG_IMPORTS=1
fi
if grep -q '"github.com/rosscartlidge/streamv3"' "$FILE"; then
    echo -e "${RED}✗${NC}"
    echo "  Error: Found old streamv3 import path (should be ssql/v4)"
    ((ERRORS++))
    WRONG_IMPORTS=1
fi
if [ $WRONG_IMPORTS -eq 0 ]; then
    echo -e "${GREEN}✓${NC}"
fi

# Check 3: SQL-style naming (Where not Filter, Select not Map)
echo -n "Checking SQL-style API usage... "
NAMING_ISSUES=0
if grep -q 'ssql\.Filter(' "$FILE"; then
    echo -e "${YELLOW}⚠${NC}"
    echo "  Warning: Found ssql.Filter() - should use ssql.Where() for filtering"
    ((WARNINGS++))
    NAMING_ISSUES=1
fi
if grep -qP 'ssql\.FlatMap\(' "$FILE"; then
    echo -e "${YELLOW}⚠${NC}"
    echo "  Warning: Found ssql.FlatMap() - should use ssql.SelectMany()"
    ((WARNINGS++))
    NAMING_ISSUES=1
fi
if grep -qP 'ssql\.Take\(' "$FILE"; then
    echo -e "${YELLOW}⚠${NC}"
    echo "  Warning: Found ssql.Take() - should use ssql.Limit()"
    ((WARNINGS++))
    NAMING_ISSUES=1
fi
if grep -qP 'ssql\.Skip\(' "$FILE"; then
    echo -e "${YELLOW}⚠${NC}"
    echo "  Warning: Found ssql.Skip() - should use ssql.Offset()"
    ((WARNINGS++))
    NAMING_ISSUES=1
fi
if [ $NAMING_ISSUES -eq 0 ]; then
    echo -e "${GREEN}✓${NC}"
fi

# Check 4: Error handling for I/O operations
echo -n "Checking error handling... "
IO_OPS=0
IO_HANDLED=0
for op in ReadCSV ReadJSON ReadJSONFast ReadArrow; do
    if grep -q "${op}(" "$FILE"; then
        ((IO_OPS++))
    fi
done
if [ $IO_OPS -gt 0 ]; then
    if grep -q "if err != nil" "$FILE"; then
        echo -e "${GREEN}✓${NC}"
    else
        echo -e "${RED}✗${NC}"
        echo "  Error: I/O operations found but no error handling"
        ((ERRORS++))
    fi
else
    echo -e "${BLUE}N/A${NC} (no I/O operations)"
fi

# Check 5: Proper GroupByFields usage
echo -n "Checking GroupByFields usage... "
if grep -q "GroupByFields(" "$FILE"; then
    if grep -qP 'GroupByFields\(\s*\[\]string\{' "$FILE"; then
        echo -e "${RED}✗${NC}"
        echo "  Error: Wrong GroupByFields API - should be GroupByFields(namespace, field1, field2, ...)"
        ((ERRORS++))
    else
        echo -e "${GREEN}✓${NC}"
    fi
else
    echo -e "${BLUE}N/A${NC} (no grouping)"
fi

# Check 6: Proper Aggregate usage
echo -n "Checking Aggregate usage... "
if grep -q "Aggregate(" "$FILE"; then
    if grep -qP 'Count\("[^"]+"\)' "$FILE" && ! grep -q 'Count()' "$FILE"; then
        echo -e "${RED}✗${NC}"
        echo "  Error: Wrong Count() syntax - Count() takes no parameters, field name is the map key"
        ((ERRORS++))
    elif grep -q 'map\[string\]ssql\.AggregateFunc{' "$FILE"; then
        echo -e "${GREEN}✓${NC}"
    else
        echo -e "${YELLOW}⚠${NC}"
        echo "  Warning: Should use map[string]ssql.AggregateFunc{...}"
        ((WARNINGS++))
    fi
else
    echo -e "${BLUE}N/A${NC} (no aggregation)"
fi

# Check 7: Proper use of Chain or Pipe
echo -n "Checking composition style... "
MULTI_OPS=$(grep -cP 'ssql\.(Where|Select|GroupByFields|Aggregate|SortBy|Limit|Offset|Update|InnerJoin|LeftJoin|DistinctBy)\(' "$FILE" || true)
if [ "$MULTI_OPS" -ge 2 ]; then
    if grep -q "Chain(" "$FILE" || grep -q "Pipe(" "$FILE"; then
        echo -e "${GREEN}✓${NC}"
    else
        echo -e "${YELLOW}⚠${NC}"
        echo "  Warning: Multiple operations found - consider using Chain() for readability"
        ((WARNINGS++))
    fi
elif [ "$MULTI_OPS" -ge 1 ]; then
    echo -e "${GREEN}✓${NC} (single operation, Chain() not required)"
else
    echo -e "${BLUE}N/A${NC} (no stream operations)"
fi

# Check 8: Record access patterns
echo -n "Checking Record access... "
RECORD_ISSUES=0
if grep -qP 'record\["|r\["|rec\["' "$FILE"; then
    echo -e "${RED}✗${NC}"
    echo "  Error: Direct map access on Record (use GetOr/Get instead)"
    ((ERRORS++))
    RECORD_ISSUES=1
fi
if grep -qP '\.\(string\)|\.\(int64\)|\.\(float64\)' "$FILE"; then
    echo -e "${YELLOW}⚠${NC}"
    echo "  Warning: Type assertion found - prefer GetOr() for safe field access"
    ((WARNINGS++))
    RECORD_ISSUES=1
fi
if grep -q 'SetAny(' "$FILE"; then
    echo -e "${RED}✗${NC}"
    echo "  Error: SetAny() was removed in v2 - use typed setters (.String(), .Int(), .Float())"
    ((ERRORS++))
    RECORD_ISSUES=1
fi
if [ $RECORD_ISSUES -eq 0 ]; then
    echo -e "${GREEN}✓${NC}"
fi

# Check 9: Join function usage
echo -n "Checking Join usage... "
if grep -qP 'ssql\.Join\(' "$FILE" && ! grep -qP 'ssql\.(Inner|Left|Right|Full|Lookup)Join\(' "$FILE"; then
    echo -e "${RED}✗${NC}"
    echo "  Error: Bare ssql.Join() doesn't exist - use InnerJoin, LeftJoin, etc."
    ((ERRORS++))
elif grep -qP 'ssql\.(Inner|Left|Right|Full|Lookup)Join\(' "$FILE"; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${BLUE}N/A${NC} (no joins)"
fi

# Check 10: Signal processing patterns
echo -n "Checking signal processing patterns... "
if grep -q 'ssql\.FFT\|ssql\.Spectrogram\|ssql\.Convolve\|ssql\.Correlate' "$FILE"; then
    SIGNAL_ISSUES=0
    if grep -q 'ssql\.FFT(' "$FILE" && ! grep -q 'ssql\.ExtractSignal\|ssql\.Signal' "$FILE"; then
        echo -e "${YELLOW}⚠${NC}"
        echo "  Warning: FFT used but no signal extraction found - ensure input is ssql.Signal type"
        ((WARNINGS++))
        SIGNAL_ISSUES=1
    fi
    if [ $SIGNAL_ISSUES -eq 0 ]; then
        echo -e "${GREEN}✓${NC}"
    fi
else
    echo -e "${BLUE}N/A${NC} (no signal processing)"
fi

# Check 11: Expression language patterns (CLI-specific, check in Go context)
echo -n "Checking for CLI-only patterns in Go code... "
if grep -q 'ExprAgg(' "$FILE"; then
    echo -e "${RED}✗${NC}"
    echo "  Error: ExprAgg() doesn't exist in ssql package - expressions are CLI-only"
    ((ERRORS++))
else
    echo -e "${GREEN}✓${NC}"
fi

# Check 12: Compilation test
echo -n "Checking compilation... "
COMPILE_DIR="/tmp/ssql-ai-validate-$$"
mkdir -p "$COMPILE_DIR"
cp "$FILE" "$COMPILE_DIR/main.go"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

cat > "$COMPILE_DIR/go.mod" << GOMOD
module ssql-ai-validate

go 1.24.8

require github.com/rosscartlidge/ssql/v4 v4.11.0

replace github.com/rosscartlidge/ssql/v4 => ${PROJECT_DIR}
GOMOD
cp "$PROJECT_DIR/go.sum" "$COMPILE_DIR/go.sum" 2>/dev/null || true

if (cd "$COMPILE_DIR" && go mod tidy 2>/dev/null && go build -o /dev/null . 2>/dev/null); then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${RED}✗${NC}"
    echo "  Error: Code does not compile"
    (cd "$COMPILE_DIR" && go build . 2>&1 | head -10) | while IFS= read -r line; do
        echo "  $line"
    done
    ((ERRORS++))
fi
rm -rf "$COMPILE_DIR"

echo ""
echo -e "${BLUE}Validation Summary:${NC}"
echo "  Errors: $ERRORS"
echo "  Warnings: $WARNINGS"

if [ $ERRORS -eq 0 ] && [ $WARNINGS -eq 0 ]; then
    echo -e "${GREEN}✓ All checks passed!${NC}"
    exit 0
elif [ $ERRORS -eq 0 ]; then
    echo -e "${YELLOW}⚠ Passed with warnings${NC}"
    exit 0
else
    echo -e "${RED}✗ Validation failed${NC}"
    exit 1
fi
