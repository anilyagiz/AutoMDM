package automdmctl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/cmd/automdmctl/automdmctl/testing_utils"
	"github.com/anilyagiz/automdm/v4/pkg/scripts"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/stretchr/testify/require"
)

func TestRunScriptCommand(t *testing.T) {
	_, ds := testing_utils.RunServerWithMockedDS(t,
		&service.TestServerOpts{
			License: &automdm.LicenseInfo{
				Tier: automdm.TierPremium,
			},
			NoCacheDatastore: true,
		},
		&service.TestServerOpts{
			HTTPServerConfig: &http.Server{WriteTimeout: 90 * time.Second}, // nolint:gosec
		},
	)
	adminUserByID := ds.UserByIDFunc
	technician := &automdm.User{GlobalRole: new(automdm.RoleTechnician)}

	ds.LoadHostSoftwareFunc = func(ctx context.Context, host *automdm.Host, includeCVEScores bool) error {
		return nil
	}
	ds.ListLabelsForHostFunc = func(ctx context.Context, hid uint) ([]*automdm.Label, error) {
		return nil, nil
	}
	ds.ListPoliciesForHostFunc = func(ctx context.Context, host *automdm.Host) ([]*automdm.HostPolicy, error) {
		return nil, nil
	}
	ds.GetHostCustomHostVitalsFunc = func(ctx context.Context, hostID uint) ([]automdm.HostCustomHostVital, error) {
		return nil, nil
	}
	ds.ListHostBatteriesFunc = func(ctx context.Context, hid uint) ([]*automdm.HostBattery, error) {
		return nil, nil
	}
	ds.HostLiteFunc = func(ctx context.Context, hid uint) (*automdm.Host, error) {
		return &automdm.Host{}, nil
	}
	ds.ListUpcomingHostMaintenanceWindowsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostMaintenanceWindow, error) {
		return nil, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.GetScriptIDByNameFunc = func(ctx context.Context, name string, teamID *uint) (uint, error) {
		return 1, nil
	}
	ds.IsExecutionPendingForHostFunc = func(ctx context.Context, hid uint, scriptID uint) (bool, error) {
		return false, nil
	}

	generateValidPath := func() string {
		return writeTmpScriptContents(t, "echo hello world", ".sh")
	}
	exceedsMaxCharsUnsaved := strings.Repeat("a", automdm.UnsavedScriptMaxRuneLen+1)
	exceedsMaxCharsSaved := strings.Repeat("a", automdm.SavedScriptMaxRuneLen+1)

	expectedOutputSuccess := `
Exit code: 0 (Script ran successfully.)

Output:

-------------------------------------------------------------------------------------

hello world

-------------------------------------------------------------------------------------
`

	expectedQuietOutputSuccess := `hello world
`

	type testCase struct {
		name                string
		user                *automdm.User
		scriptPath          func() string
		scriptName          string
		teamID              *uint
		savedScriptContents func() ([]byte, error)
		scriptResult        *automdm.HostScriptResult
		quiet               bool
		async               bool
		expectOutput        string
		expectErrMsg        string
		expectNotFound      bool
		expectOffline       bool
		expectPending       bool
	}

	cases := []testCase{
		{
			name:           "host not found",
			scriptPath:     generateValidPath,
			expectErrMsg:   automdm.HostNotFoundErrMsg,
			expectNotFound: true,
		},
		{
			name:         "invalid file type",
			scriptPath:   func() string { return writeTmpScriptContents(t, "echo hello world", ".txt") },
			expectErrMsg: automdm.RunScriptInvalidTypeErrMsg,
		},
		{
			name:         "invalid hashbang",
			scriptPath:   func() string { return writeTmpScriptContents(t, "#! /foo/bar", ".sh") },
			expectErrMsg: `Interpreter not supported. Supported interpreters are "#!/bin/sh", "#!/bin/bash", "#!/bin/zsh", "#!/usr/bin/env python3", or an absolute path to "python" / "python3".`,
		},
		{
			name:         "unsupported hashbang",
			scriptPath:   func() string { return writeTmpScriptContents(t, "#!/bin/ksh", ".sh") },
			expectErrMsg: `Interpreter not supported. Supported interpreters are "#!/bin/sh", "#!/bin/bash", "#!/bin/zsh", "#!/usr/bin/env python3", or an absolute path to "python" / "python3".`,
		},
		{
			name:       "posix shell hashbang",
			scriptPath: func() string { return writeTmpScriptContents(t, "#!/bin/sh", ".sh") },
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(0),
				Output:   "hello world",
			},
			expectOutput: expectedOutputSuccess,
		},
		{
			name:       "zsh hashbang",
			scriptPath: func() string { return writeTmpScriptContents(t, "#!/bin/zsh", ".sh") },
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(0),
				Output:   "hello world",
			},
			expectOutput: expectedOutputSuccess,
		},
		{
			name:       "usr zsh hashbang",
			scriptPath: func() string { return writeTmpScriptContents(t, "#!/usr/bin/zsh", ".sh") },
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(0),
				Output:   "hello world",
			},
			expectOutput: expectedOutputSuccess,
		},
		{
			name:       "zsh hashbang with arguments",
			scriptPath: func() string { return writeTmpScriptContents(t, "#!/bin/zsh -x", ".sh") },
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(0),
				Output:   "hello world",
			},
			expectOutput: expectedOutputSuccess,
		},
		{
			name: "script too long (unsaved)",
			scriptPath: func() string {
				return writeTmpScriptContents(t, exceedsMaxCharsUnsaved, ".sh")
			},
			expectErrMsg: "Script is too large. Script referenced by '--script-path' is limited to 10,000 characters. To run larger script save it to AutoMDM and use '--script-name'.",
		},
		{
			name: "script not too long (unsaved)",
			scriptPath: func() string {
				return writeTmpScriptContents(t, exceedsMaxCharsUnsaved[:automdm.UnsavedScriptMaxRuneLen], ".sh")
			},
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(0),
				Output:   "hello world",
			},
			expectOutput: expectedOutputSuccess,
		},
		{
			name:       "script too long (saved)",
			scriptName: "foo",
			savedScriptContents: func() ([]byte, error) {
				return []byte(exceedsMaxCharsSaved), nil
			},
			expectErrMsg: "Script is too large. It's limited to 500,000 characters (approximately 10,000 lines).",
		},
		{
			name:       "script not too long (saved)",
			scriptName: "foo",
			savedScriptContents: func() ([]byte, error) {
				return []byte(exceedsMaxCharsUnsaved), nil
			},
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(0),
				Output:   "hello world",
			},
			expectOutput: expectedOutputSuccess,
		},
		{
			name:         "script-path and script-name disallowed",
			scriptPath:   generateValidPath,
			scriptName:   "foo",
			expectErrMsg: `Only one of '--script-path' or '--script-name' or '-- <contents>' is allowed.`,
		},
		{
			name:         "missing one of script-path and script-nqme",
			expectErrMsg: `One of '--script-path' or '--script-name' or '-- <contents>' must be specified.`,
		},
		{
			name:         "script-path and team disallowed",
			scriptPath:   generateValidPath,
			teamID:       ptr.Uint(1),
			expectErrMsg: `Only one of '--script-path' or '--team' is allowed.`,
		},
		{
			name:         "script empty",
			scriptPath:   func() string { return writeTmpScriptContents(t, "", ".sh") },
			expectErrMsg: `Script contents must not be empty.`,
		},
		{
			name:         "invalid utf8",
			scriptPath:   func() string { return writeTmpScriptContents(t, "\xff\xfa", ".sh") },
			expectErrMsg: `Wrong data format. Only plain text allowed.`,
		},
		{
			name:       "script successful",
			scriptPath: generateValidPath,
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(0),
				Output:   "hello world",
			},
			expectOutput: expectedOutputSuccess,
		},
		{
			name:       "script quiet",
			scriptPath: generateValidPath,
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(0),
				Output:   "hello world\n",
			},
			expectOutput: expectedQuietOutputSuccess,
			quiet:        true,
		},
		{
			name:       "script failed",
			scriptPath: generateValidPath,
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(1),
				Output:   "",
			},
			expectOutput: `
Exit code: 1 (Script failed.)

Output:

-------------------------------------------------------------------------------------



-------------------------------------------------------------------------------------
`,
		},
		{
			name:       "script killed",
			scriptPath: generateValidPath,
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(-1),
				Output:   "Oh no!",
				Message:  automdm.HostScriptTimeoutMessage(ptr.Int(int(scripts.MaxHostExecutionTime.Seconds()))),
			},
			expectOutput: `
Error: Timeout. AutoMDM stopped the script after 300 seconds to protect host performance.

Output before timeout:

-------------------------------------------------------------------------------------

Oh no!

-------------------------------------------------------------------------------------
`,
		},
		{
			name:       "scripts disabled",
			scriptPath: generateValidPath,
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(-2),
				Output:   "",
				Message:  automdm.RunScriptDisabledErrMsg,
			},
			expectOutput: `
Error: Scripts are disabled for this host. To run scripts, deploy the automdmd agent with scripts enabled.

`,
		},
		{
			name:       "output truncated",
			scriptPath: generateValidPath,
			scriptResult: &automdm.HostScriptResult{
				ExitCode: ptr.Int64(0),
				Output:   exceedsMaxCharsUnsaved,
			},
			expectOutput: fmt.Sprintf(`
Exit code: 0 (Script ran successfully.)

Output:

-------------------------------------------------------------------------------------

AutoMDM records the last 10,000 characters to prevent downtime.

%s

-------------------------------------------------------------------------------------
`, exceedsMaxCharsUnsaved),
		},
		// TODO: this would take 5 minutes to run, we don't want that kind of slowdown in our test suite
		// but can be useful to have around for manual testing.
		// {
		//	name:         "host timeout",
		//	scriptPath:   generateValidPath,
		//	expectErrMsg: automdm.RunScriptHostTimeoutErrMsg,
		// },
		{
			name:         "technician cannot run script from path",
			user:         technician,
			scriptPath:   generateValidPath,
			expectErrMsg: automdm.RunScriptForbiddenErrMsg,
		},
		{
			name:         "technician cannot run script from path async",
			user:         technician,
			scriptPath:   generateValidPath,
			async:        true,
			expectErrMsg: automdm.RunScriptForbiddenErrMsg,
		},
		{
			name:       "technician can run saved script",
			user:       technician,
			scriptName: "foo",
			scriptResult: &automdm.HostScriptResult{
				ExitCode: new(int64(0)),
				Output:   "hello world",
			},
			expectOutput: expectedOutputSuccess,
		},
	}

	setupDS := func(t *testing.T, c testCase) {
		ds.UserByIDFunc = adminUserByID
		if c.user != nil {
			ds.UserByIDFunc = func(ctx context.Context, id uint) (*automdm.User, error) {
				return c.user, nil
			}
		}
		ds.HostIDByIdentifierFunc = func(ctx context.Context, ident string, _ automdm.HostIDByIdentifierOptions) (uint, error) {
			if ident != "host1" || c.expectNotFound {
				return 0, &notFoundError{}
			}
			return 42, nil
		}
		ds.GetHostIssuesLastUpdatedFunc = func(ctx context.Context, hostID uint) (time.Time, error) {
			return time.Now(), nil
		}
		ds.HostFunc = func(ctx context.Context, hid uint) (*automdm.Host, error) {
			if hid != 42 || c.expectNotFound {
				return nil, &notFoundError{}
			}
			h := automdm.Host{ID: hid, SeenTime: time.Now(), OrbitNodeKey: ptr.String("abc")}
			if c.expectOffline {
				h.SeenTime = time.Now().Add(-time.Hour)
			}
			return &h, nil
		}
		ds.ListPendingHostScriptExecutionsFunc = func(ctx context.Context, hid uint) ([]*automdm.HostScriptResult, error) {
			require.Equal(t, uint(42), hid)
			if c.expectPending {
				return []*automdm.HostScriptResult{{HostID: uint(42)}}, nil
			}
			return nil, nil
		}
		ds.GetHostScriptExecutionResultFunc = func(ctx context.Context, execID string) (*automdm.HostScriptResult, error) {
			if c.scriptResult != nil {
				return c.scriptResult, nil
			}
			return &automdm.HostScriptResult{}, nil
		}
		ds.GetHostLockWipeStatusFunc = func(ctx context.Context, host *automdm.Host) (*automdm.HostLockWipeStatus, error) {
			return &automdm.HostLockWipeStatus{}, nil
		}
		ds.NewHostScriptExecutionRequestFunc = func(ctx context.Context, req *automdm.HostScriptRequestPayload) (*automdm.HostScriptResult, error) {
			require.Equal(t, uint(42), req.HostID)
			// Otherwise a wrongly queued script makes automdmctl poll forever for a result.
			if c.expectErrMsg != "" {
				return nil, errors.New("unexpected script execution request")
			}
			return &automdm.HostScriptResult{
				Hostname:       "host1",
				HostID:         req.HostID,
				ScriptContents: req.ScriptContents,
				ExecutionID:    "123",
			}, nil
		}
		ds.IsHostDiskEncryptionKeyArchivedFunc = func(ctx context.Context, hostID uint) (bool, error) {
			return false, nil
		}
		ds.ConditionalAccessBypassedAtFunc = func(ctx context.Context, hostID uint) (*time.Time, error) {
			return nil, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		if c.savedScriptContents != nil {
			ds.GetScriptContentsFunc = func(ctx context.Context, id uint) ([]byte, error) {
				return c.savedScriptContents()
			}
			ds.ScriptFunc = func(ctx context.Context, id uint) (*automdm.Script, error) {
				return &automdm.Script{ID: id, Name: "foo"}, nil
			}
		} else {
			ds.GetScriptContentsFunc = func(ctx context.Context, id uint) ([]byte, error) {
				return []byte("echo hello world"), nil
			}
			ds.ScriptFunc = func(ctx context.Context, id uint) (*automdm.Script, error) {
				return &automdm.Script{ID: id, Name: "foo"}, nil
			}
		}
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setupDS(t, c)
			args := []string{"run-script", "--host", "host1"}

			if c.scriptPath != nil {
				scriptPath := c.scriptPath()
				defer os.Remove(scriptPath)
				args = append(args, "--script-path", scriptPath)
			}

			if c.scriptName != "" {
				args = append(args, "--script-name", c.scriptName)
			}

			if c.quiet {
				args = append(args, "--quiet")
			}

			if c.async {
				args = append(args, "--async")
			}

			if c.teamID != nil {
				args = append(args, "--team", fmt.Sprintf("%d", *c.teamID))
			}

			b, err := runAppNoChecks(args)
			if c.expectErrMsg != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), c.expectErrMsg)
			} else {
				require.NoError(t, err)
			}
			if c.scriptResult != nil {
				out := b.String()
				require.NoError(t, err)
				require.NotEmpty(t, out)
				require.Equal(t, c.expectOutput, out)
			} else {
				require.Empty(t, b.String())
			}
		})
	}
}

func writeTmpScriptContents(t *testing.T, scriptContents string, extension string) string {
	tmpFile, err := os.CreateTemp(t.TempDir(), "*"+extension)
	require.NoError(t, err)
	_, err = tmpFile.WriteString(scriptContents)
	require.NoError(t, err)
	return tmpFile.Name()
}
