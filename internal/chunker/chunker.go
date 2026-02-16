// Package chunker splits markdown text into chunks for search indexing.
package chunker

import (
	"strings"
)

const (
	DefaultTargetSize = 400
	DefaultMinSize    = 100
	DefaultMaxSize    = 600
)

// Options configures chunking behavior.
type Options struct {
	TargetSize int
	MinSize    int
	MaxSize    int
}

// DefaultOptions returns default chunking options.
func DefaultOptions() Options {
	return Options{
		TargetSize: DefaultTargetSize,
		MinSize:    DefaultMinSize,
		MaxSize:    DefaultMaxSize,
	}
}

// ChunkResult represents a chunk with its position in the original text.
type ChunkResult struct {
	Text      string
	StartLine int
	EndLine   int
}

// Chunk splits text into chunks. Short text (<= maxSize) returns a single chunk.
func Chunk(text string, opts Options) []ChunkResult {
	if opts.TargetSize == 0 {
		opts = DefaultOptions()
	}

	text = strings.TrimSpace(text)
	if len(text) == 0 {
		return nil
	}

	// Short content — no chunking needed
	if len(text) <= opts.MaxSize {
		lines := strings.Count(text, "\n")
		return []ChunkResult{{Text: text, StartLine: 1, EndLine: lines + 1}}
	}

	// Split into blocks on markdown boundaries
	blocks := splitBlocks(text)

	// Merge small blocks, split large ones, targeting opts.TargetSize
	return mergeBlocks(blocks, opts)
}

// block is an intermediate representation of a text section.
type block struct {
	text      string
	startLine int
	endLine   int
}

// splitBlocks splits text on heading lines and double newlines.
func splitBlocks(text string) []block {
	lines := strings.Split(text, "\n")
	var blocks []block
	var current []string
	startLine := 1

	flush := func(endLine int) {
		if len(current) == 0 {
			return
		}
		t := strings.TrimSpace(strings.Join(current, "\n"))
		if t != "" {
			blocks = append(blocks, block{text: t, startLine: startLine, endLine: endLine})
		}
		current = nil
		startLine = endLine + 1
	}

	prevEmpty := false
	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Split on headings
		if strings.HasPrefix(trimmed, "#") && len(current) > 0 {
			flush(lineNum - 1)
		}

		// Split on double newlines (blank line after blank line)
		if trimmed == "" {
			if prevEmpty && len(current) > 0 {
				flush(lineNum - 1)
			}
			prevEmpty = true
			current = append(current, line)
			continue
		}
		prevEmpty = false
		current = append(current, line)
	}
	flush(len(lines))

	return blocks
}

// mergeBlocks combines small blocks and splits oversized ones.
func mergeBlocks(blocks []block, opts Options) []ChunkResult {
	var results []ChunkResult
	var accum block

	flushAccum := func() {
		t := strings.TrimSpace(accum.text)
		if t == "" {
			return
		}
		// If accumulated block is too large, hard-split it
		if len(t) > opts.MaxSize {
			results = append(results, hardSplit(t, accum.startLine, opts)...)
		} else {
			lines := strings.Count(t, "\n")
			results = append(results, ChunkResult{Text: t, StartLine: accum.startLine, EndLine: accum.startLine + lines})
		}
		accum = block{}
	}

	for _, b := range blocks {
		if accum.text == "" {
			accum = b
			continue
		}

		combined := accum.text + "\n\n" + b.text
		if len(combined) <= opts.TargetSize {
			accum.text = combined
			accum.endLine = b.endLine
		} else {
			flushAccum()
			accum = b
		}
	}
	flushAccum()

	return results
}

// hardSplit breaks text that exceeds maxSize on line boundaries.
func hardSplit(text string, startLine int, opts Options) []ChunkResult {
	lines := strings.Split(text, "\n")
	var results []ChunkResult
	var current []string
	curStart := startLine
	curLen := 0

	for i, line := range lines {
		lineLen := len(line)
		if curLen+lineLen > opts.TargetSize && len(current) > 0 {
			t := strings.TrimSpace(strings.Join(current, "\n"))
			if t != "" {
				results = append(results, ChunkResult{
					Text:      t,
					StartLine: curStart,
					EndLine:   startLine + i - 1,
				})
			}
			current = nil
			curStart = startLine + i
			curLen = 0
		}
		current = append(current, line)
		curLen += lineLen + 1 // +1 for newline
	}

	if len(current) > 0 {
		t := strings.TrimSpace(strings.Join(current, "\n"))
		if t != "" {
			results = append(results, ChunkResult{
				Text:      t,
				StartLine: curStart,
				EndLine:   startLine + len(lines) - 1,
			})
		}
	}

	return results
}
