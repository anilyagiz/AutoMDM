package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"runtime"

	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl"
	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/goquerycmd"
	"github.com/urfave/cli/v2"
)

func main() {
	// Register the goquery subcommand here so that the
	// github.com/AbGuthrie/goquery/v2 dependency (and its init function) are
	// only linked into the automdmctl binary, not into other binaries that
	// import the automdmctl package (e.g. automdm server).
	automdmctl.SetGoqueryRunner(goquerycmd.Run)

	app := automdmctl.CreateApp(os.Stdin, os.Stdout, os.Stderr, exitErrHandler)
	automdmctl.StashRawArgs(app, os.Args)
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stdout, "Error: %+v\n", automdmctl.CleanStatusCodeErr(err))
		os.Exit(1)
	}
}

// exitErrHandler implements cli.ExitErrHandlerFunc. If there is an error, prints it to stderr and exits with status 1.
func exitErrHandler(c *cli.Context, err error) {
	if err == nil {
		return
	}

	fmt.Fprintf(c.App.ErrWriter, "Error: %+v\n", automdmctl.CleanStatusCodeErr(err))

	if errors.Is(err, fs.ErrPermission) {
		switch runtime.GOOS {
		case "darwin", "linux":
			fmt.Fprintf(c.App.ErrWriter, "\nThis error can usually be resolved by fixing the permissions on the %s directory, or re-running this command with sudo.\n", path.Dir(c.String("config")))
		case "windows":
			fmt.Fprintf(c.App.ErrWriter, "\nThis error can usually be resolved by fixing the permissions on the %s directory, or re-running this command with 'Run as administrator'.\n", path.Dir(c.String("config")))
		}
	}
	cli.OsExiter(1)
}
