// Package vizjs embeds the shared display-sink renderer (DFC119 Phase
// C). The SOURCE of truth is cmd/ssql-playground/ssql-ui-viz.js; make
// explore-wasm syncs the copies and TestVizModuleCopiesIdentical
// gates the sync. Owning the embed HERE means every consumer —
// library callers of AnimateChart, the CLI, the workspace — gets the
// module without plumbing.
package vizjs

import _ "embed"

//go:embed ssql-ui-viz.js
var JS string
