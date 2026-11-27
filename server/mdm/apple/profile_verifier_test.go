package apple_mdm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/require"
)

func TestVerifyHostMDMProfiles(t *testing.T) {
	ctx := context.Background()
	host := &automdm.Host{
		DetailUpdatedAt: time.Now(),
	}
	ds := new(mock.Store)

	tests := []struct {
		name             string
		expectedProfiles map[string]*automdm.ExpectedMDMProfile
		retryCounts      []automdm.HostMDMProfileRetryCount
		installed        map[string]*automdm.HostMacOSProfile
		toVerify         []string
		toFail           []string
		toRetry          []string
		expectErr        bool
	}{
		{
			name:      "error on getting expected profiles",
			expectErr: true,
		},
		{
			name:             "all profiles verified",
			expectedProfiles: map[string]*automdm.ExpectedMDMProfile{"profile1": {}},
			installed:        map[string]*automdm.HostMacOSProfile{"profile1": {}},
			toVerify:         []string{"profile1"},
		},
		{
			name: "profiles missing, not within grace period, no retries yet",
			expectedProfiles: map[string]*automdm.ExpectedMDMProfile{
				"profile1": {},
				"profile2": {EarliestInstallDate: host.DetailUpdatedAt.Add(-24 * time.Hour)},
			},
			installed: map[string]*automdm.HostMacOSProfile{"profile1": {}},
			toVerify:  []string{"profile1"},
			toRetry:   []string{"profile2"},
		},
		{
			name:             "profiles missing, with and without retries, not within grace period",
			expectedProfiles: map[string]*automdm.ExpectedMDMProfile{"profile1": {}, "profile2": {}},
			retryCounts: []automdm.HostMDMProfileRetryCount{
				{ProfileIdentifier: "profile1", Retries: 0},
				{ProfileIdentifier: "profile2", Retries: mdm.MaxAppleProfileRetries},
			},
			installed: map[string]*automdm.HostMacOSProfile{},
			toRetry:   []string{"profile1"},
			toFail:    []string{"profile2"},
		},
		{
			name: "host profile installed prior to uploading profile to AutoMDM",
			expectedProfiles: map[string]*automdm.ExpectedMDMProfile{
				"profile1": {EarliestInstallDate: time.Now().Add(-2 * time.Hour)},
			},
			installed: map[string]*automdm.HostMacOSProfile{
				"profile1": {InstallDate: time.Now().Add(-24 * time.Hour)},
			},
			toRetry: []string{"profile1"},
		},
		{
			name: "host profile installed prior to uploading profile to AutoMDM with max retries",
			expectedProfiles: map[string]*automdm.ExpectedMDMProfile{
				"profile1": {EarliestInstallDate: time.Now().Add(-2 * time.Hour)},
			},
			installed: map[string]*automdm.HostMacOSProfile{
				"profile1": {InstallDate: time.Now().Add(-24 * time.Hour)},
			},
			retryCounts: []automdm.HostMDMProfileRetryCount{
				{ProfileIdentifier: "profile1", Retries: mdm.MaxAppleProfileRetries},
			},
			toFail: []string{"profile1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// setup mocks
			ds.GetHostMDMProfilesExpectedForVerificationFunc = func(ctx context.Context, host *automdm.Host) (map[string]*automdm.ExpectedMDMProfile, error) {
				if tc.expectErr {
					return nil, errors.New("error")
				}
				return tc.expectedProfiles, nil
			}

			ds.GetHostMDMProfilesRetryCountsFunc = func(ctx context.Context, host *automdm.Host) ([]automdm.HostMDMProfileRetryCount, error) {
				return tc.retryCounts, nil
			}

			ds.UpdateHostMDMProfilesVerificationFunc = func(ctx context.Context, host *automdm.Host, verified, toFail, toRetry []string) error {
				require.ElementsMatch(t, tc.toVerify, verified, "verified profiles do not match")
				require.ElementsMatch(t, tc.toFail, toFail, "failed profiles do not match")
				require.ElementsMatch(t, tc.toRetry, toRetry, "retried profiles do not match")
				return nil
			}

			// run the test
			err := VerifyHostMDMProfiles(ctx, ds, host, tc.installed)
			if tc.expectErr {
				require.Error(t, err)
				require.False(
					t,
					ds.UpdateHostMDMProfilesVerificationFuncInvoked,
					"UpdateHostMDMProfilesVerificationFunc should not have been called",
				)
			} else {
				require.NoError(t, err)
				require.True(t, ds.UpdateHostMDMProfilesVerificationFuncInvoked, "UpdateHostMDMProfilesVerificationFunc should have been called")
			}

			ds.UpdateHostMDMProfilesVerificationFuncInvoked = false
		})
	}
}
