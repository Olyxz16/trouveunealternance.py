package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	resetAll   bool
	resetBatch int
)

func init() {
	resetCmd.Flags().BoolVarP(&resetAll, "all", "a", false, "Reset all companies for re-enrichment")
	resetCmd.Flags().IntVarP(&resetBatch, "batch", "b", 20, "Number of companies to reset (if not --all)")
	rootCmd.AddCommand(resetCmd)
}

var resetCmd = &cobra.Command{
	Use:   "reset-enrich",
	Short: "Reset companies to NEW status for re-enrichment",
	Run: func(cmd *cobra.Command, args []string) {
		var companies []struct {
			ID     uint
			Name   string
			Status string
		}

		query := database.Model(&struct {
			ID     uint
			Name   string
			Status string
		}{}).Table("companies")

		if resetAll {
			query = query.Where("status != 'NEW'")
		} else {
			query = query.Where("status != 'NEW'").Limit(resetBatch)
		}

		if err := query.Find(&companies).Error; err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return
		}

		if len(companies) == 0 {
			fmt.Println("No companies to reset.")
			return
		}

		resetCount := 0
		for _, c := range companies {
			if err := database.ResetCompanyForEnrichment(c.ID); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to reset %s (ID %d): %v\n", c.Name, c.ID, err)
				continue
			}
			resetCount++
		}

		fmt.Printf("Reset %d companies for re-enrichment.\n", resetCount)
	},
}
