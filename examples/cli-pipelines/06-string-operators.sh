#!/bin/bash
# Example 6: String Operators
# Demonstrate contains, startswith, endswith operators

set -e

echo "Example 6: String Matching Operators"
echo "====================================="
echo

# Create sample data with email addresses
cat > /tmp/users.csv <<EOF
name,email,department
Alice,alice@engineering.com,Engineering
Bob,bob@sales.org,Sales
Carol,carol@engineering.com,Engineering
David,david@marketing.net,Marketing
Eve,eve.smith@engineering.com,Engineering
Frank,frank@sales.org,Sales
EOF

echo "Input data (users.csv):"
cat /tmp/users.csv
echo
echo "---"
echo

# Example 1: contains operator
echo "Example 6a: Find emails containing 'engineering'"
echo "Command:"
echo "  ssql from /tmp/users.csv | \\"
echo "    ssql where -match email contains engineering | \\"
echo "    ssql to csv"
echo
echo "Results:"

ssql from /tmp/users.csv | \
  ssql where -match email contains engineering | \
  ssql to csv

echo
echo "---"
echo

# Example 2: endswith operator
echo "Example 6b: Find emails ending with '.org'"
echo "Command:"
echo "  ssql from /tmp/users.csv | \\"
echo "    ssql where -match email endswith .org | \\"
echo "    ssql to csv"
echo
echo "Results:"

ssql from /tmp/users.csv | \
  ssql where -match email endswith .org | \
  ssql to csv

echo
echo "---"
echo

# Example 3: startswith operator
echo "Example 6c: Find names starting with 'C'"
echo "Command:"
echo "  ssql from /tmp/users.csv | \\"
echo "    ssql where -match name startswith C | \\"
echo "    ssql to csv"
echo
echo "Results:"

ssql from /tmp/users.csv | \
  ssql where -match name startswith C | \
  ssql to csv

echo
echo "String operators available:"
echo "  - contains: String contains substring"
echo "  - startswith: String starts with prefix"
echo "  - endswith: String ends with suffix"
