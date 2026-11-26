#!/usr/bin/env python3
"""heart_draw.py -- emit the argv for one closed heart stroke, paced under 5 s.

WHY A SCRIPT AND NOT A HAND-WRITTEN LEG LIST
    Three hand-built attempts on this rig produced MOUNDS. The two faults were
    structural, not tuning:
      1. the parametric heart was swept over HALF its domain (t in [0, pi]
         instead of [0, 2pi]), so the curve was an open arc;
      2. the legs were planned by hand from deltas, so the closure was checked
         by inspection rather than by arithmetic.
    Here the curve is generated from the closed-form heart, sampled by ARC
    LENGTH (so reports are evenly spaced in distance, which is what the pad
    records), and the emitted legs are asserted to close before anything is
    printed.

THE HEART, IN THE FORM THE PAD RECORDS
    x(t) = 16 sin^3 t
    y(t) = 13 cos t - 5 cos 2t - 2 cos 3t - cos 4t
    t in [0, 2pi) -- ONE closed loop. Two sharp minima of y (the lobes' tops),
    one maximum (the tip), and the cleft is the local minimum between the lobe
    maxima in the DERIVATIVE sense. In screen terms the glyph is flipped: the
    tip is where y is LARGEST.

EMITTED SHAPE
    The argv printed is a COMPLETE invocation:

        kvm mouse rel -human=false -gap G down  drag dx dy ... up

    with every negative delta as its own token. That is the rule that cost a
    failed run earlier in this project:
        `drag 4 -6` as ONE string  ->  `kvm: at token 3: unknown verb "drag 4 -6"`

USAGE
    python scripts/heart_draw.py                      # default size/pacing
    python scripts/heart_draw.py --scale 5 --gap 12
    python scripts/heart_draw.py --exec               # run it via .\\kvm.exe
    python scripts/heart_draw.py --json               # the plan, not argv

    --exec runs the stroke and prints the process exit code, so a measurement
    loop is:  python scripts/heart_draw.py --exec && python scripts/heart_check.py <trace>
"""

import argparse
import json
import math
import subprocess
import sys

# the closed-form heart. NOT a half sweep: t runs the whole circle.
def heart_xy(t):
    """The closed-form heart, in SCREEN terms: the apex points DOWN.

    The textbook heart has its TIP at +y and its cleft at -y. On a screen y
    grows DOWNWARD, so emitting the textbook y unchanged stands the heart on its
    lobes with the point upward -- measured: every glyph this generator produced
    until this fix was upside down, and the user caught it before the detector
    did (because the detector's own axis convention had the same flip, see
    heart_check.py TIP_IS_MAX_RY). The negation here is the whole fix; doing it
    in the delta emission instead would hide an axis error inside a loop.
    """
    s = math.sin(t)
    return (16.0 * s * s * s,
            -(13.0 * math.cos(t) - 5.0 * math.cos(2 * t)
              - 2.0 * math.cos(3 * t) - math.cos(4 * t)))


def heart_polyline(n):
    """n points around the whole curve, STARTING AT THE TIP.

    The start point matters for the detector: it wants the drawn stroke to be
    closed, and starting at the tip keeps the pen-down moment at the sharpest
    feature, where a 1 px aim error costs least shape fidelity.

    THE LOOP MUST BE CLOSED BEFORE ROTATION. An earlier version took n points
    over [0, 2pi] (so the last one already repeated the first), then truncated
    the LIST to n-1 and rotated -- which deleted the sample just before the
    start instead of the duplicate, so walking the legs ended one whole sweep
    short of the origin. Measured: "PLAN BUG: legs do not close, residual
    [39, 60]" -- i.e. exactly the first leg's displacement was missing.
    Build the CLOSED ring first, rotate it, then re-append the start.
    """
    ring = [heart_xy(2 * math.pi * i / n) for i in range(n)]   # closed ring of n
    tip = max(range(n), key=lambda i: ring[i][1])
    body = ring[tip:] + ring[:tip]                             # rotate the ring
    body.append(body[0])                                       # close explicitly
    return body


def resample_even(pts, step):
    """Resample a closed polyline at fixed ARC-LENGTH step. Returns integer deltas."""
    acc = [0.0]
    for i in range(1, len(pts)):
        acc.append(acc[-1] + math.hypot(pts[i][0] - pts[i - 1][0],
                                       pts[i][1] - pts[i - 1][1]))
    total = acc[-1]
    count = max(8, int(round(total / step)))
    out = []
    j = 0
    for k in range(count):
        target = total * k / count
        while j < len(acc) - 2 and acc[j + 1] < target:
            j += 1
        span = acc[j + 1] - acc[j]
        f = 0.0 if span <= 0 else (target - acc[j]) / span
        out.append((pts[j][0] + (pts[j + 1][0] - pts[j][0]) * f,
                    pts[j][1] + (pts[j + 1][1] - pts[j][1]) * f))
    out.append(out[0])          # close the loop exactly
    return out


def plan(scale, step, gap_ms):
    poly = heart_polyline(720)
    even = resample_even(poly, step / scale if step else 0.0)
    # integer pixel waypoints, then deltas between consecutive waypoints
    wps = [(round(x * scale), round(y * scale)) for x, y in even]
    # THE LEGS ARE RELATIVE, SO THE START OFFSET CANCELS. An earlier version
    # accumulated the legs from an assumed origin (0,0) and then compared the
    # endpoint against [0,0]; but the glyph does not begin at its own origin --
    # it begins wherever the pointer already is -- so the "residual" was simply
    # the negative of the first waypoint (measured: residual [-39, 60] while
    # the plan was perfectly closed). Sum ONLY the deltas, which is the quantity
    # the emitter actually sends.
    legs = []
    for i in range(1, len(wps)):
        legs.append((wps[i][0] - wps[i - 1][0], wps[i][1] - wps[i - 1][1]))
    residual = (sum(d[0] for d in legs), sum(d[1] for d in legs))
    # the glyph in screen terms is y-DOWN: the model's +y is the tip, so it
    # lands at the BOTTOM of the pad, which is what a heart looks like.
    w = max(p[0] for p in wps) - min(p[0] for p in wps)
    h = max(p[1] for p in wps) - min(p[1] for p in wps)
    return {
        "wps": wps,
        "legs": legs,
        "closure": list(residual),    # must be [0,0]: the deltas sum to nothing
        "w_px": w,
        "h_px": h,
        "reports": len(legs) + 2,     # down + legs + up
        "gap_ms": gap_ms,
        "duration_ms": (len(legs) + 2) * gap_ms,
        "scale": scale,
    }


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--scale", type=float, default=5.0,
                    help="pixels per model unit (glyph is ~32 x 29 units, so 5 -> ~160 x 145 px)")
    ap.add_argument("--step", type=float, default=6.0,
                    help="target pixels between emitted reports")
    ap.add_argument("--gap", type=int, default=12,
                    help="ms between reports; duration = reports * gap, keep < 5000")
    ap.add_argument("--limit-ms", type=int, default=5000,
                    help="hard ceiling on the emitted stroke duration (user constraint)")
    ap.add_argument("--exec", action="store_true", help="run the stroke via .\\kvm.exe")
    ap.add_argument("--human", choices=["true", "false"], default="false")
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args(argv)

    p = plan(a.scale, a.step, a.gap)
    if p["closure"] != [0, 0]:
        # This is an ASSERTION, not a statistic: a non-zero closure means the
        # leg list does not return to its origin and the glyph cannot be a
        # heart. It fires only if resample_even is changed wrongly.
        raise SystemExit("PLAN BUG: legs do not close, residual %r" % (p["closure"],))
    if p["duration_ms"] > a.limit_ms:
        # Honour the user's 5 s ceiling by TIGHTENING the pacing, never by
        # silently dropping legs (dropping legs re-opens the glyph).
        need = max(4, a.limit_ms // p["reports"])
        p["gap_ms"] = need
        p["duration_ms"] = p["reports"] * need
        p["retuned"] = True

    if a.json:
        print(json.dumps(p, indent=2))
        return 0

    cmd = [r".\kvm.exe", "mouse", "rel", "-human=" + a.human,
           "-gap", str(p["gap_ms"]), "down"]
    for dx, dy in p["legs"]:
        cmd += ["drag", str(dx), str(dy)]
    cmd += ["up"]

    sys.stderr.write(
        "heart: %d reports, glyph %dx%d px, closure residual %r, "
        "duration %d ms (gap %d ms)%s\n"
        % (p["reports"], p["w_px"], p["h_px"], p["closure"],
           p["duration_ms"], p["gap_ms"],
           " [gap retuned to meet the %d ms ceiling]" % a.limit_ms
           if p.get("retuned") else ""))
    if p["duration_ms"] > a.limit_ms:
        sys.stderr.write("WARNING: even at gap 4 ms this exceeds %d ms\n" % a.limit_ms)

    if not a.exec:
        print(" ".join(cmd))
        return 0

    r = subprocess.run(cmd)
    sys.stderr.write("kvm exit %d\n" % r.returncode)
    return r.returncode


if __name__ == "__main__":
    sys.exit(main())
