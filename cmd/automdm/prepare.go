package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"github.com/WatchBeam/clock"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/migrations/tables"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	"github.com/spf13/cobra"
)

func createPrepareCmd(configManager config.Manager) *cobra.Command {
	prepareCmd := &cobra.Command{
		Use:   "prepare",
		Short: "Subcommands for initializing AutoMDM infrastructure",
		Long: `
Subcommands for initializing AutoMDM infrastructure

To setup AutoMDM infrastructure, use one of the available commands.
`,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help() //nolint:errcheck
		},
	}

	noPrompt := false
	// Whether to show table stats before and after the migration
	showTableStats := false

	dbCmd := &cobra.Command{
		Use:   "db",
		Short: "Given correct database configurations, prepare the databases for use",
		Long:  ``,
		Run: func(cmd *cobra.Command, args []string) {
			config, err := configManager.LoadConfig()
			if err != nil {
				initFatal(err, "loading config")
			}

			if dev_mode.IsEnabled {
				applyDevFlags(&config)
				noPrompt = true
			}

			// migrations issue a prepared statement per row, the path most exposed to 1615
			ds, err := mysql.New(config.Mysql, clock.C, mysql.WithReprepareRetry())
			if err != nil {
				initFatal(err, "creating db connection")
			}

			status, err := ds.MigrationStatus(cmd.Context())
			if err != nil {
				initFatal(err, "retrieving migration status")
			}
			if showTableStats {
				defer func() {
					stats, err := ds.GetTableRowCounts(cmd.Context())
					if err != nil {
						initFatal(err, "getting table stats")
					}
					statsAsJSON, err := json.Marshal(stats)
					if err != nil {
						initFatal(err, "encoding table row counts to JSON")
					}
					fmt.Printf("Table Row Counts: %s\n", statsAsJSON)
				}()
			}

			switch status.StatusCode {
			case automdm.NoMigrationsCompleted:
				// OK
			case automdm.AllMigrationsCompleted:
				fmt.Println("Migrations already completed. Nothing to do.")
				return
			case automdm.SomeMigrationsCompleted:
				if !noPrompt {
					printMissingMigrationsPrompt(status.MissingTable)
					bufio.NewScanner(os.Stdin).Scan()
				}
			case automdm.UnknownMigrations:
				printUnknownMigrationsMessage(status.UnknownTable)
				if dev_mode.IsEnabled {
					os.Exit(1)
				}
			}

			if err := ds.MigrateTables(cmd.Context()); err != nil {
				initFatal(err, "migrating db schema")
			}

			fmt.Println("Migrations completed.")
		},
	}

	dbCmd.PersistentFlags().BoolVar(&noPrompt, "no-prompt", false, "disable prompting before migrations (for use in scripts)")
	dbCmd.PersistentFlags().BoolVar(&dev_mode.IsEnabled, "dev", false, "Enable developer options")
	dbCmd.PersistentFlags().BoolVar(&showTableStats, "with-table-stats", false, "Show approximate table row counts after migrations")

	prepareCmd.AddCommand(dbCmd)
	return prepareCmd
}

func printUnknownMigrationsMessage(versions []int64) {
	explanation := "#   Your AutoMDM database has unrecognized migrations. This could happen when\n" +
		"#   running an older version of AutoMDM on a newer migrated database.\n"
	// Migrations newer than the baseline also come from AutoMDM 4 releases after
	// 4.95.0, which can be released after the AutoMDM 5 version an admin upgrades to.
	if len(versions) > 0 && slices.Min(versions) > tables.AutoMDM5BaselineVersion {
		explanation = "#   Your AutoMDM database has migrations from a newer AutoMDM release, such as a\n" +
			"#   newer AutoMDM 5 release or an AutoMDM 4 release after 4.95.0 (4.95.1, 4.96.0,\n" +
			"#   etc.). Upgrade to the latest AutoMDM 5 release.\n"
	}
	fmt.Printf("################################################################################\n"+
		"# WARNING:\n"+
		explanation+
		"#\n"+
		"#   Unknown migrations: %s.\n"+
		"################################################################################\n",
		migrationsToString(versions))
}

func printMissingMigrationsPrompt(tables []int64) {
	fmt.Printf("################################################################################\n"+
		"# WARNING:\n"+
		"#   This will perform AutoMDM database migrations. Please back up your data before\n"+
		"#   continuing.\n"+
		"#\n"+
		"#   Missing migrations: %s.\n"+
		"#\n"+
		"#   Press Enter to continue, or Control-c to exit.\n"+
		"################################################################################\n",
		migrationsToString(tables))
}

func migrationsToString(tables []int64) string {
	if len(tables) == 0 {
		return "unknown"
	}
	return fmt.Sprintf("tables=%v", tables)
}
