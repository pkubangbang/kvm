package main

// Tests for the append-only evidence store.
//
// Each of the four cases at the top FAILED on the store as it stood before the
// 2026-11-25 fixes, and they are kept because each one is a silent-loss mode
// that produced a wrong conclusion on this rig before it was a test:
//
//	(a) same-millisecond ts collision — a real HID drag lost its drags and, on
//	    one measured run, its `down`; the file could not reconstruct the stroke.
//	(b) epoch map lived in memory only — after a restart the next events were
//	    written with epoch 0 behind an epoch 2 boundary, resurrecting retired ink.
//	(c) torn trailing line — O_APPEND adds no separator, so a tail without a
//	    newline merged with the next append into one unparseable line and BOTH
//	    records were lost.
//	(d) countsUpTo counted every epoch below the new one, not the one the clear
//	    actually destroyed, so clear#2 reported the whole file's history.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- helpers -------------------------------------------------------------

func f64(v float64) *float64 { return &v }

// post sends one batch the way the page does.
func post(t *testing.T, s *traceStore, client string, evs ...padEvent) (appended, dup int) {
	t.Helper()
	body, err := json.Marshal(padPost{Client: client, Version: Protocol, Events: evs})
	if err != nil {
		t.Fatalf("marshal post: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/log", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handlePad(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("post %s: %d %s", client, w.Code, w.Body.String())
	}
	var resp struct {
		Appended   int `json:"appended"`
		Duplicates int `json:"duplicates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, w.Body.String())
	}
	return resp.Appended, resp.Duplicates
}

// records reads a client's log straight off disk — never through an API, because
// there is no read API (PAD-PROTOCOL.md §2).
func records(t *testing.T, s *traceStore, client string) []padRecord {
	t.Helper()
	recs, err := readLog(s.path(client))
	if err != nil {
		t.Fatalf("readLog: %v", err)
	}
	return recs
}

func kinds(recs []padRecord) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Kind
	}
	return out
}

// --- (a) same-millisecond events: the trade, pinned -----------------------

// TWO EVENTS OF THE SAME KIND AT THE SAME MILLISECOND ARE A RETRANSMIT.
//
// This is the contract the store chose, and it is asserted rather than hidden:
// the de-duplication key is (ts, kind), so a same-ts same-kind pair appends once.
// The page cannot produce such a pair any more (nextTS() in page.html is
// strictly increasing), so this only fires for a STALE TAB still running old
// JS — where the residual loss is exactly one of two same-kind events at one ms.
//
// What the key DOES buy is the property in the sibling test below: four
// DIFFERENT kinds at one millisecond all survive, which bare-ts de-dup dropped
// (measured: a 315 px drag reduced to two records).
func TestSameMillisecondBatchKeepsOneEventPerKind(t *testing.T) {
	s := newTraceStore(t.TempDir())
	// A press + coalesced moves + release inside one JS tick: every event
	// carries the SAME ts. The second `drag` is indistinguishable from a
	// retransmit of the first, so it is dropped BY DESIGN.
	evs := []padEvent{
		{Kind: "down", TS: 100, RX: f64(0.1), RY: f64(0.1)},
		{Kind: "drag", TS: 100, RX: f64(0.2), RY: f64(0.1)},
		{Kind: "drag", TS: 100, RX: f64(0.3), RY: f64(0.1)},
		{Kind: "up", TS: 100, RX: f64(0.4), RY: f64(0.1)},
	}
	appended, dup := post(t, s, "c", evs...)
	if appended != 3 || dup != 1 {
		t.Fatalf("appended=%d dup=%d, want 3/1 (one same-kind pair is a retransmit)", appended, dup)
	}
	got := kinds(records(t, s, "c"))
	want := []string{"down", "drag", "up"}
	if len(got) != len(want) {
		t.Fatalf("log holds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d = %q, want %q (%v)", i, got[i], want[i], got)
		}
	}
}

// FOUR DIFFERENT KINDS AT ONE MILLISECOND ALL SURVIVE. This is what widening
// the key from ts to (ts, kind) actually delivers, and the old store lost three
// of these four.
func TestSameMillisecondDifferentKindsAllSurvive(t *testing.T) {
	s := newTraceStore(t.TempDir())
	evs := []padEvent{
		{Kind: "down", TS: 100, RX: f64(0.1), RY: f64(0.1)},
		{Kind: "drag", TS: 100, RX: f64(0.2), RY: f64(0.1)},
		{Kind: "drag2", TS: 100, RX: f64(0.3), RY: f64(0.1)}, // rejected kind — must not occupy the key
		{Kind: "up", TS: 100, RX: f64(0.4), RY: f64(0.1)},
	}
	appended, dup := post(t, s, "c", evs...)
	if appended != 3 || dup != 0 {
		t.Fatalf("appended=%d dup=%d, want 3/0 (an unknown kind is not an event)", appended, dup)
	}
	got := kinds(records(t, s, "c"))
	want := []string{"down", "drag", "up"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("log holds %v, want %v", got, want)
		}
	}
}

// A real gesture — down, drags at DISTINCT ts, up — must land whole. This is
// the shape the 315 px Z top bar failed to produce.
func TestGestureWithDistinctStampsLandsWhole(t *testing.T) {
	s := newTraceStore(t.TempDir())
	post(t, s, "c", padEvent{Kind: "down", TS: 10, RX: f64(0.1), RY: f64(0.1)})
	for i := int64(1); i <= 5; i++ {
		post(t, s, "c", padEvent{Kind: "drag", TS: 10 + i, RX: f64(0.1 + 0.1*float64(i)), RY: f64(0.1)})
	}
	post(t, s, "c", padEvent{Kind: "up", TS: 16, RX: f64(0.7), RY: f64(0.1)})
	got := kinds(records(t, s, "c"))
	if len(got) != 7 || got[0] != "down" || got[6] != "up" {
		t.Fatalf("gesture landed as %v, want down + 5 drags + up", got)
	}
}

// A retry of an already-written batch must append NOTHING: the whole point of
// the (client, ts) key is that flush()'s retry path is idempotent.
func TestRetryOfSameBatchAppendsNothing(t *testing.T) {
	s := newTraceStore(t.TempDir())
	evs := []padEvent{
		{Kind: "down", TS: 7, RX: f64(0.1), RY: f64(0.1)},
		{Kind: "up", TS: 8, RX: f64(0.1), RY: f64(0.1)},
	}
	post(t, s, "c", evs...)
	appended, dup := post(t, s, "c", evs...)
	if appended != 0 || dup != 2 {
		t.Fatalf("retry appended=%d dup=%d, want 0/2 — the retry path duplicates", appended, dup)
	}
	if n := len(records(t, s, "c")); n != 2 {
		t.Fatalf("log holds %d records after a retry, want 2", n)
	}
}

// --- (b) epoch survives a restart ---------------------------------------

func TestEpochSeededFromDiskOnRestart(t *testing.T) {
	dir := t.TempDir()
	s1 := newTraceStore(dir)
	post(t, s1, "c", padEvent{Kind: "load", TS: 1})
	post(t, s1, "c", padEvent{Kind: "down", TS: 2, RX: f64(0.1), RY: f64(0.1)})
	post(t, s1, "c", padEvent{Kind: "up", TS: 3, RX: f64(0.2), RY: f64(0.2)})
	post(t, s1, "c", padEvent{Kind: "clear", TS: 4})
	if e := s1.epoch("c"); e < 2 {
		t.Fatalf("epoch after load+clear = %d, want >= 2", e)
	}

	// A fresh process on the same directory: the on-disk log is the only index.
	s2 := newTraceStore(dir)
	cur := s2.epoch("c")
	post(t, s2, "c", padEvent{Kind: "down", TS: 5, RX: f64(0.3), RY: f64(0.3)})

	all := records(t, s2, "c")
	last := all[len(all)-1]
	if last.Kind != "down" {
		t.Fatalf("last record = %q, want down", last.Kind)
	}
	if last.Epoch != cur {
		t.Fatalf("post-restart epoch = %d, want the seeded %d — the epoch reset to zero", last.Epoch, cur)
	}
	if last.Epoch < 2 {
		t.Fatalf("post-restart epoch = %d, want >= 2 so retired ink is not resurrected", last.Epoch)
	}
}

// --- (c) a torn trailing line is terminated, not merged -----------------

func TestTornTailIsTerminatedBeforeAppend(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	file := s.path("c")
	torn := `{"client":"c","ts":1,"kind":"down","epoch":1,"rx":0.1,"ry":0.1}`
	if err := os.WriteFile(file, []byte(torn), 0o644); err != nil {
		t.Fatalf("seed torn tail: %v", err)
	}

	post(t, s, "c", padEvent{Kind: "up", TS: 2, RX: f64(0.2), RY: f64(0.2)})

	recs := records(t, s, "c")
	if len(recs) != 2 {
		raw, _ := os.ReadFile(file)
		t.Fatalf("parsed %d records, want 2 — the torn tail swallowed the append (file: %.200s)", len(recs), raw)
	}
	if recs[0].Kind != "down" || recs[1].Kind != "up" {
		t.Fatalf("kinds = %v, want [down up]", kinds(recs))
	}
}

// --- (d) a clear counts only the epoch it destroys ----------------------

func TestSecondClearCountsOnlyItsOwnEpoch(t *testing.T) {
	s := newTraceStore(t.TempDir())
	post(t, s, "c", padEvent{Kind: "load", TS: 1})

	// Three strokes in the first epoch.
	ts := int64(10)
	for i := 0; i < 3; i++ {
		post(t, s, "c", padEvent{Kind: "down", TS: ts, RX: f64(0.1), RY: f64(0.1)})
		post(t, s, "c", padEvent{Kind: "drag", TS: ts + 1, RX: f64(0.2), RY: f64(0.2)})
		post(t, s, "c", padEvent{Kind: "up", TS: ts + 2, RX: f64(0.3), RY: f64(0.3)})
		ts += 3
	}
	post(t, s, "c", padEvent{Kind: "clear", TS: ts})

	// One stroke in the second epoch, then a second clear.
	ts++
	post(t, s, "c", padEvent{Kind: "down", TS: ts, RX: f64(0.1), RY: f64(0.1)})
	post(t, s, "c", padEvent{Kind: "up", TS: ts + 1, RX: f64(0.2), RY: f64(0.2)})
	ts += 2
	post(t, s, "c", padEvent{Kind: "clear", TS: ts})

	var clears []padRecord
	for _, r := range records(t, s, "c") {
		if r.Kind == "clear" {
			clears = append(clears, r)
		}
	}
	if len(clears) != 2 {
		t.Fatalf("found %d clears, want 2", len(clears))
	}
	if clears[0].Strokes != 3 || clears[0].Pts != 9 {
		t.Errorf("clear#1 = %d strokes/%d points, want 3/9", clears[0].Strokes, clears[0].Pts)
	}
	if clears[1].Strokes != 1 || clears[1].Pts != 2 {
		t.Errorf("clear#2 = %d strokes/%d points, want 1/2 — it counted retired epochs",
			clears[1].Strokes, clears[1].Pts)
	}
}

// --- the new page contract, round-tripped through the store --------------

// The reworked page logs `down` at the FIRST INK of a stroke and `up` at the
// lift, so every logged stroke is down+drag*+up with at least one segment. That
// grouping must survive the store untouched — the store's job is to preserve the
// sequence, not to reinterpret it. This asserts the exact epoch-28-shaped tail
// the OLD page produced is no longer what the log contains: two downs against
// two ups, no phantom pair at an identical point.
func TestPairedGestureRoundTripsThroughStore(t *testing.T) {
	s := newTraceStore(t.TempDir())

	// Gesture 1: a Z-like path. `down` at the first ink (0.207), then drags.
	post(t, s, "c", padEvent{Kind: "load", TS: 1})
	post(t, s, "c", padEvent{Kind: "down", TS: 2, RX: f64(0.207), RY: f64(0.167)})
	for i := 0; i < 5; i++ {
		post(t, s, "c", padEvent{Kind: "drag", TS: int64(3 + i), RX: f64(0.3 + float64(i)*0.07), RY: f64(0.3)})
	}
	post(t, s, "c", padEvent{Kind: "up", TS: 9, RX: f64(0.661), RY: f64(0.722)})

	// Gesture 2, separated by a real lift — no residual pair.
	post(t, s, "c", padEvent{Kind: "down", TS: 100, RX: f64(0.235), RY: f64(0.190)})
	post(t, s, "c", padEvent{Kind: "drag", TS: 101, RX: f64(0.5), RY: f64(0.3)})
	post(t, s, "c", padEvent{Kind: "up", TS: 102, RX: f64(0.652), RY: f64(0.401)})

	recs := records(t, s, "c")
	var downs, ups int
	open := false
	var strokes [][]padRecord
	var cur []padRecord
	for _, r := range recs {
		switch r.Kind {
		case "down":
			downs++
			if open {
				t.Fatalf("a down arrived while a stroke was already open: %v", kinds(recs))
			}
			open = true
			cur = []padRecord{r}
		case "drag":
			if !open {
				t.Fatalf("a drag arrived with no open stroke: %v", kinds(recs))
			}
			cur = append(cur, r)
		case "up":
			ups++
			if !open {
				t.Fatalf("an up arrived with no open stroke: %v", kinds(recs))
			}
			open = false
			cur = append(cur, r)
			strokes = append(strokes, cur)
			cur = nil
		}
	}
	if open {
		t.Fatalf("a stroke was left open in the log: %v", kinds(recs))
	}
	if downs != 2 || ups != 2 {
		t.Fatalf("downs=%d ups=%d, want 2/2 — the page's pairing did not survive the store", downs, ups)
	}
	if len(strokes) != 2 {
		t.Fatalf("grouped %d strokes, want 2", len(strokes))
	}
	for i, st := range strokes {
		if len(st) < 3 {
			t.Fatalf("stroke %d holds %d records, want down+drag*+up", i, len(st))
		}
		if st[0].Kind != "down" || st[len(st)-1].Kind != "up" {
			t.Fatalf("stroke %d = %v, want down ... up", i, kinds(st))
		}
	}
}

// A clear must retire the strokes before it and leave the epoch monotonic. This
// is the "no retired ink returns" guarantee at the store level: after the clear
// the highest epoch in the file is the clear's, and everything below it is
// retired by construction.
func TestClearRetiresAndEpochStaysMonotonic(t *testing.T) {
	s := newTraceStore(t.TempDir())
	post(t, s, "c", padEvent{Kind: "load", TS: 1})
	post(t, s, "c", padEvent{Kind: "down", TS: 2, RX: f64(0.1), RY: f64(0.1)})
	post(t, s, "c", padEvent{Kind: "up", TS: 3, RX: f64(0.2), RY: f64(0.2)})
	post(t, s, "c", padEvent{Kind: "clear", TS: 4})

	recs := records(t, s, "c")
	// The epoch the pre-clear stroke lived in, and the epoch the clear opened.
	var retired, clearEpoch int
	for _, r := range recs {
		switch r.Kind {
		case "down", "up":
			retired = r.Epoch
		case "clear":
			clearEpoch = r.Epoch
		}
	}
	if clearEpoch <= retired {
		t.Fatalf("clear did not open a new epoch: clear=%d retired=%d", clearEpoch, retired)
	}
	// The observer's rule: only the highest epoch is the current picture. A
	// pre-clear stroke must NOT be in it.
	for _, r := range recs {
		if r.Epoch == clearEpoch && (r.Kind == "down" || r.Kind == "up" || r.Kind == "drag") {
			t.Fatalf("pre-clear ink %q lives in the clear's epoch %d — retired ink is the current picture", r.Kind, clearEpoch)
		}
	}

	// A post-clear stroke belongs to the epoch the clear opened: it is the new
	// picture, and it is strictly above the retired generation.
	post(t, s, "c", padEvent{Kind: "down", TS: 5, RX: f64(0.3), RY: f64(0.3)})
	post(t, s, "c", padEvent{Kind: "up", TS: 6, RX: f64(0.4), RY: f64(0.4)})
	recs = records(t, s, "c")
	last := recs[len(recs)-1]
	if last.Epoch != clearEpoch {
		t.Fatalf("post-clear epoch = %d, want the clear's epoch %d", last.Epoch, clearEpoch)
	}
	if last.Epoch <= retired {
		t.Fatalf("post-clear epoch = %d, want > retired %d — new ink landed in the retired generation", last.Epoch, retired)
	}

	// And the epochs in the file are non-decreasing: nothing was written behind
	// the boundary.
	prev := -1
	for _, r := range recs {
		if r.Epoch < prev {
			t.Fatalf("epochs went backwards in one file: %d then %d", prev, r.Epoch)
		}
		prev = r.Epoch
	}
}

func TestClientIDCannotEscapeDir(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	post(t, s, "../../etc/passwd", padEvent{Kind: "down", TS: 1, RX: f64(0.1), RY: f64(0.1)})
	got := s.path("../../etc/passwd")
	if filepath.Dir(got) != dir {
		t.Fatalf("client id escaped the store dir: %q", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("sanitised file was not written inside dir: %v", err)
	}
}

func TestConcurrentSameClientPostsAllLand(t *testing.T) {
	s := newTraceStore(t.TempDir())
	const n = 8
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer func() { recover() }()
			body, _ := json.Marshal(padPost{Client: "c", Version: Protocol, Events: []padEvent{
				{Kind: "down", TS: int64(100 + i), RX: f64(0.1), RY: f64(0.1)},
			}})
			req := httptest.NewRequest(http.MethodPost, "/api/log", strings.NewReader(string(body)))
			w := httptest.NewRecorder()
			s.handlePad(w, req)
			if w.Code != http.StatusOK {
				done <- errors.New(w.Body.String())
				return
			}
			done <- nil
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent post failed: %v", err)
		}
	}
	if got := len(records(t, s, "c")); got != n {
		t.Fatalf("log holds %d records, want %d — concurrent posts were lost", got, n)
	}
}

func TestCorruptTraceIsPreserved(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	file := s.path("c")
	bad := "{\"client\":\"c\",\"ts\":1,\"kind\":\"down\"}\nnot json at all\n"
	if err := os.WriteFile(file, []byte(bad), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The store must not truncate or rewrite a file it cannot fully parse.
	post(t, s, "c", padEvent{Kind: "up", TS: 2, RX: f64(0.1), RY: f64(0.1)})
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "not json at all") {
		t.Fatalf("corrupt line was dropped from the file: %.300s", raw)
	}
	if !strings.Contains(string(raw), `"up"`) {
		t.Fatalf("the append after a corrupt line did not land: %.300s", raw)
	}
}

func TestGetIsGone(t *testing.T) {
	s := newTraceStore(t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/api/log?client=c", nil)
	w := httptest.NewRecorder()
	s.handlePad(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/log = %d, want 405 — there is no read API", w.Code)
	}
}

func TestProtocolMismatchIs409(t *testing.T) {
	s := newTraceStore(t.TempDir())
	body, _ := json.Marshal(padPost{Client: "c", Version: Protocol + 1, Events: []padEvent{{Kind: "down", TS: 1}}})
	req := httptest.NewRequest(http.MethodPost, "/api/log", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	s.handlePad(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("version mismatch = %d, want 409", w.Code)
	}
}

func TestUnknownKindIsRejected(t *testing.T) {
	s := newTraceStore(t.TempDir())
	appended, dup := post(t, s, "c", padEvent{Kind: "bogus", TS: 1})
	if appended != 0 || dup != 0 {
		t.Fatalf("appended=%d dup=%d for an unknown kind, want 0/0", appended, dup)
	}
	if n := len(records(t, s, "c")); n != 0 {
		t.Fatalf("unknown kind reached the log (%d records)", n)
	}
}
