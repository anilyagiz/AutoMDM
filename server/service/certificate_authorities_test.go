package service

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/server"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/stretchr/testify/require"
)

func TestBatchApplyCertificateAuthoritiesFree(t *testing.T) {
	svc, ctx := newTestService(t, new(mock.Store), nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierFree}})
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
	opts := automdm.BatchApplyCertificateAuthoritiesOpts{ViaGitOps: true}

	// No CAs is what most Free GitOps repos send, and must keep working.
	require.NoError(t, svc.BatchApplyCertificateAuthorities(ctx, automdm.GroupedCertificateAuthorities{}, opts))

	// Any CA type needs Premium, including the ones the Free check used to miss.
	for name, cas := range map[string]automdm.GroupedCertificateAuthorities{
		"custom est": {EST: []automdm.ESTProxyCA{{Name: "E"}}},
		"smallstep":  {Smallstep: []automdm.SmallstepSCEPProxyCA{{Name: "S"}}},
		"google cas": {GoogleCAS: []automdm.GoogleCASCA{{Name: "G"}}},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, svc.BatchApplyCertificateAuthorities(ctx, cas, opts), automdm.ErrMissingLicense)
		})
	}
}

func newMockDigicertCA(url string, name string) automdm.DigiCertCA {
	digiCertCA := automdm.DigiCertCA{
		Name:                          name,
		URL:                           url,
		APIToken:                      "api_token",
		ProfileID:                     "profile_id",
		CertificateCommonName:         "common_name",
		CertificateUserPrincipalNames: []string{"user_principal_name"},
		CertificateSeatID:             "seat_id",
	}
	return digiCertCA
}

func newMockCustomSCEPProxyCA(url string, name string) automdm.CustomSCEPProxyCA {
	challenge, _ := server.GenerateRandomText(6)
	return automdm.CustomSCEPProxyCA{
		Name:      name,
		URL:       url,
		Challenge: challenge,
	}
}

func newMockSmallstepSCEPProxyCA(url, challengeURL, name string) automdm.SmallstepSCEPProxyCA {
	return automdm.SmallstepSCEPProxyCA{
		Name:         name,
		URL:          url,
		ChallengeURL: challengeURL,
		Username:     "username",
		Password:     "password",
	}
}
