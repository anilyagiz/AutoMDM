package automdmctl

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/urfave/cli/v2"
)

const (
	hostsFlagName        = "hosts"
	labelFlagName        = "label"
	labelAutoMDMFlagName = "label-automdm"
	statusFlagName       = "status"
	searchQueryFlagName  = "search_query"
)

func hostsCommand() *cli.Command {
	return &cli.Command{
		Name:  "hosts",
		Usage: "Manage AutoMDM hosts",
		Subcommands: []*cli.Command{
			transferCommand(),
		},
	}
}

func transferCommand() *cli.Command {
	return &cli.Command{
		Name:      "transfer",
		Usage:     "Transfer one or more hosts to a team",
		UsageText: `This command will gather the set of hosts specified and transfer them to the team.`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     automdmFlagName,
				Usage:    "Team name hosts will be transferred to. Use '' for unassigned",
				Required: true,
			},
			&cli.StringFlag{
				Name:  hostsFlagName,
				Usage: "Comma-separated hostnames to transfer",
			},
			&cli.StringFlag{
				Name:  labelFlagName,
				Usage: "Label name to transfer",
			},
			&cli.StringFlag{
				Name:  labelAutoMDMFlagName,
				Usage: "Team name the --label label belongs to. Use when more than one team has a label with that name",
			},
			&cli.StringFlag{
				Name:  statusFlagName,
				Usage: "Status to use when filtering hosts",
			},
			&cli.StringFlag{
				Name:  searchQueryFlagName,
				Usage: "A search query that returns matching hostnames to be transferred",
			},
			configFlag(),
			contextFlag(),
			yamlFlag(),
			debugFlag(),
		},
		Action: func(c *cli.Context) error {
			client, err := clientFromCLI(c)
			if err != nil {
				return err
			}

			team := c.String(automdmFlagName)
			var hosts []string
			if hostsFlag := c.String(hostsFlagName); hostsFlag != "" {
				hosts = strings.Split(hostsFlag, ",")
			}
			label := c.String(labelFlagName)
			labelAutoMDM := c.String(labelAutoMDMFlagName)
			status := c.String(statusFlagName)
			searchQuery := c.String(searchQueryFlagName)

			if hosts != nil {
				if label != "" || labelAutoMDM != "" || searchQuery != "" || status != "" {
					return errors.New("--hosts cannot be used along side any other flag")
				}
			} else {
				if label == "" && searchQuery == "" && status == "" {
					return errors.New("You need to define either --hosts, or one or more of --label, --status, --search_query")
				}
				if label == "" && labelAutoMDM != "" {
					return errors.New("--label-automdm can only be used with --label")
				}
			}

			err = client.TransferHosts(hosts, label, labelAutoMDM, status, searchQuery, team)
			var sce kithttp.StatusCoder
			if label != "" && labelAutoMDM == "" && errors.As(err, &sce) && sce.StatusCode() == http.StatusConflict {
				return fmt.Errorf("To transfer one team's label, add --%s <team name>: %w", labelAutoMDMFlagName, err)
			}
			if hosts != nil && errors.As(err, &sce) && sce.StatusCode() == http.StatusForbidden {
				// GitOps users can't look up hosts by hostname, so they're denied before the transfer request.
				if me, meErr := client.Me(); meErr == nil && isGitOpsUser(me) {
					return errors.New(automdm.GitOpsTransferHostErrorMessage)
				}
			}
			return err
		},
	}
}
