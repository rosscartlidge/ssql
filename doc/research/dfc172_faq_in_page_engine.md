# The Playground Engine in the FAQ Page

Reference: DFC172
Created: 2026-11-26
Last modified: 2026-11-26

[Back to Index](./README.md)

Ross asked (2026-11-26): what about putting the playground's wasm
implementation into the FAQ page, so the page can run pipelines and has a
virtual filesystem? This is a write-up; nothing has been changed.

**In short:**

- **It is useful, for one reason above the others.** A visitor's real file
  could stay in their browser, at any size. Today they paste at most
  20 KB, and it goes to the server. With an engine in the page, they pick
  the file; the page sends the server only its header and a few rows (what
  they paste today, and shown to them first); and the verified answer runs
  on the whole file, locally. Editing and rerunning in place, and charts
  inline, come with it.
- **It should not take over verification.** The model's correction loop
  stays on the server, against the sample data, as DFC169 §4.1 decided.
  Moving it into the browser would remove server-side execution, and with
  it the jail, the biggest attack surface DFC171 discusses. But the server
  would then be trusting whatever the browser reports, the loop would
  cross the network on every correction, and the page would depend on a
  6 MB download before its first answer (§5).
- **The cheapest way is to embed the playground itself.** An inline iframe
  of the GitHub Pages playground, rather than a second copy of the engine
  and its UI in `page.html`. Answers already open there through `#p=` and
  `#w=` links (DFC169 §12.15). What is missing is a way to hand over a file
  too big for a link: a `postMessage` handshake, about a day's work (§4).

## 1. What the FAQ does today

- **The server runs every pipeline**: schema-mode pre-flight, then a run
  in the bubblewrap jail with the slim build (DFC169 §12; DFC171 §1). It
  runs against the playground's sample files, plus whatever the visitor
  pasted. A paste is limited to 20 KB and 8 files (`maxPaste`,
  `scripts/faq/visitor.go`).
- **The answer links into the playground.** A `#p=` link carries the
  pipeline. A `#w=` link also carries the visitor's pasted files (DFC169
  §12.15), up to 256 KB inflated. The visitor reruns and edits it there, in
  another tab.
- **Pasted data reaches the server.** The log keeps only its counts (no
  content and no addresses), but the visitor still has to trust that.
  Someone with a 50 MB export or a confidential file has two choices:
  paste a header and copy the pipeline by hand, or not use the FAQ.

## 2. What an engine in the page would add

In order of value:

1. **Local files of any size, kept local.** The page gets a "use a file
   from your computer" picker, and reads the file with the File API. It
   shows the visitor the header and first few rows it will send, which is
   today's paste, built for them. When the answer comes back verified, the
   engine runs it on the whole file in the page's virtual filesystem. The
   server never sees more than the visitor saw being sent.
2. **Run, edit and rerun in place.** The output and any chart sit under
   the answer. A follow-up question starts from a pipeline the visitor has
   already tried on their own data.
3. **A shared answer can be rerun**, by whoever opens the share link.

What it does not add: better answers. The model, the docs and the
server-side check are unchanged.

## 3. What it costs

These are measured on 2026-11-26 against the deployed playground.

- **Download: 5.9 MB, gzip-compressed** (24.5 MB uncompressed). GitHub
  Pages serves it gzip-compressed with `Access-Control-Allow-Origin: *`.
  The FAQ page itself is 24 KB. So the engine is loaded only when the
  visitor first asks for it (the picker, or "run here"), never with the
  page.
- **Compile: about 60 ms** for the 24 MB module in V8 (node 2026-11,
  `WebAssembly.compile`; baseline compilation, with optimisation later and
  lazy). Download dominates, then the Go runtime's start-up.
- **Memory.** The file lives in the virtual filesystem, as a JavaScript
  string or bytes, and again in Go's heap while it is read. A 100 MB file
  is fine on a desktop and doubtful on a phone. The playground has the
  same limit today.
- **Who serves it.** If the FAQ served the engine itself, every visitor
  who used it would pull 6 MB through Funnel, from the laptop's uplink.
  GitHub Pages serves it already, with caching.

## 4. Two ways to put it in the page

### A. Embed the playground (recommended)

An `<iframe>` of `https://rosscartlidge.github.io/ssql/playground.html`
under each verified answer, opened on request:

- **Small things** go in the URL, as today: `#p=PIPELINE`, or `#w=` with
  the pasted files.
- **A local file** goes by `postMessage`. Once the playground is ready, it
  posts `{ssql: "ready"}` to its parent. The FAQ page replies with
  `{ssql: "workspace", v: 1, p, files: {name: ArrayBuffer}}`, transferring
  the buffers rather than copying them. The playground puts the files
  through the checks `#w=` already uses: the name pattern, the count, no
  `..`. It applies a size limit of its own (say 200 MB), lists the files
  under "Available datasets", and shows the pipeline without running it.
- **The FAQ page's CSP** gains `frame-src https://rosscartlidge.github.io`.
  Nothing else changes. The engine never runs in the FAQ's origin, so the
  FAQ needs neither `wasm-unsafe-eval` nor script from anywhere else.

**Why this way:**

- One UI, not two. The playground's editor, completion, help, chart
  sandbox and dataset list are all there already, and its end-to-end
  harness (`scripts/playground-test.sh`) tests them. A second copy in
  `page.html` would drift. The explore page (DFC107) went through this
  with `ssql-ui.js`, and still keeps an embedded copy in step.
- The cross-origin boundary is a feature. The visitor's file goes to the
  playground's origin, which has no server at all.

**Who may send a workspace.** Any page can frame the playground and post
to it. That is harmless: it can only load data into a playground showing
in that visitor's own browser, and the page that sent it cannot read the
frame. So the playground can accept a workspace from any parent, as it
accepts a `#w=` link from anyone. It never runs a pipeline because of a
message; the visitor clicks Run.

**Effort: about a day.**

- In the playground: the message handler, the size limit, and harness
  cases. The harness should cover a good workspace, a bad name, an
  oversized file, and a message that arrives before the engine is ready.
- In the FAQ page:
  - the picker;
  - the header-and-rows extraction, which reuses `sectionsOf`;
  - the "this is what will be sent" preview;
  - the iframe.

### B. The engine in the FAQ page itself

Load `wasm_exec.js`, `fs-polyfill.js` and `ssql-wasm.js` into `page.html`,
as `to explore -wasm` does (`cmd/ssql/wasm/`). Then run pipelines with
`executePipeline`.

- **For:** the page controls everything, and output can be laid out next
  to the answer.
- **Against:**
  - a second runner and a second output view;
  - CSP gains `wasm-unsafe-eval`, plus either the engine's origin or 6 MB
    served through Funnel;
  - the engine's version follows whichever copy is loaded. Served by the
    FAQ, it is the staged revision; from Pages, it is main.
- **Effort:** two to three days, mostly UI that the playground already has.

## 5. Should the browser do the verifying?

DFC169 §4.1 weighed this as option B and chose the server for the loop.
DFC171 adds a reason to look again: the jail is where the public
instance's risk lives, and the browser has no jail to escape.

**What it would take.** The loop would turn inside out. The server would
return a draft pipeline. The page would run it, in schema mode and then
for real, and post the error or the output back. The server would ask the
model again, up to the retry limit.

**Why not, today:**

- **Trust.** "Verified" would mean "the visitor's browser says so". For
  the visitor's own answer that is fine. But the log's status column,
  which `review.sh` and the eval comparisons read, would be whatever
  clients send. A modified page could put a crafted "output" in front of
  the model; that is prompt injection through the verifier, a new channel.
- **Latency and reach.** Every correction becomes a round trip, and the
  first answer waits for the 6 MB engine. A phone on a bad network gets a
  worse FAQ, or none.
- **A second semantics.** The jail runs real bash over the slim binary.
  The playground runs its own shim, which simulates pipes and process
  substitution in JavaScript (`ssql-ui.js`). That shim is another lane of
  "one semantics, many backends", and no gate compares it with the CLI.
  Today a shim difference only spoils a rerun. If the shim verified, it
  would decide what visitors are told is correct.
- **The eval stays on the server either way.** So the browser loop would
  be a second verifier to keep in step with the first.

**What would change the answer:** a jail escape found in the wild, or a
public instance that has to run without a jail, for example on a host
without user namespaces. Then the inside-out loop is the fallback. It
would be paired with a server-side schema-mode pre-flight, which runs no
user data, so the model still gets field errors from a trusted source.

## 6. Security

- **The engine runs in the visitor's browser,** on a pipeline the server
  has already validated (ssql stages only). The slim wasm has no
  `net/http` (DFC151), so a pipeline cannot reach the network. The files
  it sees are the ones the visitor gave it.
- **Chart HTML** renders in the playground's sandboxed frame (DFC169
  §12.16). Option B would need the same sandbox in the FAQ page.
- **What reaches the server** is the header and the rows the visitor was
  shown. The extraction must be visible and editable, not a silent
  sample.
- **A share link** never runs anything by itself. Opening one shows the
  answer; the visitor clicks Run.

## 7. Recommendation

1. **Do A** if visitors' own data is the case to serve. It is the one gain
   the server can never provide, and it reuses the playground nearly
   whole. It is about a day of work.
2. **Keep verification on the server** (§5). Revisit only if the jail
   stops being trustworthy, or stops being available.
3. **Don't do B** unless the inline playground turns out too heavy in an
   iframe, for example on phones. Try A first and look.

## Open questions

- **Picker or drop zone?** A drop zone on the paste box is the natural
  place, and the paste box would then show the extracted preview.
- **How many rows go to the server?** Today a visitor pastes what they
  like, up to 20 KB. The page could default to the header plus 20 rows,
  which the visitor can edit.
- **The size limit.** The playground has none for uploads today. Measure
  a 100 MB and a 500 MB file on a desktop and on a phone before choosing.
- **Should the FAQ ever serve the engine itself** (option B's variant),
  so the answer and the rerun use the same revision? Only if version skew
  between main and the staged FAQ shows up in practice. Today's links
  already run on main.
