#!/usr/bin/env python3
"""Strip narration comments from Go sources, keep only what the code needs.

Keeps, in this order of importance:
  * `//go:build` / `// +build` constraints (build correctness),
  * a doc comment whose opening line starts with the identifier it documents
    (`// Foo ...` / `// foo ...`) — that is the Go convention godoc reads,
  * `//` comments the compiler needs: `//go:`, `//nolint`, `//export`, cgo,
  * blank-line structure of the code itself.

Drops every other `//` line-comment, however long, and every trailing
`//` comment on a code line. Block comments (`/* */`) are kept only when they
are doc comments or build constraints.

Usage: strip_comments.py <file> [...]     (rewrite in place; prints a summary)
"""
import re
import sys

BUILD = re.compile(r"^//\s*(go:build|\+build|go:generate|go:linkname|go:embed|go:noinline|go:nosplit|go:noescape|export |nolint|cgo )")
DOC = re.compile(r"^//\s*([A-Za-z_][A-Za-z0-9_]*)\b")


def is_doc(first_line: str, ident: str) -> bool:
    m = DOC.match(first_line)
    return bool(m and m.group(1).lower() == ident.lower())


def strip(src: str) -> str:
    lines = src.splitlines()
    out = []
    i = 0
    n = len(lines)
    while i < n:
        line = lines[i]
        s = line.strip()

        # block comment
        if s.startswith("/*"):
            block = []
            while i < n:
                block.append(lines[i])
                if "*/" in lines[i]:
                    i += 1
                    break
                i += 1
            body = "\n".join(block)
            keep = body.lstrip().startswith("/*") and ("go:build" in body or "Package " in body)
            if keep:
                out.extend(block)
            continue

        # line comment: gather the contiguous comment paragraph
        if s.startswith("//"):
            para = []
            while i < n and lines[i].strip().startswith("//"):
                para.append(lines[i])
                i += 1
            first = para[0].strip()
            if BUILD.match(first):
                out.extend(para)
                continue
            # a doc comment: first line names the following declaration
            j = i
            while j < n and lines[j].strip() == "":
                j += 1
            ident = None
            if j < n:
                m = re.match(r"^(?:func|type|var|const)\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)", lines[j].strip())
                if m:
                    ident = m.group(1)
            if ident and is_doc(first, ident):
                out.append(para[0])
            continue

        # code line: drop trailing // comment that is not a compiler directive
        code = line
        idx = code.find("//")
        if idx > 0:
            head = code[:idx]
            # keep `//` inside string literals: crude but adequate — quote count
            if head.count('"') % 2 == 0 and head.count("`") % 2 == 0:
                tail = code[idx:]
                if not BUILD.match(tail.strip()):
                    code = head.rstrip()
        out.append(code)
        i += 1

    # collapse runs of >2 blank lines to one (a doc paragraph needs none)
    res = []
    blanks = 0
    for l in out:
        if l.strip() == "":
            blanks += 1
            if blanks > 1:
                continue
        else:
            blanks = 0
        res.append(l)
    return "\n".join(res).rstrip() + "\n"


def main(argv):
    total_before = total_after = 0
    for path in argv:
        src = open(path).read()
        new = strip(src)
        before, after = len(src.splitlines()), len(new.splitlines())
        total_before += before
        total_after += after
        open(path, "w").write(new)
        print(f"{path}: {before} -> {after} lines")
    print(f"total: {total_before} -> {total_after} lines")


if __name__ == "__main__":
    main(sys.argv[1:])
