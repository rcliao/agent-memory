package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCJKSegment(t *testing.T) {
	cases := map[string]string{
		"我目前的拖鞋":             "我目 目前 前的 的拖 拖鞋",
		"plain english text": "plain english text",
		"鞋":                  "鞋",
		"拖鞋":                 "拖鞋",
		"買了new拖鞋today":       "買了 new 拖鞋 today",
	}
	for in, want := range cases {
		if got := cjkSegment(in); got != want {
			t.Errorf("cjkSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCJKQueryTerm(t *testing.T) {
	cases := map[string]string{
		"deploy": "deploy",
		"拖鞋":     "拖鞋",
		"鴻禧菇":    `"鴻禧 禧菇"`,
	}
	for in, want := range cases {
		if got := cjkQueryTerm(in); got != want {
			t.Errorf("cjkQueryTerm(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCJKSearchEndToEnd: the measured failure — a CJK word inside a longer
// sentence must be findable by FTS after bigram segmentation.
func TestCJKSearchEndToEnd(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "cjk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	put := func(key, content string) {
		if _, err := s.Put(ctx, PutParams{NS: "t", Key: key, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	put("slippers", "我目前的拖鞋是台灣買的還蠻貴的")
	put("mushroom", "今天晚餐有清炒鴻禧菇跟沙茶牛肉")
	put("english", "we deployed the pipeline with ArgoCD yesterday")

	for query, wantKey := range map[string]string{
		"拖鞋":      "slippers", // the measured 0-match case
		"鴻禧菇":     "mushroom",
		"沙茶牛肉":    "mushroom",
		"deploys": "english", // porter still works for English
	} {
		res, err := s.Search(ctx, SearchParams{NS: "t", Query: query, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range res {
			if r.Key == wantKey {
				found = true
			}
		}
		if !found {
			t.Errorf("query %q did not find %q (got %d results)", query, wantKey, len(res))
		}
	}

	// Update + delete keep the segmented index in sync via triggers.
	put("slippers", "換了新的居家拖鞋很好穿") // new version
	res, _ := s.Search(ctx, SearchParams{NS: "t", Query: "居家拖鞋", Limit: 5})
	found := false
	for _, r := range res {
		if r.Key == "slippers" {
			found = true
		}
	}
	if !found {
		t.Error("updated content not findable — trigger out of sync")
	}
}
