// ssql-ui.js — the shared interactive layer for ssql WASM pages
// (browser playground AND `to explore -wasm` output). One implementation
// of: paren-aware help-at-cursor (Alt-h), Tab / as-you-type completion
// (commands, flags, operators, files, pipeline-aware field names and
// values), the passive suggestion popup, the pipeline parser (procsub
// aware) and executor.
//
// Page contract (all global, scripts share scope):
//   elements:  #pipeline (textarea), #completions (popup div), #status
//   functions: ssqlExec(args, stdin, env?) — the WASM bridge
//              showOutput(result, label)  — where help text renders
//   globals:   window.ssqlUIReady = true when the engine is up
//              window.SSQL_UI_READY_TEXT — status restore text (optional)
//              window.ssqlUIOnInput      — extra input hook (optional)

// --- Completion bindings ---
// All help/completion machinery lives in a FACTORY: a binding ties one
// input element to one executor. The default binding is the #pipeline
// textarea over the global ssqlExec (the WASM bridge). Pages can attach
// the same machinery to other inputs with other executors via
// window.ssqlUIBindCompletion — the explore server-head input uses an
// HTTP executor that POSTs the cursor protocol to /api/cursor (DFC108).
// Executors may be sync (wasm) or async (fetch): every call is awaited.

// --- shared pure helpers ---

// Values can contain spaces ("Peter Allworth") — quote on insert so the
// pipeline still parses as one argument.
function maybeQuote(cand) {
    return /\s/.test(cand) ? "'" + cand.replace(/'/g, "'\\''") + "'" : cand;
}

// The raw partial word being completed: the non-whitespace run ending at
// the caret (raw text, unlike the shell-split word — fine for the
// flag/subcommand/file candidates the engine returns). Stops at
// whitespace OR a pipe — mirrors the bash binding's ${line##*[ |]}.
function currentRawWord(ta) {
    const before = ta.value.slice(0, ta.selectionStart);
    return before.match(/([^\s|]*)$/)[1];
}

function insertCompletion(ta, replaceLen, text, addSpace) {
    const start = ta.selectionStart - replaceLen;
    ta.value = ta.value.slice(0, start) + text + (addSpace ? ' ' : '') + ta.value.slice(ta.selectionEnd);
    ta.selectionStart = ta.selectionEnd = start + text.length + (addSpace ? 1 : 0);
}

function longestCommonPrefix(list) {
    let p = list[0];
    for (const s of list) {
        while (!s.startsWith(p)) p = p.slice(0, -1);
    }
    return p;
}

// Caret pixel position via the mirror-div trick: clone the input's text
// metrics, render the text up to the caret plus a marker span, and read
// the marker's position.
function caretPixelPos(ta) {
    const div = document.createElement('div');
    const cs = getComputedStyle(ta);
    for (const p of ['fontFamily', 'fontSize', 'fontWeight', 'lineHeight', 'letterSpacing',
        'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
        'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth', 'boxSizing']) {
        div.style[p] = cs[p];
    }
    div.style.position = 'absolute';
    div.style.visibility = 'hidden';
    div.style.whiteSpace = 'pre-wrap';
    div.style.wordWrap = 'break-word';
    div.style.width = ta.clientWidth + 'px';
    div.style.left = ta.offsetLeft + 'px'; // overlay the input exactly
    div.style.top = ta.offsetTop + 'px';
    div.textContent = ta.value.slice(0, ta.selectionStart);
    const marker = document.createElement('span');
    marker.textContent = '​';
    div.appendChild(marker);
    ta.parentElement.appendChild(div);
    const pos = {
        left: div.offsetLeft + marker.offsetLeft,
        top: div.offsetTop + marker.offsetTop + marker.offsetHeight - ta.scrollTop,
    };
    div.remove();
    return pos;
}

// Completion hints in the status line are TRANSIENT: they fade back to
// "Ready" after a few seconds and clear as soon as you keep typing —
// a hint set once used to sit there forever.
let statusRestoreTimer = null;
function setTransientStatus(msg) {
    const st = document.getElementById('status');
    st.textContent = msg;
    st.dataset.transient = '1';
    if (statusRestoreTimer) clearTimeout(statusRestoreTimer);
    statusRestoreTimer = setTimeout(clearTransientStatus, 4000);
}
function clearTransientStatus() {
    const st = document.getElementById('status');
    if (st.dataset.transient) {
        st.textContent = window.SSQL_UI_READY_TEXT || 'Ready';
        delete st.dataset.transient;
    }
}

// --- the factory ---
// opts: { input:  the textarea/input element (required)
//         exec:   (args, stdin, env?) -> result | Promise<result>
//                 where result = {stdout, stderr, exitCode} (required)
//         ready:  () => bool (default: window.ssqlUIReady)
//         schemaFields: async (before) => fields|null — pipeline-aware
//                 field-name provider (Ctrl-O). Omit when the executor
//                 can't run schema-mode pipelines (e.g. the HTTP head
//                 binding); field slots then fall back to a status hint.
//         bigValueSource: (file) => bool — skip as-you-type value
//                 sampling for large sources (default: vFS size check;
//                 the HTTP binding uses () => false — sampling happens
//                 server-side and is capped there). }
function createCompletionBinding(opts) {
    const ta = opts.input;
    const exec = opts.exec;
    const ready = opts.ready || (() => window.ssqlUIReady);
    const schemaFields = opts.schemaFields || null;
    const bigValueSource = opts.bigValueSource ||
        (f => (typeof _fsReadFileBytes === 'function' ? (_fsReadFileBytes(f) || []).length : 0) > (1 << 20));

    let compState = null; // { cands, sel, passive, prefix } while the popup is open
    // Env accumulated from completion directives — the equivalent of the
    // bash script's `export AUTOCLI_CACHE_FILE=…`. Completing a data-file
    // path caches it here, enabling downstream VALUE completion.
    let completionEnv = {};
    let autoSuggestTimer = null;
    let suppressWordStart = -1; // Escape'd word start; cleared on word change

    // cursorContext: the shared caret math for help + Tab completion —
    // COMP_WORDS-style word index, ported from the bash bindings.
    async function cursorContext() {
        if (!ready()) return null;
        const before = ta.value.slice(0, ta.selectionStart);
        if (!before.trim()) {
            return { before, words: ['ssql', ''], args: [''], pos: 1, prefixNeeded: true };
        }
        const stage = (await exec(['-cursor-stage', before], '')).stdout;
        if (/^ssql(\s|$)/.test(stage)) {
            const words = shellSplit(stage); // words[0] === 'ssql'
            const args = words.slice(1);
            let pos;
            if (/\s$/.test(stage)) { // caret sits on a new empty word
                pos = words.length;
                args.push('');
            } else {
                pos = words.length - 1;
            }
            return { before, words, args, pos };
        }
        // Stage-start boilerplate: every stage begins "ssql ", so a bare
        // stage ("| ") or a single partial word ("| fr") is treated as if
        // "ssql " were already typed.
        const t = stage.trim();
        if ((t === '' || /^[a-z][a-z-]*$/.test(t)) && !/\s\S/.test(stage)) {
            return { before, words: ['ssql', t], args: [t], pos: 1, prefixNeeded: true };
        }
        return null;
    }

    async function helpAtCursor() {
        if (!ready()) return;
        const ctx = await cursorContext();
        if (!ctx) {
            showOutput({ stdout: '', stderr: 'Put the cursor on an ssql command or flag, then ask for help (Alt-h).', exitCode: 1 }, 'Help');
            return;
        }
        const res = await exec(['-help-at', String(ctx.pos), ...ctx.args], '');
        if (res.exitCode !== 0 || !res.stdout.trim()) {
            showOutput({ stdout: '', stderr: res.stderr || 'No help available here.', exitCode: 1 }, 'Help');
            return;
        }
        showOutput(res, 'Help — ' + (ctx.words[ctx.pos] || ctx.words[ctx.words.length - 1] || 'ssql'));
    }

    // valueSourceFile: the data file feeding the cursor position, derived
    // from the PIPELINE (paren-aware) — one Go implementation,
    // commands.ValueSourceFile via the -value-source protocol flag.
    async function valueSourceFile(before) {
        return (await exec(['-value-source', before], '')).stdout.trim() || null;
    }

    async function completionCandidates(ctx, cheapOnly) {
        const env = Object.assign({}, completionEnv);
        const derived = await valueSourceFile(ctx.before);
        // cheapOnly (the as-you-type path): skip value sampling for large
        // sources — the engine reads up to 10k records per query, which
        // would stutter typing. Tab still samples unconditionally.
        if (derived && !(cheapOnly && bigValueSource(derived))) {
            env.AUTOCLI_CACHE_FILE = derived;
        }
        const out = (await exec(['-complete', String(ctx.pos), ...ctx.args], '', env)).stdout;
        const cands = [], hints = [];
        for (const l of out.split('\n').map(x => x.trim()).filter(Boolean)) {
            // {"type":...} lines are protocol directives, not candidates.
            if (/^\{.*\}$/.test(l)) {
                try {
                    const d = JSON.parse(l);
                    if (d.type === 'field_cache' && d.filepath) {
                        completionEnv.AUTOCLI_CACHE_FILE = d.filepath;
                    } else if (d.type === 'env' && d.key) {
                        completionEnv[d.key] = d.value || '';
                    }
                } catch (e) { /* not a directive — ignore the line */ }
                continue;
            }
            if (/^<.*>$/.test(l) || l === 'Use-Ctrl-O' || l === 'Values-Use-Ctrl-O') hints.push(l);
            else cands.push(l);
        }
        return { cands, hints };
    }

    function showCompletions(cands, prefix) {
        // passive until the user arrows into the list: Enter stays inert,
        // typing keeps filtering; Tab or click accepts.
        compState = { cands, sel: 0, passive: true, prefix: prefix || '' };
        const el = document.getElementById('completions');
        el.innerHTML = '';
        cands.forEach((c, i) => {
            const d = document.createElement('div');
            d.textContent = c;
            if (i === 0) d.className = 'sel';
            // mousedown, not click: click fires after the input blurs and
            // the caret context is gone.
            d.addEventListener('mousedown', e => { e.preventDefault(); acceptCompletion(i); });
            el.appendChild(d);
        });
        const pos = caretPixelPos(ta);
        el.style.left = Math.max(0, Math.min(pos.left, ta.offsetLeft + ta.clientWidth - 180)) + 'px';
        el.style.top = (pos.top + 2) + 'px';
        el.style.display = 'block';
    }

    function hideCompletions() {
        compState = null;
        document.getElementById('completions').style.display = 'none';
    }

    function moveCompletionSel(delta) {
        if (!compState) return;
        compState.passive = false;
        const n = compState.cands.length;
        compState.sel = (compState.sel + delta + n) % n;
        const el = document.getElementById('completions');
        [...el.children].forEach((d, i) => d.className = i === compState.sel ? 'sel' : '');
        el.children[compState.sel].scrollIntoView({ block: 'nearest' });
    }

    function acceptCompletion(idx) {
        if (!compState) return;
        const cand = compState.cands[idx ?? compState.sel];
        const isDir = cand.endsWith('/');
        insertCompletion(ta, currentRawWord(ta).length, compState.prefix + maybeQuote(cand), !isDir);
        hideCompletions();
        ta.focus();
    }

    async function fieldCompleteAtCursor() {
        const before = ta.value.slice(0, ta.selectionStart);
        if (!before.trim()) return;
        const fields = schemaFields ? await schemaFields(before) : null;
        if (!fields || !fields.length) {
            setTransientStatus('No upstream schema found for field completion');
            return;
        }
        const cur = currentRawWord(ta);
        const matches = fields.filter(f => f.startsWith(cur));
        if (!matches.length) {
            setTransientStatus('No field matches ' + JSON.stringify(cur) + ' (fields: ' + fields.join(', ') + ')');
            return;
        }
        if (matches.length === 1) {
            insertCompletion(ta, cur.length, maybeQuote(matches[0]), true);
            return;
        }
        const lcp = longestCommonPrefix(matches);
        if (lcp.length > cur.length) insertCompletion(ta, cur.length, lcp, false);
        showCompletions(matches);
    }

    // pendingPrefix: the boilerplate to insert at a prefixless stage
    // start — with a leading space when the caret sits after "|".
    function currentWordStart() {
        return ta.selectionStart - currentRawWord(ta).length;
    }
    function pendingPrefix(needed) {
        if (!needed) return '';
        const i = currentWordStart();
        return (i > 0 && ta.value[i - 1] === '|') ? ' ssql ' : 'ssql ';
    }

    async function completeAtCursor() {
        const ctx = await cursorContext();
        if (!ctx) return;
        if (ctx.pos === 0) return; // caret on the program word itself
        const { cands, hints } = await completionCandidates(ctx);
        if (cands.length === 0) {
            // A field slot: the engine can't see across pipes, but a
            // schema-capable binding can.
            if (hints.includes('Use-Ctrl-O') || hints.includes('<FIELD>')) {
                if (schemaFields) { await fieldCompleteAtCursor(); }
                else setTransientStatus('A field name goes here (type it — this input has no upstream schema view)');
                return;
            }
            if (hints.includes('Values-Use-Ctrl-O')) {
                setTransientStatus('A field value goes here, but no source file was found to sample values from');
            } else if (hints.length) {
                const friendly = {
                    '<name>': 'a new field name goes here — your choice, just type it',
                    '<expression>': 'an expression goes here — press "? Help" (Alt-h) for the function reference',
                    '<init-expr>': 'an init expression, e.g. {s:0} — see "? Help" (Alt-h)',
                    '<every-expr>': 'a per-record expression, e.g. {s:s+salary} — see "? Help" (Alt-h)',
                    '<final-expr>': 'a final expression over the state, e.g. s — see "? Help" (Alt-h)',
                    '<FILE>': 'a file path goes here',
                };
                setTransientStatus(hints.map(h => friendly[h] || ('expected here: ' + h)).join(' · '));
            }
            return;
        }
        const stagePrefix = pendingPrefix(ctx.prefixNeeded);
        if (cands.length === 1) {
            insertCompletion(ta, currentRawWord(ta).length, stagePrefix + maybeQuote(cands[0]), !cands[0].endsWith('/'));
            return;
        }
        // Bash-style: extend to the longest common prefix, then show the
        // list (skipped when the "ssql " prefix is pending).
        const cur = currentRawWord(ta);
        if (!stagePrefix) {
            const lcp = longestCommonPrefix(cands);
            if (lcp.length > cur.length) insertCompletion(ta, cur.length, lcp, false);
        }
        showCompletions(cands, stagePrefix);
    }

    // --- As-you-type completion ---
    // Debounced passive popup; Escape suppresses per word-start. Async
    // executors add a staleness guard: if the caret or text moved while
    // a request was in flight, the answer is dropped, not shown.
    async function autoSuggest() {
        if (!ready() || document.activeElement !== ta) return;
        const cur = currentRawWord(ta);
        const passiveOpen = compState && compState.passive;
        if (currentWordStart() === suppressWordStart) return;
        const snapshot = ta.value + ' ' + ta.selectionStart;
        const ctx = await cursorContext();
        if (!ctx) { if (passiveOpen) hideCompletions(); return; }
        if (ctx.pos === 0) { if (passiveOpen) hideCompletions(); return; }
        let { cands, hints } = await completionCandidates(ctx, true);
        if (!cands.length && schemaFields && (hints.includes('Use-Ctrl-O') || hints.includes('<FIELD>'))) {
            const fields = await schemaFields(ctx.before);
            if (fields) cands = fields.filter(f => f.startsWith(cur));
        }
        if (ta.value + ' ' + ta.selectionStart !== snapshot) return; // stale
        if (!cands.length) { if (compState && compState.passive) hideCompletions(); return; }
        showCompletions(cands, pendingPrefix(ctx.prefixNeeded));
    }

    ta.addEventListener('input', () => {
        if (window.ssqlUIOnInput) window.ssqlUIOnInput();
        clearTransientStatus();
        if (autoSuggestTimer) clearTimeout(autoSuggestTimer);
        autoSuggestTimer = setTimeout(autoSuggest, 150);
    });

    ta.addEventListener('keydown', e => {
        if (e.altKey && !e.ctrlKey && !e.metaKey && (e.key === 'h' || e.key === 'H')) {
            e.preventDefault();
            helpAtCursor();
            return;
        }
        if (compState) {
            switch (e.key) {
                case 'ArrowDown': e.preventDefault(); moveCompletionSel(1); return;
                case 'ArrowUp': e.preventDefault(); moveCompletionSel(-1); return;
                case 'Tab':
                    e.preventDefault();
                    if (compState.passive) { acceptCompletion(); } else { moveCompletionSel(e.shiftKey ? -1 : 1); }
                    return;
                case 'Enter':
                    // Passive popup must never steal Enter — close it and
                    // let the key do its thing (newline, or the head
                    // input's run-on-Enter).
                    if (compState.passive) { hideCompletions(); return; }
                    e.preventDefault(); acceptCompletion(); return;
                case 'Escape':
                    e.preventDefault();
                    suppressWordStart = currentWordStart();
                    hideCompletions();
                    return;
                default: hideCompletions(); // fall through: let the key type
            }
            return;
        }
        if (e.key === 'Tab' && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey) {
            e.preventDefault();
            completeAtCursor();
            return;
        }
        // Ctrl-O: explicit field-name completion, CLI parity.
        if (e.ctrlKey && !e.metaKey && !e.altKey && (e.key === 'o' || e.key === 'O')) {
            e.preventDefault();
            fieldCompleteAtCursor();
        }
    });
    ta.addEventListener('blur', () => setTimeout(hideCompletions, 150));

    return { helpAtCursor, completeAtCursor, fieldCompleteAtCursor, hideCompletions, cursorContext };
}
window.ssqlUIBindCompletion = createCompletionBinding;

// --- Pipeline-aware field-name completion (the CLI's Ctrl-O) ---
// -complete-source picks the command whose schema feeds the cursor;
// that upstream runs through the pipeline simulator under
// SSQL_MODE=schema and `generate schema` prints the field names. WASM
// only — the default binding's schemaFields provider.

const schemaFieldsCache = new Map(); // upstream text -> field names

// The cache is keyed by upstream TEXT, so anything that rewrites a vFS
// file under the same name (a server-head run replacing data.jsonl, a
// re-upload, a bar run that tees a file) must clear it — the text is
// unchanged but the schema behind it may not be.
window.ssqlUISchemaCacheClear = () => schemaFieldsCache.clear();

function schemaFieldsAtCursor(before) {
    const src = ssqlExec(['-complete-source', before], '').stdout.trim();
    if (!src) return null;
    if (schemaFieldsCache.has(src)) return schemaFieldsCache.get(src);
    try {
        const env = { SSQL_MODE: 'schema' };
        const stages = parsePipeline(src, true, false, env);
        let data = '';
        for (const args of stages) {
            const res = ssqlExec(args, data, env);
            if (res.exitCode !== 0) return null;
            data = res.stdout;
        }
        const g = ssqlExec(['generate', 'schema'], data, null);
        if (g.exitCode !== 0) return null;
        const fields = g.stdout.split('\n').map(x => x.trim()).filter(Boolean);
        if (schemaFieldsCache.size > 30) schemaFieldsCache.clear();
        schemaFieldsCache.set(src, fields);
        return fields;
    } catch (e) {
        return null;
    }
}

// --- The default binding: #pipeline over the WASM bridge ---
// Created at load; the delegating globals keep the page contract that
// predates the factory (pages and harnesses call helpAtCursor() etc.).
const ssqlUIPipelineBinding = createCompletionBinding({
    input: document.getElementById('pipeline'),
    exec: (args, stdin, env) => ssqlExec(args, stdin, env),
    schemaFields: (before) => schemaFieldsAtCursor(before),
});
function helpAtCursor() { return ssqlUIPipelineBinding.helpAtCursor(); }
function completeAtCursor() { return ssqlUIPipelineBinding.completeAtCursor(); }
function fieldCompleteAtCursor() { return ssqlUIPipelineBinding.fieldCompleteAtCursor(); }
function hideCompletions() { return ssqlUIPipelineBinding.hideCompletions(); }



// --- Pipeline parser ---

// Parse a pipeline string into an array of {args: string[], stdin: string|null} stages.
// Handles <(...) process substitution by executing inner pipelines first.
function parsePipeline(pipelineStr, isTopLevel, ssqlgoMode, env) {
    // Strip bash-style comments: # to end of line (but not inside quotes)
    pipelineStr = stripComments(pipelineStr);

    // First, find and resolve <(...) blocks
    let resolved = resolveProcessSubstitutions(pipelineStr, isTopLevel, ssqlgoMode, env);

    // Split on " | ssql " (but not inside quotes)
    // Simple approach: split on /\s*\|\s*ssql\s+/
    let parts = resolved.split(/\s*\|\s*ssql\s+/);

    // First part starts with "ssql " — strip it
    if (parts[0].trim().startsWith('ssql ')) {
        parts[0] = parts[0].trim().substring(5);
    }

    return parts.map(p => shellSplit(p.trim()));
}

// Split a string into tokens respecting single and double quotes (shell-style).
// Strip # comments (to end of line), respecting quotes.
function stripComments(s) {
    let result = '';
    let quote = null;
    for (let i = 0; i < s.length; i++) {
        const c = s[i];
        if (quote) {
            result += c;
            if (c === quote) quote = null;
        } else if (c === '"' || c === "'") {
            result += c;
            quote = c;
        } else if (c === '#') {
            // Skip to end of line
            while (i < s.length && s[i] !== '\n') i++;
            result += '\n';
        } else {
            result += c;
        }
    }
    return result;
}

function shellSplit(s) {
    const tokens = [];
    let current = '';
    let quote = null;
    for (let i = 0; i < s.length; i++) {
        const c = s[i];
        if (quote) {
            if (c === quote) {
                quote = null;
            } else {
                current += c;
            }
        } else if (c === '"' || c === "'") {
            quote = c;
        } else if (/\s/.test(c)) {
            if (current) { tokens.push(current); current = ''; }
        } else {
            current += c;
        }
    }
    if (current) tokens.push(current);
    return tokens;
}

// Map of temp file paths to original <(...) expressions, for restoring in output.
let procSubMap = {};

// Resolve <(...) by executing the inner pipeline and writing result to a temp file.
// Returns the pipeline string with <(...) replaced by temp file paths.
function resolveProcessSubstitutions(str, isTopLevel, ssqlgoMode, env) {
    let result = str;
    let procSubId = 0;
    if (isTopLevel !== false) procSubMap = {};

    // Find <(...) blocks — handle nesting by finding matching parens
    while (true) {
        let start = result.indexOf('<(');
        if (start === -1) break;

        // Find matching closing paren
        let depth = 0;
        let end = start + 1;
        for (; end < result.length; end++) {
            if (result[end] === '(') depth++;
            if (result[end] === ')') {
                depth--;
                if (depth === 0) break;
            }
        }

        let innerPipeline = result.substring(start + 2, end);
        let originalExpr = result.substring(start, end + 1); // "<(...)"
        // Use /dev/fd/ path so Go's os.Stat sees it as non-regular (like real process substitution)
        let tempFile = `/dev/fd/${100 + procSubId++}`;
        procSubMap[tempFile] = originalExpr;

        // Execute inner pipeline (not top-level — don't reset procSubMap)
        let stages = parsePipeline(innerPipeline, false, ssqlgoMode, env);
        let data = '';
        for (const args of stages) {
            let execArgs = args;
            // In SSQLGO mode, add -generate to inner pipeline commands
            // so they produce fragments (not data) for the join's func fragment
            if (ssqlgoMode && !execArgs.includes('-generate') && !execArgs.includes('-g')) {
                execArgs = [...execArgs, '-generate'];
            }
            let res = ssqlExec(execArgs, data, env);
            if (res.exitCode !== 0) {
                throw new Error(`Process substitution failed: ${res.stderr}`);
            }
            data = res.stdout;
        }

        // Write result to virtual file
        _fsWriteFile(tempFile, data);

        // Replace <(...) with temp file path
        result = result.substring(0, start) + tempFile + result.substring(end + 1);
    }

    return result;
}

// --- Execution ---

function executePipeline(pipelineStr, ssqlgoMode, env) {
    if (!window.ssqlUIReady) return { stdout: '', stderr: 'engine not ready', exitCode: 1 };

    // Set SSQLGO env if needed
    if (ssqlgoMode) {
        // Go WASM reads env via go.env — but we can't set it after init.
        // Instead, add -generate flag to each command except the last.
        // Actually, for the fragment pipeline, we need SSQLGO=1 behavior.
        // The ssql CLI checks os.Getenv("SSQLGO") in the shouldGenerate() helper.
        // For WASM, we'll set it via the Go process env.
        // go.env is set before go.run() — too late to change.
        // Alternative: use -generate flag explicitly.
    }

    try {
        let stages = parsePipeline(pipelineStr, true, ssqlgoMode, env);
        let data = '';

        for (let i = 0; i < stages.length; i++) {
            let args = stages[i];

            // In SSQLGO mode, add -generate to data commands (not to generate subcommand)
            if (ssqlgoMode && i < stages.length - 1) {
                // Don't add -generate if args already has it, or if this is a "generate" command
                if (args[0] !== 'generate' && !args.includes('-generate') && !args.includes('-g')) {
                    args = [...args, '-generate'];
                }
            }

            let res = ssqlExec(args, data, env);
            if (res.exitCode !== 0) {
                return res;
            }
            data = res.stdout;
        }

        return { stdout: data, stderr: '', exitCode: 0 };
    } catch (e) {
        return { stdout: '', stderr: e.message, exitCode: 1 };
    }
}



// --- Files: uploads in, downloads out (shared) ---
function ssqlUIEscapeHtml(s) {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// --- Download files a run created in the virtual FS ---
// The polyfill logs every written path; after a run, list the
// user-visible ones with download buttons (Blob-based, binary-safe).

function hideFilesBar() {
    const bar = document.getElementById('filesBar');
    if (bar) bar.style.display = 'none';
}

function downloadVFSFile(name) {
    const bytes = _fsReadFileBytes(name);
    if (!bytes) return;
    const url = URL.createObjectURL(new Blob([bytes]));
    const a = document.createElement('a');
    a.href = url;
    a.download = name.split('/').pop();
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 5000);
}

function showCreatedFiles() {
    if (!document.getElementById('filesBar')) return;
    const names = _fsWriteLog().filter(p =>
        !p.startsWith('tmp/') && !p.startsWith('/tmp/') &&
        !p.startsWith('dev/fd/') && !p.startsWith('/dev/fd/'));
    const bar = document.getElementById('filesBar');
    if (!names.length) { bar.style.display = 'none'; return; }
    bar.innerHTML = 'Files created — click to download: ';
    for (const n of names) {
        const btn = document.createElement('button');
        const bytes = _fsReadFileBytes(n);
        btn.innerHTML = '<span class="fname">' + ssqlUIEscapeHtml(n) + '</span> ⬇ ' +
            (bytes ? (bytes.length >= 1024 ? (bytes.length / 1024).toFixed(1) + ' KB' : bytes.length + ' B') : '');
        btn.addEventListener('click', () => downloadVFSFile(n));
        bar.appendChild(btn);
    }
    // 'block', not '': the element is hidden by a STYLESHEET rule, so
    // clearing the inline style falls back to display:none and the bar
    // never appears (shipped bug — the harness checked the inline style).
    bar.style.display = 'block';
}


// ssqlUIWriteUpload: put an uploaded file into the virtual FS so
// pipelines can read it by name (`from name.csv`, `join name.csv`, …).
// Clears the schema cache (new data = new fields) and confirms in the
// status line. Pages hook window.ssqlUIOnUpload for their own refresh.
function ssqlUIWriteUpload(name, contents) {
    window.ssqlUISchemaCacheClear();
    _fsWriteFile(name, contents);
    schemaFieldsCache.clear();
    setTransientStatus('Uploaded ' + name + ' — use it in pipelines by name');
    if (window.ssqlUIOnUpload) window.ssqlUIOnUpload(name);
}
