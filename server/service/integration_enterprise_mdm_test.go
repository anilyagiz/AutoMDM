package service

// MDM-related tests for the enterprise (premium) suite, which runs without MDM configured.
//
// Belongs here: Apple and Windows OS update settings (automdm config and enforcement), Linux disk
// encryption, lock/unlock/wipe on Windows and Linux, and endpoints that require MDM when it is not
// configured.
//
// Does not belong here: tests that need MDM enabled (integration_mdm_*_test.go), setup
// experience (integration_enterprise_setup_experience_test.go), and VPP apps
// (integration_enterprise_software_test.go).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/mdm"
	mdmtest "github.com/anilyagiz/automdm/v4/server/mdm/testing_utils"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s *integrationEnterpriseTestSuite) TestWindowsUpdatesTeamConfig() {
	t := s.T()

	// Mock Apple GDMF API (required for validating OS update minimum version settings)
	mdmtest.StartNewAppleGDMFTestServer(t)

	// Create a team
	team := &automdm.Team{
		Name:        t.Name(),
		Description: "Team description",
		Secrets:     []*automdm.EnrollSecret{{Secret: "XYZ"}},
	}
	var tmResp teamResponse
	s.DoJSON("POST", "/api/latest/automdm/teams", team, http.StatusOK, &tmResp)
	require.Equal(t, team.Name, tmResp.Team.Name)
	team.ID = tmResp.Team.ID

	checkWindowsOSUpdatesProfile(t, s.ds, &team.ID, nil)

	// modify the team's config
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"windows_updates": &automdm.WindowsUpdates{
				DeadlineDays:    optjson.SetInt(5),
				GracePeriodDays: optjson.SetInt(2),
			},
		},
	}, http.StatusOK, &tmResp)
	require.Equal(t, 5, tmResp.Team.Config.MDM.WindowsUpdates.DeadlineDays.Value)
	require.Equal(t, 2, tmResp.Team.Config.MDM.WindowsUpdates.GracePeriodDays.Value)
	s.lastActivityMatches(automdm.ActivityTypeEditedWindowsUpdates{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "deadline_days": 5, "grace_period_days": 2}`, team.ID, team.Name), 0)

	checkWindowsOSUpdatesProfile(t, s.ds, &team.ID, &automdm.WindowsUpdates{
		DeadlineDays:    optjson.SetInt(5),
		GracePeriodDays: optjson.SetInt(2),
	})

	// get the team via the GET endpoint, check that it properly returns the mdm
	// settings.
	var getTmResp getTeamResponse
	s.DoJSON("GET", "/api/latest/automdm/teams/"+fmt.Sprint(team.ID), nil, http.StatusOK, &getTmResp)
	require.Equal(t, withCreateDefaults(automdm.TeamMDM{
		MacOSUpdates: automdm.AppleOSUpdateSettings{
			MinimumVersion: optjson.String{Set: true},
			Deadline:       optjson.String{Set: true},
			DeadlineDays:   optjson.Int{Set: true},
			UpdateNewHosts: optjson.Bool{Set: true},
		},
		IOSUpdates: automdm.AppleOSUpdateSettings{
			MinimumVersion: optjson.String{Set: true},
			Deadline:       optjson.String{Set: true},
			DeadlineDays:   optjson.Int{Set: true},
			UpdateNewHosts: optjson.Bool{Set: true},
		},
		IPadOSUpdates: automdm.AppleOSUpdateSettings{
			MinimumVersion: optjson.String{Set: true},
			Deadline:       optjson.String{Set: true},
			DeadlineDays:   optjson.Int{Set: true},
			UpdateNewHosts: optjson.Bool{Set: true, Valid: false, Value: false},
		},
		WindowsUpdates: automdm.WindowsUpdates{
			DeadlineDays:    optjson.SetInt(5),
			GracePeriodDays: optjson.SetInt(2),
		},
		MacOSSetup: automdm.MacOSSetup{
			EnableReleaseDeviceManually:      optjson.SetBool(false),
			Software:                         optjson.Slice[*automdm.MacOSSetupSoftware]{Set: true, Value: []*automdm.MacOSSetupSoftware{}},
			ManualAgentInstall:               optjson.Bool{Set: true},
			LockEndUserInfo:                  optjson.SetBool(false),
			EnableManagedLocalAccount:        optjson.SetBool(false),
			EndUserLocalAccountType:          optjson.SetString("admin"),
			EnableManagedLocalAccountWindows: optjson.SetBool(false),
		},
		WindowsSettings: defaultExpectedWindowsSettings(),
		AndroidSettings: automdm.AndroidSettings{
			Certificates: optjson.Slice[automdm.CertificateTemplateSpec]{Set: true, Value: []automdm.CertificateTemplateSpec{}},
		},
	}), getTmResp.Team.Config.MDM)

	// only update the deadline
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"windows_updates": &automdm.WindowsUpdates{
				DeadlineDays:    optjson.SetInt(6),
				GracePeriodDays: optjson.SetInt(2),
			},
		},
	}, http.StatusOK, &tmResp)
	require.Equal(t, 6, tmResp.Team.Config.MDM.WindowsUpdates.DeadlineDays.Value)
	require.Equal(t, 2, tmResp.Team.Config.MDM.WindowsUpdates.GracePeriodDays.Value)
	lastActivity := s.lastActivityMatches(automdm.ActivityTypeEditedWindowsUpdates{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "deadline_days": 6, "grace_period_days": 2}`, team.ID, team.Name), 0)

	checkWindowsOSUpdatesProfile(t, s.ds, &team.ID, &automdm.WindowsUpdates{
		DeadlineDays:    optjson.SetInt(6),
		GracePeriodDays: optjson.SetInt(2),
	})

	// setting the macos updates doesn't alter the windows updates
	tmResp = teamResponse{}
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates": &automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString("14.6.1"),
				Deadline:       optjson.SetString("2021-01-01"),
			},
		},
	}, http.StatusOK, &tmResp)
	require.Equal(t, "14.6.1", tmResp.Team.Config.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2021-01-01", tmResp.Team.Config.MDM.MacOSUpdates.Deadline.Value)
	require.Equal(t, 6, tmResp.Team.Config.MDM.WindowsUpdates.DeadlineDays.Value)
	require.Equal(t, 2, tmResp.Team.Config.MDM.WindowsUpdates.GracePeriodDays.Value)
	// did not create a new activity for windows updates
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedWindowsUpdates{}.ActivityName(), "", lastActivity)
	lastActivity = s.lastActivityMatches(automdm.ActivityTypeEditedMacOSMinVersion{}.ActivityName(), ``, 0)

	checkWindowsOSUpdatesProfile(t, s.ds, &team.ID, &automdm.WindowsUpdates{
		DeadlineDays:    optjson.SetInt(6),
		GracePeriodDays: optjson.SetInt(2),
	})

	// sending a nil MDM or WindowsUpdates config doesn't modify anything
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": nil,
	}, http.StatusOK, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"windows_updates": nil,
		},
	}, http.StatusOK, &tmResp)
	require.Equal(t, "14.6.1", tmResp.Team.Config.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2021-01-01", tmResp.Team.Config.MDM.MacOSUpdates.Deadline.Value)
	require.Equal(t, 6, tmResp.Team.Config.MDM.WindowsUpdates.DeadlineDays.Value)
	require.Equal(t, 2, tmResp.Team.Config.MDM.WindowsUpdates.GracePeriodDays.Value)
	// no new activity is created
	s.lastActivityMatches("", "", lastActivity)

	checkWindowsOSUpdatesProfile(t, s.ds, &team.ID, &automdm.WindowsUpdates{
		DeadlineDays:    optjson.SetInt(6),
		GracePeriodDays: optjson.SetInt(2),
	})

	// sending empty WindowsUpdates fields empties both fields
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"windows_updates": map[string]any{
				"deadline_days":     nil,
				"grace_period_days": nil,
			},
		},
	}, http.StatusOK, &tmResp)
	require.False(t, tmResp.Team.Config.MDM.WindowsUpdates.DeadlineDays.Valid)
	require.False(t, tmResp.Team.Config.MDM.WindowsUpdates.GracePeriodDays.Valid)
	s.lastActivityMatches(automdm.ActivityTypeEditedWindowsUpdates{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "deadline_days": null, "grace_period_days": null}`, team.ID, team.Name), 0)

	checkWindowsOSUpdatesProfile(t, s.ds, &team.ID, nil)

	// error checks:

	// try to set an invalid deadline
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"windows_updates": map[string]any{
				"deadline_days":     1000,
				"grace_period_days": 1,
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)

	// try to set an invalid grace period
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"windows_updates": map[string]any{
				"deadline_days":     1,
				"grace_period_days": 1000,
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)

	// try to set a deadline but not a grace period
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"windows_updates": map[string]any{
				"deadline_days": 1,
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)

	// try to set a grace period but no deadline
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"windows_updates": map[string]any{
				"grace_period_days": 1,
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)

	// try to set an empty grace period but a non-empty deadline
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"windows_updates": map[string]any{
				"deadline_days":     1,
				"grace_period_days": nil,
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
}

func (s *integrationEnterpriseTestSuite) assertAppleOSUpdatesDeclaration(teamID *uint, profileName string, expected *automdm.AppleOSUpdateSettings) {
	t := s.T()
	if teamID == nil {
		teamID = new(uint(0))
	}

	var declUUID string
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		err := sqlx.GetContext(context.Background(), q, &declUUID,
			`SELECT declaration_uuid FROM mdm_apple_declarations WHERE team_id = ? AND name = ?`, teamID, profileName)
		if expected == nil {
			require.Error(t, err)
			return nil
		}
		return err
	})

	if expected == nil {
		// we already validated that the declaration did not exist
		return
	}
	decl, err := s.ds.GetMDMAppleDeclaration(context.Background(), declUUID)
	require.NoError(t, err)

	require.Contains(t, string(decl.RawJSON), fmt.Sprintf(`"TargetOSVersion": "%s"`, expected.MinimumVersion.Value))
	require.Contains(t, string(decl.RawJSON), fmt.Sprintf(`"TargetLocalDateTime": "%sT12:00:00"`, expected.Deadline.Value))
}

func (s *integrationEnterpriseTestSuite) TestAppleOSUpdatesTeamConfig() {
	t := s.T()

	// Mock Apple GDMF API (required for validating OS update minimum version settings)
	mdmtest.StartNewAppleGDMFTestServer(t)

	team := &automdm.Team{
		Name:        t.Name(),
		Description: "Team description",
		Secrets:     []*automdm.EnrollSecret{{Secret: "XYZ"}},
	}
	var tmResp teamResponse
	s.DoJSON("POST", "/api/latest/automdm/teams", team, http.StatusOK, &tmResp)
	require.Equal(t, team.Name, tmResp.Team.Name)
	team.ID = tmResp.Team.ID

	// no OS updates settings at the moment
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMMacOSUpdatesProfileName, nil)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIOSUpdatesProfileName, nil)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIPadOSUpdatesProfileName, nil)

	// modify the team's config (macOS first)
	macOSUpdates := &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("14.6.1"),
		Deadline:       optjson.SetString("2021-01-01"),
		UpdateNewHosts: optjson.SetBool(true),
	}
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates": macOSUpdates,
		},
	}, http.StatusOK, &tmResp)
	require.Equal(t, "14.6.1", tmResp.Team.Config.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2021-01-01", tmResp.Team.Config.MDM.MacOSUpdates.Deadline.Value)
	require.True(t, tmResp.Team.Config.MDM.MacOSUpdates.UpdateNewHosts.Value)

	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeEditedMacOSMinVersion{}.ActivityName(),
		fmt.Sprintf(`{"team_id": %d, "team_name": %q, "minimum_version": "14.6.1", "deadline": "2021-01-01"}`, team.ID, team.Name), 0,
	)

	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeEnabledMacosUpdateNewHosts{}.ActivityName(),
		fmt.Sprintf(
			`{"team_id": %d, "team_name": %q}`,
			team.ID,
			team.Name,
		), 0,
	)

	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMMacOSUpdatesProfileName, macOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIOSUpdatesProfileName, nil)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIPadOSUpdatesProfileName, nil)

	// modify the team's config (now iOS and iPadOS)
	iOSUpdates := &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6.1"),
		Deadline:       optjson.SetString("2022-02-02"),
		UpdateNewHosts: optjson.SetBool(true),
	}
	iPadOSUpdates := &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6.1"),
		Deadline:       optjson.SetString("2023-03-03"),
		UpdateNewHosts: optjson.SetBool(true),
	}
	fmt.Printf("%+v\n", iOSUpdates)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ios_updates":    iOSUpdates,
			"ipados_updates": iPadOSUpdates,
		},
	}, http.StatusOK, &tmResp)
	require.Equal(t, "14.6.1", tmResp.Team.Config.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2021-01-01", tmResp.Team.Config.MDM.MacOSUpdates.Deadline.Value)
	require.Equal(t, optjson.SetBool(true), tmResp.Team.Config.MDM.MacOSUpdates.UpdateNewHosts)

	require.Equal(t, "17.6.1", tmResp.Team.Config.MDM.IOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2022-02-02", tmResp.Team.Config.MDM.IOSUpdates.Deadline.Value)
	// UpdateNewHosts values are ignored for iOS
	require.Equal(t, optjson.Bool{Set: true}, tmResp.Team.Config.MDM.IOSUpdates.UpdateNewHosts)

	require.Equal(t, "17.6.1", tmResp.Team.Config.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2023-03-03", tmResp.Team.Config.MDM.IPadOSUpdates.Deadline.Value)
	// UpdateNewHosts values are ignored for iPadOS
	require.Equal(t, optjson.Bool{Set: true}, tmResp.Team.Config.MDM.IPadOSUpdates.UpdateNewHosts)

	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedMacOSMinVersion{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "minimum_version": "14.6.1", "deadline": "2021-01-01"}`, team.ID, team.Name), 0)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIOSMinVersion{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "minimum_version": "17.6.1", "deadline": "2022-02-02"}`, team.ID, team.Name), 0)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIPadOSMinVersion{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "minimum_version": "17.6.1", "deadline": "2023-03-03"}`, team.ID, team.Name), 0)

	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMMacOSUpdatesProfileName, macOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIOSUpdatesProfileName, iOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIPadOSUpdatesProfileName, iPadOSUpdates)

	// only update the deadlines
	macOSUpdates = &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("14.6.1"),
		Deadline:       optjson.SetString("2025-10-01"),
		UpdateNewHosts: optjson.SetBool(true),
	}
	iOSUpdates = &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6.1"),
		Deadline:       optjson.SetString("2024-02-02"),
	}
	iPadOSUpdates = &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6.1"),
		Deadline:       optjson.SetString("2024-03-03"),
	}
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates":  macOSUpdates,
			"ios_updates":    iOSUpdates,
			"ipados_updates": iPadOSUpdates,
		},
	}, http.StatusOK, &tmResp)
	require.Equal(t, "14.6.1", tmResp.Team.Config.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2025-10-01", tmResp.Team.Config.MDM.MacOSUpdates.Deadline.Value)
	require.Equal(t, optjson.SetBool(true), tmResp.Team.Config.MDM.MacOSUpdates.UpdateNewHosts)

	require.Equal(t, "17.6.1", tmResp.Team.Config.MDM.IOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2024-02-02", tmResp.Team.Config.MDM.IOSUpdates.Deadline.Value)
	require.Equal(t, optjson.Bool{Set: true}, tmResp.Team.Config.MDM.IOSUpdates.UpdateNewHosts)

	require.Equal(t, "17.6.1", tmResp.Team.Config.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2024-03-03", tmResp.Team.Config.MDM.IPadOSUpdates.Deadline.Value)
	require.Equal(t, optjson.Bool{Set: true}, tmResp.Team.Config.MDM.IPadOSUpdates.UpdateNewHosts)

	macOSLastActivity := s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedMacOSMinVersion{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "minimum_version": "14.6.1", "deadline": "2025-10-01"}`, team.ID, team.Name), 0)
	iOSLastActivity := s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIOSMinVersion{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "minimum_version": "17.6.1", "deadline": "2024-02-02"}`, team.ID, team.Name), 0)
	iPadOSLastActivity := s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIPadOSMinVersion{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "minimum_version": "17.6.1", "deadline": "2024-03-03"}`, team.ID, team.Name), 0)

	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMMacOSUpdatesProfileName, macOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIOSUpdatesProfileName, iOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIPadOSUpdatesProfileName, iPadOSUpdates)

	// Unchecking the UpdateNewHosts flag should register as an activity
	macOSUpdates = &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("14.6.1"),
		Deadline:       optjson.SetString("2025-10-01"),
		UpdateNewHosts: optjson.SetBool(false),
	}
	iOSUpdates = &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6.1"),
		Deadline:       optjson.SetString("2024-02-02"),
	}
	iPadOSUpdates = &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6.1"),
		Deadline:       optjson.SetString("2024-03-03"),
	}
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates":  macOSUpdates,
			"ios_updates":    iOSUpdates,
			"ipados_updates": iPadOSUpdates,
		},
	}, http.StatusOK, &tmResp)
	require.False(t, tmResp.Team.Config.MDM.MacOSUpdates.UpdateNewHosts.Value)
	s.lastActivityOfTypeMatches(
		automdm.ActivityTypeDisabledMacosUpdateNewHosts{}.ActivityName(),
		fmt.Sprintf(
			`{"team_id": %d, "team_name": %q}`,
			team.ID,
			team.Name,
		), 0,
	)

	// setting the windows updates doesn't alter the apple updates
	tmResp = teamResponse{}
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"windows_updates": &automdm.WindowsUpdates{
				DeadlineDays:    optjson.SetInt(10),
				GracePeriodDays: optjson.SetInt(2),
			},
		},
	}, http.StatusOK, &tmResp)
	require.Equal(t, "14.6.1", tmResp.Team.Config.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2025-10-01", tmResp.Team.Config.MDM.MacOSUpdates.Deadline.Value)
	require.Equal(t, "17.6.1", tmResp.Team.Config.MDM.IOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2024-02-02", tmResp.Team.Config.MDM.IOSUpdates.Deadline.Value)
	require.Equal(t, "17.6.1", tmResp.Team.Config.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2024-03-03", tmResp.Team.Config.MDM.IPadOSUpdates.Deadline.Value)
	require.Equal(t, 10, tmResp.Team.Config.MDM.WindowsUpdates.DeadlineDays.Value)
	require.Equal(t, 2, tmResp.Team.Config.MDM.WindowsUpdates.GracePeriodDays.Value)
	// did not create a new activity for os updates
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedMacOSMinVersion{}.ActivityName(), "", macOSLastActivity)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIOSMinVersion{}.ActivityName(), "", iOSLastActivity)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIPadOSMinVersion{}.ActivityName(), "", iPadOSLastActivity)
	lastActivity := s.lastActivityMatches(automdm.ActivityTypeEditedWindowsUpdates{}.ActivityName(), ``, 0)

	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMMacOSUpdatesProfileName, macOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIOSUpdatesProfileName, iOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIPadOSUpdatesProfileName, iPadOSUpdates)

	// sending a nil MDM or MacOSUpdate config doesn't modify anything
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": nil,
	}, http.StatusOK, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates": nil,
		},
	}, http.StatusOK, &tmResp)
	require.Equal(t, "14.6.1", tmResp.Team.Config.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2025-10-01", tmResp.Team.Config.MDM.MacOSUpdates.Deadline.Value)
	require.Equal(t, "17.6.1", tmResp.Team.Config.MDM.IOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2024-02-02", tmResp.Team.Config.MDM.IOSUpdates.Deadline.Value)
	require.Equal(t, "17.6.1", tmResp.Team.Config.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2024-03-03", tmResp.Team.Config.MDM.IPadOSUpdates.Deadline.Value)
	// no new activity is created
	s.lastActivityMatches("", "", lastActivity)

	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMMacOSUpdatesProfileName, macOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIOSUpdatesProfileName, iOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIPadOSUpdatesProfileName, iPadOSUpdates)

	// sending macos settings but no macos_updates does not change the macos updates
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"apple_settings": map[string]any{},
		},
	}, http.StatusOK, &tmResp)
	require.Equal(t, "14.6.1", tmResp.Team.Config.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2025-10-01", tmResp.Team.Config.MDM.MacOSUpdates.Deadline.Value)
	// no new activity is created
	s.lastActivityMatches("", "", lastActivity)

	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMMacOSUpdatesProfileName, macOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIOSUpdatesProfileName, iOSUpdates)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIPadOSUpdatesProfileName, iPadOSUpdates)

	// sending empty apple os updates fields empties both fields and removes the DDM profiles
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates": map[string]any{
				"minimum_version": "",
				"deadline":        nil,
			},
			"ios_updates": map[string]any{
				"minimum_version": "",
				"deadline":        nil,
			},
			"ipados_updates": map[string]any{
				"minimum_version": "",
				"deadline":        nil,
			},
		},
	}, http.StatusOK, &tmResp)
	require.Empty(t, tmResp.Team.Config.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Empty(t, tmResp.Team.Config.MDM.MacOSUpdates.Deadline.Value)
	require.Empty(t, tmResp.Team.Config.MDM.IOSUpdates.MinimumVersion.Value)
	require.Empty(t, tmResp.Team.Config.MDM.IOSUpdates.Deadline.Value)
	require.Empty(t, tmResp.Team.Config.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Empty(t, tmResp.Team.Config.MDM.IPadOSUpdates.Deadline.Value)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedMacOSMinVersion{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "minimum_version": "", "deadline": ""}`, team.ID, team.Name), 0)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIOSMinVersion{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "minimum_version": "", "deadline": ""}`, team.ID, team.Name), 0)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIPadOSMinVersion{}.ActivityName(), fmt.Sprintf(`{"team_id": %d, "team_name": %q, "minimum_version": "", "deadline": ""}`, team.ID, team.Name), 0)

	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMMacOSUpdatesProfileName, nil)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIOSUpdatesProfileName, nil)
	s.assertAppleOSUpdatesDeclaration(&team.ID, mdm.AutoMDMIPadOSUpdatesProfileName, nil)

	// error checks:

	// try to set an invalid deadline
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates": map[string]any{
				"minimum_version": "14.6.1",
				"deadline":        "2021-01-01T00:00:00Z",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ios_updates": map[string]any{
				"minimum_version": "14.6.1",
				"deadline":        "2021-01-01T00:00:00Z",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ipados_updates": map[string]any{
				"minimum_version": "14.6.1",
				"deadline":        "2021-01-01T00:00:00Z",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)

	// try to set an invalid minimum version
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates": map[string]any{
				"minimum_version": "14.6.1 (19A583)",
				"deadline":        "2021-01-01T00:00:00Z",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ios_updates": map[string]any{
				"minimum_version": "14.6.1 (19A583)",
				"deadline":        "2021-01-01T00:00:00Z",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ipados_updates": map[string]any{
				"minimum_version": "14.6.1 (19A583)",
				"deadline":        "2021-01-01T00:00:00Z",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)

	// try to set a deadline but not a minimum version
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates": map[string]any{
				"deadline": "2021-01-01T00:00:00Z",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ios_updates": map[string]any{
				"deadline": "2021-01-01T00:00:00Z",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ipados_updates": map[string]any{
				"deadline": "2021-01-01T00:00:00Z",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)

	// try to set an empty deadline but not a minimum version
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates": map[string]any{
				"deadline": "",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ios_updates": map[string]any{
				"deadline": "",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ipados_updates": map[string]any{
				"deadline": "",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)

	// try to set a minimum version but not a deadline
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates": map[string]any{
				"minimum_version": "14.6.1 (19A583)",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ios_updates": map[string]any{
				"minimum_version": "14.6.1 (19A583)",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ipados_updates": map[string]any{
				"minimum_version": "14.6.1 (19A583)",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)

	// try to set an empty minimum version but not a deadline
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"macos_updates": map[string]any{
				"minimum_version": "",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ios_updates": map[string]any{
				"minimum_version": "",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
	s.DoJSON("PATCH", fmt.Sprintf("/api/latest/automdm/teams/%d", team.ID), map[string]any{
		"mdm": map[string]any{
			"ipados_updates": map[string]any{
				"minimum_version": "",
			},
		},
	}, http.StatusUnprocessableEntity, &tmResp)
}

func (s *integrationEnterpriseTestSuite) TestLinuxDiskEncryption() {
	t := s.T()

	// create a Linux host
	noTeamHost, err := s.ds.NewHost(context.Background(), &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now(),
		NodeKey:         new(strings.ReplaceAll(t.Name(), "/", "_") + "3"),
		OsqueryHostID:   new(strings.ReplaceAll(t.Name(), "/", "_") + "3"),
		UUID:            t.Name() + "3",
		Hostname:        t.Name() + "foo3.local",
		PrimaryIP:       "192.168.1.3",
		PrimaryMac:      "30-65-EC-6F-C4-60",
		Platform:        "ubuntu",
		OSVersion:       "Ubuntu 22.04",
	})
	require.NoError(t, err)
	orbitKey := setOrbitEnrollment(t, noTeamHost, s.ds)
	noTeamHost.OrbitNodeKey = &orbitKey

	team, err := s.ds.NewTeam(context.Background(), &automdm.Team{Name: "A team"})
	require.NoError(t, err)
	teamID := &team.ID
	teamHost, err := s.ds.NewHost(context.Background(), &automdm.Host{
		DetailUpdatedAt: time.Now(),
		LabelUpdatedAt:  time.Now(),
		PolicyUpdatedAt: time.Now(),
		SeenTime:        time.Now(),
		NodeKey:         new(strings.ReplaceAll(t.Name(), "/", "_") + "2"),
		OsqueryHostID:   new(strings.ReplaceAll(t.Name(), "/", "_") + "2"),
		UUID:            t.Name() + "2",
		Hostname:        t.Name() + "foo2.local",
		PrimaryIP:       "192.168.1.2",
		PrimaryMac:      "30-65-EC-6F-C4-59",
		Platform:        "rhel",
		OSVersion:       "Fedora 38.0", // this check is why HostLite now includes os_version in the data it's selecting
		TeamID:          teamID,
	})
	require.NoError(t, err)
	teamOrbitKey := setOrbitEnrollment(t, teamHost, s.ds)
	teamHost.OrbitNodeKey = &teamOrbitKey

	// NO TEAM //

	// config profiles endpoint should work but show all zeroes
	var profileSummary getMDMProfilesSummaryResponse
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles/summary", getMDMProfilesSummaryRequest{}, http.StatusOK, &profileSummary)
	require.Equal(t, automdm.MDMProfilesSummary{}, profileSummary.MDMProfilesSummary)

	// set encrypted for host
	require.NoError(t, s.ds.SetOrUpdateHostDisksEncryption(context.Background(), noTeamHost.ID, true, nil))

	// should still show zeroes
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles/summary", getMDMProfilesSummaryRequest{}, http.StatusOK, &profileSummary)
	require.Equal(t, automdm.MDMProfilesSummary{}, profileSummary.MDMProfilesSummary)

	// should be nil before disk encryption is turned on
	// from host details
	getHostResp := getHostResponse{}
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", noTeamHost.ID), nil, http.StatusOK, &getHostResp)
	require.Nil(t, getHostResp.Host.MDM.OSSettings)

	// and my device
	deviceToken := "for_sure_secure"
	createDeviceTokenForHost(t, s.ds, noTeamHost.ID, deviceToken)

	getDeviceHostResp := getDeviceHostResponse{}
	res := s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+deviceToken, nil, http.StatusOK)
	err = json.NewDecoder(res.Body).Decode(&getDeviceHostResp)
	require.NoError(t, err)
	require.Nil(t, getHostResp.Host.MDM.OSSettings)

	// turn on disk encryption enforcement
	s.Do("POST", "/api/latest/automdm/disk_encryption", updateDiskEncryptionRequest{EnableDiskEncryption: new(true)}, http.StatusNoContent)

	// should be populated after disk encryption is turned on
	// from host details
	getHostResp = getHostResponse{}
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", noTeamHost.ID), nil, http.StatusOK, &getHostResp)
	require.NotNil(t, getHostResp.Host.MDM.OSSettings)

	// and my device
	getDeviceHostResp = getDeviceHostResponse{}
	res = s.DoRawNoAuth("GET", "/api/latest/automdm/device/"+deviceToken, nil, http.StatusOK)
	err = json.NewDecoder(res.Body).Decode(&getDeviceHostResp)
	require.NoError(t, err)
	require.NotNil(t, getHostResp.Host.MDM.OSSettings)

	// should show the Linux host as pending
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles/summary", getMDMProfilesSummaryRequest{}, http.StatusOK, &profileSummary)
	require.Equal(t, automdm.MDMProfilesSummary{Pending: 1}, profileSummary.MDMProfilesSummary)

	// encryption summary should succeed (Linux encryption doesn't require MDM)
	var summary getMDMDiskEncryptionSummaryResponse
	s.DoJSON("GET", "/api/latest/automdm/disk_encryption", getMDMDiskEncryptionSummaryRequest{}, http.StatusOK, &summary)
	// disk is encrypted but key hasn't been escrowed yet
	require.Equal(t, automdm.MDMDiskEncryptionSummary{ActionRequired: automdm.MDMPlatformsCounts{Linux: 1}}, *summary.MDMDiskEncryptionSummary)

	// trigger escrow process from device
	// should fail because default Orbit version is too old
	res = s.DoRawNoAuth("POST", fmt.Sprintf("/api/latest/automdm/device/%s/mdm/linux/trigger_escrow", deviceToken), nil, http.StatusBadRequest)
	res.Body.Close()

	// should succeed now that Orbit version isn't too old
	require.NoError(t, s.ds.SetOrUpdateHostOrbitInfo(context.Background(), noTeamHost.ID, automdm.MinOrbitLUKSVersion, sql.NullString{}, sql.NullBool{}))
	res = s.DoRawNoAuth("POST", fmt.Sprintf("/api/latest/automdm/device/%s/mdm/linux/trigger_escrow", deviceToken), nil, http.StatusNoContent)
	res.Body.Close()

	// confirm that Orbit endpoint shows notification flag
	var orbitResponse automdm.OrbitGetConfigResponse
	s.DoJSON("POST", "/api/automdm/orbit/config", automdm.OrbitGetConfigRequest{OrbitNodeKey: orbitKey}, http.StatusOK, &orbitResponse)
	require.True(t, orbitResponse.Notifications.RunDiskEncryptionEscrow)

	// confirm that second Orbit pull doesn't show notification flag
	var secondOrbitResponse automdm.OrbitGetConfigResponse
	s.DoJSON("POST", "/api/automdm/orbit/config", automdm.OrbitGetConfigRequest{OrbitNodeKey: orbitKey}, http.StatusOK, &secondOrbitResponse)
	require.False(t, secondOrbitResponse.Notifications.RunDiskEncryptionEscrow)

	// the agent is now prompting; triggering again must not re-queue, and the response says so
	res = s.DoRawNoAuth("POST", fmt.Sprintf("/api/latest/automdm/device/%s/mdm/linux/trigger_escrow", deviceToken), nil, http.StatusConflict)
	require.Contains(t, extractServerErrorText(res.Body), automdm.LinuxEscrowInFlightMessage)
	res.Body.Close()
	retryAfter, err := strconv.Atoi(res.Header.Get("Retry-After"))
	require.NoError(t, err)
	require.Positive(t, retryAfter)
	require.LessOrEqual(t, retryAfter, int(automdm.LinuxEscrowInFlightWindow.Seconds()))
	var inFlightOrbitResponse automdm.OrbitGetConfigResponse
	s.DoJSON("POST", "/api/automdm/orbit/config", automdm.OrbitGetConfigRequest{OrbitNodeKey: orbitKey}, http.StatusOK, &inFlightOrbitResponse)
	require.False(t, inFlightOrbitResponse.Notifications.RunDiskEncryptionEscrow)

	// a progress report keeps the escrow in flight past the window
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		_, err := q.ExecContext(t.Context(), `UPDATE host_disk_encryption_keys SET escrow_sent_at = DATE_SUB(escrow_sent_at, INTERVAL 1 HOUR) WHERE host_id = ?`, noTeamHost.ID)
		return err
	})
	s.Do("POST", "/api/automdm/orbit/luks_data", automdm.OrbitPostLUKSRequest{
		OrbitNodeKey: *noTeamHost.OrbitNodeKey,
		Status:       automdm.LinuxEscrowStatusPrompting,
	}, http.StatusNoContent)
	res = s.DoRawNoAuth("POST", fmt.Sprintf("/api/latest/automdm/device/%s/mdm/linux/trigger_escrow", deviceToken), nil, http.StatusConflict)
	res.Body.Close()

	// a canceled prompt ends the in-flight state without recording an error, so a retry
	// queues immediately and the host's encryption status is left as it was
	var beforeCancel, afterCancel getMDMDiskEncryptionSummaryResponse
	s.DoJSON("GET", "/api/latest/automdm/disk_encryption", getMDMDiskEncryptionSummaryRequest{}, http.StatusOK, &beforeCancel)
	s.Do("POST", "/api/automdm/orbit/luks_data", automdm.OrbitPostLUKSRequest{
		OrbitNodeKey: *noTeamHost.OrbitNodeKey,
		Status:       automdm.LinuxEscrowStatusCanceled,
	}, http.StatusNoContent)
	s.DoJSON("GET", "/api/latest/automdm/disk_encryption", getMDMDiskEncryptionSummaryRequest{}, http.StatusOK, &afterCancel)
	require.Equal(t, *beforeCancel.MDMDiskEncryptionSummary, *afterCancel.MDMDiskEncryptionSummary)
	res = s.DoRawNoAuth("POST", fmt.Sprintf("/api/latest/automdm/device/%s/mdm/linux/trigger_escrow", deviceToken), nil, http.StatusNoContent)
	res.Body.Close()
	var canceledOrbitResponse automdm.OrbitGetConfigResponse
	s.DoJSON("POST", "/api/automdm/orbit/config", automdm.OrbitGetConfigRequest{OrbitNodeKey: orbitKey}, http.StatusOK, &canceledOrbitResponse)
	require.True(t, canceledOrbitResponse.Notifications.RunDiskEncryptionEscrow)

	// set an error first; the successful write should overwrite that
	s.Do("POST", "/api/automdm/orbit/luks_data", automdm.OrbitPostLUKSRequest{
		OrbitNodeKey: *noTeamHost.OrbitNodeKey,
		ClientError:  "Houston, we had a problem",
	}, http.StatusNoContent)

	// a report from the agent ends the in-flight state, so a retry queues immediately
	res = s.DoRawNoAuth("POST", fmt.Sprintf("/api/latest/automdm/device/%s/mdm/linux/trigger_escrow", deviceToken), nil, http.StatusNoContent)
	res.Body.Close()
	var retriggerOrbitResponse automdm.OrbitGetConfigResponse
	s.DoJSON("POST", "/api/automdm/orbit/config", automdm.OrbitGetConfigRequest{OrbitNodeKey: orbitKey}, http.StatusOK, &retriggerOrbitResponse)
	require.True(t, retriggerOrbitResponse.Notifications.RunDiskEncryptionEscrow)

	// the in-flight window expired while the user was still typing and they clicked Create key
	// again, so a stale request is pending when the key finally arrives
	mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
		_, err := q.ExecContext(t.Context(), `UPDATE host_disk_encryption_keys SET escrow_sent_at = DATE_SUB(escrow_sent_at, INTERVAL 1 HOUR) WHERE host_id = ?`, noTeamHost.ID)
		return err
	})
	res = s.DoRawNoAuth("POST", fmt.Sprintf("/api/latest/automdm/device/%s/mdm/linux/trigger_escrow", deviceToken), nil, http.StatusNoContent)
	res.Body.Close()

	// upload LUKS data
	keySlot := new(uint(1))
	s.Do("POST", "/api/automdm/orbit/luks_data", automdm.OrbitPostLUKSRequest{
		OrbitNodeKey: *noTeamHost.OrbitNodeKey,
		Passphrase:   "whale makes pail rise",
		Salt:         "the team i like lost",
		KeySlot:      keySlot,
	}, http.StatusNoContent)

	// saving the key drops the stale request, so the user is not prompted again
	var afterKeyOrbitResponse automdm.OrbitGetConfigResponse
	s.DoJSON("POST", "/api/automdm/orbit/config", automdm.OrbitGetConfigRequest{OrbitNodeKey: orbitKey}, http.StatusOK, &afterKeyOrbitResponse)
	require.False(t, afterKeyOrbitResponse.Notifications.RunDiskEncryptionEscrow)

	// confirm verified
	s.DoJSON("GET", "/api/latest/automdm/disk_encryption", getMDMDiskEncryptionSummaryRequest{}, http.StatusOK, &summary)
	require.Equal(t, automdm.MDMDiskEncryptionSummary{Verified: automdm.MDMPlatformsCounts{Linux: 1}}, *summary.MDMDiskEncryptionSummary)

	// get passphrase back
	var keyResponse getHostEncryptionKeyResponse
	s.DoJSON("GET", fmt.Sprintf(`/api/latest/automdm/hosts/%d/encryption_key`, noTeamHost.ID), getHostEncryptionKeyRequest{}, http.StatusOK, &keyResponse)
	require.Equal(t, "whale makes pail rise", keyResponse.EncryptionKey.DecryptedValue)

	// TEAM //
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles/summary", getMDMProfilesSummaryRequest{TeamID: teamID}, http.StatusOK, &profileSummary)
	require.Equal(t, automdm.MDMProfilesSummary{}, profileSummary.MDMProfilesSummary)

	// set encrypted for host
	require.NoError(t, s.ds.SetOrUpdateHostDisksEncryption(context.Background(), teamHost.ID, true, nil))

	// should still show zeroes
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles/summary", getMDMProfilesSummaryRequest{TeamID: teamID}, http.StatusOK, &profileSummary)
	require.Equal(t, automdm.MDMProfilesSummary{}, profileSummary.MDMProfilesSummary)

	// turn on disk encryption enforcement for team
	s.Do("POST", "/api/latest/automdm/disk_encryption", updateDiskEncryptionRequest{TeamID: teamID, EnableDiskEncryption: new(true)}, http.StatusNoContent)

	// should show the Linux host as pending
	s.DoJSON("GET", "/api/latest/automdm/configuration_profiles/summary", getMDMProfilesSummaryRequest{TeamID: teamID}, http.StatusOK, &profileSummary)
	require.Equal(t, automdm.MDMProfilesSummary{Pending: 1}, profileSummary.MDMProfilesSummary)

	// encryption summary should show host as action required
	s.DoJSON("GET", "/api/latest/automdm/disk_encryption", getMDMDiskEncryptionSummaryRequest{TeamID: teamID}, http.StatusOK, &summary)
	require.Equal(t, automdm.MDMDiskEncryptionSummary{ActionRequired: automdm.MDMPlatformsCounts{Linux: 1}}, *summary.MDMDiskEncryptionSummary)

	// upload LUKS data (no error, and no trigger, first this time)
	keySlot = new(uint(3))
	s.Do("POST", "/api/automdm/orbit/luks_data", automdm.OrbitPostLUKSRequest{ // #nosec G101 - test data
		OrbitNodeKey: *teamHost.OrbitNodeKey,
		Passphrase:   "the mome raths outgrabe",
		Salt:         "jabberwocky, but salty",
		KeySlot:      keySlot,
	}, http.StatusNoContent)

	// confirm verified
	s.DoJSON("GET", "/api/latest/automdm/disk_encryption", getMDMDiskEncryptionSummaryRequest{TeamID: teamID}, http.StatusOK, &summary)
	require.Equal(t, automdm.MDMDiskEncryptionSummary{Verified: automdm.MDMPlatformsCounts{Linux: 1}}, *summary.MDMDiskEncryptionSummary)

	// get passphrase back
	s.DoJSON("GET", fmt.Sprintf(`/api/latest/automdm/hosts/%d/encryption_key`, teamHost.ID), getHostEncryptionKeyRequest{}, http.StatusOK, &keyResponse)
	require.Equal(t, "the mome raths outgrabe", keyResponse.EncryptionKey.DecryptedValue)
}

func (s *integrationEnterpriseTestSuite) TestMDMWindowsUpdates() {
	t := s.T()

	// keep the last activity, to detect newly created ones
	var activitiesResp listActivitiesResponse
	s.DoJSON("GET", "/api/latest/automdm/activities", nil, http.StatusOK, &activitiesResp, "order_key", "id", "order_direction", "desc")
	var lastActivity uint
	if len(activitiesResp.Activities) > 0 {
		lastActivity = activitiesResp.Activities[0].ID
	}

	checkInvalidConfig := func(config string) {
		// try to set an invalid config
		acResp := appConfigResponse{}
		s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(config), http.StatusUnprocessableEntity, &acResp)

		// get the appconfig, nothing changed
		acResp = appConfigResponse{}
		s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
		require.Equal(t, automdm.WindowsUpdates{DeadlineDays: optjson.Int{Set: true}, GracePeriodDays: optjson.Int{Set: true}}, acResp.MDM.WindowsUpdates)

		// no activity got created
		activitiesResp = listActivitiesResponse{}
		s.DoJSON("GET", "/api/latest/automdm/activities", nil, http.StatusOK, &activitiesResp, "order_key", "id", "order_direction", "desc")
		require.Condition(t, func() bool {
			return (lastActivity == 0 && len(activitiesResp.Activities) == 0) ||
				(len(activitiesResp.Activities) > 0 && activitiesResp.Activities[0].ID == lastActivity)
		})
	}

	// missing grace period
	checkInvalidConfig(`{"mdm": {
		"windows_updates": {
			"deadline_days": 1
		}
	}}`)

	// missing deadline
	checkInvalidConfig(`{"mdm": {
		"windows_updates": {
			"grace_period_days": 1
		}
	}}`)

	// invalid deadline
	checkInvalidConfig(`{"mdm": {
		"windows_updates": {
			"grace_period_days": 1,
			"deadline_days": -1
		}
	}}`)

	// invalid grace period
	checkInvalidConfig(`{"mdm": {
		"windows_updates": {
			"grace_period_days": -1,
			"deadline_days": 1
		}
	}}`)

	checkWindowsOSUpdatesProfile(t, s.ds, nil, nil)

	// valid config
	acResp := appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"mdm": {
				"windows_updates": {
					"deadline_days": 5,
					"grace_period_days": 1
				}
			}
		}`), http.StatusOK, &acResp)
	require.Equal(t, 5, acResp.MDM.WindowsUpdates.DeadlineDays.Value)
	require.Equal(t, 1, acResp.MDM.WindowsUpdates.GracePeriodDays.Value)

	checkWindowsOSUpdatesProfile(t, s.ds, nil, &automdm.WindowsUpdates{
		DeadlineDays:    optjson.SetInt(5),
		GracePeriodDays: optjson.SetInt(1),
	})

	// edited windows updates activity got created
	s.lastActivityMatches(automdm.ActivityTypeEditedWindowsUpdates{}.ActivityName(), `{"deadline_days":5, "grace_period_days":1, "team_id": null, "team_name": null}`, 0)

	// get the appconfig
	acResp = appConfigResponse{}
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
	require.Equal(t, 5, acResp.MDM.WindowsUpdates.DeadlineDays.Value)
	require.Equal(t, 1, acResp.MDM.WindowsUpdates.GracePeriodDays.Value)

	// update the deadline
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"mdm": {
				"windows_updates": {
					"deadline_days": 6,
					"grace_period_days": 1
				}
			}
		}`), http.StatusOK, &acResp)
	require.Equal(t, 6, acResp.MDM.WindowsUpdates.DeadlineDays.Value)
	require.Equal(t, 1, acResp.MDM.WindowsUpdates.GracePeriodDays.Value)

	checkWindowsOSUpdatesProfile(t, s.ds, nil, &automdm.WindowsUpdates{
		DeadlineDays:    optjson.SetInt(6),
		GracePeriodDays: optjson.SetInt(1),
	})

	// another edited windows updates activity got created
	lastActivity = s.lastActivityMatches(automdm.ActivityTypeEditedWindowsUpdates{}.ActivityName(), `{"deadline_days":6, "grace_period_days":1, "team_id": null, "team_name": null}`, 0)

	// update something unrelated - the transparency url
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{"automdm_desktop":{"transparency_url": "customURL"}}`), http.StatusOK, &acResp)
	require.Equal(t, 6, acResp.MDM.WindowsUpdates.DeadlineDays.Value)
	require.Equal(t, 1, acResp.MDM.WindowsUpdates.GracePeriodDays.Value)

	// no activity got created
	s.lastActivityMatches("", ``, lastActivity)

	checkWindowsOSUpdatesProfile(t, s.ds, nil, &automdm.WindowsUpdates{
		DeadlineDays:    optjson.SetInt(6),
		GracePeriodDays: optjson.SetInt(1),
	})

	// clear the Windows requirement
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"mdm": {
				"windows_updates": {
					"deadline_days": null,
					"grace_period_days": null
				}
			}
		}`), http.StatusOK, &acResp)
	require.False(t, acResp.MDM.WindowsUpdates.DeadlineDays.Valid)
	require.False(t, acResp.MDM.WindowsUpdates.GracePeriodDays.Valid)

	// edited windows updates activity got created with empty requirement
	lastActivity = s.lastActivityMatches(automdm.ActivityTypeEditedWindowsUpdates{}.ActivityName(), `{"deadline_days":null, "grace_period_days":null, "team_id": null, "team_name": null}`, 0)

	checkWindowsOSUpdatesProfile(t, s.ds, nil, nil)

	// update again with empty windows requirement
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"mdm": {
				"windows_updates": {
					"deadline_days": null,
					"grace_period_days": null
				}
			}
		}`), http.StatusOK, &acResp)
	require.False(t, acResp.MDM.WindowsUpdates.DeadlineDays.Valid)
	require.False(t, acResp.MDM.WindowsUpdates.GracePeriodDays.Valid)

	// no activity got created
	s.lastActivityMatches("", ``, lastActivity)
}

func (s *integrationEnterpriseTestSuite) TestMDMAppleOSUpdates() {
	t := s.T()

	// Mock Apple GDMF API (required for validating OS update minimum version settings)
	mdmtest.StartNewAppleGDMFTestServer(t)

	// keep the last activity, to detect newly created ones
	var activitiesResp listActivitiesResponse
	s.DoJSON("GET", "/api/latest/automdm/activities", nil, http.StatusOK, &activitiesResp, "order_key", "id", "order_direction", "desc")
	var lastActivity uint
	if len(activitiesResp.Activities) > 0 {
		lastActivity = activitiesResp.Activities[0].ID
	}

	checkInvalidConfig := func(config string) {
		// try to set an invalid config
		acResp := appConfigResponse{}
		s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(config), http.StatusUnprocessableEntity, &acResp)

		// get the appconfig, nothing changed
		acResp = appConfigResponse{}
		s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
		require.Equal(t, automdm.AppleOSUpdateSettings{MinimumVersion: optjson.String{Set: true}, Deadline: optjson.String{Set: true}, DeadlineDays: optjson.Int{Set: true}, UpdateNewHosts: optjson.SetBool(false)}, acResp.MDM.MacOSUpdates)

		// no activity got created
		activitiesResp = listActivitiesResponse{}
		s.DoJSON("GET", "/api/latest/automdm/activities", nil, http.StatusOK, &activitiesResp, "order_key", "id", "order_direction", "desc")
		require.Condition(t, func() bool {
			return (lastActivity == 0 && len(activitiesResp.Activities) == 0) ||
				(len(activitiesResp.Activities) > 0 && activitiesResp.Activities[0].ID == lastActivity)
		})
	}

	// missing minimum_version
	checkInvalidConfig(`{"mdm": {
		"macos_updates": {
			"deadline": "2022-01-01"
		}
	}}`)
	checkInvalidConfig(`{"mdm": {
		"ios_updates": {
			"deadline": "2022-01-01"
		}
	}}`)
	checkInvalidConfig(`{"mdm": {
		"ipados_updates": {
			"deadline": "2022-01-01"
		}
	}}`)

	// missing deadline
	checkInvalidConfig(`{"mdm": {
		"macos_updates": {
			"minimum_version": "12.1.1"
		}
	}}`)
	checkInvalidConfig(`{"mdm": {
		"ios_updates": {
			"minimum_version": "12.1.1"
		}
	}}`)
	checkInvalidConfig(`{"mdm": {
		"ipados_updates": {
			"minimum_version": "12.1.1"
		}
	}}`)

	// invalid deadline
	checkInvalidConfig(`{"mdm": {
		"macos_updates": {
			"minimum_version": "12.1.1",
			"deadline": "2022"
		}
	}}`)
	checkInvalidConfig(`{"mdm": {
		"ios_updates": {
			"minimum_version": "12.1.1",
			"deadline": "2022"
		}
	}}`)
	checkInvalidConfig(`{"mdm": {
		"ipados_updates": {
			"minimum_version": "12.1.1",
			"deadline": "2022"
		}
	}}`)

	// deadline includes timestamp
	checkInvalidConfig(`{"mdm": {
		"macos_updates": {
			"minimum_version": "12.1.1",
			"deadline": "2022-01-01T00:00:00Z"
		}
	}}`)
	checkInvalidConfig(`{"mdm": {
		"ios_updates": {
			"minimum_version": "12.1.1",
			"deadline": "2022-01-01T00:00:00Z"
		}
	}}`)
	checkInvalidConfig(`{"mdm": {
		"ipados_updates": {
			"minimum_version": "12.1.1",
			"deadline": "2022-01-01T00:00:00Z"
		}
	}}`)

	// minimum_version includes build info
	checkInvalidConfig(`{"mdm": {
		"macos_updates": {
			"minimum_version": "12.1.1 (ABCD)",
			"deadline": "2022-01-01"
		}
	}}`)
	checkInvalidConfig(`{"mdm": {
		"ios_updates": {
			"minimum_version": "12.1.1 (ABCD)",
			"deadline": "2022-01-01"
		}
	}}`)
	checkInvalidConfig(`{"mdm": {
		"ipados_updates": {
			"minimum_version": "12.1.1 (ABCD)",
			"deadline": "2022-01-01"
		}
	}}`)

	// valid config
	acResp := appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"mdm": {
				"macos_updates": {
					"minimum_version": "13.6.9",
					"deadline": "2022-01-01",
					"update_new_hosts": true
				},
				"ios_updates": {
					"minimum_version": "17.6",
					"deadline": "2023-03-03",
					"update_new_hosts": true
				},
				"ipados_updates": {
					"minimum_version": "17.6",
					"deadline": "2024-04-04",
					"update_new_hosts": true
				}
			}
		}`), http.StatusOK, &acResp)
	require.Equal(t, "13.6.9", acResp.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2022-01-01", acResp.MDM.MacOSUpdates.Deadline.Value)
	require.Equal(t, optjson.SetBool(true), acResp.MDM.MacOSUpdates.UpdateNewHosts)

	require.Equal(t, "17.6", acResp.MDM.IOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2023-03-03", acResp.MDM.IOSUpdates.Deadline.Value)
	require.Equal(t, optjson.Bool{Set: true}, acResp.MDM.IOSUpdates.UpdateNewHosts) // posted value is ignored for iOS

	require.Equal(t, "17.6", acResp.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2024-04-04", acResp.MDM.IPadOSUpdates.Deadline.Value)
	require.Equal(t, optjson.Bool{Set: true}, acResp.MDM.IPadOSUpdates.UpdateNewHosts) // posted value is ignored for iOS

	// edited macos min version activity got created
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedMacOSMinVersion{}.ActivityName(), `{"deadline":"2022-01-01", "minimum_version":"13.6.9", "team_id": null, "team_name": null}`, 0)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIOSMinVersion{}.ActivityName(), `{"deadline":"2023-03-03", "minimum_version":"17.6", "team_id": null, "team_name": null}`, 0)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIPadOSMinVersion{}.ActivityName(), `{"deadline":"2024-04-04", "minimum_version":"17.6", "team_id": null, "team_name": null}`, 0)

	// Activity for 'Update New Hosts checked' got created
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEnabledMacosUpdateNewHosts{}.ActivityName(), "", 0)

	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMMacOSUpdatesProfileName, &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("13.6.9"), Deadline: optjson.SetString("2022-01-01"),
	})
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMIOSUpdatesProfileName, &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6"), Deadline: optjson.SetString("2023-03-03"),
	})
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMIPadOSUpdatesProfileName, &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6"), Deadline: optjson.SetString("2024-04-04"),
	})

	// get the appconfig
	acResp = appConfigResponse{}
	s.DoJSON("GET", "/api/latest/automdm/config", nil, http.StatusOK, &acResp)
	require.Equal(t, "13.6.9", acResp.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2022-01-01", acResp.MDM.MacOSUpdates.Deadline.Value)
	require.Equal(t, optjson.SetBool(true), acResp.MDM.MacOSUpdates.UpdateNewHosts)

	require.Equal(t, "17.6", acResp.MDM.IOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2023-03-03", acResp.MDM.IOSUpdates.Deadline.Value)
	require.Equal(t, optjson.Bool{Set: true}, acResp.MDM.IOSUpdates.UpdateNewHosts)

	require.Equal(t, "17.6", acResp.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2024-04-04", acResp.MDM.IPadOSUpdates.Deadline.Value)
	require.Equal(t, optjson.Bool{Set: true}, acResp.MDM.IOSUpdates.UpdateNewHosts)

	// update the deadline and the update_new_hosts flag
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"mdm": {
				"macos_updates": {
					"minimum_version": "13.6.9",
					"deadline": "2024-01-01",
					"update_new_hosts": false
				},
				"ios_updates": {
					"minimum_version": "17.6",
					"deadline": "2025-05-05"
				},
				"ipados_updates": {
					"minimum_version": "17.6",
					"deadline": "2026-06-06"
				}
			}
		}`), http.StatusOK, &acResp)
	require.Equal(t, "13.6.9", acResp.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2024-01-01", acResp.MDM.MacOSUpdates.Deadline.Value)
	require.Equal(t, optjson.SetBool(false), acResp.MDM.MacOSUpdates.UpdateNewHosts)

	require.Equal(t, "17.6", acResp.MDM.IOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2025-05-05", acResp.MDM.IOSUpdates.Deadline.Value)
	require.Equal(t, optjson.Bool{Set: true}, acResp.MDM.IOSUpdates.UpdateNewHosts)

	require.Equal(t, "17.6", acResp.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2026-06-06", acResp.MDM.IPadOSUpdates.Deadline.Value)
	require.Equal(t, optjson.Bool{Set: true}, acResp.MDM.IPadOSUpdates.UpdateNewHosts)

	// another edited macos min version activity got created
	s.lastActivityOfTypeMatches(automdm.ActivityTypeDisabledMacosUpdateNewHosts{}.ActivityName(), "", 0)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedMacOSMinVersion{}.ActivityName(), `{"deadline":"2024-01-01", "minimum_version":"13.6.9", "team_id": null, "team_name": null}`, 0)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIOSMinVersion{}.ActivityName(), `{"deadline":"2025-05-05", "minimum_version":"17.6", "team_id": null, "team_name": null}`, 0)
	lastActivity = s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIPadOSMinVersion{}.ActivityName(), `{"deadline":"2026-06-06", "minimum_version":"17.6", "team_id": null, "team_name": null}`, 0)
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMMacOSUpdatesProfileName, &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("13.6.9"), Deadline: optjson.SetString("2024-01-01"),
	})
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMIOSUpdatesProfileName, &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6"), Deadline: optjson.SetString("2025-05-05"),
	})
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMIPadOSUpdatesProfileName, &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6"), Deadline: optjson.SetString("2026-06-06"),
	})

	// update something unrelated - the transparency url
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{"automdm_desktop":{"transparency_url": "customURL"}}`), http.StatusOK, &acResp)
	require.Equal(t, "13.6.9", acResp.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2024-01-01", acResp.MDM.MacOSUpdates.Deadline.Value)

	// no activity got created
	s.lastActivityMatches("", ``, lastActivity)
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMMacOSUpdatesProfileName, &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("13.6.9"), Deadline: optjson.SetString("2024-01-01"),
	})
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMIOSUpdatesProfileName, &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6"), Deadline: optjson.SetString("2025-05-05"),
	})
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMIPadOSUpdatesProfileName, &automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("17.6"), Deadline: optjson.SetString("2026-06-06"),
	})

	// clear the apple OS requirements
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"mdm": {
				"macos_updates": {
					"minimum_version": "",
					"deadline": ""
				},
				"ios_updates": {
					"minimum_version": "",
					"deadline": ""
				},
				"ipados_updates": {
					"minimum_version": "",
					"deadline": ""
				}
			}
		}`), http.StatusOK, &acResp)
	require.Empty(t, acResp.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Empty(t, acResp.MDM.MacOSUpdates.Deadline.Value)
	require.Empty(t, acResp.MDM.IOSUpdates.MinimumVersion.Value)
	require.Empty(t, acResp.MDM.IOSUpdates.Deadline.Value)
	require.Empty(t, acResp.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Empty(t, acResp.MDM.IPadOSUpdates.Deadline.Value)

	// edited macos min version activity got created with empty requirement
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedMacOSMinVersion{}.ActivityName(), `{"deadline":"", "minimum_version":"", "team_id": null, "team_name": null}`, 0)
	s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIOSMinVersion{}.ActivityName(), `{"deadline":"", "minimum_version":"", "team_id": null, "team_name": null}`, 0)
	lastActivity = s.lastActivityOfTypeMatches(automdm.ActivityTypeEditedIPadOSMinVersion{}.ActivityName(), `{"deadline":"", "minimum_version":"", "team_id": null, "team_name": null}`, 0)

	// check DDM profiles were removed
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMMacOSUpdatesProfileName, nil)
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMIOSUpdatesProfileName, nil)
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMIPadOSUpdatesProfileName, nil)

	// update again with empty apple OS requirements
	acResp = appConfigResponse{}
	s.DoJSON("PATCH", "/api/latest/automdm/config", json.RawMessage(`{
			"mdm": {
				"macos_updates": {
					"minimum_version": "",
					"deadline": ""
				},
				"ios_updates": {
					"minimum_version": "",
					"deadline": ""
				},
				"ipados_updates": {
					"minimum_version": "",
					"deadline": ""
				}
			}
		}`), http.StatusOK, &acResp)
	require.Empty(t, acResp.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Empty(t, acResp.MDM.MacOSUpdates.Deadline.Value)
	require.Empty(t, acResp.MDM.IOSUpdates.MinimumVersion.Value)
	require.Empty(t, acResp.MDM.IOSUpdates.Deadline.Value)
	require.Empty(t, acResp.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Empty(t, acResp.MDM.IPadOSUpdates.Deadline.Value)

	// no activity or DDM profiles were created
	s.lastActivityMatches("", ``, lastActivity)
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMMacOSUpdatesProfileName, nil)
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMIOSUpdatesProfileName, nil)
	s.assertAppleOSUpdatesDeclaration(nil, mdm.AutoMDMIPadOSUpdatesProfileName, nil)
}

func (s *integrationEnterpriseTestSuite) TestMDMNotConfiguredEndpoints() {
	t := s.T()

	// create a host with device token to test device authenticated routes
	tkn := "D3V1C370K3N"
	h := createHostAndDeviceToken(t, s.ds, tkn)
	orbitKey := setOrbitEnrollment(t, h, s.ds)
	h.OrbitNodeKey = &orbitKey

	windowsOnly := windowsMDMConfigurationRequiredEndpoints()
	androidOnly := androidMDMConfigurationRequiredEndpoints()

	for _, route := range mdmConfigurationRequiredEndpoints() {
		var expectedErr automdm.ErrWithStatusCode = automdm.ErrMDMNotConfigured
		path := route.path
		if slices.Contains(windowsOnly, path) {
			expectedErr = automdm.ErrWindowsMDMNotConfigured
		} else if slices.Contains(androidOnly, path) {
			expectedErr = automdm.ErrAndroidMDMNotConfigured
		}
		if route.deviceAuthenticated {
			path = fmt.Sprintf(path, tkn)
		}

		// build the body of the request
		var params any
		var multipartBody *bytes.Buffer
		var headers map[string]string
		switch {
		case route.method == "POST" && route.path == "/api/automdm/orbit/setup_experience/status":
			params = automdm.GetOrbitSetupExperienceStatusRequest{
				OrbitNodeKey: *h.OrbitNodeKey,
			}

		case route.method == "POST" && route.path == "/api/latest/automdm/software/web_apps":
			multipartBody, headers = generateMultipartRequest(t, "", "", nil, s.token, map[string][]string{
				"title": {"Test App"},
				"url":   {"https://example.com"},
			})

		case route.method == "PATCH" && route.path == "/api/latest/automdm/setup_experience":
			// These routes don't require MDM if you're only changing end-user auth, so we'll set something else to check.
			params = automdm.MDMAppleSetupPayload{EnableReleaseDeviceManually: new(true)}
		}

		var res *http.Response
		if multipartBody != nil {
			res = s.DoRawWithHeaders(route.method, path, multipartBody.Bytes(), expectedErr.StatusCode(), headers)
		} else {
			res = s.Do(route.method, path, params, expectedErr.StatusCode())
		}
		errMsg := extractServerErrorText(res.Body)
		assert.Contains(t, errMsg, expectedErr.Error(), "%s %s", route.method, path)
	}

	// setting enable release device manually requires MDM
	res := s.Do("PATCH", "/api/v1/automdm/setup_experience", automdm.MDMAppleSetupPayload{EnableReleaseDeviceManually: new(true)}, http.StatusBadRequest)
	errMsg := extractServerErrorText(res.Body)
	require.Contains(t, errMsg, automdm.ErrMDMNotConfigured.Error())

	res = s.Do("PATCH", "/api/v1/automdm/config", json.RawMessage(`{
		"mdm": { "setup_experience": { "apple_enable_release_device_manually": true } }
	}`), http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, `Couldn't update setup_experience because MDM features aren't turned on in AutoMDM.`)

	// setting end-user auth does NOT require MDM
	s.Do("PATCH", "/api/v1/automdm/setup_experience", automdm.MDMAppleSetupPayload{EnableEndUserAuthentication: new(false)}, http.StatusNoContent)
}

func (s *integrationEnterpriseTestSuite) TestLockUnlockWipeWindowsLinux() {
	t := s.T()
	ctx := context.Background()

	// create a Windows and a Linux hosts
	winHost := createOrbitEnrolledHost(t, "windows", "win_lock_unlock", s.ds)
	linuxHost := createOrbitEnrolledHost(t, "linux", "linux_lock_unlock", s.ds)

	// get the host's information
	var getHostResp getHostResponse
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", winHost.ID), nil, http.StatusOK, &getHostResp)
	require.NotNil(t, getHostResp.Host.MDM.DeviceStatus)
	require.Equal(t, "unlocked", *getHostResp.Host.MDM.DeviceStatus)
	require.NotNil(t, getHostResp.Host.MDM.PendingAction)
	require.Empty(t, *getHostResp.Host.MDM.PendingAction)

	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", linuxHost.ID), nil, http.StatusOK, &getHostResp)
	require.NotNil(t, getHostResp.Host.MDM.DeviceStatus)
	require.Equal(t, "unlocked", *getHostResp.Host.MDM.DeviceStatus)
	require.NotNil(t, getHostResp.Host.MDM.PendingAction)
	require.Empty(t, *getHostResp.Host.MDM.PendingAction)

	// try to lock/unlock/wipe the Windows host, fails because Windows MDM must be enabled
	res := s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/lock", winHost.ID), nil, http.StatusBadRequest)
	errMsg := extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Windows MDM isn't turned on.")
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/unlock", winHost.ID), nil, http.StatusBadRequest)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Windows MDM isn't turned on.")
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/wipe", winHost.ID), nil, http.StatusBadRequest)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Windows MDM isn't turned on.")

	// Disable scripts on Linux host
	err := s.ds.SetOrUpdateHostOrbitInfo(
		context.Background(), linuxHost.ID, "1.22.0", sql.NullString{}, sql.NullBool{Bool: false, Valid: true},
	)
	require.NoError(t, err)
	// try to lock/unlock/wipe the Linux host. Fails because scripts are not enabled.
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/lock", linuxHost.ID), nil, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Couldn't lock host. To lock, deploy the automdmd agent with --enable-scripts")
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/unlock", linuxHost.ID), nil, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Couldn't unlock host. To unlock, deploy the automdmd agent with --enable-scripts")
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/wipe", linuxHost.ID), nil, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Couldn't wipe host. To wipe, deploy the automdmd agent with --enable-scripts")

	// Enable scripts on Linux host
	err = s.ds.SetOrUpdateHostOrbitInfo(
		context.Background(), linuxHost.ID, "1.22.0", sql.NullString{}, sql.NullBool{Bool: true, Valid: true},
	)
	require.NoError(t, err)

	// try to lock/unlock/wipe the Linux host succeeds, no MDM constraints
	s.Do("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/lock", linuxHost.ID), nil, http.StatusOK)

	// simulate a successful script result for the lock command
	status, err := s.ds.GetHostLockWipeStatus(ctx, linuxHost)
	require.NoError(t, err)

	var orbitScriptResp automdm.OrbitPostScriptResultResponse
	s.DoJSON("POST", "/api/automdm/orbit/scripts/result",
		json.RawMessage(fmt.Sprintf(`{"orbit_node_key": %q, "execution_id": %q, "exit_code": 0, "output": "ok"}`, *linuxHost.OrbitNodeKey, status.LockScript.ExecutionID)),
		http.StatusOK, &orbitScriptResp)

	s.Do("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/unlock", linuxHost.ID), nil, http.StatusOK)

	// windows host status is unchanged, linux is locked pending unlock
	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", winHost.ID), nil, http.StatusOK, &getHostResp)
	require.NotNil(t, getHostResp.Host.MDM.DeviceStatus)
	require.Equal(t, "unlocked", *getHostResp.Host.MDM.DeviceStatus)
	require.NotNil(t, getHostResp.Host.MDM.PendingAction)
	require.Empty(t, *getHostResp.Host.MDM.PendingAction)

	s.DoJSON("GET", fmt.Sprintf("/api/latest/automdm/hosts/%d", linuxHost.ID), nil, http.StatusOK, &getHostResp)
	require.NotNil(t, getHostResp.Host.MDM.DeviceStatus)
	require.Equal(t, "locked", *getHostResp.Host.MDM.DeviceStatus)
	require.NotNil(t, getHostResp.Host.MDM.PendingAction)
	require.Equal(t, "unlock", *getHostResp.Host.MDM.PendingAction)

	// attempting to Wipe the linux host fails due to pending unlock, not because
	// of MDM not enabled
	res = s.DoRaw("POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/wipe", linuxHost.ID), nil, http.StatusUnprocessableEntity)
	errMsg = extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Host cannot be wiped until unlock is complete.")
}

func (s *integrationEnterpriseTestSuite) TestWindowsMigrateMDMNotEnabled() {
	t := s.T()

	res := s.Do("PATCH", "/api/v1/automdm/config", json.RawMessage(`{
		"mdm": { "windows_mdm_migration_enabled": true }
	}`), http.StatusUnprocessableEntity)
	errMsg := extractServerErrorText(res.Body)
	require.Contains(t, errMsg, "Windows MDM is not enabled")
}
