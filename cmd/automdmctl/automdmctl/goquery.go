package automdmctl

import (
	"errors"

	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/urfave/cli/v2"
)

// goqueryRunner is set by the automdmctl binary's main package via
// SetGoqueryRunner. Keeping the github.com/AbGuthrie/goquery/v2 import out of
// this package prevents its init function from being linked into binaries
// (e.g. automdm server) that transitively import the automdmctl package without
// needing the goquery subcommand.
var goqueryRunner func(*service.Client) error

// SetGoqueryRunner registers the implementation of the `automdmctl goquery`
// subcommand. Call from package main before CreateApp.
func SetGoqueryRunner(runner func(*service.Client) error) {
	goqueryRunner = runner
}

func goqueryCommand() *cli.Command {
	return &cli.Command{
		Name:  "goquery",
		Usage: "Start the goquery interface",
		Flags: []cli.Flag{
			configFlag(),
			contextFlag(),
			yamlFlag(),
			debugFlag(),
		},
		Action: func(c *cli.Context) error {
			if goqueryRunner == nil {
				return errors.New("goquery support is not built into this binary")
			}
			automdmClient, err := clientFromCLI(c)
			if err != nil {
				return err
			}
			return goqueryRunner(automdmClient)
		},
	}
}
