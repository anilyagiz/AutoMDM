package microsoft_mdm

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/require"
)

func TestPreprocessWindowsProfileContentsCustomHostVitals(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.ListHostsLiteByUUIDsFunc = func(ctx context.Context, filter automdm.TeamFilter, uuids []string) ([]*automdm.Host, error) {
		return []*automdm.Host{{ID: 55, UUID: "host-uuid-55"}}, nil
	}

	ctx := license.NewContext(t.Context(), &automdm.LicenseInfo{Tier: automdm.TierPremium})
	appConfig, err := ds.AppConfig(ctx)
	require.NoError(t, err)

	newDeps := func() ProfilePreprocessDependencies {
		return ProfilePreprocessDependencies{
			Context:                    ctx,
			Logger:                     slog.New(slog.DiscardHandler),
			DataStore:                  ds,
			HostIDForUUIDCache:         map[string]uint{},
			AppConfig:                  appConfig,
			ManagedCertificatePayloads: &[]*automdm.MDMManagedCertificate{},
		}
	}

	profile := `<Replace><Item><Data>$AUTOMDM_HOST_VITAL_3</Data></Item></Replace>`

	t.Run("substitutes the host's value with XML escaping", func(t *testing.T) {
		ds.ExpandCustomHostVitalsFunc = func(ctx context.Context, hostID uint, doc string) (string, error) {
			require.Equal(t, uint(55), hostID)
			// simulate escaping of a value containing an XML-special char
			return `<Replace><Item><Data>a &amp; b</Data></Item></Replace>`, nil
		}
		result, err := PreprocessWindowsProfileContentsForDeployment(newDeps(), ProfilePreprocessParams{
			HostUUID: "host-uuid-55", ProfileUUID: "prof-1",
		}, profile)
		require.NoError(t, err)
		require.Equal(t, `<Replace><Item><Data>a &amp; b</Data></Item></Replace>`, result)
	})

	t.Run("value substituted into a scep subject name is quoted", func(t *testing.T) {
		scepProfile := testSyncMLItem(testDeviceSubjectNameLocURI, `CN=$AUTOMDM_HOST_VITAL_3,O=AutoMDM QA`)
		ds.ExpandCustomHostVitalsFunc = func(ctx context.Context, hostID uint, doc string) (string, error) {
			// The document arrives with the subject name value already marked, so substitute into
			// what was handed over rather than returning a canned profile.
			return strings.Replace(doc, "$AUTOMDM_HOST_VITAL_3", "Doe, Jane", 1), nil
		}
		result, err := PreprocessWindowsProfileContentsForDeployment(newDeps(), ProfilePreprocessParams{
			HostUUID: "host-uuid-55", ProfileUUID: "prof-1",
		}, scepProfile)
		require.NoError(t, err)
		require.Equal(t, testSyncMLItem(testDeviceSubjectNameLocURI, `CN="Doe, Jane",O=AutoMDM QA`), result)
	})

	t.Run("missing/empty value marks the profile failed with detail", func(t *testing.T) {
		ds.ExpandCustomHostVitalsFunc = func(ctx context.Context, hostID uint, doc string) (string, error) {
			return "", &automdm.MissingCustomHostVitalValueError{MissingIDs: []uint{3}}
		}
		_, err := PreprocessWindowsProfileContentsForDeployment(newDeps(), ProfilePreprocessParams{
			HostUUID: "host-uuid-55", ProfileUUID: "prof-1",
		}, profile)
		require.Error(t, err)
		var procErr *MicrosoftProfileProcessingError
		require.ErrorAs(t, err, &procErr)
		require.Contains(t, procErr.Error(), "AUTOMDM_HOST_VITAL_3")
	})
}
