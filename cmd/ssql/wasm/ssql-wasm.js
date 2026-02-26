// SsqlWasm - JavaScript wrapper for the ssql WASM module.
// Provides a clean API for calling ssql data transformations from the browser.
class SsqlWasm {
    constructor() {
        this._ready = false;
        this._go = null;
    }

    // init loads the WASM module from a URL. Call once before using any other methods.
    // wasmUrl: path to ssql.wasm (default: './ssql.wasm')
    // Returns a promise that resolves when the module is ready.
    async init(wasmUrl = './ssql.wasm') {
        if (this._ready) return;

        // Load wasm_exec.js runtime (must be loaded before this script)
        if (typeof Go === 'undefined') {
            throw new Error('wasm_exec.js must be loaded before ssql-wasm.js');
        }

        this._go = new Go();
        const result = await WebAssembly.instantiateStreaming(
            fetch(wasmUrl),
            this._go.importObject
        );
        this._go.run(result.instance);

        // Wait for ssqlReady flag set by Go main()
        await this._waitReady();
        this._ready = true;
    }

    // initFromBytes loads the WASM module from an ArrayBuffer of bytes.
    // Use this when the WASM binary is embedded (e.g. base64-decoded) rather than fetched.
    async initFromBytes(wasmBytes) {
        if (this._ready) return;

        if (typeof Go === 'undefined') {
            throw new Error('wasm_exec.js must be loaded before ssql-wasm.js');
        }

        this._go = new Go();
        const result = await WebAssembly.instantiate(wasmBytes, this._go.importObject);
        this._go.run(result.instance);

        await this._waitReady();
        this._ready = true;
    }

    async _waitReady() {
        for (let i = 0; i < 100; i++) {
            if (window.ssqlReady) return;
            await new Promise(r => setTimeout(r, 10));
        }
        throw new Error('ssql WASM module did not become ready');
    }

    _checkReady() {
        if (!this._ready) throw new Error('Call init() before using ssql WASM operations');
    }

    _parseResult(result) {
        if (typeof result === 'string' && result.startsWith('{"error"')) {
            const parsed = JSON.parse(result);
            if (parsed.error) throw new Error('ssql: ' + parsed.error);
        }
        return JSON.parse(result);
    }

    // where filters data rows matching the condition.
    // data: array of objects
    // field: field name to filter on
    // op: comparison operator (eq, ne, gt, ge, lt, le, contains, startswith, endswith, regex)
    // value: comparison value (string)
    where(data, field, op, value) {
        this._checkReady();
        const result = window.ssqlWhere(JSON.stringify(data), field, op, String(value));
        return this._parseResult(result);
    }

    // sort sorts data by a field.
    // data: array of objects
    // field: field name to sort by
    // desc: true for descending, false for ascending
    sort(data, field, desc = false) {
        this._checkReady();
        const result = window.ssqlSort(JSON.stringify(data), field, desc);
        return this._parseResult(result);
    }

    // groupBy groups data by a field and applies an aggregation.
    // data: array of objects
    // groupField: field to group by
    // aggField: field to aggregate (ignored for 'count')
    // aggFunc: aggregation function (count, sum, avg, min, max)
    groupBy(data, groupField, aggField, aggFunc) {
        this._checkReady();
        const result = window.ssqlGroupBy(JSON.stringify(data), groupField, aggField, aggFunc);
        return this._parseResult(result);
    }

    // distinct removes duplicate rows based on a field value.
    // data: array of objects
    // field: field to deduplicate by
    distinct(data, field) {
        this._checkReady();
        const result = window.ssqlDistinct(JSON.stringify(data), field);
        return this._parseResult(result);
    }

    // limit returns a page of rows.
    // data: array of objects
    // n: maximum number of rows to return
    // offset: number of rows to skip (default 0)
    limit(data, n, offset = 0) {
        this._checkReady();
        const result = window.ssqlLimit(JSON.stringify(data), n, offset);
        return this._parseResult(result);
    }

    // pipeline runs multiple operations in a single WASM call.
    // data: array of objects
    // operations: array of operation objects, e.g.:
    //   [
    //     {op: 'where', field: 'age', operator: 'gt', value: '25'},
    //     {op: 'sort', field: 'name', desc: false},
    //     {op: 'limit', n: 100, offset: 0}
    //   ]
    pipeline(data, operations) {
        this._checkReady();
        const result = window.ssqlPipeline(JSON.stringify(data), JSON.stringify(operations));
        return this._parseResult(result);
    }
}
