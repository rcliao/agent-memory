package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestEvalClockInvariance guards the frozen-clock fix: the same seeded corpus
// queried under clocks six hours apart must produce identical rankings, both
// for Search and Context. Before the store clock existed, seed backdates and
// scoring read the real wall clock at slightly different instants, so recency
// decay shifted between runs and rank ties flipped with the time of day.
func TestEvalClockInvariance(t *testing.T) {
	ctx := context.Background()

	runAt := func(epoch time.Time) (map[string][]string, map[string][]string) {
		s := newTestStore(t)
		s.SetClock(func() time.Time { return epoch })
		if _, err := SeedStore(ctx, s, DefaultSeedCorpus()); err != nil {
			t.Fatalf("seed: %v", err)
		}

		queries := []string{
			"how does the API gateway route requests",
			"what happened during the performance incident",
			"deploy process",
			"user preferences for communication",
		}
		searchOut := map[string][]string{}
		contextOut := map[string][]string{}
		for _, q := range queries {
			res, err := s.Search(ctx, SearchParams{Query: q, Limit: 10})
			if err != nil {
				t.Fatalf("search %q: %v", q, err)
			}
			if len(res) == 0 {
				t.Fatalf("search %q returned nothing — query no longer matches the seed corpus", q)
			}
			searchOut[q] = extractKeys(res)

			cres, err := s.Context(ctx, ContextParams{NS: "project:alpha", Query: q, Budget: 1000})
			if err != nil {
				t.Fatalf("context %q: %v", q, err)
			}
			contextOut[q] = contextKeys(cres)
		}
		return searchOut, contextOut
	}

	morningSearch, morningCtx := runAt(evalClockEpoch)
	eveningSearch, eveningCtx := runAt(evalClockEpoch.Add(6 * time.Hour))

	for q := range morningSearch {
		if !reflect.DeepEqual(morningSearch[q], eveningSearch[q]) {
			t.Errorf("search ranking for %q changed with wall clock:\n  t0: %v\n  t0+6h: %v",
				q, morningSearch[q], eveningSearch[q])
		}
		if !reflect.DeepEqual(morningCtx[q], eveningCtx[q]) {
			t.Errorf("context ranking for %q changed with wall clock:\n  t0: %v\n  t0+6h: %v",
				q, morningCtx[q], eveningCtx[q])
		}
	}
}
