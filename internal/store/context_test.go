package store

import (
	"context"
	"testing"
)

func TestContextBasic(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()

	// Put some memories
	s.Put(ctx, PutParams{NS: "test", Key: "go-lang", Content: "Go is a statically typed language"})
	s.Put(ctx, PutParams{NS: "test", Key: "rust-lang", Content: "Rust is a systems language with borrow checker"})
	s.Put(ctx, PutParams{NS: "test", Key: "python-lang", Content: "Python is a dynamic language popular for ML"})

	result, err := s.Context(ctx, ContextParams{
		NS:     "test",
		Query:  "language",
		Budget: 4000,
	})
	if err != nil {
		t.Fatalf("context: %v", err)
	}

	if len(result.Memories) == 0 {
		t.Fatal("expected at least one memory in context")
	}
	if result.Budget != 4000 {
		t.Errorf("expected budget 4000, got %d", result.Budget)
	}
}

func TestContextBudgetLimit(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()

	// Put a large memory
	longContent := ""
	for i := 0; i < 100; i++ {
		longContent += "This is a line about programming languages and their features. "
	}
	s.Put(ctx, PutParams{NS: "test", Key: "big", Content: longContent})
	s.Put(ctx, PutParams{NS: "test", Key: "small", Content: "Go is great for programming"})

	// Very small budget
	result, err := s.Context(ctx, ContextParams{
		NS:     "test",
		Query:  "programming",
		Budget: 50, // ~200 chars
	})
	if err != nil {
		t.Fatalf("context: %v", err)
	}

	// Should still return something (excerpt or small memory)
	if len(result.Memories) == 0 {
		t.Fatal("expected at least one memory even with small budget")
	}
}

func TestContextEmpty(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()

	result, err := s.Context(ctx, ContextParams{
		Query:  "nothing here",
		Budget: 4000,
	})
	if err != nil {
		t.Fatalf("context: %v", err)
	}

	if len(result.Memories) != 0 {
		t.Errorf("expected empty memories, got %d", len(result.Memories))
	}
}

func TestContextPriorityBoosting(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()

	// Put memories with different priorities
	s.Put(ctx, PutParams{NS: "test", Key: "low-pri", Content: "low priority info about coding", Priority: "low"})
	s.Put(ctx, PutParams{NS: "test", Key: "critical-pri", Content: "critical info about coding", Priority: "critical"})

	result, err := s.Context(ctx, ContextParams{
		NS:     "test",
		Query:  "coding",
		Budget: 4000,
	})
	if err != nil {
		t.Fatalf("context: %v", err)
	}

	if len(result.Memories) < 2 {
		t.Fatalf("expected 2 memories, got %d", len(result.Memories))
	}

	// Critical should score higher
	if result.Memories[0].Key != "critical-pri" {
		t.Errorf("expected critical-pri first, got %s", result.Memories[0].Key)
	}
}
