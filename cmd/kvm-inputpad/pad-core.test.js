// pad-core.test.js — the DOM pointer layer and the stroke machine, driven with
// no browser.
//
// RUN: node cmd/kvm-inputpad/pad-core.test.js
//
// WHY THIS FILE EXISTS
//
// The user's report is "the mouse event handling … is the source of chaos" and
// "the most up events in particular is not captured correctly causing the ink
// to linger". This file turns that report into assertions, using the recorded
// evidence in strokes.d/ (14 instances of `up` followed 1 ms later by `down` at
// an IDENTICAL rx/ry inside one epoch — a stroke the user never drew).
//
// Every test below was written BEFORE the rework. The ones marked [FAILED-ON-OLD]
// failed against the old pad-core.js/pad-events.js and pass after it; the ones
// marked [PASSED-ON-OLD] pinned behaviour that was already correct and must not
// regress.
'use strict';

const assert = require('node:assert');

// pad-core.js ends with `module.exports`; pad-events.js guards it. Both are
// loadable here exactly as the harness needs them.
const { createPadCore, LIFT_SETTLE_MS, RESIDUAL_PRESS_MS } = require('./pad-core.js');
const { bindPad } = require('./pad-events.js');

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// makePad builds the machine with a fake clock and fake timers, so the settle
// window is exercised deterministically instead of by sleeping.
function makePad() {
  const timers = [];
  let clock = 0;
  const events = [];      // onEvent kinds, in order — the log the page would post
  const ats = [];         // the positions that went with them
  const transitions = [];
  const inks = [];        // painted segments
  const repaints = [];    // {strokes, live, state}

  const core = createPadCore({
    posOf: (ev) => (ev && ev.rx !== undefined)
      ? { rx: ev.rx, ry: ev.ry, x: Math.floor(ev.rx * 128), y: Math.floor(ev.ry * 128), fx: 0, fy: 0 }
      : null,
    onInk: (from, to) => { inks.push([from && from.rx, to.rx]); },
    onRepaint: (s) => { repaints.push({ strokes: s.strokes.length, live: s.live.length, state: s.state }); },
    onEvent: (kind, at) => { events.push(kind); ats.push(at ? { rx: at.rx, ry: at.ry } : null); },
    onTransition: (from, to, why) => { transitions.push(from + '>' + to + ':' + why); },
    setTimer: (fn, ms) => { const h = { fn, at: clock + ms }; timers.push(h); return h; },
    clearTimer: (h) => { const i = timers.indexOf(h); if (i >= 0) timers.splice(i, 1); },
    now: () => clock,
  });

  return {
    core, events, ats, transitions, inks, repaints,
    advance(ms) {
      const target = clock + ms;
      for (;;) {
        const due = timers.filter((t) => t.at <= target).sort((a, b) => a.at - b.at)[0];
        if (!due) break;
        const i = timers.indexOf(due);
        if (i >= 0) timers.splice(i, 1);
        clock = due.at;
        due.fn();
      }
      clock = target;
    },
    pending: () => timers.length,
    now: () => clock,
    setClock(t) { clock = t; },
  };
}

// makeDom binds bindPad to a fake canvas + window, so the fan-out of terminal
// signals is exercised exactly as the browser would deliver it.
function makeDom(pad) {
  const cvH = {};
  const winH = {};
  const cv = {
    addEventListener: (t, fn) => { (cvH[t] = cvH[t] || []).push(fn); },
    getBoundingClientRect: () => ({ left: 0, top: 0, width: 768, height: 768, right: 768, bottom: 768 }),
    setPointerCapture() {},
    releasePointerCapture() {},
  };
  const win = { addEventListener: (t, fn) => { (winH[t] = winH[t] || []).push(fn); } };
  const bound = bindPad({
    canvas: cv, windowTarget: win, core: pad.core,
    posOf: (ev) => (ev.rx !== undefined ? { rx: ev.rx, ry: ev.ry } : null),
    onSample() {}, show() {}, onIgnore() {},
  });
  function fire(target, type, ev) {
    const table = target === 'cv' ? cvH : winH;
    (table[type] || []).forEach((fn) => fn(ev));
  }
  return { bound, fire, cv, win };
}

// pointer builds a browser-shaped PointerEvent. Real events carry a monotonic
// timeStamp per dispatch, which is why two signals for ONE lift never share a
// key — that is the property the old dedup depended on and must not.
let evSeq = 1;
function pointer(rx, ry, opts) {
  const o = opts || {};
  evSeq += 1;
  return {
    clientX: rx * 768, clientY: ry * 768,
    rx: rx, ry: ry,
    button: o.button === undefined ? 0 : o.button,
    buttons: o.buttons === undefined ? 1 : o.buttons,
    pointerId: o.pointerId === undefined ? 7 : o.pointerId,
    timeStamp: o.timeStamp === undefined ? evSeq : o.timeStamp,
    relatedTarget: o.relatedTarget === undefined ? null : o.relatedTarget,
    getCoalescedEvents: () => [],
  };
}

const results = [];
function test(name, tag, fn) {
  try {
    fn();
    results.push({ name, tag, ok: true });
    console.log('PASS  [' + tag + '] ' + name);
  } catch (err) {
    results.push({ name, tag, ok: false, err });
    console.log('FAIL  [' + tag + '] ' + name + '\n        ' + String(err && err.message).split('\n').join('\n        '));
  }
}

// ---------------------------------------------------------------------------
// 1. THE LOGGED DEFECT: a residual pointerdown re-opens a committed stroke
// ---------------------------------------------------------------------------
// strokes.d/342dac2b….jsonl, epoch 28 tail:
//   up   ts=…813774 rx=0.24197 ry=0.51834
//   down ts=…813775 rx=0.24197 ry=0.51834   <- 1 ms later, SAME position
//   up   ts=…813885 rx=0.24197 ry=0.51834
//   clear epoch 29, cleared_strokes=2       <- one gesture reported as two
// A physical press cannot land on the exact point it just lifted from. This is
// a residual report, and it must not open a stroke.
test('residual pointerdown at the lift point does not open a stroke', 'FAILED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.5, 0.5, { timeStamp: 1000 }));
  dom.fire('cv', 'pointermove', pointer(0.6, 0.5, { timeStamp: 1010 }));
  dom.fire('cv', 'pointerup', pointer(0.6, 0.5, { timeStamp: 1020 }));
  pad.advance(LIFT_SETTLE_MS + 10);              // the lift commits

  assert.deepStrictEqual(pad.events, ['down', 'drag', 'up'], 'the first gesture must land whole');

  // The residual press, 1 ms later, at the identical point.
  dom.fire('cv', 'pointerdown', pointer(0.6, 0.5, { timeStamp: 1021 }));

  assert.deepStrictEqual(
    pad.events, ['down', 'drag', 'up'],
    'a residual pointerdown at the lift point opened a phantom stroke: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.isDown, false, 'the latch was re-opened by a residual press');
});

// The same guard must not swallow a REAL next stroke: a press somewhere else, or
// after a human pause, is a new stroke even if it happens to be nearby.
test('a real next stroke still opens after a commit', 'PASSED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.2, 0.2, { timeStamp: 2000 }));
  dom.fire('cv', 'pointermove', pointer(0.3, 0.2, { timeStamp: 2010 }));
  dom.fire('cv', 'pointerup', pointer(0.3, 0.2, { timeStamp: 2020 }));
  pad.advance(LIFT_SETTLE_MS + 10);

  dom.fire('cv', 'pointerdown', pointer(0.7, 0.7, { timeStamp: 2100 }));  // elsewhere
  dom.fire('cv', 'pointermove', pointer(0.75, 0.7, { timeStamp: 2110 }));
  dom.fire('cv', 'pointerup', pointer(0.75, 0.7, { timeStamp: 2120 }));
  pad.advance(LIFT_SETTLE_MS + 10);

  assert.deepStrictEqual(pad.events, ['down', 'drag', 'up', 'down', 'drag', 'up'],
    'two real gestures must stay two strokes: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.committed.length, 2);
});

// ---------------------------------------------------------------------------
// 1b. THE BARE-MOVE RE-ENTRY (the "stroke that never ends")
// ---------------------------------------------------------------------------
// A slow human gesture outlives the 40 ms settle window: the host synthesised a
// terminal signal mid-drag, the settle fired and committed, and then more of the
// SAME drag arrives as bare pointermoves. The old machine re-entered on
// `state === 'settling'` from a MOVE alone (pad-core.js:205, before the rework),
// so the stroke had no end and its tail was recorded into it — the trailing
// scribble measured inside epoch 49 Z2.
test('a bare move cannot reopen a stroke that already ended', 'FAILED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.10, 0.50, { timeStamp: 10000 }));
  dom.fire('cv', 'pointermove', pointer(0.30, 0.50, { timeStamp: 10010 }));

  // The host's synthetic lift, then the settle fires: the stroke is committed.
  dom.fire('cv', 'pointerup', pointer(0.30, 0.50, { timeStamp: 10020 }));
  pad.advance(LIFT_SETTLE_MS + 10);
  assert.strictEqual(pad.core.committed.length, 1, 'the settle did not commit');

  const before = pad.events.length;

  // The drag continues, but NO new press ever arrives.
  dom.fire('cv', 'pointermove', pointer(0.60, 0.60, { timeStamp: 10050 }));
  dom.fire('cv', 'pointermove', pointer(0.80, 0.70, { timeStamp: 10060 }));
  dom.fire('cv', 'pointermove', pointer(0.90, 0.80, { timeStamp: 10070 }));
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.strictEqual(pad.events.length, before,
    'bare moves after a commit appended ink with no press: ' + JSON.stringify(pad.events.slice(before)));
  assert.strictEqual(pad.core.committed.length, 1, 'a bare move created a ' + pad.core.committed.length + 'th stroke');
  assert.ok(pad.core.stats.droppedMoves >= 3,
    'the dropped moves were not counted (stats.droppedMoves=' + pad.core.stats.droppedMoves + ')');
});

// A move arriving INSIDE the settle window is likewise not evidence of a new
// press: the terminal signal already named the stroke as ended.
test('a bare move inside the settle window does not cancel the commit', 'FAILED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.10, 0.50, { timeStamp: 10100 }));
  dom.fire('cv', 'pointermove', pointer(0.30, 0.50, { timeStamp: 10110 }));
  dom.fire('cv', 'pointerup', pointer(0.30, 0.50, { timeStamp: 10120 }));
  pad.advance(10);                                    // inside the settle window
  dom.fire('cv', 'pointermove', pointer(0.55, 0.55, { timeStamp: 10125 })); // bare move, no press
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.deepStrictEqual(pad.events, ['down', 'drag', 'up'],
    'a bare move reopened the stroke: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.committed.length, 1);
  assert.strictEqual(pad.core.committed[0].length, 2, 'the stroke tail was extended by a bare move');
});

// The residual pair the host DOES send — pointerup then pointerdown — is a new
// press, so it may resume. This is the one legal re-entry, and it must not lose
// the samples that follow it.
test('the host residual pair resumes the stroke and loses no samples', 'FAILED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.10, 0.50, { timeStamp: 10200 }));
  for (let i = 1; i <= 5; i++) {
    const t = 10200 + i * 2;
    const x = 0.10 + i * 0.05;
    dom.fire('cv', 'pointermove', pointer(x, 0.50, { timeStamp: t }));
    dom.fire('cv', 'pointerup', pointer(x, 0.50, { timeStamp: t + 1 }));    // synthetic lift
    dom.fire('cv', 'pointerdown', pointer(x, 0.50, { timeStamp: t + 2 }));  // residual press
  }
  dom.fire('cv', 'pointerup', pointer(0.35, 0.50, { timeStamp: 10300 }));
  pad.advance(LIFT_SETTLE_MS * 4);

  assert.deepStrictEqual(pad.events.filter((k) => k !== 'drag'), ['down', 'up'],
    'the residual pair produced more than one down/up: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.committed.length, 1);
  assert.strictEqual(pad.core.committed[0].length, 6,
    'samples were lost across the residual pair: ' + pad.core.committed[0].length + ' points, want 6');
});

// ---------------------------------------------------------------------------
// 2. THE HOST'S SYNTHETIC PAIR: one physical gesture, one down and one up
// ---------------------------------------------------------------------------
// The host synthesises a terminal signal after almost every relative report and
// then delivers the next report as a fresh press. Ten reports must still be ONE
// stroke with ONE down and ONE up — this is the "down 150 vs up 145" imbalance
// in the recorded log.
test('a held drag survives the host residual pair as ONE stroke', 'FAILED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.10, 0.50, { timeStamp: 5000 }));
  for (let i = 1; i <= 10; i++) {
    const x = 0.10 + i * 0.05;
    const t = 5000 + i * 2;
    dom.fire('cv', 'pointermove', pointer(x, 0.50, { timeStamp: t }));
    dom.fire('cv', 'pointerup', pointer(x, 0.50, { timeStamp: t + 1 }));   // synthetic lift
    dom.fire('cv', 'pointerdown', pointer(x, 0.50, { timeStamp: t + 2 })); // residual press
  }
  dom.fire('cv', 'pointerup', pointer(0.6, 0.5, { timeStamp: 5100 }));     // the real lift
  pad.advance(LIFT_SETTLE_MS * 4);

  const downs = pad.events.filter((k) => k === 'down').length;
  const ups = pad.events.filter((k) => k === 'up').length;
  assert.strictEqual(downs, 1, 'one physical press must be one down, got ' + downs + ': ' + JSON.stringify(pad.events));
  assert.strictEqual(ups, 1, 'one physical lift must be one up, got ' + ups + ': ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.committed.length, 1, 'the drag was split into ' + pad.core.committed.length + ' strokes');
  assert.ok(pad.core.committed[0].length >= 10,
    'the stroke lost samples: ' + pad.core.committed[0].length);
});

// After any session the counts must agree: no stroke is opened and left open.
test('down count equals up count after a session', 'FAILED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  for (let g = 0; g < 3; g++) {
    const y = 0.2 + g * 0.2;
    dom.fire('cv', 'pointerdown', pointer(0.1, y, { timeStamp: 6000 + g * 100 }));
    dom.fire('cv', 'pointermove', pointer(0.3, y, { timeStamp: 6010 + g * 100 }));
    dom.fire('cv', 'pointerup', pointer(0.3, y, { timeStamp: 6020 + g * 100 }));
    dom.fire('cv', 'pointerdown', pointer(0.3, y, { timeStamp: 6021 + g * 100 })); // residual
    dom.fire('cv', 'pointerup', pointer(0.3, y, { timeStamp: 6022 + g * 100 }));
    pad.advance(LIFT_SETTLE_MS + 10);
  }

  const downs = pad.events.filter((k) => k === 'down').length;
  const ups = pad.events.filter((k) => k === 'up').length;
  assert.strictEqual(downs, ups, 'down=' + downs + ' up=' + ups + ' — orphaned strokes: ' + JSON.stringify(pad.events));
  assert.strictEqual(downs, 3, 'three gestures must be three strokes, got ' + downs);
});

// ---------------------------------------------------------------------------
// 3. THE TERMINAL FAN-OUT: many signals for one lift
// ---------------------------------------------------------------------------
// pointerup, then the mouseup it precedes, then lostpointercapture — three
// dispatches, three different timeStamps, ONE lift.
test('pointerup + mouseup + lostpointercapture = one commit, one up', 'PASSED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.2, 0.2, { timeStamp: 7000 }));
  dom.fire('cv', 'pointermove', pointer(0.4, 0.2, { timeStamp: 7010 }));
  dom.fire('cv', 'pointerup', pointer(0.4, 0.2, { timeStamp: 7020 }));
  dom.fire('win', 'mouseup', pointer(0.4, 0.2, { timeStamp: 7023 }));
  dom.fire('cv', 'lostpointercapture', pointer(0.4, 0.2, { timeStamp: 7024 }));
  pad.advance(LIFT_SETTLE_MS * 4);

  assert.deepStrictEqual(pad.events, ['down', 'drag', 'up'],
    'the fan-out produced more than one terminal event: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.committed.length, 1);
  assert.strictEqual(pad.pending(), 0, 'a timer was left armed after the commit');
});

// The latch must reach 'up' and STAY there: a lostpointercapture arriving after
// the stroke cannot reopen it or add ink.
//
// CONTRACT CHANGE (deliberate): a press that lifts without moving draws no ink
// and therefore records nothing. The old machine logged down+up and pushed a
// one-point "stroke" for it, which is phantom ink in the record — it is what
// made `clear` report cleared_strokes:2 for one gesture. Rule 1: no ink, no
// event. So this asserts ZERO strokes and ZERO records, not one.
test('a motionless press records no stroke and the late signal adds nothing', 'NEW-CONTRACT', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.2, 0.2, { timeStamp: 8000 }));
  dom.fire('cv', 'pointerup', pointer(0.2, 0.2, { timeStamp: 8010 }));
  pad.advance(LIFT_SETTLE_MS + 10);
  assert.deepStrictEqual(pad.events, [], 'a motionless press was recorded as a stroke');
  assert.strictEqual(pad.core.committed.length, 0);
  const before = pad.events.length;

  dom.fire('cv', 'lostpointercapture', pointer(0.2, 0.2, { timeStamp: 8100 }));
  dom.fire('win', 'mouseup', pointer(0.2, 0.2, { timeStamp: 8101 }));
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.strictEqual(pad.core.state, 'up', 'the latch reopened on a late terminal signal');
  assert.strictEqual(pad.events.length, before, 'a late terminal signal appended events');
  assert.strictEqual(pad.core.committed.length, 0);
});

// A position arriving while the latch is closed must never open ink: only a
// press may. This is the invariant that makes "residual frames" harmless.
test('a move with no press open appends nothing', 'PASSED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointermove', pointer(0.3, 0.3, { timeStamp: 9000 }));
  dom.fire('cv', 'pointermove', pointer(0.4, 0.4, { timeStamp: 9001 }));
  assert.deepStrictEqual(pad.events, [], 'ink appeared without a press: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.state, 'up');
});

// ---------------------------------------------------------------------------
// 4. BUTTON POLICE
// ---------------------------------------------------------------------------
test('a non-primary button opens no stroke and draws no ink', 'PASSED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.5, 0.5, { button: 2, timeStamp: 9100 }));
  dom.fire('cv', 'pointermove', pointer(0.6, 0.5, { timeStamp: 9110 }));
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.deepStrictEqual(pad.events, [], 'a right-button press opened a stroke: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.state, 'up');
  assert.strictEqual(pad.core.committed.length, 0);
});

// A right-button press in the MIDDLE of a left-button stroke must not disturb
// the stroke in progress: no commit, no new down.
test('a mid-stroke non-primary press does not split the stroke', 'FAILED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.2, 0.2, { timeStamp: 9200 }));
  dom.fire('cv', 'pointermove', pointer(0.3, 0.2, { timeStamp: 9210 }));
  dom.fire('cv', 'pointerdown', pointer(0.3, 0.2, { button: 2, timeStamp: 9211 })); // right button mid-stroke
  dom.fire('cv', 'pointermove', pointer(0.4, 0.2, { timeStamp: 9220 }));
  dom.fire('cv', 'pointerup', pointer(0.4, 0.2, { timeStamp: 9230 }));
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.deepStrictEqual(pad.events, ['down', 'drag', 'drag', 'up'],
    'a mid-stroke right press disturbed the stroke: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.committed.length, 1);
});

// ---------------------------------------------------------------------------
// 5. LINGERING INK ON THE CANVAS
// ---------------------------------------------------------------------------
// pointerout (leaving the window) then blur: one commit, and NO live tail left
// for paintAll to keep drawing.
test('pointerout then blur commits once and leaves no live ink', 'PASSED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.2, 0.2, { timeStamp: 9300 }));
  dom.fire('cv', 'pointermove', pointer(0.4, 0.3, { timeStamp: 9310 }));
  dom.fire('win', 'pointerout', pointer(0.4, 0.3, { timeStamp: 9320, relatedTarget: null }));
  dom.fire('win', 'blur', pointer(0.4, 0.3, { timeStamp: 9321 }));
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.deepStrictEqual(pad.events, ['down', 'drag', 'up'],
    'a window exit produced the wrong terminal sequence: ' + JSON.stringify(pad.events));
  const snap = pad.core.snapshot();
  assert.strictEqual(snap.strokes.length, 1);
  assert.strictEqual(snap.live.length, 0, 'the lift left live ink painted');
  assert.strictEqual(snap.state, 'up');
});

// A residual frame that repeats the last position must not hold the latch open
// forever — the 40 ms settle must still fire.
test('a repeated-position frame cannot hold the latch open', 'PASSED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.2, 0.2, { timeStamp: 9400 }));
  dom.fire('cv', 'pointermove', pointer(0.4, 0.2, { timeStamp: 9410 }));
  dom.fire('cv', 'pointerup', pointer(0.4, 0.2, { timeStamp: 9420 }));
  for (let i = 0; i < 20; i++) {
    dom.fire('cv', 'pointermove', pointer(0.4, 0.2, { timeStamp: 9420 + i, buttons: 0 })); // same point
    pad.advance(5);
  }
  pad.advance(LIFT_SETTLE_MS);

  assert.strictEqual(pad.core.state, 'up', 'the latch never closed');
  assert.strictEqual(pad.core.committed.length, 1, 'the stroke never committed');
  assert.deepStrictEqual(pad.events, ['down', 'drag', 'up'], JSON.stringify(pad.events));
});

// A post-release residual frame whose position is BEHIND the last sample must
// not cut a stroke that is still being drawn. The old movedBackwards heuristic
// committed on that geometry alone.
test('a backwards residual frame does not split a held stroke', 'FAILED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.10, 0.50, { timeStamp: 9500 }));
  dom.fire('cv', 'pointermove', pointer(0.60, 0.50, { timeStamp: 9510 }));
  // A residual frame at buttons=0 that jumps back more than a quarter canvas.
  dom.fire('cv', 'pointermove', pointer(0.20, 0.50, { timeStamp: 9511, buttons: 0 }));
  dom.fire('cv', 'pointermove', pointer(0.70, 0.50, { timeStamp: 9520 }));
  dom.fire('cv', 'pointerup', pointer(0.70, 0.50, { timeStamp: 9530 }));
  pad.advance(LIFT_SETTLE_MS * 2);

  const downs = pad.events.filter((k) => k === 'down').length;
  assert.strictEqual(downs, 1, 'a backwards frame opened a second stroke: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.committed.length, 1,
    'the held stroke was cut into ' + pad.core.committed.length + ' strokes by geometry alone');
});

// ---------------------------------------------------------------------------
// 6. BOUNDARIES: press during settle, clear during settle, pagehide
// ---------------------------------------------------------------------------
// A press during the settle, somewhere ELSE, is a new gesture: the old stroke is
// committed first and the new press is latched. Under the new model the latch is
// 'armed' until the first ink, so the assertion is isDown + a second `down` once
// the new press moves.
test('a press during the settle commits the old stroke and latches anew', 'NEW-CONTRACT', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.1, 0.1, { timeStamp: 9600 }));
  dom.fire('cv', 'pointermove', pointer(0.2, 0.1, { timeStamp: 9610 }));
  dom.fire('cv', 'pointerup', pointer(0.2, 0.1, { timeStamp: 9620 }));
  pad.advance(5);                                    // still inside the settle window
  dom.fire('cv', 'pointerdown', pointer(0.6, 0.6, { timeStamp: 9630 }));

  assert.strictEqual(pad.core.committed.length, 1, 'the previous stroke was not committed on a repress');
  assert.strictEqual(pad.core.isDown, true, 'the new press did not latch');
  assert.deepStrictEqual(pad.events, ['down', 'drag', 'up'], 'the repress logged before drawing');

  dom.fire('cv', 'pointermove', pointer(0.7, 0.6, { timeStamp: 9640 }));
  dom.fire('cv', 'pointerup', pointer(0.7, 0.6, { timeStamp: 9650 }));
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.deepStrictEqual(pad.events, ['down', 'drag', 'up', 'down', 'drag', 'up'],
    'the repress did not become its own stroke: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.committed.length, 2, 'the repress merged into the previous stroke');
});

test('clear during the settle leaves the canvas empty and commits nothing', 'PASSED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.1, 0.1, { timeStamp: 9700 }));
  dom.fire('cv', 'pointermove', pointer(0.5, 0.5, { timeStamp: 9710 }));
  dom.fire('cv', 'pointerup', pointer(0.5, 0.5, { timeStamp: 9720 }));
  pad.advance(5);                                    // inside the settle window
  pad.core.clear();
  pad.advance(LIFT_SETTLE_MS * 4);

  assert.deepStrictEqual(pad.events, ['down', 'drag', 'clear'],
    'a timer fired after clear and appended events: ' + JSON.stringify(pad.events));
  const snap = pad.core.snapshot();
  assert.strictEqual(snap.strokes.length, 0, 'retired ink came back after clear');
  assert.strictEqual(snap.live.length, 0, 'live ink survived clear');
  assert.strictEqual(snap.state, 'up');
  assert.strictEqual(pad.core.committed.length, 0);
});

// Clear while a pen is still DOWN: the ink must be gone and a later lift must
// not resurrect the pre-clear stroke.
test('clear while the pen is down retires the stroke for good', 'FAILED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.1, 0.1, { timeStamp: 9750 }));
  dom.fire('cv', 'pointermove', pointer(0.5, 0.5, { timeStamp: 9760 }));
  pad.core.clear();
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.strictEqual(pad.core.snapshot().strokes.length, 0, 'pre-clear ink survived clear');
  assert.strictEqual(pad.core.snapshot().live.length, 0, 'the pre-clear tail survived clear');

  // The lift for the cleared stroke arrives afterwards.
  dom.fire('cv', 'pointerup', pointer(0.5, 0.5, { timeStamp: 9770 }));
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.strictEqual(pad.core.committed.length, 0,
    'the cleared stroke was committed AFTER the clear: ' + pad.core.committed.length + ' strokes');
});

test('pagehide during the settle is one commit, not two', 'PASSED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.2, 0.2, { timeStamp: 9800 }));
  dom.fire('cv', 'pointermove', pointer(0.4, 0.2, { timeStamp: 9810 }));
  dom.fire('cv', 'pointerup', pointer(0.4, 0.2, { timeStamp: 9820 }));
  pad.advance(5);
  dom.fire('win', 'pagehide', pointer(0.4, 0.2, { timeStamp: 9830 }));
  pad.advance(LIFT_SETTLE_MS * 4);

  assert.deepStrictEqual(pad.events, ['down', 'drag', 'up'],
    'pagehide during the settle produced a second terminal event: ' + JSON.stringify(pad.events));
  assert.strictEqual(pad.core.committed.length, 1);
});

// ---------------------------------------------------------------------------
// 7. STROKE INTEGRITY
// ---------------------------------------------------------------------------
// down + drag* + up round-trips as ONE stroke with every point, in order.
test('one stroke round-trips intact, never split', 'PASSED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.10, 0.10, { timeStamp: 9900 }));
  const xs = [0.20, 0.30, 0.40, 0.50];
  xs.forEach((x, i) => dom.fire('cv', 'pointermove', pointer(x, 0.10 + i * 0.01, { timeStamp: 9910 + i })));
  dom.fire('cv', 'pointerup', pointer(0.50, 0.13, { timeStamp: 9920 }));
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.strictEqual(pad.core.committed.length, 1, 'the stroke was split');
  assert.strictEqual(pad.core.committed[0].length, 5, 'the stroke holds ' + pad.core.committed[0].length + ' points, want 5');
  assert.deepStrictEqual(
    pad.core.committed[0].map((p) => p.rx),
    [0.10, 0.20, 0.30, 0.40, 0.50]);
});

// Two identical positions in a row are one point, not two, but a float that
// differs only in the last bits IS the same place. The old strict === on floats
// let host jitter inflate every stroke.
test('near-identical positions collapse to one point', 'FAILED-ON-OLD', () => {
  const pad = makePad();
  const dom = makeDom(pad);

  dom.fire('cv', 'pointerdown', pointer(0.25, 0.25, { timeStamp: 9950 }));
  dom.fire('cv', 'pointermove', pointer(0.25 + 1e-12, 0.25, { timeStamp: 9951 }));
  dom.fire('cv', 'pointermove', pointer(0.30, 0.25, { timeStamp: 9952 }));
  dom.fire('cv', 'pointerup', pointer(0.30, 0.25, { timeStamp: 9953 }));
  pad.advance(LIFT_SETTLE_MS * 2);

  assert.strictEqual(pad.core.committed[0].length, 2,
    'float jitter added a point: ' + pad.core.committed[0].length + ' points, want 2');
});

// ---------------------------------------------------------------------------
// export the settle constants so a Go test can keep them in step
// ---------------------------------------------------------------------------
console.log('\nLIFT_SETTLE_MS=' + LIFT_SETTLE_MS + ' RESIDUAL_PRESS_MS=' + RESIDUAL_PRESS_MS);

const failed = results.filter((r) => !r.ok);
console.log('\n' + (results.length - failed.length) + '/' + results.length + ' passed');
if (failed.length) {
  console.log('\nFAILED:');
  failed.forEach((f) => console.log('  [' + f.tag + '] ' + f.name));
  process.exit(1);
}
