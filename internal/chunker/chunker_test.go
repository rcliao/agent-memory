package chunker

import (
	"strings"
	"testing"
)

func TestChunk_EmptyInput(t *testing.T) {
	result := Chunk("", DefaultOptions())
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestChunk_ShortContent(t *testing.T) {
	text := "This is a short memory."
	result := Chunk(text, DefaultOptions())
	if len(result) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(result))
	}
	if result[0].Text != text {
		t.Errorf("expected %q, got %q", text, result[0].Text)
	}
	if result[0].StartLine != 1 {
		t.Errorf("expected StartLine 1, got %d", result[0].StartLine)
	}
}

func TestChunk_SplitsOnHeadings(t *testing.T) {
	// Each section needs to be long enough that total exceeds MaxSize
	section := strings.Repeat("Some content filling space. ", 12) // ~336 chars
	text := "# Section One\n\n" + section + "\n\n# Section Two\n\n" + section + "\n\n# Section Three\n\n" + section

	result := Chunk(text, DefaultOptions())
	if len(result) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(result))
	}

	// First chunk should contain "Section One"
	if !strings.Contains(result[0].Text, "Section One") {
		t.Errorf("first chunk should contain 'Section One', got %q", result[0].Text)
	}
}

func TestChunk_RespectsMaxSize(t *testing.T) {
	opts := Options{TargetSize: 200, MinSize: 50, MaxSize: 300}
	// Generate text >300 chars with line breaks
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "This is a line of text that is about fifty characters long.")
	}
	text := strings.Join(lines, "\n") // ~1200 chars
	result := Chunk(text, opts)
	if len(result) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(result))
	}
}

func TestChunk_MergesSmallBlocks(t *testing.T) {
	text := `# A

Short.

# B

Also short.`

	opts := Options{TargetSize: 400, MinSize: 100, MaxSize: 600}
	result := Chunk(text, opts)
	// The whole thing is under MaxSize, so should be 1 chunk
	if len(result) != 1 {
		t.Errorf("expected 1 merged chunk, got %d", len(result))
	}
}

func TestChunk_DoubleNewlineSplit(t *testing.T) {
	// Build paragraphs that together exceed MaxSize
	para := strings.Repeat("This is a sentence. ", 15) // ~300 chars each
	text := para + "\n\n" + para + "\n\n" + para

	opts := Options{TargetSize: 400, MinSize: 100, MaxSize: 500}
	result := Chunk(text, opts)
	if len(result) < 2 {
		t.Fatalf("expected at least 2 chunks from paragraph splits, got %d", len(result))
	}
}
