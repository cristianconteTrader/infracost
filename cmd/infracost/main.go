package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/infracost/infracost/internal/apiclient"
	"github.com/infracost/infracost/internal/config"
	"github.com/infracost/infracost/internal/providers/terraform"
	"github.com/infracost/infracost/internal/ui"
	"github.com/infracost/infracost/internal/update"
	"github.com/infracost/infracost/internal/version"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/fatih/color"
)

var spinner *ui.Spinner

func main() {
	var appErr error
	updateMessageChan := make(chan *update.Info)

	cfg := config.DefaultConfig()
	appErr = cfg.LoadFromEnv()

	defer func() {
		if appErr != nil {
			handleCLIError(cfg, appErr)
		}

		unexpectedErr := recover()
		if unexpectedErr != nil {
			handleUnexpectedErr(cfg, unexpectedErr)
		}

		handleUpdateMessage(updateMessageChan)

		if appErr != nil || unexpectedErr != nil {
			os.Exit(1)
		}
	}()

	startUpdateCheck(cfg, updateMessageChan)

	rootCmd := &cobra.Command{
		Use:     "infracost",
		Version: version.Version,
		Short:   "Cloud cost estimates for Terraform",
		Long: fmt.Sprintf(`Infracost - cloud cost estimates for Terraform

%s
  https://infracost.io/docs`, ui.BoldString("DOCS")),
		Example: `  Generate a cost diff from Terraform directory with any required Terraform flags:

      infracost diff --path /path/to/code --terraform-plan-flags "-var-file=my.tfvars"
	
  Generate a full cost breakdown from Terraform directory with any required Terraform flags:

      infracost breakdown --path /path/to/code --terraform-plan-flags "-var-file=my.tfvars"`,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			cfg.Environment.Command = cmd.Name()

			return loadGlobalFlags(cfg, cmd)
		},
		PreRun: func(cmd *cobra.Command, args []string) {
			// If there's no args and the current dir isn't a Terraform dir show the help
			cwd, err := os.Getwd()
			if err == nil && len(cfg.Environment.Flags) == 0 && !terraform.IsTerraformDir(cwd) {
				_ = cmd.Help()
				os.Exit(0)
			}

			// Print the deprecation warnings
			msg := ui.WarningString("┌────────────────────────────────────────────────────────────────────────┐\n")
			msg += fmt.Sprintf("%s %s %s %s\n",
				ui.WarningString("│"),
				ui.WarningString("Warning:"),
				"The root command is deprecated and will be removed in v0.9.0.",
				ui.WarningString("│"),
			)

			msg += fmt.Sprintf("%s %s %s                                         %s\n",
				ui.WarningString("│"),
				"Please use",
				ui.PrimaryString("infracost breakdown"),
				ui.WarningString("│"),
			)

			msg += fmt.Sprintf("%s %s %s %s\n",
				ui.WarningString("│"),
				"Migration details:",
				ui.LinkString("https://www.infracost.io/docs/guides/v0.8_migration"),
				ui.WarningString("│"),
			)
			msg += ui.WarningString("└────────────────────────────────────────────────────────────────────────┘")

			if cfg.IsLogging() {
				for _, l := range strings.Split(ui.StripColor(msg), "\n") {
					log.Warn(l)
				}
			} else {
				fmt.Fprintln(os.Stderr, msg)
			}

			processDeprecatedEnvVars(cfg)
			processDeprecatedFlags(cmd)

			fmt.Fprintln(os.Stderr, "")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// The root command will be deprecated
			return breakdownCmd(cfg).RunE(cmd, args)
		},
	}

	// Add deprecated flags since the root command is deprecated
	addRootDeprecatedFlags(rootCmd)

	rootCmd.PersistentFlags().Bool("no-color", false, "Turn off colored output")
	rootCmd.PersistentFlags().String("log-level", "", "Log level (trace, debug, info, warn, error, fatal)")

	rootCmd.AddCommand(registerCmd(cfg))
	rootCmd.AddCommand(diffCmd(cfg))
	rootCmd.AddCommand(breakdownCmd(cfg))
	rootCmd.AddCommand(outputCmd(cfg))
	rootCmd.AddCommand(reportCmd(cfg))

	rootCmd.SetUsageTemplate(fmt.Sprintf(`%s{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

%s
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

%s
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

%s{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

%s
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

%s
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

%s{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`,
		ui.BoldString("USAGE"),
		ui.BoldString("ALIAS"),
		ui.BoldString("EXAMPLES"),
		ui.BoldString("AVAILABLE COMMANDS"),
		ui.BoldString("FLAGS"),
		ui.BoldString("GLOBAL FLAGS"),
		ui.BoldString("ADDITIONAL HELP TOPICS"),
	))

	rootCmd.SetVersionTemplate("Infracost {{.Version}}\n")

	appErr = rootCmd.Execute()
}

func startUpdateCheck(cfg *config.Config, c chan *update.Info) {
	go func() {
		updateInfo, err := update.CheckForUpdate(cfg)
		if err != nil {
			log.Debugf("error checking for update: %v", err)
		}
		c <- updateInfo
		close(c)
	}()
}

func checkAPIKey(apiKey string, apiEndpoint string, defaultEndpoint string) error {
	if apiEndpoint == defaultEndpoint && apiKey == "" {
		return errors.New(fmt.Sprintf(
			"No INFRACOST_API_KEY environment variable is set.\nWe run a free Cloud Pricing API, to get an API key run %s",
			ui.PrimaryString("infracost register"),
		))
	}

	return nil
}

func handleCLIError(cfg *config.Config, cliErr error) {
	if spinner != nil {
		spinner.Fail()
		fmt.Fprintln(os.Stderr, "")
	}

	if cliErr.Error() != "" {
		ui.PrintError(cliErr.Error())
	}

	err := apiclient.ReportCLIError(cfg, cliErr)
	if err != nil {
		log.Warnf("Error reporting CLI error: %s", err)
	}
}

func handleUnexpectedErr(cfg *config.Config, unexpectedErr interface{}) {
	if spinner != nil {
		spinner.Fail()
		fmt.Fprintln(os.Stderr, "")
	}

	stack := string(debug.Stack())

	ui.PrintUnexpectedError(unexpectedErr, stack)

	err := apiclient.ReportCLIError(cfg, fmt.Errorf("%s\n%s", unexpectedErr, stack))
	if err != nil {
		log.Warnf("Error reporting unexpected error: %s", err)
	}
}

func handleUpdateMessage(updateMessageChan chan *update.Info) {
	updateInfo := <-updateMessageChan
	if updateInfo != nil {
		msg := fmt.Sprintf("\n%s %s %s → %s\n%s\n",
			ui.WarningString("Update:"),
			"A new version of Infracost is available:",
			ui.PrimaryString(version.Version),
			ui.PrimaryString(updateInfo.LatestVersion),
			ui.Indent(updateInfo.Cmd, "  "),
		)
		fmt.Fprint(os.Stderr, msg)
	}
}

func loadGlobalFlags(cfg *config.Config, cmd *cobra.Command) error {
	if cmd.Flags().Changed("no-color") {
		cfg.NoColor, _ = cmd.Flags().GetBool("no-color")
	}
	color.NoColor = cfg.NoColor

	if cmd.Flags().Changed("log-level") {
		cfg.LogLevel, _ = cmd.Flags().GetString("log-level")
		err := cfg.ConfigureLogger()
		if err != nil {
			return err
		}
	}

	if cmd.Flags().Changed("pricing-api-endpoint") {
		cfg.PricingAPIEndpoint, _ = cmd.Flags().GetString("pricing-api-endpoint")
	}

	cfg.Environment.IsDefaultPricingAPIEndpoint = cfg.PricingAPIEndpoint == cfg.DefaultPricingAPIEndpoint

	flagNames := make([]string, 0)

	cmd.Flags().Visit(func(f *pflag.Flag) {
		flagNames = append(flagNames, f.Name)
	})

	cfg.Environment.Flags = flagNames

	return nil
}
