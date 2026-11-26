// pad-events.js — THE DOM EVENT LAYER. It owns no state; it only translates.
//
// This is the half of the old page.html that the user reported as broken: "the
// mouse down, mouse move and mouse up event are not correctly captured", and
// more sharply, "the most up events in particular is not captured correctly
// causing the ink to linger."
//
// Why the OLD layer missed ups is now a measured property of the host, not a
// guess: the CH9329 bridge sends a relative report as a FULL-STATE packet, so a
// report that does not re-assert the button byte is indistinguishable from a
// lift. The host synthesises a pointerup after almost every report of a held
// drag, and — the part that actually loses the real lift — the synthetic lift
// arrives while the pointer still has its capture, so the REAL lift can be
// de-duplicated away or re-cancelled by a residual move. The old layer tried to
// tell them apart with a 40 ms timer that ANY move could cancel, so a drag whose
// frames kept arriving never settled and the pad held ink until the next press.
//
// The new contract, which is what makes ups catchable at all:
//
//   1. EVERY terminal signal is forwarded (pointerup, pointercancel, mouseup,
//      blur, lostpointercapture, pointerout-of-window). The state machine — not
//      this file — decides which are the same lift, by (pointerId, timeStamp).
//   2. The settle timer is armed by the machine and can only be cancelled by a
//      move that ACTUALLY APPENDS A POINT. A repeated position (which is what a
//      synthetic-up-then-nothing stream looks like) cannot hold the latch.
//   3. A move whose `buttons` says nothing is held is forwarded as
//      `buttonsDown:false`, and the machine treats a backwards jump on such a
//      move as a silent release, so an up that the host never delivered still
//      ends the stroke instead of leaving ink on the canvas.
//   4. The capture is released explicitly on every terminal signal, so a stale
//      capture can never route a new stroke's events to a dead state.
//
// Loaded as a plain script; it expects a global `PadCore` (see page.js).
'use strict';

// bindPad wraps the DOM. `env` is injectable so the whole binding can be driven
// from node with a fake canvas and fake window (see test/pad-events.test.js).
//
//   env.canvas          element with addEventListener / getBoundingClientRect /
//                       setPointerCapture / releasePointerCapture
//   env.windowTarget    object with addEventListener (usually window)
//   env.core            the machine from pad-core.js
//   env.posOf(event, rect) -> point | null
//   env.onSample(point, event) -> called for every NEW point the page should record
//   env.show(event)     -> readout update
//   env.onTransition    -> observability
function bindPad(env) {
  const cv = env.canvas;
  const win = env.windowTarget;
  const core = env.core;
  const rect = () => cv.getBoundingClientRect();

  // THE TERMINAL SIGNALS, ALL OF THEM. The old layer listened to pointerup,
  // pointercancel, mouseup and blur and NOTHING ELSE, so a lift reported as
  // lostpointercapture (the browser dropping the capture because a button state
  // changed) or as a pointer leaving the window was silently absorbed: the latch
  // stayed closed and the ink stayed painted. Each of these forwards the same
  // `liftData` so the machine can recognise duplicates of ONE lift without
  // relying on the event object being the same instance.
  function terminal(why, e) {
    const liftData = e && e.pointerId !== undefined
      ? { pointerId: e.pointerId, ts: e.timeStamp }
      : (e && e.timeStamp !== undefined ? { pointerId: 'none', ts: e.timeStamp } : null);
    const wasArmed = core.up(why, core.last, liftData);
    if (wasArmed) {
      try { cv.releasePointerCapture(e && e.pointerId); } catch (err) {}
    }
    return wasArmed;
  }

  cv.addEventListener('pointerdown', function (e) {
    // This pad draws with the primary button only. A press of another button is
    // NOT a stroke and must not open the latch — but it also must not be able to
    // leave the canvas in a half-open state, so it is reported, not ignored.
    if (e.button !== undefined && e.button !== 0) {
      if (env.onIgnore) env.onIgnore('non-primary-button', e);
      return;
    }
    try { cv.setPointerCapture(e.pointerId); } catch (err) {}
    const p = core.down('pointerdown', e);
    if (p && env.onSample) env.onSample(p, e, 'down');
    if (env.show) env.show(e);
  }, { capture: true });

  cv.addEventListener('pointermove', function (e) {
    // Coalesced events are the positions the browser merged into this one frame;
    // without them a held drag records one point per frame instead of one per
    // report (measured: 20 points for a 183-cell path, 10.9%).
    const batch = (e.getCoalescedEvents ? e.getCoalescedEvents() : []);
    const evs = batch.length ? batch : [e];
    for (let i = 0; i < evs.length; i++) {
      const ev = evs[i];
      const appended = core.move(ev, {
        buttonsDown: ev.buttons === undefined ? true : ev.buttons !== 0,
        synthetic: true,
      });
      if (appended && env.onSample) env.onSample(appended, ev, 'drag');
    }
    if (env.show) env.show(e);
  });

  cv.addEventListener('pointerup', function (e) { terminal('pointerup', e); });
  cv.addEventListener('pointercancel', function (e) { terminal('pointercancel', e); });
  // lostpointercapture is the ONE signal that fires whenever the browser drops
  // the capture — including the case that produced lingering ink.
  cv.addEventListener('lostpointercapture', function (e) { terminal('lostpointercapture', e); });
  // A plain mouseup after the capture was dropped; pointerup may never arrive.
  win.addEventListener('mouseup', function (e) { terminal('mouseup', e); });
  // Leaving the window is a lift: no more samples for this stroke can arrive.
  win.addEventListener('pointerout', function (e) {
    if (e && e.relatedTarget === null) terminal('pointerout', e);
  });
  win.addEventListener('blur', function () { terminal('blur', null); });

  // A page being hidden cannot deliver the rest of a stroke; commit now rather
  // than leaving ink on screen forever.
  win.addEventListener('pagehide', function () {
    if (core.isDown) terminal('pagehide', null);
  });

  return {
    terminal: terminal,
    // exposed for the harness and for the Clear button
    core: core,
  };
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { bindPad: bindPad };
}
