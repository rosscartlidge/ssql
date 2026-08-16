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

// --- Help at cursor ---
// The CLI's Alt-h binding, in the browser: -cursor-stage extracts the
// paren-aware pipeline stage at the caret, -help-at renders autocli help
// for the word the caret sits on (plus the expression-function reference
// on expression args). Same Go code as the terminal keybinding.

// cursorContext: the shared caret math for help + Tab completion. Extracts
// the paren-aware stage at the caret via -cursor-stage, splits it, and
// computes the COMP_WORDS-style word index (program name at 0; trailing
// whitespace = caret on a new empty word) — ported from the bash bindings.
// Returns null when the caret isn't on an ssql stage.
function cursorContext() {
    if (!window.ssqlUIReady) return null;
    const ta = document.getElementById('pipeline');
    const before = ta.value.slice(0, ta.selectionStart);
    // An empty (or all-whitespace) line IS a stage start: offer the
    // subcommand list with the "ssql " prefix pending.
    if (!before.trim()) {
        return { ta, before, words: ['ssql', ''], args: [''], pos: 1, prefixNeeded: true };
    }
    const stage = ssqlExec(['-cursor-stage', before], '').stdout;
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
        return { ta, before, words, args, pos };
    }
    // Stage-start boilerplate: every stage begins "ssql ", so a bare
    // stage ("| ") or a single partial word ("| fr") is treated as if
    // "ssql " were already typed — completion offers subcommands and
    // ACCEPTANCE inserts the prefix (ctx.prefixNeeded → compState.prefix).
    const t = stage.trim();
    if ((t === '' || /^[a-z][a-z-]*$/.test(t)) && !/\s\S/.test(stage)) {
        return { ta, before, words: ['ssql', t], args: [t], pos: 1, prefixNeeded: true };
    }
    return null;
}

function helpAtCursor() {
    if (!window.ssqlUIReady) return;
    const ctx = cursorContext();
    if (!ctx) {
        showOutput({ stdout: '', stderr: 'Put the cursor on an ssql command or flag, then ask for help (Alt-h).', exitCode: 1 }, 'Help');
        return;
    }
    const res = ssqlExec(['-help-at', String(ctx.pos), ...ctx.args], '');
    if (res.exitCode !== 0 || !res.stdout.trim()) {
        showOutput({ stdout: '', stderr: res.stderr || 'No help available here.', exitCode: 1 }, 'Help');
        return;
    }
    showOutput(res, 'Help — ' + (ctx.words[ctx.pos] || ctx.words[ctx.words.length - 1] || 'ssql'));
}

// --- Tab completion ---
// Tab calls the CLI's own completion engine (`-complete N words...`, the
// same protocol bash completion uses) through the WASM bridge. File
// completion lists the virtual FS, so the sample datasets complete too.
// Angle-bracket entries (<VALUE>, <FIELD>) and Use-Ctrl-O are display
// hints from the engine, not insertable candidates.

let compState = null; // { cands, sel } while the popup is open

// Env accumulated from completion directives — the playground's
// equivalent of the bash script's `export AUTOCLI_CACHE_FILE=…`.
// Completing a data-file path caches it here, which enables downstream
// VALUE completion (`where -if country eq <Tab>` → real values).
let completionEnv = {};

// valueSourceFile: the data file feeding the cursor position, derived
// from the PIPELINE rather than the tab-completion cache. Bash needs the
// cache because its completion can't see across pipes; this (and the
// CLI's Ctrl-O value phase) can. One Go implementation —
// commands.ValueSourceFile via the -value-source protocol flag.
function valueSourceFile(before) {
    return ssqlExec(['-value-source', before], '').stdout.trim() || null;
}

function completionCandidates(ctx, cheapOnly) {
    const env = Object.assign({}, completionEnv);
    const derived = valueSourceFile(ctx.before);
    // cheapOnly (the as-you-type path): skip value sampling for large
    // sources — the engine reads up to 10k records per query, which
    // would stutter typing. Tab still samples unconditionally.
    if (derived) {
        const big = cheapOnly && (_fsReadFileBytes(derived) || []).length > (1 << 20);
        if (!big) env.AUTOCLI_CACHE_FILE = derived;
    }
    const out = ssqlExec(['-complete', String(ctx.pos), ...ctx.args], '', env).stdout;
    const cands = [], hints = [];
    for (const l of out.split('\n').map(s => s.trim()).filter(Boolean)) {
        // {"type":...} lines are protocol directives, not candidates —
        // the bash completion script consumes them the same way.
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

// Values can contain spaces ("Peter Allworth") — quote on insert so the
// pipeline still parses as one argument.
function maybeQuote(cand) {
    return /\s/.test(cand) ? "'" + cand.replace(/'/g, "'\\''") + "'" : cand;
}

// The raw partial word being completed: the non-whitespace run ending at
// the caret (raw text, unlike the shell-split word — fine for the
// flag/subcommand/file candidates the engine returns).
function currentRawWord(ta) {
    const before = ta.value.slice(0, ta.selectionStart);
    // Stop at whitespace OR a pipe — mirrors the bash binding's
    // ${line##*[ |]}. Without the pipe, the "word" after a bare "|" was
    // the pipe itself, and acceptance ate it.
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

// Caret pixel position via the mirror-div trick: clone the textarea's text
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
    div.style.left = ta.offsetLeft + 'px'; // overlay the textarea exactly
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

function showCompletions(ta, cands, prefix) {
    // passive until the user arrows into the list: Enter stays a newline,
    // typing keeps filtering; Tab or click accepts.
    compState = { cands, sel: 0, passive: true, prefix: prefix || '' };
    const el = document.getElementById('completions');
    el.innerHTML = '';
    cands.forEach((c, i) => {
        const d = document.createElement('div');
        d.textContent = c;
        if (i === 0) d.className = 'sel';
        // mousedown, not click: click fires after the textarea blurs and
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
    const ta = document.getElementById('pipeline');
    const cand = compState.cands[idx ?? compState.sel];
    const isDir = cand.endsWith('/');
    insertCompletion(ta, currentRawWord(ta).length, compState.prefix + maybeQuote(cand), !isDir);
    hideCompletions();
    ta.focus();
}

// --- Pipeline-aware field-name completion (the CLI's Ctrl-O) ---
// -complete-source picks the command whose schema feeds the cursor
// (paren-aware: procsub interiors, join right-side fields). That upstream
// runs through the pipeline simulator under SSQL_MODE=schema — commands
// transform a schema header instead of data, so only file headers are
// read — and `generate schema` prints the resulting field names.
// Unlike the terminal (where bash scopes Tab to one command), the
// playground sees the whole pipeline, so Tab triggers this automatically
// whenever the engine answers a field slot with the Use-Ctrl-O hint.

const schemaFieldsCache = new Map(); // upstream text -> field names

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
        const fields = g.stdout.split('\n').map(s => s.trim()).filter(Boolean);
        if (schemaFieldsCache.size > 30) schemaFieldsCache.clear();
        schemaFieldsCache.set(src, fields);
        return fields;
    } catch (e) {
        return null;
    }
}

function fieldCompleteAtCursor() {
    const ta = document.getElementById('pipeline');
    const before = ta.value.slice(0, ta.selectionStart);
    if (!before.trim()) return;
    const fields = schemaFieldsAtCursor(before);
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
    showCompletions(ta, matches);
}

function completeAtCursor() {
    const ta = document.getElementById('pipeline');
    const ctx = cursorContext();
    if (!ctx) return;
    if (ctx.pos === 0) return; // caret on the program word itself
    const { cands, hints } = completionCandidates(ctx);
    if (cands.length === 0) {
        // A field slot: the engine can't see across pipes, but we can —
        // complete from the live upstream schema.
        if (hints.includes('Use-Ctrl-O') || hints.includes('<FIELD>')) {
            fieldCompleteAtCursor();
            return;
        }
        if (hints.includes('Values-Use-Ctrl-O')) {
            setTransientStatus('A field value goes here, but no source file was found to sample values from');
        } else if (hints.length) {
            // Engine hints are honest but terse — translate the common
            // ones ("this is a slot you fill in, nothing to complete").
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
    const stagePrefix = pendingPrefix(ta, ctx.prefixNeeded);
    if (cands.length === 1) {
        insertCompletion(ta, currentRawWord(ta).length, stagePrefix + maybeQuote(cands[0]), !cands[0].endsWith('/'));
        return;
    }
    // Bash-style: extend to the longest common prefix, then show the list
    // (skipped when the "ssql " prefix is pending — extending an
    // unprefixed partial would leave invalid text).
    const cur = currentRawWord(ta);
    if (!stagePrefix) {
        const lcp = longestCommonPrefix(cands);
        if (lcp.length > cur.length) insertCompletion(ta, cur.length, lcp, false);
    }
    showCompletions(ta, cands, stagePrefix);
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

// --- As-you-type completion ---
// A debounced input listener pops the same completion popup PASSIVELY
// while typing (>=1 char of the current word): typing keeps filtering,
// Tab or click accepts, Enter stays a newline until the user arrows into
// the list, Escape suppresses suggestions for the current word. Value
// sampling auto-triggers only for small sources (Tab always may).

let autoSuggestTimer = null;
let suppressWordStart = -1; // Escape'd word start; cleared on word change

function currentWordStart(ta) {
    return ta.selectionStart - currentRawWord(ta).length;
}

// pendingPrefix: the boilerplate to insert at a prefixless stage start —
// with a leading space when the caret sits directly after "|".
function pendingPrefix(ta, needed) {
    if (!needed) return '';
    const i = currentWordStart(ta);
    return (i > 0 && ta.value[i - 1] === '|') ? ' ssql ' : 'ssql ';
}

function autoSuggest() {
    const ta = document.getElementById('pipeline');
    if (!window.ssqlUIReady || document.activeElement !== ta) return;
    const cur = currentRawWord(ta);
    const passiveOpen = compState && compState.passive;
    if (currentWordStart(ta) === suppressWordStart) return;
    const ctx = cursorContext();
    if (!ctx) { if (passiveOpen) hideCompletions(); return; }
    // The caret on the program word itself ("ssql|") is position 0 —
    // bash never completes it, and accepting would REPLACE "ssql" with a
    // subcommand. Stay quiet until after the space.
    if (ctx.pos === 0) { if (passiveOpen) hideCompletions(); return; }
    // Empty current word: suggestions pop here too — the popup is
    // PASSIVE (never steals Enter or typing), so showing what fits at
    // every position is pure discovery. This is also the only way to
    // summon a list on mobile, where there is no Tab key: after
    // "from csv " the file list just appears; tap to accept.

    let { cands, hints } = completionCandidates(ctx, true);
    if (!cands.length && (hints.includes('Use-Ctrl-O') || hints.includes('<FIELD>'))) {
        const fields = schemaFieldsAtCursor(ctx.before);
        if (fields) cands = fields.filter(f => f.startsWith(cur));
    }
    if (!cands.length) { if (compState && compState.passive) hideCompletions(); return; }
    showCompletions(ta, cands, pendingPrefix(ta, ctx.prefixNeeded));
}

document.getElementById('pipeline').addEventListener('input', () => {
    if (window.ssqlUIOnInput) window.ssqlUIOnInput();
    clearTransientStatus();
    if (autoSuggestTimer) clearTimeout(autoSuggestTimer);
    autoSuggestTimer = setTimeout(autoSuggest, 150);
});

document.getElementById('pipeline').addEventListener('keydown', e => {
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
                // Passive popup: Tab accepts the highlighted candidate
                // (bash-like). After arrow-activation, Tab cycles.
                if (compState.passive) { acceptCompletion(); } else { moveCompletionSel(e.shiftKey ? -1 : 1); }
                return;
            case 'Enter':
                // Passive popup must never steal Enter — close it and let
                // the newline happen. Accept only after arrow-activation.
                if (compState.passive) { hideCompletions(); return; }
                e.preventDefault(); acceptCompletion(); return;
            case 'Escape':
                e.preventDefault();
                suppressWordStart = currentWordStart(document.getElementById('pipeline'));
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
    // Ctrl-O: explicit field-name completion, CLI parity. preventDefault is
    // load-bearing — the browser default is "open file".
    if (e.ctrlKey && !e.metaKey && !e.altKey && (e.key === 'o' || e.key === 'O')) {
        e.preventDefault();
        fieldCompleteAtCursor();
    }
});
document.getElementById('pipeline').addEventListener('blur', () => setTimeout(hideCompletions, 150));


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
    _fsWriteFile(name, contents);
    schemaFieldsCache.clear();
    setTransientStatus('Uploaded ' + name + ' — use it in pipelines by name');
    if (window.ssqlUIOnUpload) window.ssqlUIOnUpload(name);
}
