// ssql-ui-viz.js — THE renderer for display sinks (DFC119 Phase C:
// one renderer, both worlds). The explore workspace panel and the
// standalone artifacts (`ssql … | ssql to animate out.html`) embed
// this same module, so a visualization looks and behaves identically
// live and in the artifact Copy CLI hands out — one semantics,
// applied to pixels.
//
// API: ssqlViz.render(gd, kind, cfg, rows, opts) → null | errorString.
//   gd   — the Plotly graph div
//   kind — sink key ('animate'; 'chart' pending the Chart.js→Plotly
//          artifact migration)
//   cfg  — FLAG-KEYED config, exactly the display-sink decoder's
//          shape ('-frame', '-x', '-y', '-z', '-type', '-fps',
//          '-loop', '-colorscale') — the command's grammar IS the
//          config schema, no second vocabulary.
//   rows — array of plain row objects.
//   opts — { controlsEl, title, layout }: controlsEl hosts the player
//          (play/pause, scrubber, frame label, speed). The module
//          builds the controls with ssql-viz-* classes; shells style
//          them (the artifact's player-bar CSS, the workspace's
//          panel) without owning any logic.
(function () {
    'use strict';

    function numericAwareSort(keys) {
        return keys.sort((a, b) => {
            const na = parseFloat(a), nb = parseFloat(b);
            return (isNaN(na) || isNaN(nb)) ? a.localeCompare(b) : na - nb;
        });
    }

    const KEYSEP = ''; // unit separator — no data value plausibly contains it

    function animateTraceFor(cfg, rs) {
        const fx = cfg['-x'], fy = cfg['-y'], fz = cfg['-z'];
        if ((cfg['-type'] || 'heatmap') === 'histogram') {
            return { type: 'bar', x: rs.map(r => r[fx]), y: rs.map(r => r[fy]) };
        }
        const xs = [...new Set(rs.map(r => r[fx]))];
        const ys = [...new Set(rs.map(r => r[fy]))];
        const zi = new Map(rs.map(r => [r[fx] + KEYSEP + r[fy], r[fz]]));
        return {
            type: 'heatmap',
            x: xs, y: ys,
            colorscale: cfg['-colorscale'] || 'Viridis',
            z: ys.map(yv => xs.map(xv => {
                const v = zi.get(xv + KEYSEP + yv);
                return v === undefined ? null : v;
            })),
        };
    }

    function renderAnimate(gd, cfg, rows, opts) {
        opts = opts || {};
        const ff = cfg['-frame'], fx = cfg['-x'], fy = cfg['-y'];
        if (!ff || !fx || !fy) return 'to animate needs -frame, -x and -y';
        if (!rows.length || !(ff in rows[0])) return 'frame field "' + ff + '" not in the result rows (available: ' + (rows.length ? Object.keys(rows[0]).join(', ') : 'none') + ')';
        if ((cfg['-type'] || 'heatmap') === 'heatmap' && !cfg['-z']) return 'to animate -type heatmap needs -z';
        const have = Object.keys(rows[0]);
        const missing = [fx, fy, cfg['-z']].filter(f => f && !have.includes(f));
        if (missing.length) return 'to animate references unknown field(s): ' + missing.join(', ') + ' (available: ' + have.join(', ') + ')';

        const groups = new Map();
        for (const r of rows) {
            const k = String(r[ff]);
            if (!groups.has(k)) groups.set(k, []);
            groups.get(k).push(r);
        }
        const keys = numericAwareSort([...groups.keys()]);
        const frames = keys.map(k => ({ name: k, data: [animateTraceFor(cfg, groups.get(k))] }));
        // Heatmap: pin the color scale to the GLOBAL z-range — without
        // this Plotly rescales per frame and colors jump (the old
        // bespoke artifact got this right; the module must too).
        if ((cfg['-type'] || 'heatmap') === 'heatmap') {
            let zmin = Infinity, zmax = -Infinity;
            for (const r of rows) {
                const v = r[cfg['-z']];
                if (typeof v === 'number') { if (v < zmin) zmin = v; if (v > zmax) zmax = v; }
            }
            if (zmin <= zmax) for (const f of frames) { f.data[0].zmin = zmin; f.data[0].zmax = zmax; }
        }
        const fps = parseInt(cfg['-fps']) || 5;
        const layout = Object.assign({
            title: { text: (opts.title || '') },
        }, opts.layout || {});

        // The player: one state machine, both worlds. Shells style the
        // ssql-viz-* classes; nothing here is theme-specific.
        const state = { idx: 0, playing: false, speed: 1, timer: null };
        const show = (i) => {
            state.idx = (i + keys.length) % keys.length;
            Plotly.animate(gd, [keys[state.idx]], { frame: { duration: 0, redraw: true }, mode: 'immediate' });
            if (ui.scrub) ui.scrub.value = state.idx;
            if (ui.label) ui.label.textContent = ff + ' = ' + keys[state.idx] + '  (' + (state.idx + 1) + '/' + keys.length + ')';
        };
        const stop = () => {
            state.playing = false;
            if (state.timer) { clearInterval(state.timer); state.timer = null; }
            if (ui.play) ui.play.textContent = '▶';
        };
        const play = () => {
            if (state.playing) { stop(); return; }
            state.playing = true;
            if (ui.play) ui.play.textContent = '⏸';
            state.timer = setInterval(() => {
                const next = state.idx + 1;
                if (next >= keys.length) {
                    if (loopOn) show(0);
                    else stop();
                } else {
                    show(next);
                }
            }, Math.round(1000 / (fps * state.speed)));
        };

        const ui = {};
        let loopOn = !!cfg['-loop'];
        if (opts.controlsEl) {
            const el = opts.controlsEl;
            el.innerHTML = '';
            const btn = (cls, label, fn) => {
                const b = document.createElement('button');
                b.className = cls; b.textContent = label;
                b.addEventListener('click', fn);
                el.appendChild(b);
                return b;
            };
            btn('ssql-viz-first', '\u23ee', () => { stop(); show(0); });
            btn('ssql-viz-prev', '\u23f4', () => { stop(); show(state.idx - 1); });
            ui.play = btn('ssql-viz-play', '\u25b6', () => play());
            btn('ssql-viz-next', '\u23f5', () => { stop(); show(state.idx + 1); });
            btn('ssql-viz-last', '\u23ed', () => { stop(); show(keys.length - 1); });
            ui.scrub = document.createElement('input');
            ui.scrub.className = 'ssql-viz-scrub';
            ui.scrub.type = 'range';
            ui.scrub.min = 0; ui.scrub.max = keys.length - 1; ui.scrub.value = 0;
            ui.scrub.addEventListener('input', () => { stop(); show(parseInt(ui.scrub.value)); });
            ui.scrub.style.flex = '1';
            el.appendChild(ui.scrub);
            ui.speed = document.createElement('select');
            ui.speed.className = 'ssql-viz-speed';
            for (const v of [0.5, 1, 2, 4]) {
                const o = document.createElement('option');
                o.value = v; o.textContent = v + 'x';
                if (v === 1) o.selected = true;
                ui.speed.appendChild(o);
            }
            ui.speed.addEventListener('change', () => {
                state.speed = parseFloat(ui.speed.value);
                if (state.playing) { stop(); play(); }
            });
            el.appendChild(ui.speed);
            ui.loop = btn('ssql-viz-loop', '\ud83d\udd01', () => {
                loopOn = !loopOn;
                ui.loop.style.opacity = loopOn ? '1' : '0.4';
            });
            ui.loop.style.opacity = loopOn ? '1' : '0.4';
            ui.loop.title = 'Toggle loop (L)';
            ui.label = document.createElement('span');
            ui.label.className = 'ssql-viz-label';
            el.appendChild(ui.label);
            el.style.display = 'flex';
            el.style.alignItems = 'center';
            el.style.gap = '10px';

            // Keyboard: Space play/pause, arrows step, Home/End jump,
            // L toggles loop. Guarded so typing in inputs (the
            // workspace bar!) is never hijacked.
            const doc = el.ownerDocument;
            const onKey = (e) => {
                const t = e.target;
                if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable)) return;
                if (e.code === 'Space') { e.preventDefault(); play(); }
                else if (e.key === 'ArrowLeft') { stop(); show(state.idx - 1); }
                else if (e.key === 'ArrowRight') { stop(); show(state.idx + 1); }
                else if (e.key === 'Home') { stop(); show(0); }
                else if (e.key === 'End') { stop(); show(keys.length - 1); }
                else if (e.key === 'l' || e.key === 'L') { ui.loop && ui.loop.click(); }
            };
            doc.addEventListener('keydown', onKey);
            gd._ssqlVizKeydown = { doc, onKey };
        }

        if (gd._ssqlVizStop) gd._ssqlVizStop();
        if (gd._ssqlVizKeydown) {
            gd._ssqlVizKeydown.doc.removeEventListener('keydown', gd._ssqlVizKeydown.onKey);
            gd._ssqlVizKeydown = null;
        }
        gd._ssqlVizStop = stop;
        Plotly.react(gd, frames[0].data, layout).then(() => {
            Plotly.addFrames(gd, frames);
            show(0);
            if (loopOn) play();
        });
        return null;
    }

    window.ssqlViz = {
        render(gd, kind, cfg, rows, opts) {
            if (kind === 'animate') return renderAnimate(gd, cfg, rows, opts);
            return 'no ssqlViz renderer for kind "' + kind + '"';
        },
    };
})();
