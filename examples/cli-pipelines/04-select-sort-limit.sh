#!/bin/bash
# Example 4: Select Fields, Sort, and Limit
# Create a top-N report with specific fields

set -e

echo "Example 4: Select, Sort, and Limit - Top 3 Earners"
echo "===================================================="
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
echo "Pipeline: Select name & salary, sort by salary descending, take top 3"
echo "Command:"
echo "  ssql read-csv /tmp/employees.csv | \\"
echo "    ssql select -field name + -field salary | \\"
echo "    ssql sort -field salary -desc | \\"
echo "    ssql limit -n 3 | \\"
echo "    ssql to csv"
echo
echo "Results:"

ssql read-csv /tmp/employees.csv | \
  ssql select -field name + -field salary | \
  ssql sort -field salary -desc | \
  ssql limit -n 3 | \
  ssql to csv

echo
echo "Top 3 earners:"
echo "  1. Henry - 110,000"
echo "  2. Carol - 105,000"
echo "  3. Eve - 98,000"
echo
echo "Note: The + separator in select allows multiple field selections"
