# Using AI to Generate ssql Code - Human Guide

*Complete guide for humans on getting the best ssql code from AI assistants*

---

## 🎯 Overview

This guide teaches you how to effectively use Large Language Models (Claude, ChatGPT, Gemini, etc.) to generate high-quality ssql code from natural language descriptions.

**What You'll Learn:**
- Setting up your LLM for ssql code generation
- Writing effective requests
- Verifying and iterating on generated code
- Using interactive CLI tools for maximum productivity
- Troubleshooting common issues

---

## 🚀 Quick Start (3 Steps)

### Step 1: Copy the Prompt

Open **[ai-code-generation.md](ai-code-generation.md)** and copy the entire contents (the code block starting with "You are an expert Go developer...").

### Step 2: Paste into Your LLM

Paste the prompt as your first message in:
- Claude (https://claude.ai)
- ChatGPT (https://chat.openai.com)
- Gemini (https://gemini.google.com)
- Or your preferred LLM

### Step 3: Describe What You Want

```
"Read sales data from sales.csv, filter for amounts over $500,
group by region, and show total sales per region"
```

The LLM will generate clean, working Go code that follows ssql best practices.

---

## 💻 Interactive Development with CLI Tools

For the most powerful experience, use LLM CLI tools that can **run and iterate on code** for you.

### Claude Code (Recommended)

**What it is**: Official CLI from Anthropic that combines Claude's intelligence with direct filesystem access and code execution.

**Installation**:
```bash
npm install -g @anthropic-ai/claude-code
```

**Setup for ssql (Option 1: Quick)**:
```bash
cd my-ssql-project
claude-code

# First message: Copy and paste the entire contents of
# doc/ai-code-generation.md from the ssql repository
```

**Setup for ssql (Option 2: Persistent - Recommended)**:
```bash
# In your project directory
mkdir -p .claude

# Create project-specific CLAUDE.md
cat > .claude/CLAUDE.md << 'EOF'
# ssql Project

This project uses ssql for stream processing.

## ssql Reference

For complete API documentation, use:
```bash
go doc github.com/rosscartlidge/ssql/v4
go doc github.com/rosscartlidge/ssql/v4.FunctionName
```

## Quick Rules

- Use SQL-style names: `Select`, `Where`, `Limit` (not Map, Filter, Take)
- Always handle errors from ReadCSV, ReadJSON, etc.
- CSV auto-parses numbers: use `GetOr(r, "age", int64(0))` not `GetOr(r, "age", "")`
- **🚨 CRITICAL**: Record fields are NOT directly accessible - use `MakeMutableRecord()` to create, `GetOr()` to read
- Only import packages actually used

## 🚨 Record Access (v1.0+)

**CRITICAL: Record is an encapsulated struct, NOT map[string]any**

```go
// ❌ WRONG - Direct field access will NOT compile
record["name"] = "Alice"
value := record["age"]

// ✅ CORRECT - Use builder and accessors
record := ssql.MakeMutableRecord().
    String("name", "Alice").
    Int("age", int64(30)).
    Freeze()

name := ssql.GetOr(record, "name", "")
age, exists := ssql.Get[int64](record, "age")
```

This applies to ALL external code (user code, LLM-generated code, examples).

## Important Patterns

- Read CSV: `data, err := ssql.ReadCSV("file.csv"); if err != nil { log.Fatal(err) }`
- Filter: `ssql.Where(func(r Record) bool { return condition })(data)`
- Group: `ssql.GroupByFields("groupName", "field1", "field2")(data)`
- Aggregate: `ssql.Aggregate("groupName", map[string]AggregateFunc{...})(grouped)`
- Chart: `ssql.QuickChart(data, "x", "y", "output.html")`
EOF

# Start Claude Code - it will read .claude/CLAUDE.md automatically
claude-code
```

**Example Session**:
```
You: "Read my sales.csv file and show me what fields are in it"

Claude Code:
✓ Read sales.csv
✓ Found fields: date, product, amount, region
✓ Showed first 5 records

You: "Filter for sales over $500, group by region, show totals"

Claude Code:
✓ Generated code
✓ Created main.go
✓ Ran: go run main.go
✓ Displayed results
[If errors occur, automatically fixes them]

You: "Now add a chart"

Claude Code:
✓ Updated code with chart
✓ Ran code
✓ Opened chart.html in browser
```

**Why It's Powerful**:
- ✅ Automatic iteration (fixes errors on its own)
- ✅ File awareness (reads your actual data)
- ✅ Runs code (executes and tests)
- ✅ Context aware (remembers project structure)
- ✅ ssql knowledge (via CLAUDE.md)

### Other CLI Tools

**Gemini CLI**:
```bash
npm install -g @google/generative-ai-cli
gemini-cli
# Paste ai-code-generation.md prompt, then start coding
```

**Aider (Multi-backend)**:
```bash
pip install aider-chat
aider
# Works with GPT-4, Claude, local models
```

**GitHub Copilot CLI**:
```bash
gh extension install github/gh-copilot
gh copilot suggest "create ssql pipeline to analyze sales"
```

### CLI vs Web LLMs

| Feature | Web LLMs | CLI Tools |
|---------|----------|-----------|
| Code Generation | ✅ Excellent | ✅ Excellent |
| File Access | ❌ None | ✅ Reads your files |
| Code Execution | ❌ Manual | ✅ Automatic |
| Error Fixing | 🔶 Manual | ✅ Automatic |
| Best For | Learning | Development |

---

## 📝 Writing Effective Requests

### ✅ Good Request Patterns

**1. Be Specific About Data**:
```
❌ "Analyze some data"
✅ "Read customer data from customers.csv, filter for customers with orders > $1000"
```

**2. Describe the Steps**:
```
❌ "Do sales analysis"
✅ "Read sales.csv, group by product category, calculate total revenue per category, show top 5"
```

**3. Mention Output Format**:
```
❌ "Show results"
✅ "Display results in console and create a bar chart saved as sales_chart.html"
```

**4. Specify Data Fields**:
```
❌ "Filter the data"
✅ "Filter for records where the 'amount' field is greater than 500"
```

### Request Templates

**Data Analysis**:
```
"Read [filename] data, filter for [condition], group by [field],
calculate [aggregation], and [output format]"
```

**Real-Time Processing**:
```
"Process [data source] in [time window] windows, calculate [metric],
alert when [condition]"
```

**Visualization**:
```
"Create [chart type] showing [metric] by [dimension] from [data source]"
```

**Join Analysis**:
```
"Join [dataset1] with [dataset2] on [key], find [pattern],
calculate [metric]"
```

---

## ✅ Verifying Generated Code

### Quick Verification Checklist

When you receive generated code, check:

#### Structure Check
- [ ] Has `package main` and `func main()`
- [ ] Includes all necessary imports
- [ ] **Only** imports packages actually used
- [ ] Handles errors from file operations
- [ ] Uses descriptive variable names

#### ssql API Check
- [ ] Uses SQL-style names (`Select`, `Where`, `Limit`)
- [ ] NOT using wrong names (`Map`, `Filter`, `Take`)
- [ ] Uses `MakeMutableRecord().Freeze()` for record creation
- [ ] Uses `ssql.GetOr()` for safe record access
- [ ] Includes group names in `GroupByFields()` and `Aggregate()`

#### Logic Check
- [ ] Processing steps are in logical order
- [ ] Each step is clearly understandable
- [ ] Variable names match their purpose
- [ ] Comments explain complex logic

#### Type Safety Check
- [ ] Numeric CSV fields use `int64` or `float64`
- [ ] Uses correct default values with `GetOr()`
- [ ] Type parameters included where needed

### Common Issues to Watch For

**❌ API Mistakes**:
```go
// Wrong - old/incorrect API
result := ssql.Map(fn)(data)      // Should be Select
result := ssql.Filter(fn)(data)   // Should be Where
result := ssql.Take(10)(data)     // Should be Limit
record := ssql.NewRecord().Build() // Should be MakeMutableRecord().Freeze()
```

**❌ Missing Error Handling**:
```go
// Wrong - no error check
data := ssql.ReadCSV("file.csv")

// Correct - always check errors
data, err := ssql.ReadCSV("file.csv")
if err != nil {
    log.Fatalf("Failed to read CSV: %v", err)
}
```

**❌ Wrong Types for CSV Data**:
```go
// Wrong - CSV parses "25" as int64, not string
age := ssql.GetOr(record, "age", "")

// Correct - use int64 for numeric CSV values
age := ssql.GetOr(record, "age", int64(0))
```

---

## 🔄 Iterating and Refining

### When First Result Isn't Perfect

**Strategy 1: Ask for Clarification**:
```
"The code looks good, but can you add comments explaining the aggregation step?"
```

**Strategy 2: Request Modifications**:
```
"Can you modify this to also calculate the standard deviation of sales amounts?"
```

**Strategy 3: Ask for Simplification**:
```
"This code is complex. Can you break it into simpler steps with more descriptive variable names?"
```

**Strategy 4: Request Different Approach**:
```
"Instead of grouping by region first, can you filter for high-value sales first, then group?"
```

### Example Iteration Session

**Round 1**:
```
You: "Analyze sales data to find top-performing products"
LLM: [generates basic code]
```

**Round 2**:
```
You: "Good start! Can you modify this to:
1. Only include sales from the last 30 days
2. Calculate both revenue and unit sales
3. Show the top 10 products by revenue
4. Create a bar chart of the results"
LLM: [generates enhanced code]
```

---

## 🐛 Troubleshooting

### Problem: Code Uses Wrong Function Names

**Symptoms**: `Map()`, `Filter()`, `Take()` instead of `Select()`, `Where()`, `Limit()`

**Solution**:
```
"Please update this code to use the current ssql API:
Select instead of Map, Where instead of Filter, Limit instead of Take"
```

### Problem: Complex, Hard-to-Read Code

**Symptoms**: Deeply nested function calls, unclear variable names

**Solution**:
```
"Can you rewrite this with simpler, step-by-step processing?
Use descriptive variable names and break complex operations into separate steps."
```

### Problem: Missing Error Handling

**Symptoms**: File operations without error checks

**Solution**:
```
"Please add proper error handling for all file operations and CSV reading"
```

### Problem: Wrong Record Creation API

**Symptoms**: `NewRecord().Build()` instead of `MakeMutableRecord().Freeze()`

**Solution**:
```
"Please update to use MakeMutableRecord().Freeze() for record creation"
```

### Problem: Type Issues with CSV Data

**Symptoms**: Using wrong types for numeric CSV fields

**Solution**:
```
"CSV auto-parses numbers. Please use int64 for integer fields
and float64 for decimal fields with GetOr()"
```

### When Your LLM Gets Confused

**Reset and Restart**:
1. Start a new conversation
2. Reapply the prompt template from ai-code-generation.md
3. Rephrase your request more clearly
4. Provide a simple example first

**Provide Examples**:
```
"Here's an example of the coding style I want:

```go
// Read and filter data
data, err := ssql.ReadCSV("sales.csv")
if err != nil {
    log.Fatal(err)
}

highValueSales := ssql.Where(func(r ssql.Record) bool {
    amount := ssql.GetOr(r, "amount", 0.0)
    return amount > 1000
})(data)
```

Now create similar code for analyzing customer data."
```

---

## 💡 Best Practices

### 1. Start Simple, Build Complexity

✅ **Good Progression**:
1. "Read sales.csv and show first 10 records"
2. "Filter for sales > $100"
3. "Group by region and calculate totals"
4. "Create chart of results"

❌ **Avoid**:
"Build a comprehensive sales analytics dashboard with real-time monitoring,
predictive analytics, and machine learning"

### 2. Be Specific About Data

✅ **Good**:
"The CSV has columns: customer_id, order_date, product_name, quantity, unit_price, total_amount"

❌ **Vague**:
"Analyze the sales data"

### 3. Request Human-Readable Code

✅ **Always Ask For**:
"Make the code easy to read and verify, with descriptive variable names and clear steps"

❌ **Don't Accept**:
Complex nested function calls that are hard to understand

### 4. Verify Each Step

✅ **Good Practice**:
"Before adding the chart, let me verify the data processing logic first"

❌ **Risky**:
Accepting large, complex code without reviewing each component

### 5. Build a Pattern Library

Save successful request patterns:
```
📁 Your Pattern Library:
- "Read [file], filter for [condition], group by [field], calculate [metric]"
- "Process [data] in [time] windows, detect [pattern], alert when [condition]"
- "Join [dataset1] with [dataset2], find [insight], create [visualization]"
```

### 6. Test with Small Data First

✅ **Smart Approach**:
"Generate code to test with a small sample file first, then we'll apply it to the full dataset"

❌ **Risky**:
Running generated code on large production datasets without testing

---

## 📚 Advanced Techniques

### 1. Multi-Step Requests

Break complex analyses into phases:
```
"First, help me understand this data structure.
Then I'll ask you to build the analysis pipeline."

Phase 1: "Read my sales.csv and show me what fields are available"
Phase 2: "Now create analysis to find seasonal sales patterns by product category"
Phase 3: "Add visualization and export the results"
```

### 2. Domain-Specific Language

Teach the LLM your domain terminology:
```
"In our retail business:
- 'SKU' means product identifier
- 'COGS' means cost of goods sold
- 'LTV' means customer lifetime value

Now analyze our product performance using these terms."
```

### 3. Template Creation

Ask for reusable templates:
```
"Create a template function that can analyze any CSV file to find
top N records by any numeric field, with flexible grouping"
```

### 4. Error Handling Strategies

Request robust error handling:
```
"Generate the sales analysis code, but make it robust - handle missing files,
empty data, and invalid field values gracefully"
```

---

## 🎓 Reference Cards

### Quick Request Templates

**Data Analysis**:
```
"Read [filename], filter for [condition], group by [field],
calculate [metrics], show top [N]"
```

**Real-Time Processing**:
```
"Monitor [data source] in [time window] windows, calculate [metrics],
alert when [condition]"
```

**Visualization**:
```
"Create [chart type] showing [metric] by [dimension], save as [filename]"
```

**Join Analysis**:
```
"Join [dataset1] with [dataset2] on [key field], analyze [insights],
find [patterns]"
```

### Verification Checklist (Print This)

```
□ package main and func main() present
□ ONLY imports packages actually used
□ Error handling for file operations
□ SQL-style API (Select, Where, Limit)
□ MakeMutableRecord().Freeze() for records
□ Safe record access (GetOr, Get[T])
□ Correct types for CSV data (int64, float64)
□ Descriptive variable names
□ Clear, step-by-step processing
□ Comments explain complex logic
□ Addresses all parts of request
□ Output/display logic included
```

### Common Fixes to Request

```
"Please add error handling for file operations"
"Can you use more descriptive variable names?"
"Break this into simpler steps with intermediate variables"
"Add comments explaining the aggregation logic"
"Use the current ssql API (Select not Map, Where not Filter)"
"Include the missing imports"
"Use MakeMutableRecord().Freeze() for record creation"
"Make the code easier to read and verify"
```

---

## 🌟 Success Stories

### Marketing Campaign Analysis
**User**: Sarah, Marketing Analyst
**Challenge**: Analyze email campaign performance across customer segments
**Result**: Complete analysis pipeline in 15 minutes (would have taken hours manually)

### IoT Sensor Monitoring
**User**: Mike, Operations Engineer
**Challenge**: Monitor temperature sensors and detect equipment failures
**Result**: Production monitoring system deployed in one afternoon

### Financial Risk Analysis
**User**: Lisa, Risk Analyst
**Challenge**: Analyze trading patterns for compliance issues
**Result**: Comprehensive risk analysis tool generating daily reports automatically

---

## 📖 Next Steps

### Learning More

- **[ssql Getting Started](codelab-intro.md)** - Learn ssql fundamentals
- **[API Reference](api-reference.md)** - Complete function documentation
- **[Typed Codelab](typed-codelab.md)** - The high-performance struct-based API

### AI Code Generation Resources

- **[AI Code Generation Prompt](ai-code-generation.md)** - THE prompt for LLMs (includes anti-patterns, examples, and all guidance)
- **[AI Prompt Maintenance Guide](AI-PROMPT-README.md)** - For developers maintaining the prompt

---

## 🎯 Key Takeaways

🎯 **Start with clear, specific requests**
📋 **Build complexity incrementally**
✅ **Verify each step before proceeding**
🔍 **Prioritize human-readable, verifiable code**
🔄 **Iterate and refine as needed**

Remember: The goal isn't just working code - it's code you can understand, verify, and maintain. Always prioritize clarity over cleverness.

Happy stream processing! 🚀

---

*ssql: Where natural language meets production-ready data processing* ✨
