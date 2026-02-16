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
		Use:   "list",
		Short: "List memories",
		Run:   runList,
	}

	cmd.Flags().StringP("ns", "n", "", "Filter by namespace")
	cmd.Flags().String("kind", "", "Filter by kind")
	cmd.Flags().StringP("tags", "t", "", "Filter by tags (comma-separated)")
	cmd.Flags().IntP("limit", "l", 20, "Max results")
	cmd.Flags().Bool("keys-only", false, "Only output ns/key pairs")

	RootCmd.AddCommand(cmd)
}

func runList(cmd *cobra.Command, args []string) {
	ns, _ := cmd.Flags().GetString("ns")
	kind, _ := cmd.Flags().GetString("kind")
	tagsStr, _ := cmd.Flags().GetString("tags")
	limit, _ := cmd.Flags().GetInt("limit")
	keysOnly, _ := cmd.Flags().GetBool("keys-only")

	var tags []string
	if tagsStr != "" {
		for _, t := range strings.Split(tagsStr, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
	}

	s, err := openStore()
	if err != nil {
		exitErr("open store", err)
	}
	defer s.Close()

	memories, err := s.List(cmd.Context(), store.ListParams{
		NS:    ns,
		Kind:  kind,
		Tags:  tags,
		Limit: limit,
	})
	if err != nil {
		exitErr("list", err)
	}

	if keysOnly {
		for _, m := range memories {
			fmt.Printf("%s/%s\n", m.NS, m.Key)
		}
		return
	}

	b, _ := json.MarshalIndent(memories, "", "  ")
	fmt.Println(string(b))
}
