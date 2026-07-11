package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/rcliao/ghost/internal/store"
	"github.com/spf13/cobra"
)

// gcOutput is the structured result of a gc run.
type gcOutput struct {
	Mode        string `json:"mode"`
	Deleted     int64  `json:"deleted,omitempty"`
	WouldDelete int64  `json:"would_delete,omitempty"`
	ChunksFreed int64  `json:"chunks_freed,omitempty"`
	Protected   int64  `json:"protected,omitempty"`
	Vacuumed    bool   `json:"vacuumed,omitempty"`
	Remaining   int64  `json:"remaining"`
	DBSizeBytes int64  `json:"db_size_bytes"`
}

func (o gcOutput) textSummary() string {
	switch o.Mode {
	case "expired":
		return fmt.Sprintf("gc: deleted %d expired memories, freed %d chunks, %d remaining (%s)",
			o.Deleted, o.ChunksFreed, o.Remaining, formatBytes(o.DBSizeBytes))
	case "expired_dry_run":
		return fmt.Sprintf("gc: would delete %d expired memories (%d chunks), %d remaining (%s)",
			o.WouldDelete, o.ChunksFreed, o.Remaining, formatBytes(o.DBSizeBytes))
	case "stale":
		return fmt.Sprintf("gc: deleted %d stale memories, %d protected (high/critical), %d remaining (%s)",
			o.Deleted, o.Protected, o.Remaining, formatBytes(o.DBSizeBytes))
	case "stale_dry_run":
		return fmt.Sprintf("gc: would delete %d stale memories, %d protected (high/critical), %d remaining (%s)",
			o.WouldDelete, o.Protected, o.Remaining, formatBytes(o.DBSizeBytes))
	case "purge_deleted":
		v := ""
		if o.Vacuumed {
			v = ", vacuumed"
		}
		return fmt.Sprintf("gc: purged %d soft-deleted memories, freed %d chunks%s, %d remaining (%s)",
			o.Deleted, o.ChunksFreed, v, o.Remaining, formatBytes(o.DBSizeBytes))
	case "purge_deleted_dry_run":
		return fmt.Sprintf("gc: would purge %d soft-deleted memories (%d chunks), %d remaining (%s)",
			o.WouldDelete, o.ChunksFreed, o.Remaining, formatBytes(o.DBSizeBytes))
	default:
		return fmt.Sprintf("gc: %d remaining (%s)", o.Remaining, formatBytes(o.DBSizeBytes))
	}
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func init() {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Delete expired memories",
		Run:   runGC,
	}

	cmd.Flags().Bool("dry-run", false, "Report what would be deleted without deleting")
	cmd.Flags().String("stale", "", "Soft-delete memories not accessed in duration (e.g. 30d)")
	cmd.Flags().String("purge-deleted", "", "Permanently remove soft-deleted memories + orphaned chunks older than a duration (e.g. 30d, or 'all')")
	cmd.Flags().Bool("vacuum", false, "Rebuild the database file to reclaim freed space (after purge)")
	RootCmd.AddCommand(cmd)
}

func runGC(cmd *cobra.Command, args []string) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	stale, _ := cmd.Flags().GetString("stale")
	purge, _ := cmd.Flags().GetString("purge-deleted")
	vacuum, _ := cmd.Flags().GetBool("vacuum")

	out := gcOutput{}

	if purge != "" {
		runPurgeDeleted(cmd, purge, vacuum, dryRun)
		return
	}

	if stale != "" {
		d, err := store.ParseTTL(stale)
		if err != nil {
			exitErr("gc", fmt.Errorf("invalid --stale %q — use a duration like 30d, 24h, or 60m", stale))
		}

		if dryRun {
			out.Mode = "stale_dry_run"
			result, err := st.GCStaleDryRun(cmd.Context(), d)
			if err != nil {
				exitErr("gc stale dry-run", err)
			}
			out.WouldDelete = result.MemoriesDeleted
			out.Protected = result.ProtectedCount
		} else {
			out.Mode = "stale"
			result, err := st.GCStale(cmd.Context(), d)
			if err != nil {
				exitErr("gc stale", err)
			}
			out.Deleted = result.MemoriesDeleted
			out.Protected = result.ProtectedCount
		}
	} else if dryRun {
		out.Mode = "expired_dry_run"
		result, err := st.GCDryRun(cmd.Context())
		if err != nil {
			exitErr("gc dry-run", err)
		}
		out.WouldDelete = result.MemoriesDeleted
		out.ChunksFreed = result.ChunksFreed
	} else {
		out.Mode = "expired"
		result, err := st.GC(cmd.Context())
		if err != nil {
			exitErr("gc", err)
		}
		out.Deleted = result.MemoriesDeleted
		out.ChunksFreed = result.ChunksFreed
	}

	out.Remaining, _ = st.MemoryCount(cmd.Context())

	if info, err := os.Stat(getDBPath()); err == nil {
		out.DBSizeBytes = info.Size()
	}

	if formatFlag == "text" {
		outputText(cmd, out.textSummary())
		return
	}

	outputJSONCompact(cmd, out)
}

// runPurgeDeleted permanently removes soft-deleted memories (and their orphaned
// chunks/edges) and optionally VACUUMs. Uses the concrete store so the Store
// interface is unchanged.
func runPurgeDeleted(cmd *cobra.Command, spec string, vacuum, dryRun bool) {
	sq, ok := st.(*store.SQLiteStore)
	if !ok {
		exitErr("gc purge-deleted", fmt.Errorf("purge-deleted requires the SQLite store"))
	}

	var olderThan time.Duration
	if spec != "all" && spec != "0" {
		d, err := store.ParseTTL(spec)
		if err != nil {
			exitErr("gc", fmt.Errorf("invalid --purge-deleted %q — use a duration like 30d, or 'all'", spec))
		}
		olderThan = d
	}

	out := gcOutput{}
	if dryRun {
		out.Mode = "purge_deleted_dry_run"
		r, err := sq.PurgeDeletedDryRun(cmd.Context(), olderThan)
		if err != nil {
			exitErr("gc purge dry-run", err)
		}
		out.WouldDelete = r.MemoriesPurged
		out.ChunksFreed = r.ChunksFreed
	} else {
		out.Mode = "purge_deleted"
		r, err := sq.PurgeDeleted(cmd.Context(), olderThan)
		if err != nil {
			exitErr("gc purge", err)
		}
		out.Deleted = r.MemoriesPurged
		out.ChunksFreed = r.ChunksFreed
		if vacuum {
			if err := sq.Vacuum(cmd.Context()); err != nil {
				exitErr("gc vacuum", err)
			}
			out.Vacuumed = true
		}
	}

	out.Remaining, _ = st.MemoryCount(cmd.Context())
	if info, err := os.Stat(getDBPath()); err == nil {
		out.DBSizeBytes = info.Size()
	}

	if formatFlag == "text" {
		outputText(cmd, out.textSummary())
		return
	}
	outputJSONCompact(cmd, out)
}
