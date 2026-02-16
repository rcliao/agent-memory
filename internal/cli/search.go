package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rcliao/agent-memory/internal/store"
	"github.com/spf13/cobra"
)

func init() {
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search memories by keyword",
		Long:  "Search memory content, keys, and chunks for matching text.",
		Args:  cobra.MinimumNArgs(1),
		Run:   runSearch,
	}

	cmd.Flags().StringP("ns", "n", "", "Filter by namespace")
	cmd.Flags().String("kind", "", "Filter by kind")
	cmd.Flags().IntP("limit", "l", 20, "Max results")

	RootCmd.AddCommand(cmd)
}

func runSearch(cmd *cobra.Command, args []string) {
	ns, _ := cmd.Flags().GetString("ns")
	kind, _ := cmd.Flags().GetString("kind")
	limit, _ := cmd.Flags().GetInt("limit")
	query := strings.Join(args, " ")

	s, err := openStore()
	if err != nil {
		exitErr("open store", err)
	}
	defer s.Close()

	results, err := s.Search(cmd.Context(), store.SearchParams{
		NS:    ns,
		Query: query,
		Kind:  kind,
		Limit: limit,
	})
	if err != nil {
		exitErr("search", err)
	}

	if len(results) == 0 {
		fmt.Println("[]")
		return
	}

	b, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println(string(b))
}
