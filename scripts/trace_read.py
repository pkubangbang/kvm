#!/usr/bin/env python3
"""Read the pad's accumulated trace from the SERVER's evidence, one way.

The pad stores one JSON file per client under `-out <dir>` and no longer keeps a
GET-able copy in memory, so every measurement script needs the same three steps:
find the directory, find the right client file, and normalise the points. They
used to each shell out to the server's log endpoint, which broke the moment the
server stopped answering GET from memory.

    from trace_read import read_trace
    trace = read_trace(dir_or_None)      # -> [[{x,y,fx,fy,ts,epoch}, ...], ...]

Resolution order for the directory:
    1. the `dir` argument;
    2. $KVM_PAD_OUT;
    3. a -out seen in a running kvm-inputpad's argv (best effort);
    4. "strokes.d", the server's own default.

The newest `.json` file in the directory is the client that drew last. Pass
`client=` to pin one; pass `path=` to read one file directly.

POINT NORMALISATION — the one rule that matters: fx/fy are the position INSIDE
cell (x,y) and default to 0.0 when the key is absent, because an absent key
means "this sample was recorded before offsets existed", i.e. the pointer was on
the cell origin; 0.5 (the cell centre) would silently move 1 point in 6 by half
a cell and bias every length and corner metric. Servers from the omitempty era
dropped both keys whenever the value rounded to 0, which is exactly the wrong
signal to invent a 0.5 for.
"""

import json
import os
import re
import subprocess
import sys

DEFAULT_DIR = "strokes.d"
PX_DEFAULT = 6.0

# fx/fy are an offset inside the cell; absent means the cell origin, never the
# centre. Anything reading a point must use these two helpers, not .get(...,0.5).
FX_DEFAULT = 0.0
FY_DEFAULT = 0.0


def px_per_cell(y=False):
    """Screen px per pad cell. The single source for the magic 6.

    Reads the pad's calibration file (px_per_cell / px_per_cell_y) next to this
    script and falls back to the module default when it is absent. Every metric
    used to carry its own PX = 6.0, while the measured pair is 6.024/5.955 —
    a 0.4 % error in X and 0.8 % in Y that moved every "COVERAGE %" headline.
    """
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "pad_calib.json")
    try:
        with open(path) as fh:
            c = json.load(fh)
        return float(c["px_per_cell_y" if y else "px_per_cell"])
    except (OSError, ValueError, KeyError, TypeError):
        return PX_DEFAULT


def _running_out():
    """Best-effort: the -out of a live kvm-inputpad, if one is running."""
    try:
        ps = subprocess.run(["pgrep", "-af", "kvm-inputpad"],
                            capture_output=True, text=True, timeout=3)
    except Exception:
        return None
    for line in ps.stdout.splitlines():
        m = re.search(r"-out\s+(\S+)", line)
        if m:
            return m.group(1)
    return None


def resolve_dir(directory=None):
    if directory:
        return directory
    env = os.environ.get("KVM_PAD_OUT")
    if env:
        return env
    live = _running_out()
    if live:
        return live
    return DEFAULT_DIR


def list_traces(directory=None, client=None):
    """Trace files present, newest first (by mtime, then by name)."""
    d = resolve_dir(directory)
    if not os.path.isdir(d):
        raise FileNotFoundError(
            "pad trace directory %r does not exist (pass the pad's -out dir)" % d)
    if client:
        safe = re.sub(r"[^A-Za-z0-9_-]", "", client)[:64] or "anonymous"
        p = os.path.join(d, safe + ".json")
        if not os.path.isfile(p):
            raise FileNotFoundError("no trace file for client %r (%s)" % (client, p))
        return [p]
    files = [os.path.join(d, n) for n in os.listdir(d)
             if n.endswith(".json") and not n.endswith(".corrupt")]
    files = [p for p in files if os.path.isfile(p)]
    files.sort(key=lambda p: (os.path.getmtime(p), p), reverse=True)
    return files


def points_of(stroke):
    """A stroke as continuous cell coordinates (x+fx, y+fy), offsets default 0."""
    return [(p["x"] + p.get("fx", FX_DEFAULT), p["y"] + p.get("fy", FY_DEFAULT))
            for p in stroke]


def load_file(path):
    """Parse one trace file; an unreadable/empty file is an empty trace."""
    try:
        with open(path) as fh:
            data = json.load(fh)
    except (OSError, ValueError) as e:
        raise ValueError("cannot read trace %s: %s" % (path, e))
    if data is None:
        return []
    if not isinstance(data, list):
        raise ValueError("trace %s is not an array of strokes" % path)
    return [s for s in data if isinstance(s, list) and s]


def read_trace(directory=None, client=None, path=None):
    """The accumulated trace: a list of strokes, newest client file by default."""
    if path:
        return load_file(path)
    files = list_traces(directory, client)
    if not files:
        # A pad that has not committed anything has no file, which is an EMPTY
        # trace, not an error: a caller that cleared the pad then measures must
        # see zero strokes rather than a crash.
        return []
    return load_file(files[0])


def read_events(directory=None):
    """The events.jsonl of the session, as a list of dicts (empty if absent)."""
    d = resolve_dir(directory)
    p = os.path.join(d, "events.jsonl")
    out = []
    try:
        with open(p) as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    out.append(json.loads(line))
                except ValueError:
                    continue          # a torn last line must not kill the report
    except OSError:
        return []
    return out


def newest_epoch(strokes):
    """The greatest epoch seen in a trace, 0 when the pad never set one."""
    best = 0
    for s in strokes:
        for p in s:
            e = p.get("epoch") or 0
            if e > best:
                best = e
    return best


def main():
    import argparse
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("dir", nargs="?", help="the pad's -out directory")
    ap.add_argument("--client", help="a specific client id")
    ap.add_argument("--path", help="read this trace file directly")
    ap.add_argument("--file", action="store_true", help="print the source path only")
    ap.add_argument("--points", action="store_true",
                    help="print one 'x y' per point of every stroke instead of JSON")
    a = ap.parse_args()
    trace = read_trace(a.dir, a.client, a.path)
    if a.file:
        files = [a.path] if a.path else list_traces(a.dir, a.client)
        print(files[0] if files else "")
        return 0
    if a.points:
        for i, s in enumerate(trace):
            print("# stroke %d (%d pts)" % (i, len(s)))
            for x, y in points_of(s):
                print("%.4f %.4f" % (x, y))
        return 0
    json.dump(trace, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
