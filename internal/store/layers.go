package store

import (
	"strings"

	"github.com/rcliao/ghost/internal/model"
)

// Identity layers (docs/cognitive-inspirations.md, "Self-Schema → Identity
// Layers"). Agents keep their persona in ghost as layer-tagged memories:
//
//	layer:charter     — near-immutable core (name, relationships, boundaries).
//	                    Writes require PutParams.LayerOverride; automatic
//	                    lifecycle machinery never touches them.
//	layer:personality — slow-evolving traits. Writes are allowed (and version
//	                    normally — history is the rollback store), but reflect,
//	                    merge/dedup, stale-GC, and supersede demotion are
//	                    forbidden, and soft-deleted versions are never purged.
//	layer:lore        — free-accreting episodic identity. Normal writes and
//	                    consolidation, but never silently dropped: exempt from
//	                    DELETE-class rules, merge absorption, stale-GC, and TTL.
const (
	LayerCharter     = "charter"
	LayerPersonality = "personality"
	LayerLore        = "lore"

	layerTagPrefix = "layer:"
)

// memoryLayer returns the identity layer designated by a layer:* tag, or "".
func memoryLayer(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, layerTagPrefix) {
			return strings.TrimPrefix(t, layerTagPrefix)
		}
	}
	return ""
}

// layerAutoProtected reports whether automatic lifecycle machinery (reflect
// rules, merge/dedup, stale-GC, supersede demotion, purge of old versions)
// must never mutate a memory of this layer. Charter and personality changes
// are owner/agent policy decisions, never maintenance side effects.
func layerAutoProtected(layer string) bool {
	return layer == LayerCharter || layer == LayerPersonality
}

func memoryAutoProtected(m model.Memory) bool {
	return layerAutoProtected(memoryLayer(m.Tags))
}

// SQL guards over the JSON-array tags column (layer tags serialize as
// `"layer:x"` inside the array string).
const (
	// any layer:* tag — used where no identity memory may ever be selected
	sqlNotLayerTagged = `(tags IS NULL OR tags NOT LIKE '%"layer:%')`
	// charter/personality only — their soft-deleted versions are the rollback
	// store and must survive purge; lore versions purge normally
	sqlNotLayerProtected = `NOT (COALESCE(tags,'') LIKE '%"layer:charter"%' OR COALESCE(tags,'') LIKE '%"layer:personality"%')`
)
