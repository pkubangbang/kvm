#!/usr/bin/env python3
"""Round-trip test for the kvm inputpad export contract (plan Section 3).

The contract, in the user's words:
  * the CLIENT keeps the record as Stroke = PointAndTs[] ({ts,x,y} per point),
  * it hands the whole trace over in ONE request,
  * and only after the reply does it clear the pad.

This script asserts every clause that can be observed from outside the browser,
plus the failure path (no server -> the record must NOT be lost). The clauses that
live in the page (record kept, clear after reply) are asserted through the same
endpoints the page uses: a POST is the hand-over, a GET read-back is what the page
would clear on.

Exit code 0 only when every check passes; each check prints PASS/FAIL so a
failure names itself.
"""
import json
import os
import subprocess
import sys
import time
import urllib.request

BASE = "http://127.0.0.1:8898"
OUT = "/tmp/pad-strokes.json"
failures = []


def check(name, ok, detail=""):
    print(("PASS " if ok else "FAIL ") + name + ((" | " + detail) if detail else ""))
    if not ok:
        failures.append(name)


def http(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        raw = r.read().decode()
    return raw


def get_json(path):
    return json.loads(http("GET", path))


def post_json(path, body):
    return json.loads(http("POST", path, body))


# ---- 1. the grid rect is published (Section 1) -----------------------------
box = get_json("/api/box")
check("grid rect published", box.get("w", 0) > 0 and box.get("h", 0) > 0, json.dumps(box))

# ---- 2. two strokes, hand them over in ONE request -------------------------
strokes = [
    [{"ts": 100, "x": 10, "y": 10}, {"ts": 116, "x": 20, "y": 10}, {"ts": 132, "x": 30, "y": 10}],
    [{"ts": 140, "x": 40, "y": 10}, {"ts": 156, "x": 40, "y": 30}, {"ts": 172, "x": 40, "y": 50}],
]
reply = post_json("/api/trace", strokes)
check("reply acknowledges the whole trace",
      reply.get("ok") is True and reply.get("strokes") == 2 and reply.get("points") == 6,
      json.dumps(reply))

# ---- 3. the JSON file is exactly Stroke[] of {ts,x,y} ----------------------
on_disk = json.load(open(OUT))
check("file holds 2 strokes", len(on_disk) == 2, str(len(on_disk)))
check("file matches what was sent", on_disk == strokes)
schema_ok = all(
    set(p.keys()) == {"ts", "x", "y"} and all(isinstance(p[k], int) for k in ("ts", "x", "y"))
    for st in on_disk for p in st
)
check("every point is {ts,x,y} ints", schema_ok)
ts_ok = all(all(st[i]["ts"] <= st[i + 1]["ts"] for i in range(len(st) - 1)) for st in on_disk)
check("timestamps are non-decreasing within a stroke", ts_ok)

# ---- 4. read-back is the same trace (what the page clears on) -------------
check("GET read-back equals what was exported", get_json("/api/trace") == strokes)

# ---- 5. failure path: server down -> nothing is lost on disk ---------------
before = open(OUT).read()
proc = subprocess.Popen(["pkill", "-f", "kvm-inputpad -addr 127.0.0.1:8898"],
                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
proc.wait()
time.sleep(0.6)
try:
    http("POST", "/api/trace", [{"ts": 1, "x": 1, "y": 1}])
    server_down = False
except Exception:
    server_down = True
check("server is down for the failure-path check", server_down)
check("file untouched by the failed hand-over", open(OUT).read() == before)

print()
if failures:
    print("FAILED: " + ", ".join(failures))
    sys.exit(1)
print("ALL CHECKS PASSED")
