#!/bin/bash
# Example 3: Complex AND/OR Logic
# Find employees matching: (age > 30 AND dept = Engineering) OR (salary < 70000)

set -e

echo "Example 3: Complex AND/OR Logic"
echo "================================"
echo

# Create sample data
cat > /tmp/employees.csv <<EOF
name,age,department,salary
Alice,30,Engineering,95000
Bob,25,Marketing,65000
Carol,35,Engineering,105000
David,28,Sales,70000
Eve,32,Engineering,98000
Frank,45,Marketing,80000
Grace,29,Sales,72000
Henry,38,Engineering,110000
EOF

echo "Input data (employees.csv):"
cat /tmp/employees.csv
echo
echo "---"
echo

# Run the pipeline
echo "Pipeline: (age > 30 AND department = Engineering) OR (salary < 70000)"
echo "Command:"
echo "  ssql from /tmp/employees.csv | \\"
echo "    ssql where -match age gt 30 -match department eq Engineering + -match salary lt 70000 | \\"
echo "    ssql to csv"
echo
echo "Results:"

ssql from /tmp/employees.csv | \
  ssql where -match age gt 30 -match department eq Engineering + -match salary lt 70000 | \
  ssql to csv

echo
echo "This matches:"
echo "  Clause 1 (age > 30 AND dept = Engineering):"
echo "    - Carol (35, Engineering, 105000)"
echo "    - Eve (32, Engineering, 98000)"
echo "    - Henry (38, Engineering, 110000)"
echo
echo "  Clause 2 (salary < 70000):"
echo "    - Bob (25, Marketing, 65000)"
echo
echo "Note: Multiple -match in same clause = AND"
echo "      Separate clauses with + = OR"
