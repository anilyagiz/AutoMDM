package service

import (
	"context"
	"log/slog"
	"testing"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/require"
)

func TestResolveHostNameIDPVars(t *testing.T) {
	ds := new(mock.Store)
	var scimCalls int
	ds.ScimUserByHostIDFunc = func(_ context.Context, _ uint) (*automdm.ScimUser, error) {
		scimCalls++
		return &automdm.ScimUser{
			ID:         1,
			UserName:   "jdoe@corp.com",
			GivenName:  new("Jane"),
			FamilyName: new("Doe"),
			Department: new("Eng"),
			ExternalID: new("ext-1"),
			Groups:     []automdm.ScimUserGroup{{DisplayName: "Admins"}, {DisplayName: "Eng"}},
		}, nil
	}
	ds.ListHostDeviceMappingFunc = func(_ context.Context, _ uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}

	t.Run("multiple IdP vars resolve from a single fetch", func(t *testing.T) {
		scimCalls = 0
		// The template repeats and mixes IdP vars, including _USERNAME and its
		// longer _USERNAME_LOCAL_PART sibling, to exercise the longest-first
		// substitution order.
		name, detail, err := resolveHostNameIDPVars(t.Context(), ds,
			"u=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME;lp=${AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART};d=$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT;g=$AUTOMDM_VAR_HOST_END_USER_IDP_GROUPS;x=$AUTOMDM_VAR_HOST_END_USER_IDP_EXTERNAL_ID",
			42)
		require.NoError(t, err)
		require.Empty(t, detail)
		require.Equal(t, "u=jdoe@corp.com;lp=jdoe;d=Eng;g=Admins,Eng;x=ext-1", name)
		require.Equal(t, 1, scimCalls, "end users must be fetched once regardless of the number of IdP variables")
	})

	t.Run("no IdP vars needs no fetch and leaves other tokens untouched", func(t *testing.T) {
		scimCalls = 0
		name, detail, err := resolveHostNameIDPVars(t.Context(), ds, "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL", 42)
		require.NoError(t, err)
		require.Empty(t, detail)
		// identity variables are resolved elsewhere, so they're passed through here
		require.Equal(t, "WS-$AUTOMDM_VAR_HOST_HARDWARE_SERIAL", name)
		require.Zero(t, scimCalls, "no datastore fetch when the template has no IdP variables")
	})

	t.Run("missing IdP field fails with the profile-style detail", func(t *testing.T) {
		ds.ScimUserByHostIDFunc = func(_ context.Context, _ uint) (*automdm.ScimUser, error) {
			// no department and no external id
			return &automdm.ScimUser{ID: 1, UserName: "jdoe@corp.com"}, nil
		}
		name, detail, err := resolveHostNameIDPVars(t.Context(), ds, "$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT", 42)
		require.NoError(t, err)
		require.Empty(t, name)
		require.Contains(t, detail, "no IdP department for this host")

		name, detail, err = resolveHostNameIDPVars(t.Context(), ds, "$AUTOMDM_VAR_HOST_END_USER_IDP_EXTERNAL_ID", 42)
		require.NoError(t, err)
		require.Empty(t, name)
		require.Contains(t, detail, "no IdP external ID for this host")
	})

	t.Run("no IdP user fails with the username detail", func(t *testing.T) {
		ds.ScimUserByHostIDFunc = func(_ context.Context, _ uint) (*automdm.ScimUser, error) {
			return nil, nil // no SCIM user mapped
		}
		_, detail, err := resolveHostNameIDPVars(t.Context(), ds, "$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME", 42)
		require.NoError(t, err)
		require.Contains(t, detail, "no IdP username for this host")
	})
}

// TestReconcileHostDeviceNamesExpandsCustomHostVitals covers the per-host
// $AUTOMDM_HOST_VITAL_<id> expansion step: a host with no value set for a
// referenced vital fails the row (mirroring the missing-secret path), and a
// host with a value gets it substituted before resolution. Both scenarios
// here settle on a name matching the host's current ComputerName, so the
// cron never reaches the MDM commander (nil is safe to pass).
func TestReconcileHostDeviceNamesExpandsCustomHostVitals(t *testing.T) {
	ds := new(mock.Store)
	logger := slog.New(slog.DiscardHandler)

	const tmpl = "WS-$AUTOMDM_HOST_VITAL_5"
	ds.AppConfigFunc = func(_ context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{
			MDM: automdm.MDM{
				EnabledAndConfigured: true,
				HostNameTemplate:     optjson.SetString(tmpl),
			},
		}, nil
	}
	ds.DeactivateHostDeviceNameCommandsFunc = func(_ context.Context, _ []string) error { return nil }

	type recordedStatus struct {
		status automdm.MDMDeliveryStatus
		detail string
	}
	statuses := map[string]recordedStatus{}
	ds.SetHostDeviceNameStatusFunc = func(_ context.Context, hostUUID string, status automdm.MDMDeliveryStatus, _ *string, _, detail string) error {
		statuses[hostUUID] = recordedStatus{status, detail}
		return nil
	}

	t.Run("no value set for the host fails the row", func(t *testing.T) {
		ds.ListHostsPendingDeviceNameCommandFunc = func(_ context.Context, _ int) ([]automdm.HostDeviceNamePending, error) {
			return []automdm.HostDeviceNamePending{
				{HostID: 1, HostUUID: "host-1", HardwareSerial: "SERIAL1", Platform: "darwin", ComputerName: "old-name"},
			}, nil
		}
		ds.ExpandCustomHostVitalsFunc = func(_ context.Context, hostID uint, document string) (string, error) {
			require.Equal(t, uint(1), hostID)
			require.Equal(t, tmpl, document)
			return "", &automdm.MissingCustomHostVitalValueError{MissingIDs: []uint{5}}
		}

		require.NoError(t, ReconcileHostDeviceNames(t.Context(), ds, nil, logger))
		require.Equal(t, automdm.MDMDeliveryFailed, statuses["host-1"].status)
		require.Contains(t, statuses["host-1"].detail, "no value set for this host")
	})

	t.Run("host's value is substituted and a matching name verifies without a command", func(t *testing.T) {
		ds.ListHostsPendingDeviceNameCommandFunc = func(_ context.Context, _ int) ([]automdm.HostDeviceNamePending, error) {
			return []automdm.HostDeviceNamePending{
				{HostID: 2, HostUUID: "host-2", HardwareSerial: "SERIAL2", Platform: "darwin", ComputerName: "WS-engineering", NameReportedSinceEnrollment: true},
			}, nil
		}
		ds.ExpandCustomHostVitalsFunc = func(_ context.Context, hostID uint, document string) (string, error) {
			require.Equal(t, uint(2), hostID)
			require.Equal(t, tmpl, document)
			return "WS-engineering", nil
		}

		require.NoError(t, ReconcileHostDeviceNames(t.Context(), ds, nil, logger))
		require.Equal(t, automdm.MDMDeliveryVerified, statuses["host-2"].status)
		require.Empty(t, statuses["host-2"].detail)
	})
}
