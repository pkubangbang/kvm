package main

// The pad's evidence store — append-only, per client, no read API.
//
// CONTRACT (PAD-PROTOCOL.md):
//   - one append-only log per client: <dir>/<client>.jsonl
//   - one JSON record per line; lines are appended, never rewritten
//   - de-duplication key is (client, ts)
//   - clear is a record IN THE SAME LOG as the strokes (the user's rule), and it
//     is the epoch boundary: the observer reads the highest epoch present and
//     that epoch alone is the current picture
//   - NO GET: the observer reads the file, never an API

import (
	"bufio"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// maxBodyBytes bounds one post. A single pen lift is a few KB; without a bound
// the decode and re-encode are both sized from the request.
const maxBodyBytes = 8 << 20

// Protocol is the client<->server contract version. It is tied to the software
// release: it stays 1 unless the SERVER CODE changes, and then it is bumped.
// Every post carries it; a mismatch makes the client refresh (PAD-PROTOCOL.md §2).
//
// BUMPED 1 -> 2 with the ink-lingering rework. store.go changed: the epoch map
// is now keyed by the FILE (sanitise(client)) instead of the raw client id, so a
// page still running the pre-rework JS — which logs a `down` for a press that
// draws nothing, producing the phantom down/up pairs the rework removes — must
// be made to refresh rather than keep writing records the new reader
// misinterprets. Refusal, not a silent accept: that is the protocol's rule.
const Protocol = 2

// padRecord is one line of the append-only log.
//
// KINDS ARE STRICTLY: load, down, drag, up, clear.
//   - load  — the page reloaded; the client is blank, the server knows nothing
//     about what the old page had. A BOUNDARY like clear, minus counts.
//   - down  — the button went down, at a position
//   - drag  — motion while the button is held, at a position
//   - up    — the button came up, at a position
//   - clear — the user cleared the pad. A BOUNDARY, and it records what it
//     destroyed (cleared_strokes / cleared_points)
//
// The observer reads from the LAST boundary (load or clear) onward; everything
// before it is retired. epoch is the server-assigned generation, which is what
// lets a client push only the events of its current epoch.
type padRecord struct {
	Client  string   `json:"client"`
	TS      int64    `json:"ts"`
	Kind    string   `json:"kind"`
	Epoch   int      `json:"epoch"`
	RX      *float64 `json:"rx,omitempty"`
	RY      *float64 `json:"ry,omitempty"`
	Strokes int      `json:"cleared_strokes,omitempty"`
	Pts     int      `json:"cleared_points,omitempty"`
}

// padPost is the body the client sends.
type padPost struct {
	Client  string     `json:"client"`
	Version int        `json:"version"`
	Events  []padEvent `json:"events"`
}

// padEvent is one event from the client: exactly the same shape without the
// server-assigned epoch and counts.
type padEvent struct {
	Kind string   `json:"kind"`
	TS   int64    `json:"ts"`
	RX   *float64 `json:"rx,omitempty"`
	RY   *float64 `json:"ry,omitempty"`
}

// isBoundary reports whether an event retires everything before it.
func isBoundary(kind string) bool { return kind == "load" || kind == "clear" }

// isKind reports whether kind is one of the five the protocol allows.
func isKind(kind string) bool {
	switch kind {
	case "load", "down", "drag", "up", "clear":
		return true
	}
	return false
}

// traceStore owns the log directory and the per-client locks.
//
// mu guards the in-memory maps ONLY and is never held across file I/O. Each
// client has its own mutex, held across that client's read-index-then-append so
// two concurrent posts for the same client cannot both miss a duplicate.
type traceStore struct {
	mu        sync.Mutex // epochs, fileLocks — never held across I/O
	dir       string
	epochs    map[string]int
	fileLocks map[string]*sync.Mutex
}

// newTraceStore opens the log directory and REHYDRATES the epoch map from what
// is already on disk.
//
// WHY: the epoch map used to live in memory only. After a server restart the
// next events were written with epoch 0 BEHIND an epoch 2 boundary, and since
// the observer reads "the highest epoch present", retired ink came back as the
// current picture. The append-only file is the only index this store has, so it
// must be read once at startup rather than trusted to memory.
//
// One pass per file, and a file that cannot be read is reported and skipped: a
// single corrupt client must not reset every other client's generation.
func newTraceStore(dir string) *traceStore {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("pad: cannot create %s: %v", dir, err)
	}
	t := &traceStore{
		dir:       dir,
		epochs:    map[string]int{},
		fileLocks: map[string]*sync.Mutex{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("pad: cannot list %s, starting with empty epochs: %v", dir, err)
		return t
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			log.Printf("pad: cannot read %s, keeping its epoch at 0: %v", e.Name(), rerr)
			continue
		}
		client := strings.TrimSuffix(e.Name(), ".jsonl")
		max := 0
		for _, line := range strings.Split(string(raw), "\n") {
			if line == "" {
				continue
			}
			var rec padRecord
			if json.Unmarshal([]byte(line), &rec) != nil {
				continue // a torn or corrupt line is skipped, not fatal
			}
			if rec.Epoch > max {
				max = rec.Epoch
			}
		}
		t.epochs[client] = max
	}
	return t
}

// sanitise strips anything that could escape dir from a client id.
func sanitise(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// path is the append-only log for one client.
func (t *traceStore) path(client string) string {
	id := sanitise(client)
	if id == "" {
		id = "anonymous"
	}
	return filepath.Join(t.dir, id+".jsonl")
}

// clientLock serialises one client's file work. mu is released before the
// client mutex is taken, so mu is never held across I/O. The lock is keyed by
// the FILE (see epochKey): two raw ids that share a file must share the lock,
// or concurrent posts from them interleave in one file.
func (t *traceStore) clientLock(client string) func() {
	t.mu.Lock()
	l, ok := t.fileLocks[t.epochKey(client)]
	if !ok {
		l = &sync.Mutex{}
		t.fileLocks[t.epochKey(client)] = l
	}
	t.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Epoch keys are FILE keys. The log is one file per sanitised client id, and the
// observer reads that file's highest epoch — so the generation counter must be
// keyed exactly the way the file is. It used to be keyed by the RAW client id
// while the file was keyed by sanitise(client), and newTraceStore() rehydrated
// using the FILE's stem (the sanitised key). For any id sanitise() rewrites, the
// rehydrated seat was never read: the first post-restart event was written at
// epoch 0 behind an epoch-N boundary, which is exactly the retired-ink
// resurrection the rehydration exists to prevent. Two raw ids that sanitise onto
// one file also kept independent counters, making one file's epochs
// non-monotonic.
func (t *traceStore) epochKey(client string) string {
	id := sanitise(client)
	if id == "" {
		id = "anonymous"
	}
	return id
}

// epoch reads a client's current generation. No I/O.
func (t *traceStore) epoch(client string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.epochs[t.epochKey(client)]
}

// bumpEpoch advances a client's generation — the clear path.
func (t *traceStore) bumpEpoch(client string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := t.epochKey(client)
	t.epochs[k]++
	return t.epochs[k]
}

// readLog parses an append-only log. A malformed line is skipped, not fatal: a
// torn tail is expected on a file that is being appended to.
func readLog(path string) ([]padRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []padRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		var rec padRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, sc.Err()
}

// seenTS is the de-duplication index. The FILE is still the index — no sidecar,
// nothing that dies with the process — but the key is (ts, kind), not ts alone.
//
// WHY kind is in the key: the page stamps ts ONCE PER INPUT, and the host can
// deliver a press and its first move inside the same millisecond, so a real
// gesture legitimately carries several events at one ts. Keying on ts alone
// dropped them (measured: a 315 px drag reduced to two records; on another run
// a whole `down`). Two events of the SAME kind at the same ts is the one
// combination that cannot be a genuine input — it is a retransmit.
//
// RETRIES STAY IDEMPOTENT: page.html flush() re-sends a failed batch unchanged
// (same ts, same kind), so a retry still matches the index and appends nothing.
// This deliberately does NOT key on the position: a drag that does not move the
// pointer is a real input and must survive.
func seenTS(recs []padRecord) map[string]bool {
	seen := make(map[string]bool, len(recs))
	for _, r := range recs {
		seen[dedupKey(r.TS, r.Kind)] = true
	}
	return seen
}

// dedupKey is the de-duplication key: one timestamp, one kind.
func dedupKey(ts int64, kind string) string {
	return strconv.FormatInt(ts, 10) + "\x00" + kind
}

// appendEvents appends the events that survive de-duplication, in order.
//
// THREE DEFECTS ARE FIXED HERE, each one a silent-loss mode measured on this
// rig (see the names at the top of store_test.go):
//
//	(c) TORN TAIL. O_APPEND adds no separator, so a trailing line without a
//	    newline merges with the next append into ONE unparseable line and
//	    BOTH records are lost — readLog skips malformed lines. The tail is
//	    therefore terminated before the first append, under the client lock.
//	    The whole batch is then written in ONE Write call: a per-line write
//	    is not atomic on Windows, and one write makes the batch
//	    all-or-nothing at the tail. No fsync, deliberately: this survives a
//	    process crash or kill (the bytes are in the page cache), but a POWER
//	    LOSS can still lose an arbitrary suffix, not just the tail.
//
//	(d) A CLEAR COUNTED RETIRED EPOCHS. countsUpTo used to count every epoch
//	    below the new one; it now counts only the epoch the clear destroyed.
//	    cur is captured BEFORE bumpEpoch or the fix is a no-op.
//
//	de-dup stays (client, ts) with the CLIENT's ts, unchanged: the page's
//	    flush() deliberately re-sends a failed batch with the same ts
//	    (page.html:386), so rewriting the ts here would turn that retry into
//	    a duplicate. Uniqueness is the client's job (a monotonic stamp in
//	    page.html), not the server's.
func (t *traceStore) appendEvents(client string, evs []padEvent) (appended, dup int, file string, err error) {
	file = t.path(client)
	unlock := t.clientLock(client)
	defer unlock()

	existing, rerr := readLog(file)
	if rerr != nil {
		return 0, 0, file, rerr
	}
	seen := seenTS(existing)

	f, oerr := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if oerr != nil {
		return 0, 0, file, oerr
	}
	defer f.Close()

	// (c) Terminate a torn tail before appending. The lock is already held.
	if st, serr := f.Stat(); serr == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if rf, rerr := os.Open(file); rerr == nil {
			_, _ = rf.ReadAt(last, st.Size()-1)
			_ = rf.Close()
			if last[0] != '\n' {
				if _, werr := f.Write([]byte{'\n'}); werr != nil {
					return 0, 0, file, werr
				}
			}
		}
	}

	var buf []byte
	for _, e := range evs {
		if !isKind(e.Kind) {
			continue // the five kinds are strict; anything else is not a protocol event
		}
		if seen[dedupKey(e.TS, e.Kind)] {
			dup++
			continue
		}
		rec := padRecord{
			Client: client,
			TS:     e.TS,
			Kind:   e.Kind,
			RX:     e.RX,
			RY:     e.RY,
		}
		// A boundary (load or clear) opens a new epoch. A clear also records what
		// it destroyed; a load cannot, because the client restarted and the
		// server never knew what the old page held.
		if isBoundary(e.Kind) {
			cur := t.epoch(client) // (d) the epoch being retired, BEFORE the bump
			rec.Epoch = t.bumpEpoch(client)
			if e.Kind == "clear" {
				rec.Strokes, rec.Pts = countsUpTo(existing, cur)
			}
		} else {
			rec.Epoch = t.epoch(client)
		}
		line, merr := json.Marshal(rec)
		if merr != nil {
			return appended, dup, file, merr
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
		seen[dedupKey(e.TS, e.Kind)] = true
		appended++
	}
	if len(buf) == 0 {
		return appended, dup, file, nil
	}
	if _, werr := f.Write(buf); werr != nil {
		return appended, dup, file, werr
	}
	return appended, dup, file, nil
}

// countsUpTo reports what the log held in the epoch a clear destroyed: the
// strokes (a down followed by its up) and points (down+drag+up events) recorded
// in THAT epoch alone.
//
// retires is the epoch in force BEFORE the clear bumped it. The filter used to
// be `r.Epoch >= newEpoch`, which counted every epoch below the new one, so the
// second clear reported the whole file's history (measured: 4 strokes / 11
// points where 1 stroke / 2 points existed).
//
// Known and accepted: this scans what was already on disk before this request,
// so a batch that carries down/up AND a clear does not count its own earlier
// events; and a record with no epoch field (an older file) reads back as 0 and
// lands in the `!= retires` bucket whenever retires > 0.
func countsUpTo(recs []padRecord, retires int) (strokes, points int) {
	open := false
	for _, r := range recs {
		if r.Epoch != retires || isBoundary(r.Kind) {
			continue
		}
		points++
		switch r.Kind {
		case "down":
			if !open {
				open = true
			}
		case "up":
			if open {
				strokes++
				open = false
			}
		}
	}
	if open {
		strokes++ // a stroke whose up never arrived still existed
	}
	return strokes, points
}

// handlePad is POST-only: receive the event log, de-duplicate, append.
func (t *traceStore) handlePad(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "POST the event log; there is no read API")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var post padPost
	if err := json.NewDecoder(r.Body).Decode(&post); err != nil {
		writeJSONError(w, http.StatusBadRequest, "body must be {client, version, events}")
		return
	}
	if post.Client == "" {
		writeJSONError(w, http.StatusBadRequest, "client is required")
		return
	}
	// Version mismatch: refuse, so the client refreshes and fetches the script
	// that matches this server.
	if post.Version != Protocol {
		writeJSONError(w, http.StatusConflict, "protocol mismatch: server="+itoa(Protocol)+" client="+itoa(post.Version))
		return
	}
	appended, dup, file, err := t.appendEvents(post.Client, post.Events)
	if err != nil {
		log.Printf("pad: append %s: %v", file, err)
		writeJSONError(w, http.StatusInternalServerError, "log not appended: "+err.Error())
		return
	}
	_, _ = w.Write(append(mustJSON(map[string]any{
		"ok": true, "client": post.Client, "version": Protocol,
		"appended": appended, "duplicates": dup, "epoch": t.epoch(post.Client), "file": file,
	}), '\n'))
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"error":"encode failed"}`)
	}
	return b
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(append(mustJSON(map[string]string{"error": msg}), '\n'))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
