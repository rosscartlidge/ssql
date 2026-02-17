//go:build js && wasm

// Command ssql-wasm is a WebAssembly module that exposes ssql data transformation
// operations to JavaScript. It registers global functions that the explorer HTML
// can call for filtering, sorting, grouping, and deduplication.
//
// Build with: GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o ssql.wasm ./cmd/ssql-wasm
// Or TinyGo: tinygo build -o ssql.wasm -target wasm -no-debug -panic=trap -opt=z ./cmd/ssql-wasm
package main

import (
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
		return errorJSON("ssqlWhere requires 4 arguments: jsonData, field, op, value")
	}
	ds, err := parseJSONArray(args[0].String())
	if err != nil {
		return errorJSON(fmt.Sprintf("parsing JSON: %v", err))
	}
	result := ds.where(args[1].String(), args[2].String(), args[3].String())
	return result.toJSON()
}

// jsSort: ssqlSort(jsonData, field, descending) → sorted JSON string
func jsSort(this js.Value, args []js.Value) any {
	if len(args) < 3 {
		return errorJSON("ssqlSort requires 3 arguments: jsonData, field, descending")
	}
	ds, err := parseJSONArray(args[0].String())
	if err != nil {
		return errorJSON(fmt.Sprintf("parsing JSON: %v", err))
	}
	result := ds.sortBy(args[1].String(), args[2].Bool())
	return result.toJSON()
}

// jsGroupBy: ssqlGroupBy(jsonData, groupField, aggField, aggFunc) → aggregated JSON string
func jsGroupBy(this js.Value, args []js.Value) any {
	if len(args) < 4 {
		return errorJSON("ssqlGroupBy requires 4 arguments: jsonData, groupField, aggField, aggFunc")
	}
	ds, err := parseJSONArray(args[0].String())
	if err != nil {
		return errorJSON(fmt.Sprintf("parsing JSON: %v", err))
	}
	result := ds.groupBy(args[1].String(), args[2].String(), args[3].String())
	return result.toJSON()
}

// jsDistinct: ssqlDistinct(jsonData, field) → deduplicated JSON string
func jsDistinct(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return errorJSON("ssqlDistinct requires 2 arguments: jsonData, field")
	}
	ds, err := parseJSONArray(args[0].String())
	if err != nil {
		return errorJSON(fmt.Sprintf("parsing JSON: %v", err))
	}
	result := ds.distinct(args[1].String())
	return result.toJSON()
}

// jsLimit: ssqlLimit(jsonData, n, offset) → paginated JSON string
func jsLimit(this js.Value, args []js.Value) any {
	if len(args) < 3 {
		return errorJSON("ssqlLimit requires 3 arguments: jsonData, n, offset")
	}
	ds, err := parseJSONArray(args[0].String())
	if err != nil {
		return errorJSON(fmt.Sprintf("parsing JSON: %v", err))
	}
	result := ds.limit(args[1].Int(), args[2].Int())
	return result.toJSON()
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
		return errorJSON("ssqlPipeline requires 2 arguments: jsonData, pipelineJSON")
	}
	ds, err := parseJSONArray(args[0].String())
	if err != nil {
		return errorJSON(fmt.Sprintf("parsing JSON: %v", err))
	}

	ops, err := parsePipelineOps(args[1].String())
	if err != nil {
		return errorJSON(fmt.Sprintf("parsing pipeline: %v", err))
	}

	result, err := ds.pipeline(ops)
	if err != nil {
		return errorJSON(fmt.Sprintf("pipeline error: %v", err))
	}
	return result.toJSON()
}
