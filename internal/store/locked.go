package store

import "github.com/rcliao/ghost/internal/model"

// Protected-memory contract (docs/cognitive-inspirations.md, "Self-Schema →
// Protected Memories"). Ghost deliberately has no identity subsystem — callers
// (e.g. shell) define what charter/personality/lore mean via their own tags.
// The store enforces exactly two general safety properties:
//
//	pinned — chronic accessibility AND lifecycle immunity. Automatic
//	         machinery (reflect rules, merge/dedup, stale-GC, low-utility
//	         prune, supersede demotion) never mutates a pinned memory, and
//	         the soft-deleted versions of pinned memories are never purged
//	         (version history is the rollback store).
//	locked — deliberate-change-only (the read-only bit, cf. Letta's
//	         per-block read_only). A blessed tag: overwriting a live locked
//	         memory requires PutParams.Unlock (CLI --unlock). Locked
//	         memories get the same lifecycle immunity as pinned, whether or
//	         not they are pinned.
//
// Identity change stays possible — an agent evolves by deliberately writing
// new versions of its persona keys — but only ever as an intentional act,
// never as a maintenance side effect, and always revertibly (history kept).
//
// Neither property permits a TTL: expired-GC hard-deletes, which would void
// both guarantees.
const LockedTag = "locked"

func hasLockedTag(tags []string) bool {
	for _, t := range tags {
		if t == LockedTag {
			return true
		}
	}
	return false
}

// lifecycleProtected reports whether automatic lifecycle machinery must not
// mutate this memory.
func lifecycleProtected(m model.Memory) bool {
	return m.Pinned || hasLockedTag(m.Tags)
}

// SQL guard over the pinned column and the JSON-array tags column (the
// locked tag serializes as `"locked"` inside the array string).
const sqlNotProtected = `(COALESCE(pinned,0) = 0 AND (tags IS NULL OR tags NOT LIKE '%"locked"%'))`
