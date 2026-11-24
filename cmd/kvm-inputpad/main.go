package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
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
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8899", "listen address")
	out := flag.String("out", "strokes.d", "DIRECTORY holding one trace file per client plus events.jsonl")
	box := flag.String("box", "", "grid rectangle X,Y,W,H in screen px (published at /api/box)")
	flag.Parse()

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

	store := newTraceStore(*out)
	http.HandleFunc("/api/trace", store.handleTrace)

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
		// serve it from its own cache after a restart, so a fix in page would not
		// reach a tab that merely reloads.
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(page))
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

const page = `<!doctype html><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<title>kvm inputpad 128x128</title>
<style>
 body{margin:0;font:13px system-ui;background:#111;color:#eee;
      /* Canvas + both bars are a column that exactly fills the viewport: the
         title bar is pinned to the top, the readout bar to the bottom, and the
         canvas in between shrinks instead of pushing the footer out of view.
         Without this the bottom bar sat right under the 768 px canvas and the
         page could scroll, so the footer moved with the canvas rather than
         staying at the window's bottom edge. */
      min-height:100vh;display:flex;flex-direction:column}
 /* The action bar is a FIXED-HEIGHT row above a FIXED-SIZE canvas. It used to
    be a flex row whose height followed its contents, so its buttons could slide
    under the canvas edge and a mis-aimed park pressed a button. Neither the bar
    nor the buttons may now overlap the grid: the bar is exactly 40 px tall and
    the buttons live inside it, never over the canvas. */
 /* Every header control has a FIXED width and the bar is a rigid grid, so no
    readout can resize as its text changes ("cell 0,33" -> "cell 128, 127",
    "0 strokes" -> "12 strokes", a longer status line). Before, all items were
    content-sized and flowed, so a cell readout update slid the buttons sideways
    and a park aimed at a button could land elsewhere. Fixed slots only. */
 /* Readouts do NOT belong in the title bar. Squeezing title + buttons + three
    readouts into one 40 px row made every slot clip (a ~935 px window cannot
    hold them at a readable width): "cell 88, ⋯", "1 stro⋯". So the chrome is
    split into two slim bars around the grid:
      header: title on the left, actions on the right (never moves);
      footer: cell coordinate | stroke count | live status.
    Both bars are exactly 40 px so the canvas keeps its calibrated position
    (the -box rect is measured against the canvas, not the window). */
 .bar{display:flex;align-items:center;height:40px;box-sizing:border-box;
      padding:0 10px;overflow:hidden;column-gap:10px}
 .bar.top{justify-content:space-between}
 .bar.bot{justify-content:flex-start;gap:14px;font:12px/20px ui-monospace,monospace;
           /* the status cell is the LAST column, flush to the right edge */}
 .bar.bot #live{margin-left:auto}
 /* The title line box must be TALLER than the glyphs: at 14 px text the
    system-ui descender ('p' in Inputpad) reaches below a 1-line box, and with
    line-height:1 the span cut its own text. A 20 px line box plus
    overflow:visible gives the glyphs room; the 40 px bar keeps centring it. */
 .title{font:600 14px/20px system-ui;letter-spacing:.3px;flex:none;
        overflow:visible}
 .actions{display:flex;gap:8px;flex:none}
 .bar button{height:26px;box-sizing:border-box}
 .bar span{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
 /* Fixed slots for the two counters, so a value that grows a digit does not
    push its neighbours; the status text alone takes the slack. */
 /* Fixed slots: each readout keeps one width whatever its value ("cell 0, 0"
    up to "cell 127, 127", "off-grid", "0 strokes" up to "99 exported (999
    pts)"), so growing text never shifts its neighbours. The cell slot is sized
    for the WIDEST real value plus the 'off-grid' fallback, measured in the
    bar's monospace 12 px font (~7.2 px/char): 'cell 127, 127' is 13 chars and
    'off-grid' 8, so 15ch leaves slack for a scrollbar-gutter change. */
 #hud{flex:none;width:15ch}
 #count{flex:none;width:16ch}
 #live{flex:none;width:30ch;min-width:30ch;text-align:right;color:#9aa}
 #pad{display:block;margin:0 auto;background:#fff;cursor:crosshair;
      box-shadow:0 0 0 1px #555;
      /* The 768x768 DRAWING BUFFER is fixed: cell math and the -box rect are in
         these pixels, so the buffer must never change size. What changes is its
         CSS size: it is capped to the space left between the two 40 px bars and
         keeps its 1:1 aspect, so the whole grid stays visible and reachable. A
         canvas that overflows the viewport leaves its lower rows undrawable and
         unscrollable under the footer, which is what the old fixed 768 px CSS
         height did. pos() divides by the live CSS rect, so pointer input maps
         onto the same 128x128 lattice at any scale.
         touch-action:none keeps a touch/pen drag from being claimed as a pan
         (a claimed drag arrives as pointercancel, i.e. a truncated stroke). */
      touch-action:none;-webkit-user-select:none;user-select:none;
      width:768px;height:768px;
      max-width:calc(100vw - 20px);max-height:calc(100vh - 80px);
      aspect-ratio:1/1;object-fit:contain}
 /* The bottom bar is the page footer: it sits at the window's bottom edge,
    not 19 px under the canvas, so the canvas can grow/shrink around it. */
 .bar.bot{margin-top:auto}
</style>
<div class="bar top">
  <span class="title">Inputpad</span>
  <span class="actions">
    <button id=clear>Clear</button>
    <button id=save>Export</button>
  </span>
</div>
<canvas id=pad width=768 height=768></canvas>
<div class="bar bot">
  <span id=hud>cell —,—</span>
  <span id=count>0 strokes</span>
  <span id=live></span>
</div>
<script>
const N=128, C=6, cv=document.getElementById('pad'), g=cv.getContext('2d');
const hud=document.getElementById('hud'), count=document.getElementById('count');

function grid(){
  g.fillStyle='#fff'; g.fillRect(0,0,cv.width,cv.height);
  g.fillStyle='#e6e6e6';
  for(let i=0;i<N;i+=8){ g.fillRect(i*C,0,1,cv.height); g.fillRect(0,i*C,cv.width,1);}
  g.fillStyle='#c8c8c8';
  for(let i=0;i<N;i+=32){ g.fillRect(i*C,0,1,cv.height); g.fillRect(0,i*C,cv.width,1);}
}
// A point draws at its cell CENTRE unless it carries in-cell offsets (fx, fy in
// [0,1)), in which case it draws at the exact spot the pointer visited — this is
// what renders a continuous glide instead of a staircase of cell centres, and
// what makes a sub-cell turn visible at all.
function px(p){ const fx = p.fx === undefined ? 0.5 : p.fx, fy = p.fy === undefined ? 0.5 : p.fy;
  return { x:p.x*C + fx*C, y:p.y*C + fy*C }; }
function line(a,b){ const A=px(a), B=px(b); g.strokeStyle='#111'; g.lineWidth=2; g.lineCap='round';
  g.beginPath(); g.moveTo(A.x,A.y); g.lineTo(B.x,B.y); g.stroke(); }
// pos reads a pointer event as a CONTINUOUS cell coordinate: the integer cell
// plus the fraction of the cell already travelled. Everything the page records
// and draws goes through here, so the trace is the path, not the 6 px lattice
// the pointer happened to cross.
function pos(e, rect){ const r=rect||cv.getBoundingClientRect();
  const kx=cv.width/r.width, ky=cv.height/r.height;
  const u=Math.max(0,Math.min(N-0.000001,(e.clientX-r.left)*kx/C));
  const v=Math.max(0,Math.min(N-0.000001,(e.clientY-r.top)*ky/C));
  const x=Math.floor(u), y=Math.floor(v);
  return { x, y, fx:u-x, fy:v-y }; }
function cell(e, rect){ const p=pos(e, rect); return { x:p.x, y:p.y }; }

let record = [];
let down = false, last = null, pts = [];

function paintAll(){
  grid();
  for(const st of record)
    for(let i=1;i<st.length;i++) line(st[i-1], st[i]);
  if(down) for(let i=1;i<pts.length;i++) line(pts[i-1], pts[i]);
  count.textContent = record.length + ' strokes';}

// Show the cell under the pointer even when it is NOT over the canvas (a cursor
// on the toolbar does not fire a pointermove inside the page's canvas, so the
// readout would freeze on the last grid cell). This handler is on window, NOT on
// the canvas: a pointermove over a button is dispatched to the button and never
// reaches a canvas listener, so a canvas-scoped version could never run.
function show(e){
  const r = cv.getBoundingClientRect();
  if(e.clientX < r.left || e.clientX >= r.right || e.clientY < r.top || e.clientY >= r.bottom){
    hud.textContent = 'off-grid';
    return null;
  }
  const c = cell(e, r);
  hud.textContent = 'cell ' + c.x + ', ' + c.y;
  return c;
}
// ONE writer. A HID drag leaves the bridge as ~75 reports for a 300 px leg
// (kvm -dry-run, step 4 px) but Chrome collapses them into roughly one
// pointermove per frame, so sampling "the latest position" per frame recorded
// 20 points for a 183-cell path (10.9 %). getCoalescedEvents() is the API for
// exactly this: it returns the positions Chrome merged into that frame, so the
// ink follows every report the wire actually sent.
cv.addEventListener('pointermove', e => {
  if(!down){ show(e); return; }
  // A move that says "no buttons are down" means the release was never
  // observed: commit here, before adding a sample from the released pointer.
  if(e.buttons === 0){ endOnce('stale-press', e); show(e); return; }
  const r = cv.getBoundingClientRect();          // one layout read per frame
  const evs = (e.getCoalescedEvents ? e.getCoalescedEvents() : []);
  for(const ev of (evs.length ? evs : [e])) addSample(ev, r);
  show(e);
});
function addSample(e, r){
  const p = pos(e, r);
  if(last && p.x === last.x && p.y === last.y && p.fx === last.fx && p.fy === last.fy){ return; }
  const q = {ts: Math.round(performance.now()), x:p.x, y:p.y, fx:p.fx, fy:p.fy};
  pts.push(q);
  if(last) line(last, q);
  last = q;
}
// Commit the stroke on EVERY terminal signal. A press is a browser-level state
// machine and a release can arrive as pointerup, pointercancel, blur, or a plain
// mouseup after the capture was dropped; a page that only listens for pointerup
// silently loses the stroke it has already painted. The stroke is committed even
// with a single point, so a click is an observable event too.
let downs = 0, ups = 0;
// A lift is ONE event, however many listeners observe it. De-duplicating by the
// event object's identity (not by a wall-clock window) means a fast second
// stroke — a lift, a re-press and a second lift inside one frame — is never
// swallowed: only the SAME lift is ignored the second time. A time window did
// exactly that: it ate the second stroke while its ink was already painted.
let lastLift = null;
function endStroke(why){
  if(!down){ return; }
  down = false; ups++;
  const done = pts.length ? pts : null;
  if(done) record.push(done);
  pts = [];
  paintAll();
  // FLUSH ON EVERY PEN LIFT, debounced. The page sends ONLY the stroke it just
  // drew, and the server APPENDS it to the trace file — the page never re-sends
  // its history, so a page that was open across a clear cannot resurrect cleared
  // ink, and a commit costs one stroke instead of the whole record.
  //
  // The flush waits LIFT_DEBOUNCE_MS after the lift so the lifts of one gesture
  // that several listeners report land in ONE request. The timer is the only
  // thing being coalesced; which lifts are new is decided by lastLift.
  if(done) queue.push(done);
  clearTimeout(flushT);
  flushT = setTimeout(flush, LIFT_DEBOUNCE_MS);
}
let queue = [], flushT = 0;
const LIFT_DEBOUNCE_MS = 120;
// A CLIENT ID that belongs to this PAGE, not to this session: kept in
// localStorage, so a reload keeps writing to the same trace file (the ink is
// still the same pad), while a second window, a second browser or another
// machine gets its OWN file. That is what lets two people draw at once without
// merging their traces — and the agent can point at one file.
// A CLIENT ID that belongs to this PAGE LOAD, not to the browser profile. It is
// kept in sessionStorage (per tab) with a localStorage fallback, so a reload
// keeps writing to the same trace file while a second window gets its OWN file.
// localStorage alone is per ORIGIN, so two tabs shared one id and every Clear in
// one tab silently truncated the other tab's record.
let CLIENT_ID = null;
function newId(){
  return (crypto.randomUUID ? crypto.randomUUID()
      : 'pad-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2,10));
}
function clientId(){
  if(CLIENT_ID) return CLIENT_ID;              // one id per page load, even if
  let id = null;                               // storage throws later
  try{ id = sessionStorage.getItem('kvm-pad-client'); }catch(e){}
  if(!id){ try{ id = localStorage.getItem('kvm-pad-client'); }catch(e){} }
  if(!id) id = newId();
  try{ sessionStorage.setItem('kvm-pad-client', id); }catch(e){}
  try{ localStorage.setItem('kvm-pad-client', id); }catch(e){}
  CLIENT_ID = id;
  return id;
}
function traceUrl(){
  return '/api/trace?client=' + encodeURIComponent(clientId())
              + '&name=' + encodeURIComponent(location.host);
}
// Every mutation of the server record goes through this queue, so a Clear can
// never be overtaken by a commit that was already in flight: the strokes POST is
// dispatched before the clear POST, and both are awaited in order. Without it a
// stroke lifted just before a Clear could be APPENDED to the file the Clear had
// just truncated, and the server would stamp it with the new generation.
let inFlight = Promise.resolve();
function send(body){
  const my = inFlight.then(() => fetch(traceUrl(), {
      method:'POST', headers:{'content-type':'application/json'},
      body: JSON.stringify(body), keepalive: true})
    .then(r => r.ok ? r.json() : Promise.reject('HTTP ' + r.status)));
  inFlight = my.catch(() => {});               // a failure must not wedge the queue
  return my;
}
function flush(){
  if(!queue.length) return;
  const batch = queue; queue = [];
  send(batch)
    .then(res => { live.textContent = 'committed ' + batch.length + ' stroke(s), '
                                  + res.strokes + ' on file'; })
    .catch(err => {
      // The strokes are already off the queue; put them back so the next lift
      // retries them instead of losing ink that is on screen.
      queue = batch.concat(queue);
      live.textContent = 'commit failed: ' + err;
    });
}

const live=document.getElementById('live');
// A press STARTS a stroke. Nothing else sets the down flag, so without this the
// move handler's "if(!down)" guard rejects every recorded sample and the pad
// never commits a stroke. The flag is opened here with the first sample taken
// from the press position, so a click (down+up, no move) is still observable.
// Capture the pointer so a drag that leaves the canvas keeps delivering moves:
// without it the ink stops at the canvas edge while the release still commits a
// truncated stroke.
cv.addEventListener('pointerdown', e => {
  if(e.button !== undefined && e.button !== 0) return;   // this pad draws with the primary button only
  if(down) return;                                       // a second pointer while one is down is not a new stroke
  downs++;
  down = true; last = null; pts = [];
  try{ cv.setPointerCapture(e.pointerId); }catch(err){}
  const r = cv.getBoundingClientRect();
  addSample(e, r);
  show(e);
}, {capture:true});
// A lift is identified by the EVENT, not by a time window: pointerup, mouseup
// and blur all observe the SAME release, and each must be ignored the second
// time, while a genuinely new stroke inside the same millisecond must not be.
function endOnce(why, ev){
  if(ev && ev === lastLift) return;
  if(ev) lastLift = ev;
  endStroke(why);
}
addEventListener('pointerup', e => endOnce('pointerup', e));
addEventListener('pointercancel', e => endOnce('pointercancel', e));
addEventListener('mouseup', e => endOnce('mouseup', e));
addEventListener('blur', () => endOnce('blur', null));
// A held press whose release was never observed must not wedge the pad. The old
// watchdog named an identifier that does not exist ("pointerdown"), so it threw
// once a second while a press was held and did nothing at all; this one reads
// the real pointer state from a move event, where "buttons" is trustworthy.
// (The canvas handler checks it first; this window-level one covers a release
// that happens outside the canvas.)
addEventListener('pointermove', e => { if(down && e.buttons === 0) endOnce('stale-press', e); });

// Export READS the record back. It used to POST the whole page record to an
// append-only endpoint, which duplicated every stroke on disk and then cleared
// the page counter, so "N exported" described a file holding 2N strokes.
document.getElementById('save').onclick = () => {
  count.textContent = 'reading…';
  fetch(traceUrl(), {method:'GET', headers:{'accept':'application/json'},
                     cache:'no-store'})
    .then(r => r.ok ? r.json() : Promise.reject('HTTP ' + r.status))
    .then(res => {
      const strokes = Array.isArray(res) ? res.length : 0;
      const points = Array.isArray(res)
        ? res.reduce((n, s) => n + (Array.isArray(s) ? s.length : 0), 0) : 0;
      count.textContent = strokes + ' on file (' + points + ' pts), '
                        + record.length + ' on page';
    })
    .catch(err => { count.textContent = 'export failed: ' + err; });
};
// Clear empties the page AND the trace file. Clearing only the page would leave
// the file describing strokes that are no longer on screen, so any later read
// of the file would mix generations. The pending flush queue is dropped with it:
// a stroke lifted just before the Clear must not arrive after it — and "send()"
// makes the clear POST queue BEHIND any commit already dispatched.
document.getElementById('clear').onclick = () => {
  record = []; pts = []; down = false; last = null;
  queue = []; clearTimeout(flushT);
  paintAll();
  send([])
    .then(() => { record = []; paintAll(); count.textContent = '0 strokes';
                  live.textContent = 'trace file cleared'; })
    .catch(err => { live.textContent = 'clear failed: ' + err; count.textContent = 'clear failed'; });
};

// Report where the canvas is, so a drawer can calibrate from the live rect
// instead of a hard-coded px-per-cell (the CSS size follows the viewport).
function publishBox(){
  const r = cv.getBoundingClientRect();
  if(!(r.width > 0 && r.height > 0)) return;
  fetch('/api/box', {method:'POST', headers:{'content-type':'application/json'},
    body: JSON.stringify({x: Math.round(r.left + (window.scrollX || 0)),
                          y: Math.round(r.top + (window.scrollY || 0)),
                          w: Math.round(r.width), h: Math.round(r.height)})})
    .catch(() => {});
}
addEventListener('resize', publishBox);
if(window.visualViewport) visualViewport.addEventListener('resize', publishBox);
addEventListener('scroll', publishBox, {passive:true});
// A tab closing right after a lift would otherwise drop that stroke: the debounce
// timer never runs. keepalive on the fetch lets the request outlive the page.
addEventListener('pagehide', () => {
  if(!queue.length) return;
  const batch = queue; queue = [];
  try{ send(batch).catch(() => {}); }catch(e){}
});

publishBox();
grid();
</script>`
