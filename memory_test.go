package memory

import "testing"

// TestCaptureReExport is a smoke test that the public Capture API reaches the
// internal heuristic extractor (used by consumers like shell to distill salient
// turns into searchable memories).
func TestCaptureReExport(t *testing.T) {
	got := Capture("I always prefer ripgrep over grep for searching.", CaptureOptions{})
	if len(got) == 0 {
		t.Fatal("expected at least one candidate for a stated preference")
	}
	if got[0].Kind != "semantic" {
		t.Errorf("preference should be semantic, got %q", got[0].Kind)
	}
	if got[0].Key == "" || got[0].Importance <= 0 {
		t.Errorf("candidate should have a key and importance, got %+v", got[0])
	}
}
