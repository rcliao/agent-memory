package store

import (
	"context"
	"testing"
)

func TestLinkCreate(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()

	s.Put(ctx, PutParams{NS: "test", Key: "a", Content: "memory a"})
	s.Put(ctx, PutParams{NS: "test", Key: "b", Content: "memory b"})

	link, err := s.Link(ctx, LinkParams{
		FromNS: "test", FromKey: "a",
		ToNS: "test", ToKey: "b",
		Rel: "relates_to",
	})
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if link.Rel != "relates_to" {
		t.Errorf("expected relates_to, got %s", link.Rel)
	}

	links, err := s.GetLinks(ctx, link.FromID)
	if err != nil {
		t.Fatalf("get links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
}

func TestLinkRemove(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()

	s.Put(ctx, PutParams{NS: "test", Key: "a", Content: "memory a"})
	s.Put(ctx, PutParams{NS: "test", Key: "b", Content: "memory b"})

	link, _ := s.Link(ctx, LinkParams{
		FromNS: "test", FromKey: "a",
		ToNS: "test", ToKey: "b",
		Rel: "depends_on",
	})

	// Remove it
	_, err := s.Link(ctx, LinkParams{
		FromNS: "test", FromKey: "a",
		ToNS: "test", ToKey: "b",
		Rel: "depends_on", Remove: true,
	})
	if err != nil {
		t.Fatalf("remove link: %v", err)
	}

	links, _ := s.GetLinks(ctx, link.FromID)
	if len(links) != 0 {
		t.Errorf("expected 0 links after remove, got %d", len(links))
	}
}

func TestLinkInvalidRel(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ctx := context.Background()

	s.Put(ctx, PutParams{NS: "test", Key: "a", Content: "memory a"})
	s.Put(ctx, PutParams{NS: "test", Key: "b", Content: "memory b"})

	_, err := s.Link(ctx, LinkParams{
		FromNS: "test", FromKey: "a",
		ToNS: "test", ToKey: "b",
		Rel: "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid relation")
	}
}
