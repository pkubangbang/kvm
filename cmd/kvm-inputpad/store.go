package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxBodyBytes bounds one commit. The page posts the strokes of a single pen
// lift (a few KB); the old record-replacing POST is gone, so nothing legitimate
// approaches this. Without it a single request could be arbitrarily large and
// the decode plus the re-marshal would both be sized from the request.
const maxBodyBytes = 8 << 20

// traceStore is the pad's evidence: one JSON file per client, plus an event log
// that records every action taken on the pad — a stroke, a clear, a new client.
//
// Why a file and not an HTTP record:
//   - the page holds the history, so any GET answered from server state can
//     disagree with what is painted; a file written at the moment of each pen
//     lift cannot (the agent reads exactly what the page committed);
//   - a stale page cannot resurrect cleared strokes, because a clear truncates
//     the client's file and nothing is rebuilt from the page's memory;
//   - the agent needs no HTTP API to read the ink — it opens the file.
//
// PER CLIENT: every page sends a client id (a UUID it keeps in localStorage, so
// a reload is the SAME client and a second window/tab/instance is a different
// one). Each client gets its own <dir>/<id>.json, so two people drawing at once
// cannot overwrite each other's trace, and the agent can read one tablet's ink
// without filtering somebody else's.
//
// EVERY EVENT: <dir>/events.jsonl gets one line per action — stroke, clear,
// client-new — with the client, the epoch, the point count and the file it went
// to. The stroke FILES describe the ink; the EVENT LOG describes the session.
//
// LOCKING: mu guards the in-memory maps ONLY (epochs, clients, fileLocks) and is
// never held across file I/O. Each client has its own mutex, held across that
// client's read-modify-write so two concurrent posts for the same client cannot
// lose a stroke, while two DIFFERENT clients proceed in parallel. events.jsonl
// has its own mutex so appends never interleave.
type traceStore struct {
	mu        sync.Mutex // epochs, clients, fileLocks — never held across I/O
	evMu      sync.Mutex // serialises events.jsonl appends
	dir       string
	events    string
	epochs    map[string]int
	fileLocks map[string]*sync.Mutex
	clients   int
}

func newTraceStore(dir string) *traceStore {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("trace: cannot create %s: %v", dir, err)
	}
	t := &traceStore{
		dir:       dir,
		events:    filepath.Join(dir, "events.jsonl"),
		epochs:    map[string]int{},
		fileLocks: map[string]*sync.Mutex{},
	}
	// The session starts empty: the ink on screen starts empty, and files left
	// over from a previous run describe pages that no longer exist. The event
	// log is truncated (not removed) so a reader always finds the file.
	if err := os.WriteFile(t.events, nil, 0o644); err != nil {
		log.Printf("trace: cannot initialise %s: %v", t.events, err)
	}
	t.event(map[string]any{"event": "session-start", "dir": dir})
	return t
}

// path is the trace file for one client. The id is sanitised so a client cannot
// choose a path outside dir.
func (t *traceStore) path(client string) string {
	id := sanitise(client)
	if id == "" {
		id = "anonymous"
	}
	return filepath.Join(t.dir, id+".json")
}

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

// clientLock serialises the file work of ONE client. Taking it touches mu only
// to look the mutex up; mu is released before the client mutex is acquired, so
// it is never held across file I/O and the two never deadlock.
func (t *traceStore) clientLock(client string) func() {
	t.mu.Lock()
	l, ok := t.fileLocks[client]
	if !ok {
		l = &sync.Mutex{}
		t.fileLocks[client] = l
	}
	t.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// ensureClient registers a client (epoch 1) the first time it is seen and
// reports whether this call created it. No I/O: the caller logs outside the lock.
func (t *traceStore) ensureClient(client string) (ep int, created bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ep, ok := t.epochs[client]; ok {
		return ep, false
	}
	t.epochs[client] = 1
	t.clients++
	return 1, true
}

// bumpEpoch starts a new generation for client — the clear path. No I/O.
func (t *traceStore) bumpEpoch(client string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	ep := t.epochs[client] + 1
	t.epochs[client] = ep
	return ep
}

// epoch reads a client's current generation. No I/O.
func (t *traceStore) epoch(client string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.epochs[client]
}

// clientsSeen reports how many distinct clients have been registered.
func (t *traceStore) clientsSeen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.clients
}

// event appends one line to events.jsonl. It takes evMu itself: callers must NOT
// hold mu (that would put file I/O under the map lock).
func (t *traceStore) event(rec map[string]any) {
	rec["at"] = time.Now().Format(time.RFC3339Nano)
	b, err := json.Marshal(rec)
	if err != nil {
		log.Printf("trace: marshal event: %v", err)
		return
	}
	t.evMu.Lock()
	defer t.evMu.Unlock()
	f, err := os.OpenFile(t.events, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("trace: open %s: %v", t.events, err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		log.Printf("trace: append event: %v", err)
	}
}

// writeTraceAtomic replaces path with b via a uniquely-named sibling, so a
// concurrent reader sees the old file or the new one, never a partial write.
func writeTraceAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// appendStrokes writes the strokes of ONE pen lift to the client's file. The
// page sends only what it just drew, so nothing is deduplicated here: a stroke
// that arrives is a stroke the user made. An empty batch is a CLEAR.
//
// err is non-nil when the batch was NOT persisted; the caller must not report
// success then (the old code logged and answered ok:true).
func (t *traceStore) appendStrokes(client string, strokes []Stroke) (total, points, ep int, file string, err error) {
	file = t.path(client)
	unlock := t.clientLock(client)
	defer unlock()

	if len(strokes) == 0 {
		ep = t.bumpEpoch(client)
		if werr := writeTraceAtomic(file, []byte("[]\n")); werr != nil {
			log.Printf("trace: clear %s: %v", file, werr)
			t.event(map[string]any{"event": "clear-failed", "client": client,
				"epoch": ep, "file": file, "error": werr.Error()})
			return 0, 0, ep, file, werr
		}
		// A clear by a client never seen before still creates its record; make
		// sure the event log says so instead of logging a clear for a stranger.
		t.event(map[string]any{"event": "clear", "client": client, "epoch": ep, "file": file})
		return 0, 0, ep, file, nil
	}

	ep, created := t.ensureClient(client)
	if created {
		t.event(map[string]any{"event": "client-new", "client": client, "file": file,
			"clients": t.clientsSeen()})
	}

	have, rerr := readTrace(file)
	if rerr != nil {
		if !os.IsNotExist(rerr) {
			// A file we cannot parse is evidence we must not silently destroy:
			// move it aside and start a fresh record, loudly.
			bad := fmt.Sprintf("%s.corrupt-%d", file, time.Now().Unix())
			if mvErr := os.Rename(file, bad); mvErr != nil {
				log.Printf("trace: %s unreadable (%v) and cannot be moved aside: %v", file, rerr, mvErr)
				t.event(map[string]any{"event": "strokes-failed", "client": client,
					"epoch": ep, "file": file, "error": rerr.Error()})
				return 0, 0, ep, file, rerr
			}
			log.Printf("trace: %s unreadable (%v); preserved as %s", file, rerr, bad)
			have = nil
		}
		have = nil
	}

	added := 0
	for _, s := range strokes {
		if len(s) == 0 {
			continue
		}
		for i := range s {
			s[i].Epoch = ep
		}
		have = append(have, s)
		points += len(s)
		added++
	}
	b, merr := json.MarshalIndent(have, "", "  ")
	if merr != nil {
		log.Printf("trace: marshal %s: %v", file, merr)
		t.event(map[string]any{"event": "strokes-failed", "client": client,
			"epoch": ep, "file": file, "error": merr.Error()})
		return 0, 0, ep, file, merr
	}
	if werr := writeTraceAtomic(file, append(b, '\n')); werr != nil {
		log.Printf("trace: write %s: %v", file, werr)
		t.event(map[string]any{"event": "strokes-failed", "client": client,
			"epoch": ep, "file": file, "error": werr.Error()})
		return 0, 0, ep, file, werr
	}
	t.event(map[string]any{"event": "strokes", "client": client, "epoch": ep,
		"strokes": added, "points": points, "total_strokes": len(have), "file": file})
	return len(have), points, ep, file, nil
}

// readTrace loads a trace file (an array of strokes).
func readTrace(path string) ([]Stroke, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Stroke
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// handleTrace is the client's commit endpoint: POST the strokes of one pen lift
// (an empty array clears that client's pad), GET the accumulated record.
//
// The client identifies itself with ?client=<id> (the page's own UUID) and,
// optionally, ?name=<label>. Without an id the batch goes to "anonymous", so a
// bare curl still works; a GET without an id answers the newest client file.
func (t *traceStore) handleTrace(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		t.serveTrace(w, r)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "use GET to read the trace, POST to commit")
		return
	}

	client := r.URL.Query().Get("client")
	label := r.URL.Query().Get("name")

	// Bound the body before decoding: a decode of an unbounded body is a free
	// memory spike on a loopback port anything on the box can reach.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var strokes []Stroke
	if err := json.NewDecoder(r.Body).Decode(&strokes); err != nil {
		writeJSONError(w, http.StatusBadRequest, "body must be a JSON array of strokes")
		return
	}
	if strokes == nil {
		// `null` decodes to a nil slice exactly like `[]`; treating it as a clear
		// would let an accidental empty body truncate the record.
		writeJSONError(w, http.StatusBadRequest, "body must be an array (use [] to clear)")
		return
	}

	if label != "" {
		if _, created := t.ensureClient(client); created {
			t.event(map[string]any{"event": "client-new", "client": client,
				"name": label, "file": t.path(client), "clients": t.clientsSeen()})
		}
	}

	total, points, ep, file, err := t.appendStrokes(client, strokes)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "trace not persisted: "+err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		OK      bool   `json:"ok"`
		Client  string `json:"client"`
		Strokes int    `json:"strokes"`
		Points  int    `json:"points"`
		Epoch   int    `json:"epoch"`
		File    string `json:"file"`
	}{true, client, total, points, ep, file})
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	b, err := json.Marshal(struct {
		Error string `json:"error"`
	}{msg})
	if err != nil {
		return
	}
	_, _ = w.Write(append(b, '\n'))
}

// serveTrace answers GET with the accumulated strokes of one client (the newest
// one when no ?client= is given). It reads the file directly: a rename-based
// writer means a reader needs no lock to see a consistent snapshot.
func (t *traceStore) serveTrace(w http.ResponseWriter, r *http.Request) {
	client := r.URL.Query().Get("client")
	var file string
	if client != "" {
		file = t.path(client)
	} else {
		files := t.listClients()
		if len(files) == 0 {
			_, _ = w.Write([]byte("[]\n"))
			return
		}
		file = files[0]
	}
	b, err := os.ReadFile(file)
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		// No file yet (or an empty one): the client's record is empty, which is
		// the truthful answer for a page that has not committed anything.
		_, _ = w.Write([]byte("[]\n"))
		return
	}
	w.Header().Set("X-Trace-File", file)
	if client != "" {
		w.Header().Set("X-Trace-Epoch", fmt.Sprint(t.epoch(client)))
	}
	_, _ = w.Write(b)
}

// listClients reports the trace files present, newest first, so a reader can
// find whoever is drawing without knowing their id.
func (t *traceStore) listClients() []string {
	ents, err := os.ReadDir(t.dir)
	if err != nil {
		return nil
	}
	type entry struct {
		name string
		mod  time.Time
	}
	var es []entry
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		es = append(es, entry{n, info.ModTime()})
	}
	sort.Slice(es, func(i, j int) bool { return es[i].mod.After(es[j].mod) })
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, filepath.Join(t.dir, e.name))
	}
	return out
}
