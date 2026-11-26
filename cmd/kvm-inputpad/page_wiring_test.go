package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The served document must be self-contained AND actually wire the machine into
// the page. A marker that survives into the response, or a shim that is not
// exported, leaves the page with no machine — which is how the load/clear
// boundaries stopped being posted and retired ink stayed the highest epoch.
func TestServedPageIsSelfContainedAndWired(t *testing.T) {
	doc := pageWithModules()

	if strings.Contains(doc, "PAD_MODULES") {
		t.Fatalf("the module marker survived into the served page: the modules were not inlined")
	}
	if !strings.Contains(doc, "window.createPadCore = createPadCore") {
		t.Fatalf("the machine is not exported to the page")
	}
	if !strings.Contains(doc, "window.bindPad = bindPad") {
		t.Fatalf("the event layer is not exported to the page")
	}
	// The page must call the machine's OWN boundaries, so the machine's state is
	// retired with the ink. A bare push('load') would log the boundary while the
	// machine kept a live tail.
	if !strings.Contains(doc, "core.load()") {
		t.Fatalf("the page does not invoke the load boundary on the machine")
	}
	if !strings.Contains(doc, "core.clear()") {
		t.Fatalf("the page does not invoke clear on the machine")
	}
	// createPadCore must be defined BEFORE it is called at top level.
	defineAt := strings.Index(doc, "function createPadCore")
	callAt := strings.Index(doc, "const core = createPadCore(")
	if defineAt < 0 || callAt < 0 || defineAt > callAt {
		t.Fatalf("createPadCore define=%d call=%d: the machine is used before it is defined", defineAt, callAt)
	}
}

// The page and the server must agree on the protocol version, or every post is
// refused with 409 and the client reloads in a loop. They are two literal
// constants in two languages, so this is the test that keeps them equal.
func TestPageProtocolMatchesServer(t *testing.T) {
	doc := pageWithModules()
	re := regexp.MustCompile(`const PROTOCOL = (\d+);`)
	m := re.FindStringSubmatch(doc)
	if m == nil {
		t.Fatal("const PROTOCOL not found in the served page")
	}
	got, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse page PROTOCOL: %v", err)
	}
	if got != Protocol {
		t.Fatalf("page PROTOCOL = %d but server Protocol = %d — every post would be refused", got, Protocol)
	}
}

// The page generation must move forward with the rework, because a tab keeps
// running the JS it loaded: an un-bumped JSV makes a pre-rework page
// indistinguishable from a post-rework one in the recorded file.
func TestPageGenerationBumped(t *testing.T) {
	doc := pageWithModules()
	re := regexp.MustCompile(`const JSV = (\d+);`)
	m := re.FindStringSubmatch(doc)
	if m == nil {
		t.Fatal("const JSV not found in the served page")
	}
	got, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse page JSV: %v", err)
	}
	if got < 4 {
		t.Fatalf("page JSV = %d, want >= 4: the reworked page must be distinguishable from the one it replaces", got)
	}
}

func TestServedPageHasNoExternalSubresources(t *testing.T) {
	doc := pageWithModules()
	lower := strings.ToLower(doc)
	for _, bad := range []string{"<script src", "<script  src", "src=\"/pad-core.js\"", "src=\"/pad-events.js\""} {
		if strings.Contains(lower, bad) {
			t.Fatalf("served page fetches %q: that subresource has no route and its loss kills the pad", bad)
		}
	}
}
