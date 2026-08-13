package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ghost source — declared source-identity aliases
// (docs/research/source-identity-design.md). Memory rows are never
// rewritten: a merge is an alias declaration, an unmerge is a delete, and
// resolution happens at read (ForUser boost, List --source-user).

func init() {
	sourceCmd := &cobra.Command{
		Use:   "source",
		Short: "Manage source-identity aliases (who a memory came from)",
		Long: "Declare that different source_user spellings name the same person.\n" +
			"Ghost never infers identity; equivalence is declared here and resolved\n" +
			"at read. Memory rows keep their verbatim source_user forever.",
	}

	aliasCmd := &cobra.Command{
		Use:   "alias <alias>",
		Short: "Declare an alias for a canonical source id",
		Args:  cobra.ExactArgs(1),
		RunE:  runSourceAlias,
	}
	aliasCmd.Flags().StringP("ns", "n", "", "Namespace (required)")
	aliasCmd.Flags().String("canonical", "", "Canonical source id this alias resolves to (required)")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List declared source aliases",
		Args:  cobra.NoArgs,
		RunE:  runSourceList,
	}
	listCmd.Flags().StringP("ns", "n", "", "Namespace (required)")

	rmCmd := &cobra.Command{
		Use:   "rm <alias>",
		Short: "Remove a declared alias (the spelling resolves to itself again)",
		Args:  cobra.ExactArgs(1),
		RunE:  runSourceRm,
	}
	rmCmd.Flags().StringP("ns", "n", "", "Namespace (required)")

	sourceCmd.AddCommand(aliasCmd, listCmd, rmCmd)
	RootCmd.AddCommand(sourceCmd)
}

func runSourceAlias(cmd *cobra.Command, args []string) error {
	ns, _ := cmd.Flags().GetString("ns")
	canonical, _ := cmd.Flags().GetString("canonical")
	if ns == "" || canonical == "" {
		return fmt.Errorf("--ns and --canonical are required")
	}
	if err := st.SetSourceAlias(cmd.Context(), ns, args[0], canonical); err != nil {
		return err
	}
	outputJSON(cmd, map[string]string{"ns": ns, "alias": args[0], "canonical": canonical, "status": "declared"})
	return nil
}

func runSourceList(cmd *cobra.Command, args []string) error {
	ns, _ := cmd.Flags().GetString("ns")
	if ns == "" {
		return fmt.Errorf("--ns is required")
	}
	aliases, err := st.ListSourceAliases(cmd.Context(), ns)
	if err != nil {
		return err
	}
	outputJSON(cmd, aliases)
	return nil
}

func runSourceRm(cmd *cobra.Command, args []string) error {
	ns, _ := cmd.Flags().GetString("ns")
	if ns == "" {
		return fmt.Errorf("--ns is required")
	}
	if err := st.DeleteSourceAlias(cmd.Context(), ns, args[0]); err != nil {
		return err
	}
	outputJSON(cmd, map[string]string{"ns": ns, "alias": args[0], "status": "removed"})
	return nil
}
