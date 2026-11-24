package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// traceJSON decodes the handler's body into strokes.
func traceJSON(t *testing.T, b []byte) []Stroke {
	t.Helper()
	var out []Stroke
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("body is not a stroke array: %v (body %.200s)", err, b)
	}
	return out
}

// postTrace drives handleTrace the way the page does.
func postTrace(t *testing.T, s *traceStore, query, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/trace"+query, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleTrace(w, req)
	return w
}

func getTrace(t *testing.T, s *traceStore, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/trace"+query, nil)
	w := httptest.NewRecorder()
	s.handleTrace(w, req)
	return w
}

// GET must serve the accumulated record again: 16 scripts read it and the old
// handler answered 405 for every reader.
func TestGetReturnsAccumulatedTrace(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)

	if w := postTrace(t, s, "?client=abc", `[[{"ts":1,"x":2,"y":3,"fx":0.25,"fy":0.75}]]`); w.Code != 200 {
		t.Fatalf("post: %d %.200s", w.Code, w.Body.String())
	}
	if w := postTrace(t, s, "?client=abc", `[[{"ts":2,"x":4,"y":5,"fx":0,"fy":0}]]`); w.Code != 200 {
		t.Fatalf("post 2: %d", w.Code)
	}

	w := getTrace(t, s, "?client=abc")
	if w.Code != 200 {
		t.Fatalf("GET: %d %.200s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("GET content-type = %q", ct)
	}
	got := traceJSON(t, w.Body.Bytes())
	if len(got) != 2 {
		t.Fatalf("GET returned %d strokes, want 2", len(got))
	}
	if got[0][0].X != 2 || got[0][0].Fy != 0.75 {
		t.Errorf("first point lost its values: %+v", got[0][0])
	}
}

// POST stays APPEND: a second commit must not replace the first.
func TestPostAppends(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	postTrace(t, s, "?client=c", `[[{"ts":1,"x":1,"y":1}]]`)
	postTrace(t, s, "?client=c", `[[{"ts":2,"x":2,"y":2}]]`)
	if n := len(traceJSON(t, getTrace(t, s, "?client=c").Body.Bytes())); n != 2 {
		t.Fatalf("file holds %d strokes, want 2", n)
	}
}

// An empty array truncates ONLY that client and bumps only that client's epoch.
func TestClearTruncatesOnlyThatClient(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	postTrace(t, s, "?client=a", `[[{"ts":1,"x":1,"y":1}]]`)
	postTrace(t, s, "?client=b", `[[{"ts":1,"x":9,"y":9}]]`)

	if w := postTrace(t, s, "?client=a", `[]`); w.Code != 200 {
		t.Fatalf("clear: %d", w.Code)
	}
	if n := len(traceJSON(t, getTrace(t, s, "?client=a").Body.Bytes())); n != 0 {
		t.Errorf("client a has %d strokes after clear, want 0", n)
	}
	if n := len(traceJSON(t, getTrace(t, s, "?client=b").Body.Bytes())); n != 1 {
		t.Errorf("client b lost its stroke: %d, want 1", n)
	}

	// The generation contract: a stroke committed AFTER a clear must carry an
	// epoch strictly greater than the strokes that existed BEFORE it, so a stale
	// page cannot re-add pre-clear ink under a current-looking label.
	epOf := func(w *httptest.ResponseRecorder) int {
		var resp struct {
			Epoch int `json:"epoch"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.Epoch
	}
	postTrace(t, s, "?client=g", `[[{"ts":1,"x":1,"y":1}]]`)
	before := epOf(postTrace(t, s, "?client=g", `[]`)) // clear
	after := epOf(postTrace(t, s, "?client=g", `[[{"ts":2,"x":2,"y":2}]]`))
	if after != before {
		t.Errorf("stroke after clear got epoch %d, the clear was %d", after, before)
	}
	second := epOf(postTrace(t, s, "?client=g", `[]`))
	if second <= before {
		t.Errorf("second clear epoch %d did not advance past %d", second, before)
	}
	third := epOf(postTrace(t, s, "?client=g", `[[{"ts":3,"x":3,"y":3}]]`))
	if third <= second-1 {
		t.Errorf("stroke after the second clear got epoch %d, clear was %d", third, second)
	}
}

// A `null` body decodes to a nil slice exactly like []; it must be REJECTED, not
// treated as a clear, or an accidental empty body truncates the record.
func TestNullBodyIsRejected(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	postTrace(t, s, "?client=z", `[[{"ts":1,"x":1,"y":1}]]`)
	if w := postTrace(t, s, "?client=z", `null`); w.Code != http.StatusBadRequest {
		t.Errorf("null body: got %d, want 400", w.Code)
	}
	if n := len(traceJSON(t, getTrace(t, s, "?client=z").Body.Bytes())); n != 1 {
		t.Errorf("null body truncated the record: %d strokes", n)
	}
}

// The client id cannot escape the directory, and cannot reach events.jsonl.
func TestClientIDCannotEscapeDir(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	for _, evil := range []string{"../../etc/passwd", "/etc/passwd", "..", "events.jsonl", "a/b"} {
		p := s.path(evil)
		if filepath.Dir(p) != dir {
			t.Errorf("client %q escapes the dir: %s", evil, p)
		}
		if filepath.Base(p) == "events.jsonl" {
			t.Errorf("client %q resolves onto the event log: %s", evil, p)
		}
	}
}

// A corrupt trace file must be PRESERVED, not silently replaced by the next
// stroke (the old code set have=nil and rewrote the file).
func TestCorruptTraceIsPreserved(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	p := s.path("k")
	if err := os.WriteFile(p, []byte(`[{"ts":1`), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := postTrace(t, s, "?client=k", `[[{"ts":2,"x":1,"y":1}]]`); w.Code != 200 {
		t.Fatalf("post over corrupt file: %d", w.Code)
	}
	ents, _ := os.ReadDir(dir)
	found := false
	for _, e := range ents {
		if strings.Contains(e.Name(), ".corrupt-") {
			found = true
		}
	}
	if !found {
		t.Errorf("the corrupt trace was destroyed; dir has %v", ents)
	}
	if n := len(traceJSON(t, getTrace(t, s, "?client=k").Body.Bytes())); n != 1 {
		t.Errorf("new record has %d strokes, want 1", n)
	}
}

// Concurrent posts for the SAME client must all land: the per-client lock exists
// precisely so a read-modify-write cannot drop a stroke.
func TestConcurrentSameClientPostsAllLand(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	var wg sync.WaitGroup
	const n = 12
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := `[[{"ts":` + string(rune('0'+i%10)) + `,"x":` +
				string(rune('0'+i%10)) + `,"y":0}]]`
			postTrace(t, s, "?client=same", body)
		}(i)
	}
	wg.Wait()
	if got := len(traceJSON(t, getTrace(t, s, "?client=same").Body.Bytes())); got != n {
		t.Fatalf("file holds %d strokes, want %d (a stroke was lost)", got, n)
	}
}

// Two DIFFERENT clients must not be serialised behind each other's file I/O, and
// must not corrupt each other's file.
func TestConcurrentDifferentClients(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := string(rune('a' + i))
			for j := 0; j < 5; j++ {
				postTrace(t, s, "?client="+c, `[[{"ts":1,"x":1,"y":1}]]`)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		c := string(rune('a' + i))
		if n := len(traceJSON(t, getTrace(t, s, "?client="+c).Body.Bytes())); n != 5 {
			t.Errorf("client %s has %d strokes, want 5", c, n)
		}
	}
}

// The events log must be one JSON object per line: a name with a newline cannot
// forge a second record.
func TestEventLogStaysOneLinePerEvent(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	postTrace(t, s, "?client=q&name="+url.QueryEscape("evil\n{\"event\":\"fake\"}"),
		`[[{"ts":1,"x":1,"y":1}]]`)

	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("line %d is not JSON (newline leaked?): %v: %.120s", i, err, line)
		}
	}
}

// GET answers [] for a client that has never committed, rather than a 404 or an
// empty body a json.loads caller would choke on.
func TestGetWithoutFileIsEmptyArray(t *testing.T) {
	s := newTraceStore(t.TempDir())
	w := getTrace(t, s, "?client=never")
	if w.Code != 200 {
		t.Fatalf("GET: %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("body = %q, want []", w.Body.String())
	}
}

// GET without ?client= answers the newest client file.
func TestGetWithoutClientReturnsNewest(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	postTrace(t, s, "?client=old", `[[{"ts":1,"x":1,"y":1}]]`)
	postTrace(t, s, "?client=new", `[[{"ts":2,"x":2,"y":2}],[{"ts":3,"x":3,"y":3}]]`)
	if n := len(traceJSON(t, getTrace(t, s, "").Body.Bytes())); n != 2 {
		t.Errorf("newest file has %d strokes, want 2", n)
	}
}

// A method other than GET/POST is refused with a JSON body and an Allow header.
func TestUnsupportedMethodIsJSON(t *testing.T) {
	s := newTraceStore(t.TempDir())
	req := httptest.NewRequest(http.MethodPut, "/api/trace", nil)
	w := httptest.NewRecorder()
	s.handleTrace(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d, want 405", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("content-type = %q, want JSON", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Allow") == "" {
		t.Errorf("no Allow header")
	}
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Errorf("405 body is not JSON: %v", err)
	}
}

// The append path must be reader-atomic: a concurrent GET may see the old file
// or the new one, never a partial one.
func TestReaderNeverSeesPartialFile(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	postTrace(t, s, "?client=r", `[[{"ts":1,"x":1,"y":1}]]`)

	done := make(chan struct{})
	var bad int
	var mu sync.Mutex
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			b := getTrace(t, s, "?client=r").Body.Bytes()
			if len(strings.TrimSpace(string(b))) == 0 {
				continue
			}
			var out []Stroke
			if err := json.Unmarshal(b, &out); err != nil {
				mu.Lock()
				bad++
				mu.Unlock()
			}
		}
	}()
	for i := 0; i < 60; i++ {
		postTrace(t, s, "?client=r", `[[{"ts":2,"x":2,"y":2}]]`)
	}
	<-done
	if bad != 0 {
		t.Errorf("%d partial reads across 200 concurrent GETs", bad)
	}
}

// fx/fy must always be PRESENT in the output (no omitempty): a reader that sees
// a missing key cannot tell "offset 0" from "recorded before offsets existed".
func TestFxAlwaysSerialised(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)
	postTrace(t, s, "?client=f", `[[{"ts":1,"x":1,"y":1,"fx":0,"fy":0}]]`)
	b, err := os.ReadFile(s.path("f"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"fx"`) || !strings.Contains(string(b), `"fy"`) {
		t.Errorf("fx/fy were omitted from the file: %.200s", b)
	}
}
