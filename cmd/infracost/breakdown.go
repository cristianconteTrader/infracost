package main

import (
	"github.com/infracost/infracost/internal/config"
	"github.com/infracost/infracost/internal/ui"
	"github.com/spf13/cobra"
)

func breakdownCmd(ctx *config.RunContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "breakdown",
		Short: "Show full breakdown of costs",
		Long:  "Show full breakdown of costs",
		Example: `  Use Terraform directory with any required Terraform flags:

      infracost breakdown --path /path/to/code --terraform-plan-flags "-var-file=my.tfvars"

  Use Terraform plan JSON:

      terraform plan -out tfplan.binary
      terraform show -json tfplan.binary > plan.json
      infracost breakdown --path plan.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkAPIKey(ctx.Config.APIKey, ctx.Config.PricingAPIEndpoint, ctx.Config.DefaultPricingAPIEndpoint); err != nil {
				return err
			}

			err := loadRunFlags(ctx.Config, cmd)
			if err != nil {
				return err
			}

			ctx.SetMetadata("outputFormat", ctx.Config.Format)

			err = checkRunConfig(ctx.Config)
			if err != nil {
				ui.PrintUsageErrorAndExit(cmd, err.Error())
			}

			return runMain(cmd, ctx)
		},
	}

	addRunFlags(cmd)

	cmd.Flags().Bool("terraform-use-state", false, "Use Terraform state instead of generating a plan. Applicable when path is a Terraform directory")
	cmd.Flags().String("format", "table", "Output format: json, table, html")
	cmd.Flags().StringSlice("fields", []string{"monthlyQuantity", "unit", "monthlyCost"}, "Comma separated list of output fields: price,monthlyQuantity,unit,hourlyCost,monthlyCost.\nOnly supported by table output format")

	return cmd
}
