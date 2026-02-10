//go:build js && wasm

// Command ssql-wasm is a WebAssembly module that exposes ssql data transformation
// operations to JavaScript. It registers global functions that the explorer HTML
// can call for filtering, sorting, grouping, and deduplication.
//
// Build with: GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o ssql.wasm ./cmd/ssql-wasm
package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"
)

func main() {
	js.Global().Set("ssqlWhere", js.FuncOf(jsWhere))
	js.Global().Set("ssqlSort", js.FuncOf(jsSort))
	js.Global().Set("ssqlGroupBy", js.FuncOf(jsGroupBy))
	js.Global().Set("ssqlDistinct", js.FuncOf(jsDistinct))
	js.Global().Set("ssqlLimit", js.FuncOf(jsLimit))
	js.Global().Set("ssqlPipeline", js.FuncOf(jsPipeline))
	js.Global().Set("ssqlReady", true)

	// Block forever to keep WASM module alive.
	select {}
}

// jsWhere: ssqlWhere(jsonData, field, op, value) → filtered JSON string
func jsWhere(this js.Value, args []js.Value) any {
	if len(args) < 4 {
		return errorResult("ssqlWhere requires 4 arguments: jsonData, field, op, value")
	}
	records, err := jsonToRecords(args[0].String())
	if err != nil {
		return errorResult(fmt.Sprintf("parsing JSON: %v", err))
	}

	result := applyWhere(records, args[1].String(), args[2].String(), args[3].String())
	out, err := recordsToJSON(result)
	if err != nil {
		return errorResult(fmt.Sprintf("encoding JSON: %v", err))
	}
	return out
}

// jsSort: ssqlSort(jsonData, field, descending) → sorted JSON string
func jsSort(this js.Value, args []js.Value) any {
	if len(args) < 3 {
		return errorResult("ssqlSort requires 3 arguments: jsonData, field, descending")
	}
	records, err := jsonToRecords(args[0].String())
	if err != nil {
		return errorResult(fmt.Sprintf("parsing JSON: %v", err))
	}

	result := applySort(records, args[1].String(), args[2].Bool())
	out, err := recordsToJSON(result)
	if err != nil {
		return errorResult(fmt.Sprintf("encoding JSON: %v", err))
	}
	return out
}

// jsGroupBy: ssqlGroupBy(jsonData, groupField, aggField, aggFunc) → aggregated JSON string
func jsGroupBy(this js.Value, args []js.Value) any {
	if len(args) < 4 {
		return errorResult("ssqlGroupBy requires 4 arguments: jsonData, groupField, aggField, aggFunc")
	}
	records, err := jsonToRecords(args[0].String())
	if err != nil {
		return errorResult(fmt.Sprintf("parsing JSON: %v", err))
	}

	result := applyGroupBy(records, args[1].String(), args[2].String(), args[3].String())
	if result == nil {
		return errorResult("aggregation failed")
	}
	out, err := recordsToJSON(result)
	if err != nil {
		return errorResult(fmt.Sprintf("encoding JSON: %v", err))
	}
	return out
}

// jsDistinct: ssqlDistinct(jsonData, field) → deduplicated JSON string
func jsDistinct(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return errorResult("ssqlDistinct requires 2 arguments: jsonData, field")
	}
	records, err := jsonToRecords(args[0].String())
	if err != nil {
		return errorResult(fmt.Sprintf("parsing JSON: %v", err))
	}

	result := applyDistinct(records, args[1].String())
	out, err := recordsToJSON(result)
	if err != nil {
		return errorResult(fmt.Sprintf("encoding JSON: %v", err))
	}
	return out
}

// jsLimit: ssqlLimit(jsonData, n, offset) → paginated JSON string
func jsLimit(this js.Value, args []js.Value) any {
	if len(args) < 3 {
		return errorResult("ssqlLimit requires 3 arguments: jsonData, n, offset")
	}
	records, err := jsonToRecords(args[0].String())
	if err != nil {
		return errorResult(fmt.Sprintf("parsing JSON: %v", err))
	}

	result := applyLimit(records, args[1].Int(), args[2].Int())
	out, err := recordsToJSON(result)
	if err != nil {
		return errorResult(fmt.Sprintf("encoding JSON: %v", err))
	}
	return out
}

// jsPipeline: ssqlPipeline(jsonData, pipelineJSON) → transformed JSON string
// pipelineJSON is a JSON array of operation objects:
//
//	[
//	  {"op": "where", "field": "age", "operator": "gt", "value": "25"},
//	  {"op": "sort", "field": "name", "desc": false},
//	  {"op": "limit", "n": 100, "offset": 0}
//	]
func jsPipeline(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return errorResult("ssqlPipeline requires 2 arguments: jsonData, pipelineJSON")
	}
	records, err := jsonToRecords(args[0].String())
	if err != nil {
		return errorResult(fmt.Sprintf("parsing JSON: %v", err))
	}

	var ops []map[string]any
	if err := json.Unmarshal([]byte(args[1].String()), &ops); err != nil {
		return errorResult(fmt.Sprintf("parsing pipeline: %v", err))
	}

	for _, op := range ops {
		opName, _ := op["op"].(string)
		switch opName {
		case "where":
			field, _ := op["field"].(string)
			operator, _ := op["operator"].(string)
			value, _ := op["value"].(string)
			records = applyWhere(records, field, operator, value)
		case "sort":
			field, _ := op["field"].(string)
			desc, _ := op["desc"].(bool)
			records = applySort(records, field, desc)
		case "group_by":
			groupField, _ := op["groupField"].(string)
			aggField, _ := op["aggField"].(string)
			aggFunc, _ := op["aggFunc"].(string)
			records = applyGroupBy(records, groupField, aggField, aggFunc)
		case "distinct":
			field, _ := op["field"].(string)
			records = applyDistinct(records, field)
		case "limit":
			n := intFromAny(op["n"])
			offset := intFromAny(op["offset"])
			records = applyLimit(records, n, offset)
		default:
			return errorResult(fmt.Sprintf("unknown operation: %s", opName))
		}
	}

	out, err := recordsToJSON(records)
	if err != nil {
		return errorResult(fmt.Sprintf("encoding JSON: %v", err))
	}
	return out
}

func intFromAny(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	default:
		return 0
	}
}

func errorResult(msg string) string {
	result, _ := json.Marshal(map[string]string{"error": msg})
	return string(result)
}
