#!/bin/bash
# Example 1: Basic Filtering
# Filter employees by age and department

set -e

echo "Example 1: Filter employees over 30 in Engineering department"
echo "=============================================================="
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
echo "Pipeline: Find Engineering employees over 30"
echo "Command:"
echo "  ssql read-csv /tmp/employees.csv | \\"
echo "    ssql where -match age gt 30 -match department eq Engineering | \\"
echo "    ssql to csv"
echo
echo "Results:"

ssql read-csv /tmp/employees.csv | \
  ssql where -match age gt 30 -match department eq Engineering | \
  ssql to csv

echo
echo "This filters to:"
echo "  - Carol (35, Engineering, 105000)"
echo "  - Eve (32, Engineering, 98000)"
echo "  - Henry (38, Engineering, 110000)"
