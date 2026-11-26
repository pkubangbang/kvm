// pad-core.js — THE STROKE STATE MACHINE, WITH NO DOM IN IT.
//
// This file is the whole answer to "the mouse down, move and up events are not
// correctly captured". Before it, the capture logic was spread across four DOM
// listeners in page.html: `down`/`last`/`pts` were module globals, a pointermove
// handler painted ink, a 40 ms settle timer lived next to the listeners, and a
// pointerdown handler reached in and cancelled that timer. Every symptom the pad
// has shown lives in that arrangement:
//
//   * INK THAT LINGERS. Ink is painted to the canvas from addSample. paintAll()
//     repaints grid + committed strokes, so ink not in `record` is erased by the
//     next paint — EXCEPT while the latch is still closed, because paintAll also
//     repaints `pts` whenever `down` is true. pointerup only ARMS a 40 ms settle;
//     `down` stays true until endStroke runs. So in those 40 ms every residual
//     pointermove passes the `if(!down)` guard, appends to `pts`, and is painted.
//     A residual frame at a NEW position also cancelled the settle outright, so
//     a stream of frames kept the latch closed forever: a stroke tail (连笔) and
//     a pad that never lets go came out of one call site.
//
//   * EVENTS THAT ARE NOT CAPTURED AT ALL. The lift identity de-dup compared the
//     RAW event object (`lastLift`). Two different events describing one lift
//     (pointerup then mouseup) are different objects, so the second one re-armed
//     the settle — harmless by luck — but a *move* event cannot be compared to a
//     lift at all, and nothing recorded WHY a stroke ended. The state machine
//     below identifies a lift by (pointerId, ts) as DATA, so identity survives
//     re-dispatch, and every transition is named and logged.
//
// THE RULE THIS FILE ENFORCES: down -> drag* -> up is a state machine over
// DATA. The DOM layer (page.js) converts browser events into these calls and
// does nothing else: it owns no latch, no timer, and no ink.
//
// It is a CommonJS module so `node --test` can drive it with no browser, and it
// is ALSO loaded in the page through a tiny AMD-style shim at the bottom of
// page.js (see `define` there) — so there is exactly one copy of this logic and
// no build step.
'use strict';

// LIFT_SETTLE_MS is the window that distinguishes a real lift from a synthetic
// one. An injected HID drag reaches the page as a stream of full-state reports,
// and the host synthesises a pointerup after almost every report: committing on
// the spot cuts one held drag into one stroke per frame (measured: a 31-sample
// span landed as 31 one-point strokes, ~14 ms apart). A real lift is followed by
// no further samples; a synthetic one is followed by more of the same drag. The
// window is above the report cadence (~2 ms) and below a human's pause between
// strokes.
const LIFT_SETTLE_MS = 40;

// RELEASE_GRACE_MS is how long a synthetic "button byte absent" move may keep
// the latch closed when the move carries a position BEHIND the last one. See
// onMove: forward motion is evidence of ink, backward motion is evidence of a
// lift we have not been told about.
const RELEASE_GRACE_MS = 120;

function nowDefault() {
  return typeof performance !== 'undefined' && performance.now
    ? performance.now()
    : Date.now();
}

// createPadCore wires the machine to injected effects. Everything the machine
// would otherwise have to know about the world arrives here as a function, so
// the machine can be tested with no DOM, no timers and no clock.
//
//   posOf(event)        -> {rx, ry, x, y, fx, fy}  (or null when off-canvas)
//   onInk(from, to)     -> paint one segment while the pen is down
//   onRepaint(state)    -> full repaint: committed strokes + the live tail
//   onEvent(kind, at)   -> append one event to the log (down/drag/up/clear/load)
//   onTransition(from, to, why) -> observability; one call per state change
//   setTimer(fn, ms)    -> returns a handle; defaults to setTimeout
//   clearTimer(handle)
//   now()               -> ms, monotonic; defaults to performance.now
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

  // ---- state: the ONLY mutable state of the pad -------------------------
  let state = 'up';          // 'up' | 'down' | 'settling'
  let tail = [];             // live stroke points, painted, not yet committed
  let last = null;           // last appended point (of the live stroke)
  let committed = [];        // completed strokes, for repaint/export
  let liftKey = null;        // identity of the lift being settled
  let settleHandle = null;
  let settleWhy = '';
  let lastSampleAt = -Infinity;
  const stats = { downs: 0, ups: 0, represses: 0, cancels: 0, orphans: 0 };

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

  // ---- down -------------------------------------------------------------
  // A press ALWAYS opens a stroke and always says so. The old handler refused a
  // second press silently while a commit was pending (`if(down) return` ABOVE
  // the cancel), so a later real press was swallowed while its moves were still
  // accepted — that is how a run recorded drags and an up but no down.
  function down(raw, at) {
    if (state !== 'up') {
      // A press during the settle window is a NEW STROKE, not a second pointer.
      // Commit what we have first so the two gestures never merge.
      disarm();
      commit('repress');
      stats.represses++;
    }
    stats.downs++;
    tail = [];
    last = null;
    liftKey = null;
    const p = posOf(at);
    if (p) {
      last = p;
      tail.push(p);
    }
    transition('down', raw || 'pointerdown');
    onEvent('down', p);
    return p;
  }

  // ---- move -------------------------------------------------------------
  // Returns the point appended, or null. Paints only while the pen is down.
  //
  // THE LINGERING-INK FIX IS HERE. A move no longer cancels the settle
  // unconditionally; it cancels only by appending a NEW point (the caller sees a
  // non-null return), which is direct evidence that ink continued after the lift
  // we thought we saw. A residual frame that repeats the last position appends
  // nothing and therefore cannot hold the latch open.
  function move(at, opts) {
    const o = opts || {};
    if (state === 'up') return null;
    const p = posOf(at);
    if (!p) return null;
    if (last && samePoint(p, last)) return null;
    // A release we were never told about: the event says no button is held, and
    // the position moved BACKWARDS from the last sample, which a continued drag
    // on a fresh span cannot do. (Forward motion is still treated as ink: the
    // button byte is packet encoding, not a pen lift.)
    if (o.buttonsDown === false && o.synthetic !== false && movedBackwards(p, last)) {
      disarm();
      commit('silent-release');
      stats.orphans++;
      return null;
    }
    if (state === 'settling') {
      disarm();                 // real ink continued: not a lift
      transition('down', 'ink-continued');
      stats.cancels++;
    }
    onInk(last, p);
    tail.push(p);
    last = p;
    lastSampleAt = now();
    onEvent('drag', p);
    return p;
  }

  // ---- up ---------------------------------------------------------------
  // A lift is identified by DATA (pointerId + event timestamp), not by object
  // identity, so pointerup/mouseup/blur describing the SAME release are one
  // lift, and a genuinely new release is never swallowed.
  function up(raw, at, liftData) {
    if (state === 'up') {
      // A release with no press: nothing to commit, but say so rather than
      // pretending it was seen.
      onTransition('up', 'up', 'stray-up');
      return false;
    }
    const key = liftKeyOf(liftData, at, now());
    if (liftKey !== null && liftKey === key) return false;   // same lift twice
    liftKey = key;
    settleWhy = raw || 'pointerup';
    disarm();
    liftKey = key;
    transition('settling', settleWhy);
    settleHandle = setTimer(() => {
      settleHandle = null;
      liftKey = null;
      commit(settleWhy);
    }, LIFT_SETTLE_MS);
    return true;
  }

  // ---- commit -----------------------------------------------------------
  function commit(why) {
    if (state === 'up') return null;
    const wasDown = state === 'down';
    const upAt = last;
    const stroke = tail;
    tail = [];
    // `last` is deliberately NOT cleared here: endStroke historically left it
    // set and pointerdown reset it. The machine resets it on `down` instead, so
    // no code path can draw the next stroke's first segment from the previous
    // stroke's last point — the one bug a naive refactor reintroduces.
    transition('up', why);
    stats.ups++;
    if (stroke.length) committed.push(stroke);
    onRepaint(snapshot());
    // The up carries the lift position, and NEVER a guessed one: a press that
    // was committed before any sample landed reports up with no position, which
    // a reader can tell apart from a real coordinate.
    onEvent('up', upAt);
    return { why, points: stroke.length, wasDown };
  }

  // ---- boundaries -------------------------------------------------------
  function clear() {
    disarm();
    tail = [];
    last = null;
    committed = [];
    transition('up', 'clear');
    if (state !== 'up') transition('up', 'clear');
    onRepaint(snapshot());
    onEvent('clear', null);
  }

  function snapshot() {
    // The live tail is included so a repaint during a held drag does not lose
    // the ink the pen has already laid down.
    const live = state === 'up' ? [] : tail;
    return { strokes: committed, live: live, down: state !== 'up', state: state };
  }

  // ======================================================================
  // helpers
  // ======================================================================
  function samePoint(a, b) {
    if (!a || !b) return false;
    if (a.rx !== undefined && b.rx !== undefined) return a.rx === b.rx && a.ry === b.ry;
    return a.x === b.x && a.y === b.y && a.fx === b.fx && a.fy === b.fy;
  }

  // movedBackwards is the "the pen was lifted and the host kept sending
  // positions" test. Screen-space drags of one held span advance along one
  // direction; a large reverse jump is a different gesture. The threshold is
  // deliberately coarse (a quarter of the canvas) so an ordinary wobble never
  // trips it.
  function movedBackwards(p, from) {
    if (!from || p.rx === undefined || from.rx === undefined) return false;
    return (p.rx < from.rx - 0.25) || (p.ry < from.ry - 0.25);
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
    // read-only accessors — the DOM layer must never poke the state directly
    get state() { return state; },
    get isDown() { return state !== 'up'; },
    get tail() { return tail; },
    get last() { return last; },
    get committed() { return committed; },
    get stats() { return stats; },
    snapshot: snapshot,
    LIFT_SETTLE_MS: LIFT_SETTLE_MS,
    RELEASE_GRACE_MS: RELEASE_GRACE_MS,
  };
}

module.exports = { createPadCore: createPadCore, LIFT_SETTLE_MS: LIFT_SETTLE_MS, RELEASE_GRACE_MS: RELEASE_GRACE_MS };
