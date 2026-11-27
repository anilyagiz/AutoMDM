package automdmctl

import (
	"bytes"
	"errors"
	"io"
	"os"

	eeautomdmctl "github.com/anilyagiz/automdm/v4/ee/automdmctl"
	"github.com/anilyagiz/automdm/v4/server/version"
	"github.com/urfave/cli/v2"
)

const (
	defaultFileMode = 0o600
)

func CreateApp(
	reader io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	exitErrHandler cli.ExitErrHandlerFunc,
) *cli.App {
	app := cli.NewApp()
	app.Name = "automdmctl"
	app.Usage = "CLI for operating AutoMDM"
	app.Version = version.Version().Version
	app.ExitErrHandler = exitErrHandler
	cli.VersionPrinter = func(c *cli.Context) {
		version.PrintFull()
	}
	app.Reader = reader
	app.Writer = stdout
	app.ErrWriter = stderr
	app.DisableSliceFlagSeparator = true

	app.Commands = []*cli.Command{
		apiCommand(),
		applyCommand(),
		setupCommand(),
		loginCommand(),
		logoutCommand(),
		queryCommand(),
		getCommand(),
		{
			Name:  "config",
			Usage: "Modify AutoMDM server connection settings",
			Subcommands: []*cli.Command{
				configSetCommand(),
				configGetCommand(),
			},
		},
		convertCommand(),
		goqueryCommand(),
		userCommand(),
		debugCommand(),
		previewCommand(),
		eeautomdmctl.UpdatesCommand(),
		hostsCommand(),
		vulnerabilityDataStreamCommand(),
		packageCommand(),
		generateCommand(),
		{
			// It's become common for folks to unintentionally install automdmctl when they actually
			// need the AutoMDM server. This is hopefully a more helpful error message.
			Name:  "prepare",
			Usage: "This is not the binary you're looking for. Please use the automdm server binary for prepare commands.",
			Action: func(c *cli.Context) error {
				return errors.New("This is not the binary you're looking for. Please use the automdm server binary for prepare commands.")
			},
		},
		triggerCommand(),
		mdmCommand(),
		runScriptCommand(),
		gitopsCommand(),
		generateGitopsCommand(),
		newCommand(),
	}
	registerRemovedCommands(app)
	registerRemovedFlags(app)
	return app
}

func RunApp(args []string) (*bytes.Buffer, error) {
	// first arg must be the binary name. Allow tests to omit it.
	args = append([]string{""}, args...)

	w := new(bytes.Buffer)
	app := CreateApp(nil, w, os.Stderr, func(_ *cli.Context, _ error) {})
	StashRawArgs(app, args)
	err := app.Run(args)
	return w, err
}
