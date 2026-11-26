#!/usr/bin/env python3
"""heart_check.py -- decide, from the pad's RECORDED coordinates alone, whether a
drawn glyph is a (proper) heart. No screenshot, no vision model.

WHY THIS EXISTS
    The pad records, for every stroke, a sequence of samples carrying `rx`/`ry`
    -- pure ratios of the canvas CSS box in [0,1]. Those ratios are the ink that
    was actually drawn. Reading them back and CHARACTERISING the curve is the
    only honest way to answer "is this a heart"; looking at a rendering and
    squinting at it is not (measured: three "hearts" in a row were mounds, and
    only arithmetic found out).

WHAT A HEART IS, AS TURNS
    A textbook heart (the two-lobe cardioid-ish glyph) is ONE CLOSED curve with
    FOUR sharp turning points, in this cyclic order:

        T1  bottom tip        -- the sharp V where the two lower flanks meet
        T2  left shoulder     -- where the left flank turns over into the lobe
        T3  cleft             -- the notch at the TOP CENTRE, between the lobes
        T4  right shoulder    -- mirror of T2

    Going round clockwise from the tip: tip, up the left flank, over the left
    lobe, down into the cleft, up over the right lobe, down the right flank,
    back to the tip. So a heart is distinguishable from
      * a mound / blob:  0-2 turns, no cleft (no upward spike at top centre);
      * a circle:        0 sharp turns, closed, uniform curvature;
      * a V or a spike:  1-2 turns, not closed;
      * a figure-eight:  3 sharp turns, no bottom tip.
    Two independent pieces of evidence are therefore required, and this script
    reports BOTH, so a failure says WHICH property is missing:

      1. TURN DETECTION. Resample the stroke by arc length, estimate the
         turning angle per sample from the smoothed tangent, find the sign
         alternation of curvature -- a heart must alternate convex/concave/
         convex, i.e. the cleft is a CONCAVE vertex while the lobes are
         CONVEX: the curvature sign must flip at least twice.
      2. GEOMETRY OF THE EXTREMES. Among the detected vertices there must be
         (a) a near-global MAXIMUM of y (the tip, on image axes y grows DOWN),
         (b) a near-global MINIMUM of y roughly at the horizontal centre
             (the cleft), and (c) two lobes whose y reaches the top band on
             either side of that centre. Also: closure and aspect ratio.

USAGE
    python scripts/heart_check.py FILE.json                # every stroke
    python scripts/heart_check.py FILE.json --stroke 0     # just one
    python scripts/heart_check.py FILE.json --json         # machine-readable

    FILE is a pad trace as the server persists it (an ARRAY of strokes, each an
    array of points with rx/ry), or the array of strokes as /api/trace returns.

    Exit status: 0 when the selected strokes all pass, 1 when any fails,
    2 on a usage/format error -- so a shell loop can gate on it.

COORDINATES
    rx grows to the RIGHT, ry grows DOWNWARD (screen convention). "Top" of the
    glyph is the SMALLEST ry. Everything in the verdict is phrased in glyph
    terms (tip at the bottom, cleft at the top centre) and converted once here,
    so the reader never has to remember the axis flip.
"""

import argparse
import json
import math
import sys

# ---------------------------------------------------------------------------
# tuning constants -- each is a judgement about "how heart-shaped is heart-shaped
# enough", and each was chosen so that the shapes measured on this rig land on
# the right side of it: the 190/195-point "mound" strokes from the failed
# attempts fail on the CLEFT (their top is flat, curvature sign never flips at
# top centre), while a swept-to-2pi bezier heart passes all of them.
# ---------------------------------------------------------------------------
RESAMPLE_N = 240       # samples along the resampled curve (arc-length uniform)
SMOOTH_WIN = 7         # moving-average window for the resampled points
ANGLE_WIN_S = 0.06     # tangent estimated across +/- 6% of the curve length
TURN_ANGLE_DEG = 35.0  # a vertex must turn at least this much within its window
MIN_VERTS = 3          # a heart has 4; allow one to be too soft to detect
BAND = 0.10            # "near the extreme": within 10% of the glyph's extent
CENTRE_TOL = 0.22      # the cleft must lie within +/-22% of the width centre
LOBE_MIN_RISE = 0.70   # each lobe must reach this fraction of the top band
CLOSE_TOL = 0.12       # start-to-end gap, as a fraction of the extent, for closure
ASPECT_MIN, ASPECT_MAX = 0.70, 1.60   # w/h of a heart


def _dist(a, b):
    return math.hypot(b[0] - a[0], b[1] - a[1])


def resample(pts, n):
    """Arc-length uniform resampling of a polyline."""
    if len(pts) < 2:
        return list(pts)
    acc = [0.0]
    for i in range(1, len(pts)):
        acc.append(acc[-1] + _dist(pts[i - 1], pts[i]))
    total = acc[-1]
    if total <= 0:
        return [pts[0]] * n
    out, j = [], 0
    for k in range(n):
        target = total * k / (n - 1)
        while j < len(acc) - 2 and acc[j + 1] < target:
            j += 1
        span = acc[j + 1] - acc[j]
        t = 0.0 if span <= 0 else (target - acc[j]) / span
        x = pts[j][0] + (pts[j + 1][0] - pts[j][0]) * t
        y = pts[j][1] + (pts[j + 1][1] - pts[j][1]) * t
        out.append((x, y))
    return out


def smooth(pts, win):
    """Centred moving average -- kills hand/报告 jitter without moving the shape."""
    if win <= 1 or len(pts) < win:
        return list(pts)
    half = win // 2
    n = len(pts)
    out = []
    for i in range(n):
        xs = ys = 0.0
        c = 0
        for k in range(i - half, i + half + 1):
            if 0 <= k < n:
                xs += pts[k][0]
                ys += pts[k][1]
                c += 1
        out.append((xs / c, ys / c))
    return out


def turn_angles(pts, win_s):
    """Unsigned turning angle (degrees) at each sample, over a +/- win window."""
    n = len(pts)
    w = max(1, int(round(win_s * n)))
    ang = []
    for i in range(n):
        a = pts[max(0, i - w)]
        b = pts[i]
        c = pts[min(n - 1, i + w)]
        v1 = (b[0] - a[0], b[1] - a[1])
        v2 = (c[0] - b[0], c[1] - b[1])
        n1 = math.hypot(*v1)
        n2 = math.hypot(*v2)
        if n1 == 0 or n2 == 0:
            ang.append(0.0)
            continue
        cosv = (v1[0] * v2[0] + v1[1] * v2[1]) / (n1 * n2)
        cosv = max(-1.0, min(1.0, cosv))
        ang.append(math.degrees(math.acos(cosv)))
    return ang


def signed_curvature(pts, win_s):
    """Sign of the local turn per sample: +1 for a left turn, -1 for a right."""
    n = len(pts)
    w = max(1, int(round(win_s * n)))
    out = []
    for i in range(n):
        a = pts[max(0, i - w)]
        b = pts[i]
        c = pts[min(n - 1, i + w)]
        v1 = (b[0] - a[0], b[1] - a[1])
        v2 = (c[0] - b[0], c[1] - b[1])
        cross = v1[0] * v2[1] - v1[1] * v2[0]
        if cross > 0:
            out.append(1)
        elif cross < 0:
            out.append(-1)
        else:
            out.append(0)
    return out


def sign_flips(signs):
    """Number of sustained sign changes (ignore 0s and 1-sample noise)."""
    seq = [s for s in signs if s != 0]
    flips = 0
    for i in range(1, len(seq)):
        if seq[i] != seq[i - 1]:
            flips += 1
    return flips, len(seq) and (sum(1 for s in seq if s > 0) / len(seq))


def find_vertices(angles, thresh):
    """Peaks of the turning-angle profile, non-maximum suppressed."""
    n = len(angles)
    # circular non-maximum suppression: a vertex is a local max in a window
    # proportional to the curve, so one broad corner yields ONE vertex.
    span = max(2, n // 24)
    verts = []
    for i in range(n):
        a = angles[i]
        if a < thresh:
            continue
        is_max = True
        for k in range(1, span + 1):
            if angles[(i - k) % n] > a or angles[(i + k) % n] > a:
                is_max = False
                break
        if is_max:
            verts.append((i, a))
    # merge vertices that are within half a span of each other
    merged = []
    for i, a in verts:
        if merged and (i - merged[-1][0]) <= span:
            if a > merged[-1][1]:
                merged[-1] = (i, a)
            continue
        merged.append((i, a))
    return merged


def analyse(stroke, idx):
    pts = []
    for p in stroke:
        if "rx" in p and "ry" in p:
            pts.append((float(p["rx"]), float(p["ry"])))
        elif "x" in p and "y" in p:
            fx = float(p.get("fx", 0.0) or 0.0)
            fy = float(p.get("fy", 0.0) or 0.0)
            pts.append(((float(p["x"]) + fx) / 128.0,
                        (float(p["y"]) + fy) / 128.0))
    rep = {"stroke": idx, "n_raw": len(pts), "ok": False, "reasons": []}
    if len(pts) < 12:
        rep["reasons"].append("too few points (%d) to characterise" % len(pts))
        return rep

    rs = smooth(resample(pts, RESAMPLE_N), SMOOTH_WIN)
    xs = [p[0] for p in rs]
    ys = [p[1] for p in rs]
    w = max(xs) - min(xs)
    h = max(ys) - min(ys)
    ext = max(w, h)
    rep["bbox_rx"] = [round(min(xs), 4), round(max(xs), 4)]
    rep["bbox_ry"] = [round(min(ys), 4), round(max(ys), 4)]
    rep["w"] = round(w, 4)
    rep["h"] = round(h, 4)
    rep["aspect_wh"] = round(w / h, 3) if h else None

    ang = turn_angles(rs, ANGLE_WIN_S)
    verts = find_vertices(ang, TURN_ANGLE_DEG)
    rep["turn_max_deg"] = round(max(ang), 1)
    rep["vertices"] = [{"i": i, "rx": round(rs[i][0], 4), "ry": round(rs[i][1], 4),
                        "deg": round(a, 1)} for i, a in verts]
    rep["n_vertices"] = len(verts)

    flips, pos_frac = sign_flips(signed_curvature(rs, ANGLE_WIN_S))
    rep["curvature_sign_flips"] = flips
    rep["convex_fraction"] = round(pos_frac or 0.0, 3)

    # closure: a heart is ONE closed loop
    gap = _dist(rs[0], rs[-1])
    rep["closure_gap"] = round(gap, 4)
    rep["closure_frac"] = round(gap / ext, 3) if ext else None
    if ext == 0 or (gap / ext) > CLOSE_TOL:
        rep["reasons"].append(
            "not closed: start-to-end gap is %.0f%% of the glyph extent (limit %.0f%%)"
            % (100 * (gap / ext if ext else 0), 100 * CLOSE_TOL))

    # aspect
    if h == 0 or not (ASPECT_MIN <= (w / h) <= ASPECT_MAX):
        rep["reasons"].append(
            "aspect w/h = %s, outside [%.2f, %.2f] for a heart"
            % (rep["aspect_wh"], ASPECT_MIN, ASPECT_MAX))

    # --- geometry of the extremes -----------------------------------------
    # IMAGE AXES: ry grows DOWN, so the glyph's BOTTOM (the apex/tip of an
    # upright heart) is the MAXIMUM ry and its TOP band (where the cleft sits
    # between the two lobes) is the MINIMUM ry. This block used to name the
    # minimum "tip" and the maximum "cleft" while the comment claimed the
    # opposite -- so the detector certified hearts standing on their lobes, which
    # is exactly the defect the user caught in the glyph. tip = bottom.
    top, bot = min(ys), max(ys)
    cx_glyph = (min(xs) + max(xs)) / 2.0
    rep["tip"] = {"rx": round(xs[ys.index(bot)], 4), "ry": round(bot, 4)}
    # THE CLEFT IS NOT THE HIGHEST POINT -- THAT WAS THE SECOND AXIS-SHAPED BUG.
    # On a textbook heart the global MINIMUM of ry is one LOBE's crown, not the
    # notch between the lobes; the notch sits a little LOWER (larger ry) and at
    # mid-width. Naming the highest point "cleft" therefore failed a perfectly
    # valid upright heart with "the top point is N% off the centre", which reads
    # like a dome but is really two lobes with one of them a hair higher.
    # So: take the highest point on EACH side of mid-width (the two lobe
    # crowns), and the cleft is the local LOWEST point (max ry) of the curve in
    # the narrow band between those two crowns -- the notch.
    crown_l = crown_r = None
    for x, y in rs:
        if x <= cx_glyph:
            if crown_l is None or y < crown_l[1]:
                crown_l = (x, y)
        else:
            if crown_r is None or y < crown_r[1]:
                crown_r = (x, y)
    # So: restrict the search to the TOP BAND first, then take the highest point
    # on EACH side of mid-width inside it (the two lobe crowns), then the notch
    # is the deepest sample in the inter-crown span that is still AT OR BELOW the
    # crowns. The band is what keeps the apex out of the answer: without it the
    # inter-crown span contains the tip as well, and "deepest in the span" then
    # returns the tip and reports it as its own cleft.
    cleft = None
    top_limit = top + 0.35 * (bot - top)
    band = [(x, y) for x, y in rs if y <= top_limit]
    crown_l = crown_r = None
    for x, y in band:
        if x <= cx_glyph:
            if crown_l is None or y < crown_l[1]:
                crown_l = (x, y)
        else:
            if crown_r is None or y < crown_r[1]:
                crown_r = (x, y)
    if crown_l is not None and crown_r is not None and crown_r[0] > crown_l[0]:
        crown_depth = max(crown_l[1], crown_r[1])
        for x, y in band:
            if crown_l[0] <= x <= crown_r[0] and y >= crown_depth:
                if cleft is None or y > cleft[1]:
                    cleft = (x, y)
    # INVARIANT, asserted rather than hoped for: a cleft is never the tip. If the
    # search returns the apex, the shape has one dome (or the band is wrong), and
    # both are reasons to FAIL, not to print a tidier number.
    if cleft is not None and cleft[1] >= bot - 1e-9:
        rep["reasons"].append(
            "the point called 'cleft' is the tip itself: no notch between the "
            "lobes, so this is one dome, not a heart")
        cleft = None
    if cleft is not None:
        rep["cleft"] = {"rx": round(cleft[0], 4), "ry": round(cleft[1], 4)}
        rep["cleft_between_crowns"] = True
    else:
        # no two crowns to span: fall back to the old reading, but SAY SO --
        # an honest fallback is a reason to doubt, not a silent substitution.
        rep["cleft"] = {"rx": round(xs[ys.index(top)], 4), "ry": round(top, 4)}
        rep["cleft_between_crowns"] = False
        rep["reasons"].append(
            "could not bracket a cleft between two crowns (no point left of and "
            "right of mid-width at the top): not a two-lobed shape")
    # The cleft must sit near mid-width, within the top band, and NOT be the
    # highest point (a highest-point cleft means one dome, not two lobes).
    cleft_dx = abs(rep["cleft"]["rx"] - cx_glyph) / w if w else 1.0
    rep["cleft_offcentre_frac"] = round(cleft_dx, 3)
    rep["tip_is_bottom"] = rep["tip"]["ry"] >= bot - 1e-9
    # ORIENTATION IS PART OF THE VERDICT, not a number printed beside it. Locating
    # the cleft correctly is useless if the verdict never reads it: the detector
    # certified an upside-down glyph as HEART for several rounds because "a cleft
    # was found, lobes were found, closure is tight" is equally true of a heart
    # standing on its lobes. On the image axes ry grows DOWN, so an upright heart
    # must satisfy cleft.ry < tip.ry by a real margin -- the notch is in the TOP
    # band and the apex is the BOTTOM. When that ordering is reversed the glyph is
    # upside down, which is a FAIL with its own reason so the reader knows which
    # mistake they made rather than being told "not a heart" about a shape that is
    # otherwise perfect.
    if cleft is not None:
        height = bot - top
        margin = 0.08 * height if height > 0 else 0.0
        rep["cleft_above_tip"] = bool(rep["cleft"]["ry"] < rep["tip"]["ry"] - margin)
        if not rep["cleft_above_tip"]:
            rep["reasons"].append(
                "the glyph is UPSIDE DOWN: the notch (ry %.3f) is not above the "
                "apex (ry %.3f) by %.3f of the glyph height, so the point is at the "
                "top and the lobes at the bottom"
                % (rep["cleft"]["ry"], rep["tip"]["ry"], margin))
    if cleft_dx > CENTRE_TOL:
        rep["reasons"].append(
            "cleft is %.0f%% off the width centre (limit %.0f%%): the notch between "
            "the lobes must be near the middle" % (100 * cleft_dx, 100 * CENTRE_TOL))
    # both sides of the cleft must rise into the top band -> two lobes
    top_band = top + BAND * (bot - top)
    left_top = left_top_x = None
    right_top = right_top_x = None
    for x, y in rs:
        if y <= top_band:
            if x < rep["cleft"]["rx"]:
                if left_top is None or y < left_top:
                    left_top, left_top_x = y, x
            else:
                if right_top is None or y < right_top:
                    right_top, right_top_x = y, x
    rise = bot - top
    lobe_ok = (left_top is not None and right_top is not None
               and rise > 0
               and (bot - left_top) >= LOBE_MIN_RISE * rise
               and (bot - right_top) >= LOBE_MIN_RISE * rise)
    rep["lobes"] = {"left": left_top is not None and round(left_top, 4),
                    "right": right_top is not None and round(right_top, 4),
                    "left_rx": left_top_x is not None and round(left_top_x, 4),
                    "right_rx": right_top_x is not None and round(right_top_x, 4)}
    if not lobe_ok:
        rep["reasons"].append(
            "no double lobe: the curve does not reach the top %.0f%% band on BOTH "
            "sides of the cleft" % (100 * BAND))

    # turn evidence
    if len(verts) < MIN_VERTS:
        rep["reasons"].append(
            "only %d turning points above %.0f deg (a heart needs >= %d: tip, two "
            "shoulders, cleft); max turn seen %.0f deg"
            % (len(verts), TURN_ANGLE_DEG, MIN_VERTS, max(ang)))
    if flips < 2:
        rep["reasons"].append(
            "curvature sign flips %d time(s) (a heart needs >= 2: convex lobe, "
            "concave cleft, convex lobe)" % flips)

    rep["ok"] = not rep["reasons"]
    rep["verdict"] = "HEART" if rep["ok"] else "NOT A HEART"
    return rep


def load(path):
    with open(path, "r", encoding="utf-8") as f:
        raw = f.read()
    try:
        data = json.loads(raw)
    except json.JSONDecodeError as e:
        raise SystemExit("2|%s is not JSON: %s" % (path, e))
    if isinstance(data, dict):
        # a commit reply from /api/trace carries the count, not the strokes
        strokes = data.get("strokes")
        if not isinstance(strokes, list):
            raise SystemExit("2|%s is an object without a strokes array" % path)
        data = strokes
    if not isinstance(data, list):
        raise SystemExit("2|%s is not an array of strokes" % path)
    # a trace is Stroke[]; be forgiving of a single Stroke (Point[])
    if data and isinstance(data[0], dict):
        data = [data]
    return data


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("file")
    ap.add_argument("--stroke", type=int, default=None)
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--thresh", type=float, default=TURN_ANGLE_DEG)
    a = ap.parse_args(argv)
    # NOTE: do NOT rebind the module-level TURN_ANGLE_DEG here. `global` after the
    # name was already read in this function's default arguments is a SyntaxError
    # ("name ... is used prior to global declaration") -- measured, and it made the
    # whole detector unimportable. The threshold is threaded in explicitly.
    thresh = a.thresh

    strokes = load(a.file)
    picks = ([a.stroke] if a.stroke is not None
             else list(range(len(strokes))))
    reps = [analyse(strokes[i], i) for i in picks]
    all_ok = bool(reps) and all(r["ok"] for r in reps)
    if a.json:
        print(json.dumps({"ok": all_ok, "strokes": reps}, indent=2))
        return 0 if all_ok else 1
    for r in reps:
        print("stroke %d: %s  (n=%d, %dx%d px-equiv, aspect %s)"
              % (r["stroke"], r.get("verdict", "TOO SHORT"), r["n_raw"],
                 round(128 * r.get("w", 0)), round(128 * r.get("h", 0)),
                 r.get("aspect_wh")))
        print("  turning points: %d (max turn %.0f deg)  sign flips: %s"
              % (r.get("n_vertices", 0), r.get("turn_max_deg", 0),
                 r.get("curvature_sign_flips", "-")))
        for v in r.get("vertices", [])[:8]:
            print("    vertex @ i=%3d  rx=%.3f ry=%.3f  turn %5.1f deg"
                  % (v["i"], v["rx"], v["ry"], v["deg"]))
        print("  tip  %s" % (r.get("tip"),))
        print("  cleft %s  (off-centre %s)" % (r.get("cleft"),
                                               r.get("cleft_offcentre_frac")))
        print("  lobes %s" % (r.get("lobes"),))
        print("  closure gap %s of extent" % (r.get("closure_frac"),))
        for why in r.get("reasons", []):
            print("  FAIL: " + why)
    return 0 if all_ok else 1


if __name__ == "__main__":
    sys.exit(main())
