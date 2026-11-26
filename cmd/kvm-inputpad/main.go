package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

// Box is the grid rectangle in screen pixels; X == -1 means "not known yet".
type Box struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
	// VIEWPORT COORDINATES, ALWAYS. X/Y are the canvas rect's left/top as
	// getBoundingClientRect reports them — the same space pos() subtracts from
	// clientX/clientY. They used to be posted with window.scrollX/Y added, i.e.
	// document coordinates, while pos() stayed viewport-relative: on a scrolled
	// page every consumer of this rect was off by exactly scrollX/scrollY, a
	// CONSTANT error that no amount of re-deriving px-per-cell could remove.
	// ScrollX/ScrollY are echoed below for anyone who really wants document
	// coordinates, but nothing internal uses them.
	ScrollX int `json:"scrollX"`
	ScrollY int `json:"scrollY"`
	// JSV is the page-script generation. It exists because a tab keeps running
	// the JS it loaded, so a rebuilt server can be answering a page executing
	// older code — and that was measured here: a trace file ended up mixing
	// points from two generations in one epoch, which makes "which build was
	// this measurement taken against" unanswerable after the fact. A consumer
	// compares JSV against pageJSV and knows, instead of guessing from the
	// presence of a field.
	JSV int `json:"jsv"`
	// Dpr is devicePixelRatio. It is echoed so a caller can see whether the
	// page's CSS pixels and the tool's screen pixels can differ by a factor,
	// instead of assuming they cannot.
	Dpr float64 `json:"dpr"`
}

// Mapping is one screen point converted to a pad cell. It mirrors the page's
// pos(): the same clamps, the same floor, and the SAME per-axis divisor.
type Mapping struct {
	// Cell is the X-axis cell, kept for old callers. It is NOT a pair: a
	// reader that needs both axes must take X and Y (see Point).
	Cell int `json:"cell"`
	// X, Y are the cell on EACH axis. They are filled by mapCell itself, so
	// they are always right no matter how the caller obtained the Mapping.
	// Measured bug this prevents: an earlier version left them at their zero
	// value and the /api/map handler copied Cell into both, so a caller that
	// did not pass ox/oy got y := the x-axis cell and every answer lay on
	// the diagonal.
	X int `json:"x"`
	Y int `json:"y"`
	// Point is the cell as a pair, so a reader can never take half of it.
	Point [2]int `json:"point"`
	// InGrid is false when the queried point fell outside the rect and was
	// therefore clamped to an edge cell. A caller must check it before
	// trusting X/Y: a clamped answer is a legal cell index that means
	// "off the grid", not "here".
	InGrid bool `json:"inGrid"`
	// Ox, Oy and CssX, CssY echo the space conversion: the offset the caller
	// supplied and the point it produced. Together with Box they make every
	// answer reproducible by hand, which is the only way a "the cell is wrong"
	// report can be settled instead of argued about.
	Ox   int     `json:"ox"`
	Oy   int     `json:"oy"`
	CssX float64 `json:"cssX"`
	CssY float64 `json:"cssY"`
	// Fx, Fy are the in-cell offsets the page would record for that point,
	// so a caller can compare against a trace entry without re-deriving them.
	Fx float64 `json:"fx"`
	Fy float64 `json:"fy"`
	// Box is the rect this conversion used. Echoing it is what makes a wrong
	// answer debuggable: a tool that disagrees can see WHICH rect was used.
	Box Box `json:"box"`
}

// mapCellAxis is one axis of the page's pos() applied to a CSS-pixel offset.
func mapCellAxis(d float64, span int) int {
	return int(math.Floor(clampCell(d * cells / float64(span))))
}

// mapCell is the inverse of the page's pos() for one screen point.
//
// INVARIANT: x is divided by the box WIDTH and y by the box HEIGHT, never by a
// single px-per-cell for both. The canvas is a fixed cells*cellPx buffer in a
// CSS box that max-width and max-height can make NON-SQUARE (measured on this
// rig: 768x573 after a webview resize), and that anisotropy is exactly why a
// constant 6.00 px/cell map read a few cells right of the cursor near the left
// edge. The clamp mirrors the page's, so a point off the grid reports cell 0
// with fx 0 or the last cell with fx just under 1 instead of a negative index.
func mapCell(b Box, cx, cy float64) (Mapping, bool) {
	u := (cx - float64(b.X)) * cells / float64(b.W)
	v := (cy - float64(b.Y)) * cells / float64(b.H)
	in := u >= 0 && u < cells && v >= 0 && v < cells
	u = clampCell(u)
	v = clampCell(v)
	x, y := math.Floor(u), math.Floor(v)
	xi, yi := int(x), int(y)
	return Mapping{Cell: xi, X: xi, Y: yi, Point: [2]int{xi, yi}, Fx: u - x, Fy: v - y}, in
}

// cssPoint converts a screen pixel into the page's CSS pixel space, the space
// Box is expressed in.
//
// THE RECT THE PAGE PUBLISHES IS VIEWPORT-RELATIVE CSS PX (rect.left/rect.top,
// plus window.scrollX/Y), never desktop pixels. Feeding a SCREEN pixel straight
// to mapCell mixes the two spaces and is off by the whole window frame,
// toolbar and webview offset: measured on this rig at 100 percent scale, the
// webview begins at screen (1117,105), so the error is ~1100 px and the clamp
// turned every answer into "cell 127". ox/oy IS THAT OFFSET -- the caller must
// pass it; the endpoint refuses the request without it rather than silently
// answering from the wrong space.
func cssPoint(sx, sy, ox, oy int) (float64, float64) {
	return float64(sx - ox), float64(sy - oy)
}

// clampCell reproduces the page's clamp to [0, N-0.000001] — the epsilon keeps
// a point exactly on the right/bottom edge inside the last cell.
func clampCell(f float64) float64 {
	if f < 0 {
		return 0
	}
	if top := float64(cells) - 0.000001; f > top {
		return top
	}
	return f
}

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

// startedAt is the process start instant, captured once so /api/whoami can
// answer "how long has this server been up?" — the second half of the
// stale-binary question. A build stamp says WHAT is running; an uptime says
// whether that stamp is the one currently serving, or a leftover from an
// earlier launch that still owns the port.
var startedAt = time.Now()

func main() {
	addr := flag.String("addr", "127.0.0.1:8899", "listen address")
	out := flag.String("out", "strokes.d", "DIRECTORY holding one trace file per client plus events.jsonl")
	box := flag.String("box", "", "grid rectangle X,Y,W,H in screen px (published at /api/box)")
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

	var g Box
	var gMu sync.Mutex
	g.X = -1
	if *box != "" {
		if _, err := fmt.Sscanf(*box, "%d,%d,%d,%d", &g.X, &g.Y, &g.W, &g.H); err != nil {
			log.Fatalf("bad -box %q (want X,Y,W,H)", *box)
		}
		log.Printf("grid pinned to %d,%d %dx%d", g.X, g.Y, g.W, g.H)
	}
	http.HandleFunc("/api/box", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			var b Box
			if err := json.NewDecoder(r.Body).Decode(&b); err == nil && b.W > 0 && b.H > 0 {
				// A page that reports its live rect always wins over -box: the
				// flag describes where the canvas was when the server started,
				// the POST describes where it is now.
				gMu.Lock()
				g = b
				gMu.Unlock()
			}
		}
		gMu.Lock()
		out := g
		gMu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	})

	// /api/map is the ONE authority for screen px -> pad cell. The page itself
	// converts with pos() = (clientX - rect.left) * N / rect.width, so the
	// inverse has to be built from the SAME rect the page used, with X and Y
	// scaled SEPARATELY: the canvas is a fixed 768x768 buffer in a CSS box that
	// max-width/max-height can make non-square, and a single px-per-cell for
	// both axes is then wrong on one of them (measured: a click a few cells
	// right of the cursor, worst near the left edge, whenever the webview was
	// resized without the page re-posting its rect).
	http.HandleFunc("/api/map", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "screen->cell is a GET")
			return
		}
		sx, err1 := strconv.Atoi(r.URL.Query().Get("x"))
		sy, err2 := strconv.Atoi(r.URL.Query().Get("y"))
		if err1 != nil || err2 != nil {
			writeJSONError(w, http.StatusBadRequest, "want integer x and y (screen px)")
			return
		}
		gMu.Lock()
		b := g
		gMu.Unlock()
		if b.X < 0 || b.W <= 0 || b.H <= 0 {
			writeJSONError(w, http.StatusConflict, "the page has not reported its rect yet")
			return
		}
		// sx/sy are SCREEN pixels; the page's rect is in CSS pixels relative to
		// its viewport, so ox/oy is MANDATORY: it is the screen position of the
		// page viewport's top-left (window frame + toolbar + webview chrome).
		// Without it every point lands outside the rect and clamps to the last
		// cell — measured: an earlier version answered cell 127 for x=1136
		// through 1898. There is no sane default; refusing is the honest answer.
		//
		// ox/oy only convert spaces. X and Y are both computed per axis below,
		// from the SAME cssPoint, so they can never disagree. The earlier
		// version left Y at the x-axis cell whenever oy was missing, i.e. put
		// every answer on the diagonal.
		ox, errOx := strconv.Atoi(r.URL.Query().Get("ox"))
		oy, errOy := strconv.Atoi(r.URL.Query().Get("oy"))
		if errOx != nil || errOy != nil {
			writeJSONError(w, http.StatusBadRequest,
				"want ox and oy: the screen position of the page viewport's top-left corner")
			return
		}
		cx, cy := cssPoint(sx, sy, ox, oy)
		m, in := mapCell(b, cx, cy)
		m.Box = b
		m.Ox, m.Oy = ox, oy
		m.CssX, m.CssY = cx, cy
		m.InGrid = in
		_ = json.NewEncoder(w).Encode(m)
	})

	store := newTraceStore(*out)
	http.HandleFunc("/api/log", store.handlePad)

	// /api/whoami answers "which binary is serving me?" without touching the
	// pad, the pointer, or the trace. It exists because the answer was
	// previously only obtainable with Get-Process plus a StartTime comparison,
	// and that was not done before three measurement runs — so they certified a
	// stale binary and had to be marked void. Read-only, GET only.
	http.HandleFunc("/api/whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "whoami is a GET")
			return
		}
		var mod string
		if fi, err := os.Stat(exe); err == nil {
			mod = fi.ModTime().UTC().Format(time.RFC3339)
		}
		gMu.Lock()
		b := g
		gMu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"program":  "kvm-inputpad",
			"build":    buildStamp,
			"exe":      exe,
			"exe_mtime": mod,
			"pid":      os.Getpid(),
			"addr":     *addr,
			"out":      *out,
			"started":  startedAt.UTC().Format(time.RFC3339),
			// The box is included because "the page has not reported its rect
			// yet" (x == -1) is the single most common reason /api/map refuses,
			// and it is otherwise indistinguishable from a broken server.
			"box": b,
		})
	})

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
	return strings.Replace(page, "<!--PAD_MODULES-->", inline, 1)
}


//go:embed page.html
var page string

//go:embed pad-core.js
var padCore string

//go:embed pad-events.js
var padEvents string
