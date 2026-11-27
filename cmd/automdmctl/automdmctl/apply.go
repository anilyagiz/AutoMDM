package automdmctl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anilyagiz/automdm/v4/pkg/spec"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/urfave/cli/v2"
)

func applyCommand() *cli.Command {
	var (
		flFilename string
		flForce    bool
		flDryRun   bool
	)
	return &cli.Command{
		Name:      "apply",
		Usage:     "Use for one-off imports and backwards compatibility GitOps",
		UsageText: `automdmctl apply [options]`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "f",
				EnvVars:     []string{"FILENAME"},
				Value:       "",
				Destination: &flFilename,
				Usage:       "A file to apply",
			},
			&cli.BoolFlag{
				Name:        "force",
				EnvVars:     []string{"FORCE"},
				Destination: &flForce,
				Usage:       "Force applying the file even if it raises validation errors (only supported for 'config' and 'team' specs)",
			},
			&cli.BoolFlag{
				Name:        "dry-run",
				EnvVars:     []string{"DRY_RUN"},
				Destination: &flDryRun,
				Usage:       "Do not apply the file, just validate it (only supported for 'config' and 'team' specs)",
			},
			&cli.StringFlag{
				Name:  "policies-team",
				Usage: "A team's name, this flag is only used on policies specs (overrides 'team' key in the policies file). This allows to easily import a group of policies to a team.",
			},
			configFlag(),
			contextFlag(),
			debugFlag(),
			enableLogTopicsFlag(),
			disableLogTopicsFlag(),
		},
		Action: func(c *cli.Context) error {
			// Apply log topic overrides from flags/env vars.
			applyLogTopicFlags(c)

			if flFilename == "" {
				return errors.New("-f must be specified")
			}
			b, err := os.ReadFile(flFilename)
			if err != nil {
				return err
			}
			automdmClient, err := clientFromCLI(c)
			if err != nil {
				return err
			}

			// Check if the file has a .yml or .yaml extension
			ext := strings.ToLower(filepath.Ext(flFilename))
			if ext == "" {
				return errors.New("Missing file extension: only .yml or .yaml files can be applied")
			}
			if ext != ".yml" && ext != ".yaml" {
				return fmt.Errorf("Invalid file extension %s: only .yml or .yaml files can be applied", ext)
			}

			logf := func(format string, a ...interface{}) {
				fmt.Fprintf(c.App.Writer, format, a...)
			}

			specs, err := spec.GroupFromBytes(b, spec.GroupFromBytesOpts{
				LogFn: logf,
			})
			if err != nil {
				return err
			}

			opts := automdm.ApplyClientSpecOptions{
				ApplySpecOptions: automdm.ApplySpecOptions{
					Force:  flForce,
					DryRun: flDryRun,
				},
			}
			if policiesTeamName := c.String("policies-team"); policiesTeamName != "" {
				opts.TeamForPolicies = policiesTeamName
			}
			baseDir := filepath.Dir(flFilename)

			teamsSoftwareInstallers := make(map[string][]automdm.SoftwarePackageResponse)
			teamsVPPApps := make(map[string][]automdm.VPPAppResponse)
			teamsScripts := make(map[string][]automdm.ScriptResponse)

			_, _, _, _, err = automdmClient.ApplyGroup(c.Context, false, specs, baseDir, logf, nil, opts, teamsSoftwareInstallers, teamsVPPApps, teamsScripts, nil, nil)
			if err != nil {
				return err
			}
			return nil
		},
	}
}
