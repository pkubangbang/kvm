package main

// Tests for the "ink lingering" defect: retired ink stays drawn after Clear, and
// comes back after a page reload.
//
// These were written BEFORE the rework and FAILED on the code as it stood. The
// two defects they pin are independent:
//
//	(1) THE MACHINE NEVER REACHES THE BROWSER. page.html referenced
//	    /pad-core.js and /pad-events.js as external subresources, and no handler
//	    serves them; pageWithModules() inlines the modules by replacing a
//	    <!--PAD_MODULES--> marker that page.html no longer contained. So the
//	    served document had no state machine (or a 404-ed one), the page's own
//	    script threw at createPadCore, and the load/clear boundaries were never
//	    posted. With no boundary in the log, the highest epoch stays at the
//	    generation that should have been retired -> retired ink survives Clear
//	    and returns on reload.
//
//	(2) THE EPOCH MAP IS KEYED DIFFERENTLY FROM THE LOG. traceStore.epochs is
//	    keyed by the RAW client id while the file is keyed by sanitise(client).
//	    newTraceStore() rehydrates using the FILE's stem, i.e. the sanitised
//	    key. For any client id that sanitise() changes, the rehydrated seat is
//	    never read, so the first post-restart event is written at epoch 0 behind
//	    an epoch-N boundary: exactly the resurrection the rehydration exists to
//	    prevent.

import (
	"strings"
	"testing"
)

// --- (1) the page must carry its state machine inline -------------------

// The served document must define the machine itself. It is ONE document on
// purpose: this page is the VS Code webview pane, and a subresource fetch over
// that origin has already failed here (the page rendered and its module 404'd,
// leaving the pad with no state machine at all).
func TestServedPageCarriesStateMachineInline(t *testing.T) {
	doc := pageWithModules()

	if !strings.Contains(doc, "function createPadCore") {
		t.Fatalf("served page does not define createPadCore: the machine was not inlined")
	}
	if !strings.Contains(doc, "function bindPad") {
		t.Fatalf("served page does not define bindPad: the event layer was not inlined")
	}
	// A <script src> is a subresource the page cannot survive losing, and the
	// server does not route it.
	for _, bad := range []string{"<script src=", "<script src =\""} {
		if strings.Contains(doc, bad) {
			i := strings.Index(doc, bad)
			t.Fatalf("served page still fetches an external script (%q at %d): %q",
				bad, i, doc[i:i+80])
		}
	}
}

// The inline shim must not depend on CommonJS. pad-core.js ended with an
// UNGUARDED top-level `module.exports = ...`; inlined into a <script> that line
// throws ReferenceError and createPadCore is never defined, so the shim
// (`window.createPadCore = createPadCore`) throws too. This asserts the guard is
// present in what is actually served.
func TestInlinedMachineDoesNotRequireCommonJS(t *testing.T) {
	doc := pageWithModules()

	// The guard is a multi-line construct, so look for the construct itself
	// rather than a per-line substring: every `module.exports` in the served
	// document must be preceded by a `typeof module` test.
	for _, bad := range []string{"module.exports"} {
		idx := 0
		for {
			i := strings.Index(doc[idx:], bad)
			if i < 0 {
				break
			}
			at := idx + i
			// Walk BACK over the preceding two lines: the guard is either on the
			// same line or the line immediately above the assignment.
			start := at - 200
			if start < 0 {
				start = 0
			}
			window := doc[start:at]
			if !strings.Contains(window, "typeof module") {
				t.Fatalf("unguarded module.exports reaches the browser at %d: %q",
					at, doc[at:at+40])
			}
			idx = at + len(bad)
		}
	}
}

// --- (2) epochs must not go backwards across a restart ------------------

// The rehydration seat must be the SAME key the appends use. Any client id that
// sanitise() rewrites drove the two apart, and the epoch then restarted at 0
// behind an existing boundary.
func TestEpochSeededOnRestartForSanitisedClientID(t *testing.T) {
	dir := t.TempDir()

	// A client id the store must sanitise: ':' and '/' are not path-safe.
	const client = "pad:trace/c"
	if sanitise(client) == client {
		t.Fatalf("test is not measuring anything: sanitise(%q) == %q", client, client)
	}

	s1 := newTraceStore(dir)
	post(t, s1, client, padEvent{Kind: "load", TS: 1})
	post(t, s1, client, padEvent{Kind: "down", TS: 2, RX: f64(0.1), RY: f64(0.1)})
	post(t, s1, client, padEvent{Kind: "up", TS: 3, RX: f64(0.2), RY: f64(0.2)})
	post(t, s1, client, padEvent{Kind: "clear", TS: 4})
	pre := s1.epoch(client)
	if pre < 2 {
		t.Fatalf("epoch after load+clear on the first process = %d, want >= 2", pre)
	}

	// A fresh process on the same directory. The append-only file is the ONLY
	// index this store has.
	s2 := newTraceStore(dir)
	post(t, s2, client, padEvent{Kind: "down", TS: 5, RX: f64(0.3), RY: f64(0.3)})

	all := records(t, s2, client)
	last := all[len(all)-1]
	if last.Kind != "down" {
		t.Fatalf("last record = %q, want down", last.Kind)
	}
	if last.Epoch != pre {
		t.Fatalf("post-restart epoch = %d, want the seeded %d — the epoch went BACKWARDS behind the clear boundary, so retired ink is the highest epoch and comes back",
			last.Epoch, pre)
	}
}

// Two raw client ids that sanitise onto the same file must not keep two
// independent generation counters: they write one file, and the observer reads
// that file's highest epoch. Diverging counters make the file's epochs
// non-monotonic, which is the same resurrection by another route.
func TestEpochIsPerFileNotPerRawClientID(t *testing.T) {
	dir := t.TempDir()
	s := newTraceStore(dir)

	if sanitise("a.b") != sanitise("ab") {
		t.Fatalf("test is not measuring anything: %q vs %q", sanitise("a.b"), sanitise("ab"))
	}

	post(t, s, "a.b", padEvent{Kind: "load", TS: 1})
	post(t, s, "a.b", padEvent{Kind: "clear", TS: 2})
	post(t, s, "ab", padEvent{Kind: "down", TS: 3, RX: f64(0.1), RY: f64(0.1)})

	recs := records(t, s, "a.b")
	last := recs[kindLast(recs, "down", len(recs))]
	_ = last
	epochs := make([]int, 0, len(recs))
	for _, r := range recs {
		epochs = append(epochs, r.Epoch)
	}
	for i := 1; i < len(epochs); i++ {
		if epochs[i] < epochs[i-1] {
			t.Fatalf("epochs in ONE file went backwards: %v — the two ids counted generations separately", epochs)
		}
	}
	if epochs[len(epochs)-1] < 2 {
		t.Fatalf("last epoch in the shared file = %d, want >= 2 (it restarted at 0)", epochs[len(epochs)-1])
	}
}

// kindLast is a tiny index helper: the position of the last record of a kind.
func kindLast(recs []padRecord, kind string, fallback int) int {
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Kind == kind {
			return i
		}
	}
	return fallback - 1
}
