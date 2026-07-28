package cli

import (
	"fmt"

	"github.com/rcliao/ghost/internal/store"
	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(embedCmd)
}

var embedCmd = &cobra.Command{
	Use:   "embed",
	Short: "Manage vector embeddings",
}

func init() {
	var reembedAll bool
	backfillCmd := &cobra.Command{
		Use:   "backfill",
		Short: "Generate embeddings for all chunks that don't have one",
		Long:  "Generates vector embeddings for existing memory chunks that were stored before embeddings were enabled. This is a one-time operation.\n\nWith --all, clears every stored embedding first and regenerates with the current embedder — the migration path for an embedding-model change (old vectors live in the old model's space; mixing spaces breaks similarity).",
		RunE: func(cmd *cobra.Command, args []string) error {
			sqlStore, ok := st.(*store.SQLiteStore)
			if !ok {
				return fmt.Errorf("backfill requires SQLite store")
			}

			ctx := cmd.Context()
			fmt.Fprintln(cmd.OutOrStdout(), "Backfilling embeddings...")

			skipped := 0
			backfill := sqlStore.BackfillEmbeddings
			if reembedAll {
				fmt.Fprintln(cmd.OutOrStdout(), "(--all: clearing existing embeddings, regenerating with current model)")
				backfill = sqlStore.ReembedAll
			}
			updated, err := backfill(ctx, func(done, total, skip int) {
				skipped = skip
				if done%50 == 0 || done == total {
					fmt.Fprintf(cmd.OutOrStdout(), "  %d/%d chunks embedded", done, total)
					if skip > 0 {
						fmt.Fprintf(cmd.OutOrStdout(), " (%d skipped)", skip)
					}
					fmt.Fprintln(cmd.OutOrStdout())
				}
			})
			if err != nil {
				return fmt.Errorf("backfill: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Done. %d chunks embedded", updated)
			if skipped > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), ", %d skipped", skipped)
			}
			fmt.Fprintln(cmd.OutOrStdout(), ".")
			return nil
		},
	}

	backfillCmd.Flags().BoolVar(&reembedAll, "all", false, "clear ALL embeddings first and regenerate (embedding-model migration)")
	embedCmd.AddCommand(backfillCmd)
}
