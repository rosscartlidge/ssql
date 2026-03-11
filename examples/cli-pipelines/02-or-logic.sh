#!/bin/bash
# Example 2: OR Logic with + Separator
# Find employees who are EITHER young (age < 26) OR high earners (salary > 100k)

set -e

echo "Example 2: OR Logic - Young employees OR high earners"
echo "======================================================"
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
echo "Pipeline: Find employees where age < 26 OR salary > 100000"
echo "Command:"
echo "  ssql from /tmp/employees.csv | \\"
echo "    ssql where -match age lt 26 + -match salary gt 100000 | \\"
echo "    ssql to csv"
echo
echo "Results:"

ssql from /tmp/employees.csv | \
  ssql where -match age lt 26 + -match salary gt 100000 | \
  ssql to csv

echo
echo "This matches:"
echo "  - Bob (25, Marketing, 65000) - age < 26"
echo "  - Carol (35, Engineering, 105000) - salary > 100k"
echo "  - Henry (38, Engineering, 110000) - salary > 100k"
echo
echo "Note: The + separator creates OR logic between clauses"
