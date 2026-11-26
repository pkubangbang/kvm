package main

import (
	"embed"
	_ "embed"
	"flag"
	"io/fs"
	"log"
	"os"
	"net/http"
	"strings"
	"time"
)

const (
	cells  = 128
	cellPx = 6
)

// Point is one sample of the client's record. The JSON shape is the client's:
type Point struct {
	Ts int `json:"ts"`
	X  int `json:"x"`
	Y  int `json:"y"`
	// Fx, Fy are the position INSIDE cell (X,Y), each in [0,1). X,Y alone are
	// a 6 px lattice: recording only the lattice quantises every glide to cell
	// crossings and loses any reversal that happens within one cell (the turn
	// then reads as a dead zone). The offsets carry the sub-cell path, so the
	// record can be rendered as the line that was actually drawn.
	Fx float64 `json:"fx"`
	Fy float64 `json:"fy"`
	// Rx, Ry are the AUTHORITATIVE position: pure ratios of the canvas's CSS
	// box, in [0,1]. They are what the page draws from, and they are the only
	// quantity that survives a change of display size or a squashed box:
	// x/y/fx/fy are rounded to a 128 lattice and, worse, were derived from a
	// stretched box so a fixed px-per-cell cannot recover them.
	//
	// THE SERVER MUST KEEP THEM. This struct is the decode AND the encode
	// shape of the pad's stroke record, so a field missing here is silently dropped on the
	// way to disk — measured: points written by the new page arrived with
	// keys ts,x,y,fx,fy,epoch and the rx/ry the page had just started sending
	// were gone, which made every later reader fall back to the lattice and
	// quietly lose the sub-cell path. If you add a field to the page's sample,
	// add it here in the same commit.
	Rx float64 `json:"rx"`
	Ry float64 `json:"ry"`
	// Epoch is the page generation the stroke was drawn in. The server bumps
	// its epoch on every explicit clear, so strokes from an older generation
	// (a tab still holding pre-clear history) can never be re-added.
	Epoch int `json:"epoch,omitempty"`
}

// Stroke is PointAndTs[].
type Stroke []Point


// buildStamp identifies THIS binary. It is set at link time with
//
//	go build -ldflags "-X main.buildStamp=$(git rev-parse --short HEAD)" ./cmd/kvm-inputpad
//
// and falls back to "dev" when built without it. A server that cannot say which
// binary it is has already cost a whole measurement session: an OLD
// kvm-inputpad-final.exe kept serving :8898 while the freshly built
// kvm-inputpad.exe sat on disk unopened, so every run certified the wrong
// program and the verdict had to be marked void. Publishing the stamp makes
// that mistake visible in one request instead of one forensics session.
var buildStamp = "dev"

// releaseStamp is the BUILD-ASSET RELEASE STAMP: the moment the three embedded
// assets (page.html, pad-core.js, pad-events.js) were last modified, captured
// INTO THE BINARY at compile time. It is not a runtime reading — it is baked in,
// so it says "the page you are being served was built from assets as of T".
//
// WHY THIS EXISTS. The page is //go:embed-ed, so the server hands out the page
// it was COMPILED with, never the one on disk. Measured on this rig: the running
// process had started 13:59:05 while page.html was modified 14:17:44, so the
// webview served an eighteen-minute-stale page all session — and a webview
// reload did not help, because it re-fetched the same embedded bytes. The
// symptom was a corner marker that no source file drew. Comparing this stamp
// against the assets' mtime on disk answers "is the server stale?" in ONE
// request instead of an afternoon of forensics.
//
// It is deliberately derived from the ASSETS, not from the binary: a rebuild
// that changes only Go code is not a page change, and a page change without a
// rebuild is exactly the bug this catches.
//
//nolint:gochecknoglobals // set once at init, read-only thereafter
var releaseStamp = assetsStamp()

// assetsStamp is the BUILD RELEASE STAMP: "<commit>[-dirty], assets <mtime>".
// It answers, in ONE request, "is the server serving the newest build?" — the
// question that cost a whole session when a stale binary owned the port.
//
// TWO halves, and the first one is the half that must not be able to lie:
//   * the COMMIT comes from the build itself (-ldflags -X main.buildStamp=...),
//     so it names the exact tree the binary was compiled from;
//   * the ASSET mtime comes from the embed table the compiler wrote, so it
//     survives later edits to the working tree (os.Stat on the source dir does
//     NOT: it answers "what is on disk", not "what did this binary embed").
// A build without -ldflags reports buildStamp "dev" — that is deliberate, and
// it is visible in the served page's data-build attribute.
func assetsStamp() string {
	var newest time.Time
	for _, fsys := range []embed.FS{pageStamp, padCoreStamp, padEventsStamp} {
		if t, ok := embedMTime(fsys); ok && t.After(newest) {
			newest = t
		}
	}
	if newest.IsZero() {
		return buildStamp
	}
	return buildStamp + ", assets " + newest.UTC().Format(time.RFC3339)
}


// startedAt is the process start instant, captured once at startup. A build
// stamp says WHAT is running; an uptime says whether that stamp is the one
// currently serving, or a leftover from an earlier launch that still owns the
// port — the second half of the stale-binary question.
var startedAt = time.Now()

func main() {
	addr := flag.String("addr", "127.0.0.1:8899", "listen address")
	out := flag.String("out", "strokes.d", "DIRECTORY holding one trace file per client plus events.jsonl")
	flag.Parse()

	// The identity line goes out FIRST, before any handler is registered, so a
	// terminal that started this process always shows what is running. It
	// carries the pid and the resolved out dir too: two pads on two ports are
	// otherwise indistinguishable in a process list.
	exe, _ := os.Executable()
	built := "unknown"
	if fi, err := os.Stat(exe); err == nil {
		built = fi.ModTime().Format("2006-01-02 15:04:05")
	}
	log.Printf("kvm-inputpad identity: pid=%d build=%s exe=%s built=%s addr=%s out=%s",
		os.Getpid(), buildStamp, exe, built, *addr, *out)

	store := newTraceStore(*out)
	http.HandleFunc("/api/log", store.handlePad)

	doc := pageWithModules()
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			writeJSONError(w, http.StatusNotFound, "no such path")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "the page is served over GET")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// A tab keeps running the page JS it loaded; without this Chrome may also
		// serve it from its own cache after a restart, so a fix in the page would
		// not reach a tab that merely reloads.
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(doc))
	})

	srv := &http.Server{
		Addr:              *addr,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	log.Printf("kvm-inputpad on http://%s (grid %dx%d cells, trace %s)", *addr, cells, cells, *out)
	log.Fatal(srv.ListenAndServe())
}


// THE PAGE IS THREE ASSETS, INLINED AT LOAD TIME.
//
//   page.html     — chrome + CSS + the canvas/readout/transport plumbing
//   pad-core.js   — THE STROKE STATE MACHINE: down -> drag* -> up over data,
//                   with no DOM, no timers it does not own, and no global latch
//   pad-events.js — the DOM event layer: translates browser events into calls
//                   on the machine, forwards EVERY terminal signal, owns no ink
//
// They are kept as separate files because the capture logic they replace was
// four listeners sharing three module globals plus a 40 ms settle timer; that
// arrangement is why "the mouse down, move and up events are not correctly
// captured" could not be tested outside a browser. Splitting the machine out
// makes each transition drivable from `node --test`.
//
// They are SERVED INLINED rather than as separate <script src> routes, and the
// reason is measured, not stylistic: this page is the VS Code webview pane on
// 127.0.0.1:8898. A subresource fetch over that origin failed here (the served
// page rendered but its module 404'd, and the pad then had no state machine at
// all). One document with the modules inlined cannot have that failure, and the
// source files keep the reviewable, individually-testable shape.
const (
	assetPadCore   = "pad-core.js"
	assetPadEvents = "pad-events.js"
)

// pageWithModules returns page.html with the two module files inlined as the
// last scripts, in dependency order. A missing asset is a BUILD-level defect,
// so it panics at startup rather than serving a page that silently cannot draw.
func pageWithModules() string {
	core := strings.TrimSpace(padCore)
	events := strings.TrimSpace(padEvents)
	if core == "" || events == "" {
		panic("pad assets are empty: pad-core.js and pad-events.js must be embedded")
	}
	inline := "<script>\n" + core + "\n;window.createPadCore = createPadCore;\n</script>\n" +
		"<script>\n" + events + "\n;window.bindPad = bindPad;\n</script>\n"
	out := strings.Replace(page, "<!--PAD_MODULES-->", inline, 1)
	// The QR symbol travels as a plain JS array literal: the page paints it, so
	// the page must receive it. It is generated (qr_table.go) and never edited by
	// hand; see that file's header for the payload and the reference generator.
	var sym strings.Builder
	sym.WriteString("<script>\nwindow.PAD_EYES = " + eyesJS + ";\n</script>\n")
	out = strings.Replace(out, "<!--PAD_QR-->", sym.String(), 1)

	// Stamp the page with the release value the server was compiled with, so
	// the webview can be checked by eye and without a screenshot: the served
	// page carries data-release/data-build and names its own build. There is no
	// read-back endpoint for this — the page is the only surface that reports it.
	return strings.Replace(out, "<span id=stamp",
		"<span id=stamp data-release=\""+releaseStamp+"\" data-build=\""+buildStamp+"\"", 1)
}


//go:embed page.html
var page string

//go:embed pad-core.js
var padCore string

//go:embed pad-events.js
var padEvents string

// The stamps below are one-element embed.FS views of the SAME three assets.
// They exist only to carry the mtime the compiler recorded for each file:
// embed.FS.FileInfo().ModTime() is that recorded mtime, so it survives any
// later edit to the working tree. Without them the release stamp had to stat
// the source directory at startup and reported "unknown" for a binary built
// from a different cwd — which is exactly how the stamp failed to prove
// anything the first time it was tried.
//
//go:embed page.html
var pageStamp embed.FS

//go:embed pad-core.js
var padCoreStamp embed.FS

//go:embed pad-events.js
var padEventsStamp embed.FS

// embedMTime reads the mtime the compiler recorded for the single file in a
// one-element embed.FS. Reports false when the file is absent from the table.
func embedMTime(fsys embed.FS) (time.Time, bool) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil || len(entries) == 0 {
		return time.Time{}, false
	}
	fi, err := entries[0].Info()
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}
