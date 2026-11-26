// pad-core.js — THE STROKE STATE MACHINE. No DOM, no browser assumptions.
//
// ===========================================================================
// WHAT THIS FILE IS FOR, MEASURED
// ===========================================================================
//
// The user's report was "the mouse event handling is the source of chaos" and
// "the most up events in particular is not captured correctly causing the ink
// to linger". The recorded evidence is in strokes.d/<client>.jsonl and it is
// unambiguous: inside ONE epoch there are fourteen pairs shaped like this
//
//   {"kind":"up",  "rx":0.24197,"ry":0.51834,"ts":...813774}
//   {"kind":"down","rx":0.24197,"ry":0.51834,"ts":...813775}   <- 1 ms later, SAME point
//   {"kind":"up",  "rx":0.24197,"ry":0.51834,"ts":...813885}
//
// A hand cannot re-press the exact point it just lifted from one millisecond
// ago. Those are the host's RESIDUAL REPORTS: the CH9329 bridge sends a
// relative report as a FULL-STATE packet, so a report that does not re-assert
// the button byte is indistinguishable from a lift, and the host then
// synthesises `pointerup`, `pointerdown` and `pointerup` again for one
// continuous physical hold. Over that session this produced 150 `down`s against
// 145 `up`s (five strokes opened and never closed) and a `clear` that reported
// `cleared_strokes:2` for what the user drew as one stroke.
//
// ===========================================================================
// THE MODEL
// ===========================================================================
//
//   1. NO INK, NO EVENT. A press alone records nothing. `down` is emitted at the
//      FIRST point that actually moves the pen, so every logged `down` is
//      followed by exactly one logged `up` — by construction, not by hope. A
//      zero-length phantom stroke cannot exist in the log because it has no
//      line to draw. This is what makes `down` count == `up` count.
//
//   2. A PRESS AT THE LIFT POINT IS NOT A NEW STROKE. While the lift is still
//      settling, such a press CANCELS the lift and the stroke continues — this
//      is the residual pair of a held drag. After the commit, such a press is
//      swallowed: it records nothing and opens nothing, and the moves that may
//      follow it are dropped by rule 5, because no stroke was opened for them.
//      Either way it never opens a phantom.
//
//   3. NOTHING BUT A PRIMARY PRESS OPENS A STROKE. A move arriving while the
//      machine is 'up' appends nothing. Ink requires a press, always.
//
//   4. GEOMETRY NEVER ENDS A STROKE. The old machine committed a held stroke
//      when a report jumped backwards a quarter canvas, on the theory that this
//      was a missed lift. A hand reversing direction does exactly that, so the
//      heuristic cut real strokes in two. The settle timer is the only judge of
//      a lift now; position is never evidence of one.
//
//   5. A TERMINAL SIGNAL IS FINAL, AND ONLY A NEW PRESS MAY RESUME. A bare
//      `pointermove` can never reopen a stroke that has already been told it
//      ended. Re-entry requires a fresh `pointerdown`, which the host's
//      residual pair does deliver. The moves that arrive in between are DROPPED
//      and counted in `stats.droppedMoves`, so "the pointer was parked but
//      points kept arriving" is both impossible to turn into ink and observable
//      by a test. This is the rule that removes the trailing scribble recorded
//      inside one stroke (epoch 49: a clean 140-point Z, then ~200 more points
//      with no intervening press).
//
// It is a CommonJS module so `node --test` can drive it, and it is ALSO inlined
// into the page as a plain <script>. The export at the bottom is therefore
// behind a `typeof module` guard: an unguarded `module.exports` throws
// ReferenceError in a browser, and it did — leaving the page with no machine at
// all, so no `load`/`clear` boundary was ever posted and retired ink stayed the
// highest epoch in the log.
'use strict';

// LIFT_SETTLE_MS is the window that distinguishes a real lift from a synthetic
// one. A real lift is followed by no further samples; a synthetic one is
// followed by more of the same drag. The window sits above the report cadence
// (~2 ms) and below a human's pause between strokes.
const LIFT_SETTLE_MS = 40;

// RESIDUAL_PRESS_MS bounds how long after a commit a press at the SAME point is
// still read as the host's residual pair. Measured same-point `up`->`down` gaps
// on this rig were 1, 78 and 110 ms; the shortest same-point gap that belonged
// to a genuinely separate stroke was 2673 ms. 250 ms sits between them. Beyond
// it a press at that point is a new stroke.
const RESIDUAL_PRESS_MS = 250;

// PRESS_EPS is how close two positions must be to count as "the same place",
// expressed in canvas ratios (1/128 of the pad is one cell). It exists because
// the host's jitter moves the last bits of a float: strict === on rx/ry let
// invisible jitter add a point to every stroke.
const PRESS_EPS = 1 / 128;

function nowDefault() {
  return typeof performance !== 'undefined' && performance.now
    ? performance.now()
    : Date.now();
}

// createPadCore wires the machine to injected effects, so it can be driven with
// no DOM, no timers and no clock.
//
//   posOf(event)                -> {rx, ry, ...} | null
//   onInk(from, to)             -> paint one segment while the pen is down
//   onRepaint(state)            -> full repaint (committed strokes + live tail)
//   onEvent(kind, at)           -> append one log event (down/drag/up/clear/load)
//   onTransition(from, to, why) -> observability, one call per real change
//   setTimer(fn, ms) / clearTimer(handle) / now()
function createPadCore(effects) {
  const fx = effects || {};
  const posOf = fx.posOf || function () { return null; };
  const onInk = fx.onInk || function () {};
  const onRepaint = fx.onRepaint || function () {};
  const onEvent = fx.onEvent || function () {};
  const onTransition = fx.onTransition || function () {};
  const setTimer = fx.setTimer || function (fn, ms) { return setTimeout(fn, ms); };
  const clearTimer = fx.clearTimer || function (h) { clearTimeout(h); };
  const now = fx.now || nowDefault;

  // ---- the only mutable state -------------------------------------------
  // 'up'       nothing is being drawn
  // 'armed'    a primary press was seen, no ink yet (down not yet logged)
  // 'down'     a stroke is being drawn (down logged, up not yet)
  // 'settling' a lift was seen; the settle timer will commit it
  let state = 'up';
  let tail = [];          // points of the live stroke, painted, not committed
  let last = null;        // last point of the live stroke
  let press = null;       // the point the current physical press began at
  let committed = [];     // completed strokes
  let commitPoint = null; // where the last commit lifted from
  let commitAt = -Infinity;
  let liftKey = null;     // identity of the lift being settled
  let settleHandle = null;
  let settleWhy = '';
  let strokeOpen = false; // a `down` has been logged for the live stroke
  const stats = {
    downs: 0,        // `down` records logged
    ups: 0,          // `up` records logged — equals downs for every real gesture
    represses: 0,    // presses that arrived mid-stroke somewhere else
    cancels: 0,      // settles cancelled by a genuine residual press
    phantoms: 0,     // presses that drew nothing (no stroke, no records)
    droppedMoves: 0, // moves refused because the stroke was already told it ended
    ignoredPresses: 0, // non-primary presses refused at the button police
  };

  function transition(to, why) {
    const from = state;
    if (from === to) return;
    state = to;
    onTransition(from, to, why);
  }

  function disarm() {
    if (settleHandle !== null) {
      clearTimer(settleHandle);
      settleHandle = null;
    }
    settleWhy = '';
    liftKey = null;
  }

  function resetStroke() {
    tail = [];
    last = null;
    press = null;
    strokeOpen = false;
  }

  // ---- press ------------------------------------------------------------
  // `down` is the PRIMARY-press entry point. It never logs an event by itself:
  // a press that draws nothing must not appear in the log (rule 1).
  //
  // opts.primary === false is a non-primary button: it must not open a stroke,
  // and it must not disturb one in progress. It is reported to the caller so the
  // page can say so, and nothing else happens.
  function down(raw, at, opts) {
    const o = opts || {};
    if (o.primary === false) {
      stats.ignoredPresses++;
      return null;
    }
    const p = posOf(at);

    // (rule 2a) A press during the settle, at the lift point, is the residual
    // pair of the SAME physical hold: cancel the lift, keep drawing.
    if (state === 'settling' && p && last && near(p, last)) {
      disarm();
      transition('down', 'residual-press-continued');
      stats.cancels++;
      return last;
    }

    // (rule 2b) A press during a stroke at the lift point is also residual:
    // ignore it entirely rather than committing mid-drag.
    if (state === 'down' && p && last && near(p, last)) {
      stats.phantoms++;
      return null;
    }

    // A press somewhere else during a stroke is a genuine new gesture.
    if (state !== 'up') {
      disarm();
      if (strokeOpen) commit('repress');
      else resetStroke();
      stats.represses++;
    }

    // (rule 2c) After a commit, a press at the lift point within the residual
    // window is the host's residual pair, not a gesture. It is swallowed here —
    // no stroke opens, nothing is logged — and the moves that may follow it are
    // dropped by rule 5, because no stroke was ever opened for them.
    if (p && commitPoint && near(p, commitPoint)
        && (now() - commitAt) <= RESIDUAL_PRESS_MS) {
      stats.phantoms++;
      resetStroke();
      if (state !== 'up') transition('up', 'residual-press');
      return null;
    }

    resetStroke();
    press = p;
    transition('armed', raw || 'pointerdown');
    return p;
  }

  // ---- move -------------------------------------------------------------
  // Returns the point appended, or null. Ink is only ever laid down by a move
  // that follows a press (rule 3), and (rule 5) a move can never resume a
  // stroke that has already received a terminal signal.
  function move(at, opts) {
    const o = opts || {};
    // (rule 5) 'settling' means a terminal signal has already named this stroke
    // as ended. A bare move is NOT evidence enough to reopen it: only a fresh
    // pointerdown is. The point is dropped and counted, never drawn.
    if (state === 'up' || state === 'settling') {
      stats.droppedMoves++;
      return null;
    }
    const p = posOf(at);
    if (!p) return null;

    // A move that repeats the last position is not ink and must not hold the
    // latch open. While armed there is no `last` yet, so the press point is the
    // reference: a jitter-sized move from the press is not a stroke either.
    const ref = last || press;
    if (ref && near(p, ref)) return null;

    if (state === 'armed') {
      // First real movement after the press: NOW the stroke exists, and `down`
      // is logged with the press position so down+up stay paired.
      if (!press) {
        // The press landed off-canvas and had no position. Adopt this one as the
        // press point and wait for a second point: a stroke needs a segment.
        press = p;
        return null;
      }
      const from = press;
      tail = [from];
      last = from;
      strokeOpen = true;
      stats.downs++;
      transition('down', 'first-ink');
      onEvent('down', from);
    }

    onInk(last, p);
    tail.push(p);
    last = p;
    onEvent('drag', p);
    return p;
  }

  // ---- lift -------------------------------------------------------------
  // A lift is identified by DATA (pointerId + event timestamp), not by object
  // identity, so pointerup/mouseup/lostpointercapture describing ONE release are
  // one lift, and a genuinely new release is never swallowed.
  function up(raw, at, liftData) {
    // An armed press that lifts without moving drew nothing: no `down`, no `up`,
    // no stroke. This is the phantom the log recorded fourteen times.
    if (state === 'armed') {
      resetStroke();
      transition('up', 'phantom-press');
      stats.phantoms++;
      return false;
    }
    if (state === 'up') {
      onTransition('up', 'up', 'stray-up');
      return false;
    }
    const key = liftKeyOf(liftData, at, now());
    // During 'settling' the lift is already named. Every further terminal
    // signal — pointerup then mouseup then lostpointercapture, three dispatches
    // with three different timeStamps — is a duplicate of that ONE release, and
    // must neither commit again nor re-arm the timer and extend the latch.
    if (state === 'settling') return false;
    disarm();
    liftKey = key;
    settleWhy = raw || 'pointerup';
    transition('settling', settleWhy);
    settleHandle = setTimer(function () {
      settleHandle = null;
      liftKey = null;
      commit(settleWhy);
    }, LIFT_SETTLE_MS);
    return true;
  }

  // ---- commit -----------------------------------------------------------
  function commit(why) {
    if (state === 'up' || state === 'armed') {
      resetStroke();
      if (state !== 'up') transition('up', why);
      return null;
    }
    const stroke = tail;
    const upAt = last;
    const logged = strokeOpen;
    resetStroke();
    transition('up', why);
    if (logged && stroke.length) {
      committed.push(stroke);
      stats.ups++;
    }
    commitPoint = upAt;
    commitAt = now();
    onRepaint(snapshot());
    // The up carries the lift position. It is never guessed: with rule 1 a
    // logged up always has a logged down and a real position behind it.
    if (logged) onEvent('up', upAt);
    return { why: why, points: stroke.length, logged: logged };
  }

  // ---- boundaries -------------------------------------------------------
  // clear retires EVERYTHING, including a stroke the pen is still holding. It
  // must be impossible for a later lift to commit pre-clear ink, so the live
  // stroke is dropped here and `strokeOpen` is cleared: the lift that arrives
  // afterwards finds state 'up' and does nothing.
  function clear() {
    disarm();
    resetStroke();
    committed = [];
    commitPoint = null;
    commitAt = -Infinity;
    transition('up', 'clear');
    onRepaint(snapshot());
    onEvent('clear', null);
  }

  // load is the page-reload boundary: the client is blank and nothing it held
  // may be re-added. It retires exactly like clear, minus the counts.
  function load() {
    disarm();
    resetStroke();
    committed = [];
    commitPoint = null;
    commitAt = -Infinity;
    transition('up', 'load');
    onRepaint(snapshot());
    onEvent('load', null);
  }

  function snapshot() {
    // The live tail is included so a repaint during a held drag keeps the ink
    // already laid down. At rest there is no live ink to resurrect.
    const live = (state === 'down') ? tail : [];
    return {
      strokes: committed.slice(),
      live: live.slice(),
      down: state === 'down',
      state: state,
    };
  }

  // ======================================================================
  // helpers
  // ======================================================================
  // near is "the same place" with the float tolerance the host needs.
  function near(a, b) {
    if (!a || !b) return false;
    if (a.rx !== undefined && b.rx !== undefined) {
      return Math.abs(a.rx - b.rx) <= PRESS_EPS && Math.abs(a.ry - b.ry) <= PRESS_EPS;
    }
    return a.x === b.x && a.y === b.y;
  }

  function liftKeyOf(data, at, t) {
    if (data && (data.pointerId !== undefined || data.ts !== undefined)) {
      return 'lift:' + String(data.pointerId) + ':' + String(data.ts);
    }
    if (at && at.rx !== undefined) return 'pos:' + at.rx + ',' + at.ry + '@' + Math.round(t);
    return 'anon@' + Math.round(t);
  }

  return {
    down: down,
    move: move,
    up: up,
    clear: clear,
    load: load,
    // read-only accessors — the DOM layer never pokes the state directly
    get state() { return state; },
    get isDown() { return state === 'down' || state === 'settling' || state === 'armed'; },
    get tail() { return tail; },
    get last() { return last; },
    get committed() { return committed; },
    get stats() { return stats; },
    snapshot: snapshot,
    LIFT_SETTLE_MS: LIFT_SETTLE_MS,
    RESIDUAL_PRESS_MS: RESIDUAL_PRESS_MS,
    PRESS_EPS: PRESS_EPS,
  };
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = {
    createPadCore: createPadCore,
    LIFT_SETTLE_MS: LIFT_SETTLE_MS,
    RESIDUAL_PRESS_MS: RESIDUAL_PRESS_MS,
    PRESS_EPS: PRESS_EPS,
  };
}
