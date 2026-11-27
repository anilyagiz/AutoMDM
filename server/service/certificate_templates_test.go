package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	activity_api "github.com/anilyagiz/automdm/v4/server/activity/api"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	authz_ctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	common_mysql "github.com/anilyagiz/automdm/v4/server/platform/mysql"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/require"
)

func TestCreateCertificateTemplate(t *testing.T) {
	ds := new(mock.Store)
	// Certificate templates are Premium-gated (CAs are Premium, and templates require a CA).
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	type TestCAID uint

	const (
		InvalidCATypeID TestCAID = iota + 1
		ValidCATypeID
	)

	const TeamID = 1

	ds.GetCertificateAuthorityByIDFunc = func(ctx context.Context, id uint, includeSecrets bool) (*automdm.CertificateAuthority, error) {
		if id == uint(InvalidCATypeID) {
			ca := automdm.CertificateAuthority{
				ID:   id,
				Type: string(automdm.CATypeDigiCert),
			}
			return &ca, nil
		}
		if id == uint(ValidCATypeID) {
			ca := automdm.CertificateAuthority{
				ID:   id,
				Type: string(automdm.CATypeCustomSCEPProxy),
			}
			return &ca, nil
		}
		return nil, errors.New("not found")
	}

	ds.CreateCertificateTemplateFunc = func(ctx context.Context, certificateTemplate *automdm.CertificateTemplate) (*automdm.CertificateTemplateResponse, error) {
		return &automdm.CertificateTemplateResponse{
			CertificateTemplateResponseSummary: automdm.CertificateTemplateResponseSummary{
				ID:   1,
				Name: certificateTemplate.Name,
			},
		}, nil
	}
	ds.CreatePendingCertificateTemplatesForExistingHostsFunc = func(ctx context.Context, certificateTemplateID uint, teamID uint) (int64, error) {
		return 0, nil
	}
	ds.SetCertificateTemplateVariablesFunc = func(ctx context.Context, certTemplateID uint, automdmVars []automdm.AutoMDMVarName) error {
		return nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid, Name: "Yellow jackets"}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	t.Run("Invalid CA type", func(t *testing.T) {
		_, err := svc.CreateCertificateTemplate(ctx, "my template", TeamID, uint(InvalidCATypeID), "CN=$AUTOMDM_VAR_HOST_UUID", "")
		require.Error(t, err)
		// Check that the error is about invalid CA type
		require.Contains(t, err.Error(), "Currently, only the custom_scep_proxy certificate authority is supported")
	})

	t.Run("Valid CA type", func(t *testing.T) {
		_, err := svc.CreateCertificateTemplate(ctx, "my template", TeamID, uint(ValidCATypeID), "CN=$AUTOMDM_VAR_HOST_UUID", "")
		require.NoError(t, err)
	})

	t.Run("Missing CA", func(t *testing.T) {
		_, err := svc.CreateCertificateTemplate(ctx, "my template", TeamID, 999, "CN=$AUTOMDM_VAR_HOST_UUID", "")
		require.Error(t, err)
		// Check that the error is about invalid CA type
		require.Contains(t, err.Error(), "not found")
	})

	t.Run("Empty or whitespace-only name", func(t *testing.T) {
		whitespaceNames := []string{"", " ", "  ", "\t", "\n", "   \t\n  "}
		for _, name := range whitespaceNames {
			_, err := svc.CreateCertificateTemplate(ctx, name, TeamID, uint(ValidCATypeID), "CN=$AUTOMDM_VAR_HOST_UUID", "")
			require.Error(t, err)
			require.Contains(t, err.Error(), "Certificate template name is required")
		}
	})

	t.Run("Name too long", func(t *testing.T) {
		longName := strings.Repeat("a", 256)
		_, err := svc.CreateCertificateTemplate(ctx, longName, TeamID, uint(ValidCATypeID), "CN=$AUTOMDM_VAR_HOST_UUID", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "Certificate template name is too long")
	})

	t.Run("Name with invalid characters", func(t *testing.T) {
		testCases := []struct {
			name string
		}{
			{name: "template@name"},
			{name: "template#name"},
			{name: "template$name"},
			{name: "template%name"},
			{name: "template.name"},
			{name: "template/name"},
			{name: "template\\name"},
			{name: "template!name"},
			{name: "template?name"},
			{name: "template*name"},
			{name: "template+name"},
			{name: "template=name"},
			{name: "template<name>"},
			{name: "template(name)"},
			{name: "template[name]"},
			{name: "template{name}"},
			{name: "template|name"},
			{name: "template;name"},
			{name: "template:name"},
			{name: "template'name"},
			{name: "template\"name"},
			{name: "template`name"},
			{name: "template~name"},
			{name: "template^name"},
			{name: "template	name"},
		}
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.CreateCertificateTemplate(ctx, tc.name, TeamID, uint(ValidCATypeID), "CN=$AUTOMDM_VAR_HOST_UUID", "")
				require.Error(t, err)
				require.Contains(t, err.Error(), "Invalid certificate template name")
			})
		}
	})

	t.Run("Name with valid characters", func(t *testing.T) {
		validNames := []string{
			"my template",
			" my template ",
			"my-template",
			"my_template",
			"MyTemplate123",
			"Template 1",
			"UPPERCASE",
			"lowercase",
			"Mix-Ed_Case 123",
			"a",
			"1",
			"a1",
			"1a",
		}
		for _, name := range validNames {
			t.Run(name, func(t *testing.T) {
				_, err := svc.CreateCertificateTemplate(ctx, name, TeamID, uint(ValidCATypeID), "CN=$AUTOMDM_VAR_HOST_UUID", "")
				require.NoError(t, err)
			})
		}
	})

	t.Run("Empty or whitespace-only subject name", func(t *testing.T) {
		whitespaceSubjectNames := []string{"", " ", "   \t\n  "}
		for _, subjectName := range whitespaceSubjectNames {
			_, err := svc.CreateCertificateTemplate(ctx, "my template", TeamID, uint(ValidCATypeID), subjectName, "")
			require.Error(t, err)
			require.Contains(t, err.Error(), "Certificate template subject name is required")
		}
	})
}

func TestCreateCertificateTemplateSubjectAlternativeName(t *testing.T) {
	const ValidCATypeID = uint(2)
	const TeamID = 1

	makePremiumService := func(t *testing.T) (automdm.Service, context.Context, *mock.Store) {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

		ds.GetCertificateAuthorityByIDFunc = func(ctx context.Context, id uint, includeSecrets bool) (*automdm.CertificateAuthority, error) {
			return &automdm.CertificateAuthority{ID: id, Type: string(automdm.CATypeCustomSCEPProxy)}, nil
		}
		ds.CreateCertificateTemplateFunc = func(ctx context.Context, certificateTemplate *automdm.CertificateTemplate) (*automdm.CertificateTemplateResponse, error) {
			return &automdm.CertificateTemplateResponse{
				CertificateTemplateResponseSummary: automdm.CertificateTemplateResponseSummary{
					ID:                     1,
					Name:                   certificateTemplate.Name,
					SubjectName:            certificateTemplate.SubjectName,
					SubjectAlternativeName: certificateTemplate.SubjectAlternativeName,
				},
				TeamID: certificateTemplate.TeamID,
			}, nil
		}
		ds.CreatePendingCertificateTemplatesForExistingHostsFunc = func(ctx context.Context, certificateTemplateID uint, teamID uint) (int64, error) {
			return 0, nil
		}
		ds.SetCertificateTemplateVariablesFunc = func(ctx context.Context, certTemplateID uint, automdmVars []automdm.AutoMDMVarName) error {
			return nil
		}
		ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
			return &automdm.TeamLite{ID: tid, Name: "Yellow jackets"}, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		return svc, ctx, ds
	}

	t.Run("Premium tenant with valid SAN succeeds and round-trips the value", func(t *testing.T) {
		svc, ctx, ds := makePremiumService(t)

		san := "DNS=wifi.example.com, UPN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME, EMAIL=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME"
		resp, err := svc.CreateCertificateTemplate(ctx, "wifi", TeamID, ValidCATypeID, "CN=$AUTOMDM_VAR_HOST_UUID", san)
		require.NoError(t, err)
		require.Equal(t, san, resp.SubjectAlternativeName)
		require.True(t, ds.CreateCertificateTemplateFuncInvoked)
	})

	t.Run("Non-Premium tenant cannot create any certificate template (gate is in CreateCertificateTemplate, before validation)", func(t *testing.T) {
		// Certificate templates require a CA, and CAs are Premium-only, so the whole feature is
		// gated by a Premium check at the top of Service.CreateCertificateTemplate. SAN-bearing
		// payloads are not the only ones rejected.
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierFree}})
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

		// With SAN.
		_, err := svc.CreateCertificateTemplate(ctx, "wifi-with-san", TeamID, ValidCATypeID, "CN=$AUTOMDM_VAR_HOST_UUID", "DNS=example.com")
		require.ErrorIs(t, err, automdm.ErrMissingLicense)

		// Without SAN also rejected.
		_, err = svc.CreateCertificateTemplate(ctx, "wifi-no-san", TeamID, ValidCATypeID, "CN=$AUTOMDM_VAR_HOST_UUID", "")
		require.ErrorIs(t, err, automdm.ErrMissingLicense)
	})

	t.Run("Format failures return InvalidArgumentError scoped to the SAN field", func(t *testing.T) {
		svc, ctx, _ := makePremiumService(t)

		cases := []struct {
			name     string
			san      string
			fragment string
		}{
			{"missing equals", "DNS=ok, OOPS", "missing '='"},
			{"unknown key", "FOO=bar", "unsupported key"},
			{"rfc822 not synonym", "RFC822=user@x", "unsupported key"},
			{"too long", strings.Repeat("DNS=a,", 1024), "too long"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.CreateCertificateTemplate(ctx, "wifi", TeamID, ValidCATypeID, "CN=$AUTOMDM_VAR_HOST_UUID", tc.san)
				require.Error(t, err)
				var iae *automdm.InvalidArgumentError
				require.ErrorAs(t, err, &iae)
				require.True(t, iae.HasErrors())
				details := iae.Invalid()
				require.Len(t, details, 1)
				require.Equal(t, "subject_alternative_name", details[0]["name"])
				require.Contains(t, details[0]["reason"], tc.fragment)
			})
		}
	})

	t.Run("Unsupported variable in SAN is rejected", func(t *testing.T) {
		svc, ctx, _ := makePremiumService(t)

		_, err := svc.CreateCertificateTemplate(ctx, "wifi", TeamID, ValidCATypeID, "CN=$AUTOMDM_VAR_HOST_UUID", "EMAIL=$AUTOMDM_VAR_NDES_SCEP_CHALLENGE")
		require.Error(t, err)
		require.Contains(t, err.Error(), "AUTOMDM_VAR_NDES_SCEP_CHALLENGE")
	})

	t.Run("All supported HOST variables accepted in SAN", func(t *testing.T) {
		svc, ctx, _ := makePremiumService(t)

		san := "DNS=$AUTOMDM_VAR_HOST_UUID, EMAIL=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME, " +
			"UPN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART, " +
			"URI=$AUTOMDM_VAR_HOST_END_USER_IDP_GROUPS, " +
			"DNS=$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT, " +
			"EMAIL=$AUTOMDM_VAR_HOST_END_USER_IDP_FULL_NAME, " +
			"URI=$AUTOMDM_VAR_HOST_END_USER_IDP_EXTERNAL_ID, " +
			"DNS=$AUTOMDM_VAR_HOST_PLATFORM, " +
			"DNS=$AUTOMDM_VAR_HOST_HARDWARE_SERIAL"
		resp, err := svc.CreateCertificateTemplate(ctx, "all-vars", TeamID, ValidCATypeID, "CN=$AUTOMDM_VAR_HOST_UUID", san)
		require.NoError(t, err)
		require.Equal(t, san, resp.SubjectAlternativeName)
	})
}

func TestValidateCertificateTemplateSubjectAlternativeName(t *testing.T) {
	cases := []struct {
		name        string
		san         string
		expectError bool
		errContains string
	}{
		{"empty allowed", "", false, ""},
		{"whitespace allowed", "   \t\n  ", false, ""},
		{"single DNS", "DNS=example.com", false, ""},
		{"single EMAIL", "EMAIL=user@example.com", false, ""},
		{"single UPN", "UPN=user@corp.example.com", false, ""},
		{"single IP", "IP=10.0.0.1", false, ""},
		{"single URI", "URI=spiffe://example.com/x", false, ""},
		{"all five mixed", "DNS=a, EMAIL=b@x, UPN=c@d, IP=10.0.0.1, URI=spiffe://x", false, ""},
		{"case insensitive keys", "dns=a, email=b@x, upn=c@d, ip=10.0.0.1, uri=spiffe://x", false, ""},
		{"repeated keys", "DNS=a, DNS=b, EMAIL=c@x, EMAIL=d@y", false, ""},
		{"trailing comma is fine", "DNS=a,", false, ""},
		{"missing equals", "DNS=a, OOPS", true, "missing '='"},
		{"unknown key FOO", "FOO=bar", true, "unsupported key"},
		{"RFC822 is not a synonym", "RFC822=user@x", true, "unsupported key"},
		{"length cap", strings.Repeat("DNS=a,", 1024), true, "too long"},
		{"empty key with equals only", "=value", true, "empty key"},
		{"empty value DNS=", "DNS=", true, "empty value"},
		{"empty value EMAIL= mixed", "DNS=ok.example.com, EMAIL=", true, "empty value"},
		{"separator only", ",", true, "no entries"},
		{"separator only with whitespace", " , ", true, "no entries"},
		{"only commas", ",,,", true, "no entries"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCertificateTemplateSubjectAlternativeName(tc.san, "")
			if tc.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errContains)
				// Validator must return a typed *automdm.InvalidArgumentError scoped to the SAN field (HTTP 422).
				var iae *automdm.InvalidArgumentError
				require.ErrorAs(t, err, &iae)
				require.True(t, iae.HasErrors())
				details := iae.Invalid()
				require.Len(t, details, 1)
				require.Equal(t, "subject_alternative_name", details[0]["name"])
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestApplyCertificateTemplateSpecs(t *testing.T) {
	ds := new(mock.Store)
	// Certificate templates are Premium-gated.
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})

	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	ds.TeamLiteFunc = func(ctx context.Context, id uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{
			ID:   id,
			Name: "Test Team",
		}, nil
	}

	// Set up certificate authority mocks
	certAuthorities := []*automdm.CertificateAuthority{
		{
			ID:        1,
			Name:      ptr.String("Test CA 1"),
			Type:      string(automdm.CATypeCustomSCEPProxy),
			URL:       ptr.String("https://ca1.example.com"),
			Challenge: ptr.String("challenge1"),
		},
		{
			ID:        2,
			Name:      ptr.String("Test CA 2"),
			Type:      string(automdm.CATypeCustomSCEPProxy),
			URL:       ptr.String("https://ca2.example.com"),
			Challenge: ptr.String("challenge2"),
		},
		{
			ID:                            3,
			Name:                          ptr.String("Test CA 3"),
			Type:                          string(automdm.CATypeDigiCert),
			URL:                           ptr.String("https://ca3.example.com"),
			Challenge:                     ptr.String("challenge3"),
			CertificateCommonName:         ptr.String("foo"),
			CertificateSeatID:             ptr.String("foo"),
			CertificateUserPrincipalNames: &[]string{"foo"},
			APIToken:                      ptr.String("foo"),
			ProfileID:                     ptr.String("foo"),
		},
	}

	ds.ListCertificateAuthoritiesFunc = func(ctx context.Context) ([]*automdm.CertificateAuthoritySummary, error) {
		summaries := make([]*automdm.CertificateAuthoritySummary, 0, len(certAuthorities))
		for _, ca := range certAuthorities {
			summaries = append(summaries, &automdm.CertificateAuthoritySummary{
				ID:   ca.ID,
				Name: *ca.Name,
				Type: ca.Type,
			})
		}
		return summaries, nil
	}

	// Track certificate templates that are created
	var createdCertificates []automdm.CertificateTemplate
	var nextTemplateID uint = 100

	ds.BatchUpsertCertificateTemplatesFunc = func(ctx context.Context, certificates []*automdm.CertificateTemplate) ([]uint, error) {
		createdCertificates = nil
		createdMap := make([]uint, 0, len(certificates))
		for _, cert := range certificates {
			createdCertificates = append(createdCertificates, *cert)
			createdMap = append(createdMap, cert.TeamID)
		}
		return createdMap, nil
	}

	ds.GetCertificateTemplatesByTeamIDFunc = func(ctx context.Context, teamID uint, opts automdm.ListOptions) ([]*automdm.CertificateTemplateResponseSummary, *automdm.PaginationMetadata, error) {
		var result []*automdm.CertificateTemplateResponseSummary
		for _, cert := range createdCertificates {
			if cert.TeamID == teamID {
				result = append(result, &automdm.CertificateTemplateResponseSummary{
					ID:   nextTemplateID,
					Name: cert.Name,
				})
				nextTemplateID++
			}
		}
		return result, nil, nil
	}

	ds.CreatePendingCertificateTemplatesForExistingHostsFunc = func(ctx context.Context, certificateTemplateID uint, teamID uint) (int64, error) {
		return 0, nil
	}
	ds.SetCertificateTemplateVariablesFunc = func(ctx context.Context, certTemplateID uint, automdmVars []automdm.AutoMDMVarName) error {
		return nil
	}

	ds.GetCertificateTemplateByTeamIDAndNameFunc = func(ctx context.Context, teamID uint, name string) (*automdm.CertificateTemplateResponse, error) {
		return &automdm.CertificateTemplateResponse{
			CertificateTemplateResponseSummary: automdm.CertificateTemplateResponseSummary{
				ID:   nextTemplateID,
				Name: name,
			},
			TeamID: teamID,
		}, nil
	}

	t.Run("Valid CA types", func(t *testing.T) {
		err := svc.ApplyCertificateTemplateSpecs(ctx, []*automdm.CertificateRequestSpec{
			{
				Name:                   "Template 1",
				CertificateAuthorityId: 1,
				SubjectName:            "foo",
			},
			{
				Name:                   "Template 2",
				CertificateAuthorityId: 2,
				SubjectName:            "bar",
			},
		})
		require.NoError(t, err)
		require.Len(t, createdCertificates, 2)
		require.Equal(t, "Template 1", createdCertificates[0].Name)
		require.Equal(t, uint(1), createdCertificates[0].CertificateAuthorityID)
		require.Equal(t, "Template 2", createdCertificates[1].Name)
		require.Equal(t, uint(2), createdCertificates[1].CertificateAuthorityID)
	})

	t.Run("Invalid CA type", func(t *testing.T) {
		err := svc.ApplyCertificateTemplateSpecs(ctx, []*automdm.CertificateRequestSpec{
			{
				Name:                   "Template 3",
				CertificateAuthorityId: 3,
				SubjectName:            "baz",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Currently, only the custom_scep_proxy certificate authority is supported")
	})

	t.Run("Missing CA", func(t *testing.T) {
		err := svc.ApplyCertificateTemplateSpecs(ctx, []*automdm.CertificateRequestSpec{
			{
				Name:                   "Template 4",
				CertificateAuthorityId: 4,
				SubjectName:            "baz",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})

	t.Run("Empty name", func(t *testing.T) {
		err := svc.ApplyCertificateTemplateSpecs(ctx, []*automdm.CertificateRequestSpec{
			{
				Name:                   "",
				CertificateAuthorityId: 1,
				SubjectName:            "foo",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Certificate template name is required")
	})

	t.Run("Whitespace-only name", func(t *testing.T) {
		err := svc.ApplyCertificateTemplateSpecs(ctx, []*automdm.CertificateRequestSpec{
			{
				Name:                   "   ",
				CertificateAuthorityId: 1,
				SubjectName:            "foo",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Certificate template name is required")
	})

	t.Run("Name with invalid characters", func(t *testing.T) {
		err := svc.ApplyCertificateTemplateSpecs(ctx, []*automdm.CertificateRequestSpec{
			{
				Name:                   "template@name",
				CertificateAuthorityId: 1,
				SubjectName:            "foo",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Invalid certificate template name")
	})

	t.Run("Name too long", func(t *testing.T) {
		longName := strings.Repeat("a", 256)
		err := svc.ApplyCertificateTemplateSpecs(ctx, []*automdm.CertificateRequestSpec{
			{
				Name:                   longName,
				CertificateAuthorityId: 1,
				SubjectName:            "foo",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Certificate template name is too long")
	})

	t.Run("Whitespace-only subject name", func(t *testing.T) {
		err := svc.ApplyCertificateTemplateSpecs(ctx, []*automdm.CertificateRequestSpec{
			{
				Name:                   "Template 2",
				CertificateAuthorityId: 1,
				SubjectName:            "   ",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Certificate template subject name is required")
		require.Contains(t, err.Error(), "Template 2")
	})

	// SAN coverage: the only Apply-specific assertion worth a unit test is that the cert-name
	// suffix from validateCertificateTemplateSubjectAlternativeName reaches the typed error so
	// admins applying a multi-cert spec can identify which entry is bad. Other SAN paths
	// (validator semantics, single-cert Create) are covered in TestValidateCertificateTemplate*
	// and TestCreateCertificateTemplate*.
	t.Run("Invalid SAN rejected with cert-name suffix in subject_alternative_name field", func(t *testing.T) {
		err := svc.ApplyCertificateTemplateSpecs(ctx, []*automdm.CertificateRequestSpec{
			{
				Name:                   "Template SAN bad",
				CertificateAuthorityId: 1,
				SubjectName:            "CN=$AUTOMDM_VAR_HOST_UUID",
				SubjectAlternativeName: "FOO=bar",
			},
		})
		require.Error(t, err)
		var iae *automdm.InvalidArgumentError
		require.ErrorAs(t, err, &iae)
		details := iae.Invalid()
		require.Len(t, details, 1)
		require.Equal(t, "subject_alternative_name", details[0]["name"])
		require.Contains(t, details[0]["reason"], "Template SAN bad")
		require.Contains(t, details[0]["reason"], `unsupported key "FOO"`)
	})
}

func TestReplaceCertificateVariables(t *testing.T) {
	ds := new(mock.Store)

	givenName := "Jane"
	familyName := "Doe"
	dept := "Engineering"

	ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
		return &automdm.ScimUser{
			UserName:   "jane@example.com",
			GivenName:  &givenName,
			FamilyName: &familyName,
			Department: &dept,
			ExternalID: new("00u1abcd2EFGH3ijk4x7"),
			Groups: []automdm.ScimUserGroup{
				{DisplayName: "admins"},
				{DisplayName: "devs"},
			},
		}, nil
	}
	ds.ListHostDeviceMappingFunc = func(ctx context.Context, hostID uint) ([]*automdm.HostDeviceMapping, error) {
		return nil, nil
	}

	svc := &Service{ds: ds}
	host := &automdm.Host{
		ID:             1,
		UUID:           "host-uuid-123",
		HardwareSerial: "SERIAL-456",
		Platform:       "android",
	}

	t.Run("HOST_UUID", func(t *testing.T) {
		result, err := svc.replaceCertificateVariables(t.Context(), "CN=$AUTOMDM_VAR_HOST_UUID", host, nil)
		require.NoError(t, err)
		require.Equal(t, "CN=host-uuid-123", result)
	})

	t.Run("HOST_HARDWARE_SERIAL", func(t *testing.T) {
		result, err := svc.replaceCertificateVariables(t.Context(), "CN=$AUTOMDM_VAR_HOST_HARDWARE_SERIAL", host, nil)
		require.NoError(t, err)
		require.Equal(t, "CN=SERIAL-456", result)
	})

	t.Run("HOST_PLATFORM", func(t *testing.T) {
		result, err := svc.replaceCertificateVariables(t.Context(), "O=$AUTOMDM_VAR_HOST_PLATFORM", host, nil)
		require.NoError(t, err)
		require.Equal(t, "O=android", result)
	})

	t.Run("HOST_END_USER_IDP_USERNAME", func(t *testing.T) {
		result, err := svc.replaceCertificateVariables(t.Context(), "CN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME", host, nil)
		require.NoError(t, err)
		require.Equal(t, "CN=jane@example.com", result)
	})

	t.Run("HOST_END_USER_IDP_USERNAME_LOCAL_PART", func(t *testing.T) {
		result, err := svc.replaceCertificateVariables(t.Context(), "CN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART", host, nil)
		require.NoError(t, err)
		require.Equal(t, "CN=jane", result)
	})

	t.Run("HOST_END_USER_IDP_GROUPS", func(t *testing.T) {
		result, err := svc.replaceCertificateVariables(t.Context(), "OU=$AUTOMDM_VAR_HOST_END_USER_IDP_GROUPS", host, nil)
		require.NoError(t, err)
		// Comma between groups is escaped so it's not mistaken for a DN separator.
		require.Equal(t, `OU=admins\,devs`, result)
	})

	t.Run("HOST_END_USER_IDP_DEPARTMENT", func(t *testing.T) {
		result, err := svc.replaceCertificateVariables(t.Context(), "OU=$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT", host, nil)
		require.NoError(t, err)
		require.Equal(t, "OU=Engineering", result)
	})

	t.Run("HOST_END_USER_IDP_FULL_NAME", func(t *testing.T) {
		result, err := svc.replaceCertificateVariables(t.Context(), "CN=$AUTOMDM_VAR_HOST_END_USER_IDP_FULL_NAME", host, nil)
		require.NoError(t, err)
		require.Equal(t, "CN=Jane Doe", result)
	})

	t.Run("HOST_END_USER_IDP_EXTERNAL_ID", func(t *testing.T) {
		result, err := svc.replaceCertificateVariables(t.Context(), "UID=$AUTOMDM_VAR_HOST_END_USER_IDP_EXTERNAL_ID", host, nil)
		require.NoError(t, err)
		require.Equal(t, "UID=00u1abcd2EFGH3ijk4x7", result)
	})

	t.Run("multiple variables in one string", func(t *testing.T) {
		input := "CN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME,O=$AUTOMDM_VAR_HOST_PLATFORM,OU=$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT"
		result, err := svc.replaceCertificateVariables(t.Context(), input, host, nil)
		require.NoError(t, err)
		require.Equal(t, "CN=jane@example.com,O=android,OU=Engineering", result)
	})

	t.Run("endUsersMemo is populated on first call and reused", func(t *testing.T) {
		var memo []automdm.HostEndUser
		_, err := svc.replaceCertificateVariables(t.Context(), "CN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME", host, &memo)
		require.NoError(t, err)
		require.NotNil(t, memo)
		require.Len(t, memo, 1)

		// Second call reuses the memo without hitting the datastore again.
		ds.ScimUserByHostIDFuncInvoked = false
		_, err = svc.replaceCertificateVariables(t.Context(), "CN=$AUTOMDM_VAR_HOST_END_USER_IDP_FULL_NAME", host, &memo)
		require.NoError(t, err)
		require.False(t, ds.ScimUserByHostIDFuncInvoked)
	})

	t.Run("missing IDP user returns error", func(t *testing.T) {
		ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
			return nil, &notFoundError{}
		}
		_, err := svc.replaceCertificateVariables(t.Context(), "CN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME", host, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not have an IDP user")
	})

	t.Run("missing groups returns error", func(t *testing.T) {
		ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
			return &automdm.ScimUser{UserName: "jane@example.com"}, nil
		}
		_, err := svc.replaceCertificateVariables(t.Context(), "OU=$AUTOMDM_VAR_HOST_END_USER_IDP_GROUPS", host, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not have IDP groups")
	})

	t.Run("missing department returns error", func(t *testing.T) {
		ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
			return &automdm.ScimUser{UserName: "jane@example.com"}, nil
		}
		_, err := svc.replaceCertificateVariables(t.Context(), "OU=$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT", host, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not have an IDP department")
	})

	t.Run("missing full name returns error", func(t *testing.T) {
		ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
			return &automdm.ScimUser{UserName: "jane@example.com"}, nil
		}
		_, err := svc.replaceCertificateVariables(t.Context(), "CN=$AUTOMDM_VAR_HOST_END_USER_IDP_FULL_NAME", host, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not have an IDP full name")
	})

	t.Run("missing external ID returns error", func(t *testing.T) {
		ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
			return &automdm.ScimUser{UserName: "jane@example.com"}, nil
		}
		_, err := svc.replaceCertificateVariables(t.Context(), "UID=$AUTOMDM_VAR_HOST_END_USER_IDP_EXTERNAL_ID", host, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not have an IDP external ID")
	})

	t.Run("no variables returns input unchanged", func(t *testing.T) {
		result, err := svc.replaceCertificateVariables(t.Context(), "CN=static-value", host, nil)
		require.NoError(t, err)
		require.Equal(t, "CN=static-value", result)
	})

	t.Run("unsupported variable returns error", func(t *testing.T) {
		_, err := svc.replaceCertificateVariables(t.Context(), "CN=$AUTOMDM_VAR_NDES_SCEP_CHALLENGE", host, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported AutoMDM variable")
	})

	t.Run("dollar signs in values are substituted literally", func(t *testing.T) {
		ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
			return &automdm.ScimUser{
				UserName:   "jane@example.com",
				Department: new("R$1D"),
				ExternalID: new("tenant$1"),
			}, nil
		}
		result, err := svc.replaceCertificateVariables(t.Context(),
			"UID=$AUTOMDM_VAR_HOST_END_USER_IDP_EXTERNAL_ID,OU=$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT", host, nil)
		require.NoError(t, err)
		require.Equal(t, "UID=tenant$1,OU=R$1D", result)
	})

	t.Run("special characters are RFC 4514 escaped", func(t *testing.T) {
		dept := "Sales, Marketing + Ops"
		ds.ScimUserByHostIDFunc = func(ctx context.Context, hostID uint) (*automdm.ScimUser, error) {
			return &automdm.ScimUser{
				UserName:   "jane@example.com",
				GivenName:  &givenName,
				FamilyName: &familyName,
				Department: &dept,
				Groups: []automdm.ScimUserGroup{
					{DisplayName: "group<A>"},
					{DisplayName: `group"B"`},
				},
			}, nil
		}
		result, err := svc.replaceCertificateVariables(t.Context(), "OU=$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT", host, nil)
		require.NoError(t, err)
		require.Equal(t, `OU=Sales\, Marketing \+ Ops`, result)

		result, err = svc.replaceCertificateVariables(t.Context(), "OU=$AUTOMDM_VAR_HOST_END_USER_IDP_GROUPS", host, nil)
		require.NoError(t, err)
		require.Equal(t, `OU=group\<A\>\,group\"B\"`, result)
	})
}

func TestExtractCertTemplateAutoMDMVars(t *testing.T) {
	t.Run("extracts from subject_name and SAN", func(t *testing.T) {
		vars := extractCertTemplateAutoMDMVars(
			"CN=$AUTOMDM_VAR_HOST_UUID",
			"EMAIL=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME, DNS=$AUTOMDM_VAR_HOST_PLATFORM",
		)
		require.ElementsMatch(t, []automdm.AutoMDMVarName{
			automdm.AutoMDMVarHostUUID,
			automdm.AutoMDMVarHostEndUserIDPUsername,
			automdm.AutoMDMVarHostPlatform,
		}, vars)
	})

	t.Run("returns nil for no variables", func(t *testing.T) {
		vars := extractCertTemplateAutoMDMVars("CN=static", "DNS=example.com")
		require.Nil(t, vars)
	})

	t.Run("deduplicates across subject and SAN", func(t *testing.T) {
		vars := extractCertTemplateAutoMDMVars(
			"CN=$AUTOMDM_VAR_HOST_UUID",
			"DNS=$AUTOMDM_VAR_HOST_UUID",
		)
		require.Equal(t, []automdm.AutoMDMVarName{automdm.AutoMDMVarHostUUID}, vars)
	})
}

func TestCreateCertificateTemplateVariableTracking(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil, &TestServerOpts{License: &automdm.LicenseInfo{Tier: automdm.TierPremium}})
	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	ds.GetCertificateAuthorityByIDFunc = func(ctx context.Context, id uint, includeSecrets bool) (*automdm.CertificateAuthority, error) {
		return &automdm.CertificateAuthority{ID: id, Type: string(automdm.CATypeCustomSCEPProxy)}, nil
	}
	ds.CreateCertificateTemplateFunc = func(ctx context.Context, ct *automdm.CertificateTemplate) (*automdm.CertificateTemplateResponse, error) {
		return &automdm.CertificateTemplateResponse{
			CertificateTemplateResponseSummary: automdm.CertificateTemplateResponseSummary{ID: 42, Name: ct.Name},
			TeamID:                             ct.TeamID,
		}, nil
	}
	ds.CreatePendingCertificateTemplatesForExistingHostsFunc = func(ctx context.Context, certID uint, teamID uint) (int64, error) {
		return 0, nil
	}
	ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
		return &automdm.TeamLite{ID: tid, Name: "team"}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	var capturedVars []automdm.AutoMDMVarName
	ds.SetCertificateTemplateVariablesFunc = func(ctx context.Context, certTemplateID uint, automdmVars []automdm.AutoMDMVarName) error {
		require.Equal(t, uint(42), certTemplateID)
		capturedVars = automdmVars
		return nil
	}

	_, err := svc.CreateCertificateTemplate(
		ctx, "wifi-cert", 1, 1,
		"CN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME",
		"DNS=$AUTOMDM_VAR_HOST_UUID, EMAIL=$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT",
	)
	require.NoError(t, err)
	require.True(t, ds.SetCertificateTemplateVariablesFuncInvoked)
	require.ElementsMatch(t, []automdm.AutoMDMVarName{
		automdm.AutoMDMVarHostEndUserIDPUsername,
		automdm.AutoMDMVarHostUUID,
		automdm.AutoMDMVarHostEndUserIDPDepartment,
	}, capturedVars)
}

func TestResendHostCertificateTemplate(t *testing.T) {
	ds := new(mock.Store)
	opts := &TestServerOpts{}
	svc, ctx := newTestService(t, ds, nil, nil, opts)

	const (
		hostID       = uint(1)
		templateID   = uint(42)
		teamID       = uint(10)
		templateName = "My Cert"
	)

	ctx = viewer.NewContext(ctx, viewer.Viewer{User: &automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}})

	ds.HostLiteFunc = func(ctx context.Context, id uint) (*automdm.Host, error) {
		if id == hostID {
			tid := teamID
			return &automdm.Host{ID: id, TeamID: &tid}, nil
		}
		return nil, errors.New("host not found")
	}

	ds.GetCertificateTemplateByIdFunc = func(ctx context.Context, id uint) (*automdm.CertificateTemplateResponse, error) {
		return &automdm.CertificateTemplateResponse{
			CertificateTemplateResponseSummary: automdm.CertificateTemplateResponseSummary{
				ID:   id,
				Name: templateName,
			},
		}, nil
	}

	ds.GetCertificateTemplateByIdForHostFunc = func(ctx context.Context, id uint, hostUUID string) (*automdm.CertificateTemplateResponseForHost, error) {
		return &automdm.CertificateTemplateResponseForHost{
			CertificateTemplateResponse: automdm.CertificateTemplateResponse{
				CertificateTemplateResponseSummary: automdm.CertificateTemplateResponseSummary{
					ID:   id,
					Name: templateName,
				},
			},
			Status: automdm.CertificateTemplateDelivered,
		}, nil
	}

	t.Run("succeeds and creates activity", func(t *testing.T) {
		ds.ResendHostCertificateTemplateFunc = func(ctx context.Context, hID uint, tID uint) error {
			require.Equal(t, hostID, hID)
			require.Equal(t, templateID, tID)
			return nil
		}

		var capturedActivity automdm.ActivityTypeResentCertificate
		opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
			act, ok := activity.(automdm.ActivityTypeResentCertificate)
			require.True(t, ok, "expected ActivityTypeResentCertificate, got %T", activity)
			capturedActivity = act
			return nil
		}

		err := svc.ResendHostCertificateTemplate(ctx, hostID, templateID)
		require.NoError(t, err)
		require.True(t, ds.ResendHostCertificateTemplateFuncInvoked)
		require.True(t, opts.ActivityMock.NewActivityFuncInvoked)
		require.Equal(t, hostID, capturedActivity.HostID)
		require.Equal(t, templateID, capturedActivity.CertificateTemplateID)
		require.Equal(t, templateName, capturedActivity.CertificateName)

		ds.ResendHostCertificateTemplateFuncInvoked = false
		opts.ActivityMock.NewActivityFuncInvoked = false
	})

	t.Run("returns error when host not found", func(t *testing.T) {
		err := svc.ResendHostCertificateTemplate(ctx, 99999, templateID)
		require.Error(t, err)
		require.False(t, opts.ActivityMock.NewActivityFuncInvoked)
	})

	t.Run("returns error when datastore fails", func(t *testing.T) {
		ds.ResendHostCertificateTemplateFunc = func(ctx context.Context, hID uint, tID uint) error {
			return errors.New("db error")
		}

		err := svc.ResendHostCertificateTemplate(ctx, hostID, templateID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "db error")
		require.False(t, opts.ActivityMock.NewActivityFuncInvoked)
	})

	t.Run("returns 400 when template is pending for host", func(t *testing.T) {
		ds.GetCertificateTemplateByIdForHostFunc = func(ctx context.Context, id uint, hostUUID string) (*automdm.CertificateTemplateResponseForHost, error) {
			return &automdm.CertificateTemplateResponseForHost{
				CertificateTemplateResponse: automdm.CertificateTemplateResponse{
					CertificateTemplateResponseSummary: automdm.CertificateTemplateResponseSummary{
						ID:   id,
						Name: templateName,
					},
				},
				Status: automdm.CertificateTemplatePending,
			}, nil
		}
		ds.ResendHostCertificateTemplateFuncInvoked = false

		err := svc.ResendHostCertificateTemplate(ctx, hostID, templateID)
		require.Error(t, err)

		var umErr interface{ StatusCode() int }
		require.ErrorAs(t, err, &umErr)
		require.Equal(t, 400, umErr.StatusCode())
		require.False(t, ds.ResendHostCertificateTemplateFuncInvoked)
		require.False(t, opts.ActivityMock.NewActivityFuncInvoked)
	})
}

func TestGetCertificateTemplate(t *testing.T) {
	const (
		noTeamTemplateID  = uint(1)
		teamTemplateID    = uint(2)
		missingTemplateID = uint(999)
		teamID            = uint(10)
		templateName      = "Certificate Template - Test"
	)

	globalAdmin := &automdm.User{GlobalRole: new(automdm.RoleAdmin)}
	globalObserver := &automdm.User{GlobalRole: new(automdm.RoleObserver)}
	teamAdmin := &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: teamID}, Role: automdm.RoleAdmin}}}
	otherTeamAdmin := &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: teamID + 1}, Role: automdm.RoleAdmin}}}

	type getTestOpts struct {
		svc     automdm.Service
		ctx     context.Context
		ds      *mock.Store
		authCtx *authz_ctx.AuthorizationContext
	}

	setup := func(t *testing.T, user *automdm.User) *getTestOpts {
		ds := new(mock.Store)
		svc, ctx := newTestService(t, ds, nil, nil)

		ds.GetCertificateTemplateByIdFunc = func(ctx context.Context, id uint) (*automdm.CertificateTemplateResponse, error) {
			switch id {
			case noTeamTemplateID, teamTemplateID:
				templateTeamID := uint(0)
				if id == teamTemplateID {
					templateTeamID = teamID
				}
				return &automdm.CertificateTemplateResponse{
					CertificateTemplateResponseSummary: automdm.CertificateTemplateResponseSummary{
						ID:   id,
						Name: templateName,
					},
					TeamID: templateTeamID,
				}, nil
			default:
				return nil, ctxerr.Wrap(ctx, common_mysql.NotFound("CertificateTemplate").WithID(id))
			}
		}

		authCtx := &authz_ctx.AuthorizationContext{}
		ctx = authz_ctx.NewContext(ctx, authCtx)
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

		return &getTestOpts{svc: svc, ctx: ctx, ds: ds, authCtx: authCtx}
	}

	t.Run("successful read", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			user       *automdm.User
			templateID uint
			wantTeamID uint
		}{
			{name: "global admin reads a team template", user: globalAdmin, templateID: teamTemplateID, wantTeamID: teamID},
			{name: "global admin reads a 'no team' template", user: globalAdmin, templateID: noTeamTemplateID, wantTeamID: 0},
			{name: "team admin reads a template on their team", user: teamAdmin, templateID: teamTemplateID, wantTeamID: teamID},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tt := setup(t, tc.user)

				certificate, err := tt.svc.GetCertificateTemplate(tt.ctx, tc.templateID)
				require.NoError(t, err)
				require.True(t, tt.authCtx.Checked())
				require.NotNil(t, certificate)
				require.Equal(t, tc.templateID, certificate.ID)
				require.Equal(t, templateName, certificate.Name)
				require.Equal(t, tc.wantTeamID, certificate.TeamID)
			})
		}
	})

	t.Run("forbidden error", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			user       *automdm.User
			templateID uint
		}{
			{name: "global observer reading a 'no team' template", user: globalObserver, templateID: noTeamTemplateID},
			{name: "global observer reading a team template", user: globalObserver, templateID: teamTemplateID},
			{name: "global observer reading a missing template", user: globalObserver, templateID: missingTemplateID},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tt := setup(t, tc.user)

				certificate, err := tt.svc.GetCertificateTemplate(tt.ctx, tc.templateID)
				require.Error(t, err)
				require.Nil(t, certificate)
				require.True(t, tt.authCtx.Checked())
				require.Contains(t, err.Error(), authz.ForbiddenErrorMessage)

				if tc.templateID == missingTemplateID {
					require.False(t, automdm.IsNotFound(err), "must not disclose that the template is missing")
				}
			})
		}
	})

	t.Run("not found error", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			user       *automdm.User
			templateID uint
		}{
			{name: "global admin reading a missing template", user: globalAdmin, templateID: missingTemplateID},
			{name: "team admin reading a template on another team", user: otherTeamAdmin, templateID: teamTemplateID},
			{name: "team admin on another team reading a missing template", user: otherTeamAdmin, templateID: missingTemplateID},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tt := setup(t, tc.user)

				certificate, err := tt.svc.GetCertificateTemplate(tt.ctx, tc.templateID)
				require.Error(t, err)
				require.Nil(t, certificate)
				require.True(t, tt.authCtx.Checked(), "authorization must be checked even when the template is missing")
				require.True(t, automdm.IsNotFound(err))
				require.NotContains(t, err.Error(), authz.ForbiddenErrorMessage)
			})
		}
	})
}

func TestDeleteCertificateTemplate(t *testing.T) {
	const (
		noTeamTemplateID  = uint(1)
		teamTemplateID    = uint(2)
		missingTemplateID = uint(999)
		teamID            = uint(10)
		templateName      = "Certificate Template - Test"
		teamName          = "AutoMDM Team - Test"
	)

	globalAdmin := &automdm.User{GlobalRole: new(automdm.RoleAdmin)}
	globalObserver := &automdm.User{GlobalRole: new(automdm.RoleObserver)}
	teamAdmin := &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: teamID}, Role: automdm.RoleAdmin}}}
	otherTeamAdmin := &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: teamID + 1}, Role: automdm.RoleAdmin}}}

	type deleteTestOpts struct {
		svc     automdm.Service
		ctx     context.Context
		ds      *mock.Store
		opts    *TestServerOpts
		authCtx *authz_ctx.AuthorizationContext
	}

	setup := func(t *testing.T, user *automdm.User) *deleteTestOpts {
		ds := new(mock.Store)
		opts := &TestServerOpts{}
		svc, ctx := newTestService(t, ds, nil, nil, opts)

		ds.GetCertificateTemplateByIdFunc = func(ctx context.Context, id uint) (*automdm.CertificateTemplateResponse, error) {
			switch id {
			case noTeamTemplateID, teamTemplateID:
				templateTeamID := uint(0)
				if id == teamTemplateID {
					templateTeamID = teamID
				}
				return &automdm.CertificateTemplateResponse{
					CertificateTemplateResponseSummary: automdm.CertificateTemplateResponseSummary{
						ID:   id,
						Name: templateName,
					},
					TeamID: templateTeamID,
				}, nil
			default:
				return nil, ctxerr.Wrap(ctx, common_mysql.NotFound("CertificateTemplate").WithID(id))
			}
		}
		ds.DeleteCertificateTemplateFunc = func(ctx context.Context, id uint) error {
			return nil
		}
		ds.SetHostCertificateTemplatesToPendingRemoveFunc = func(ctx context.Context, certificateTemplateID uint) error {
			return nil
		}
		ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
			return &automdm.TeamLite{ID: tid, Name: teamName}, nil
		}
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}

		authCtx := &authz_ctx.AuthorizationContext{}
		ctx = authz_ctx.NewContext(ctx, authCtx)
		ctx = viewer.NewContext(ctx, viewer.Viewer{User: user})

		return &deleteTestOpts{svc: svc, ctx: ctx, ds: ds, opts: opts, authCtx: authCtx}
	}

	t.Run("successful deletion", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			user       *automdm.User
			templateID uint
		}{
			{name: "global admin deletes a team template", user: globalAdmin, templateID: teamTemplateID},
			{name: "global admin deletes a 'no team' template", user: globalAdmin, templateID: noTeamTemplateID},
			{name: "team admin deletes a template on their team", user: teamAdmin, templateID: teamTemplateID},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tt := setup(t, tc.user)

				var capturedActivity automdm.ActivityTypeDeletedCertificate
				tt.opts.ActivityMock.NewActivityFunc = func(_ context.Context, _ *activity_api.User, activity activity_api.ActivityDetails) error {
					act, ok := activity.(automdm.ActivityTypeDeletedCertificate)
					require.True(t, ok, "expected ActivityTypeDeletedCertificate, got %T", activity)
					capturedActivity = act
					return nil
				}

				err := tt.svc.DeleteCertificateTemplate(tt.ctx, tc.templateID)
				require.NoError(t, err)
				require.True(t, tt.authCtx.Checked())
				require.True(t, tt.ds.DeleteCertificateTemplateFuncInvoked)
				require.True(t, tt.ds.SetHostCertificateTemplatesToPendingRemoveFuncInvoked)
				require.True(t, tt.opts.ActivityMock.NewActivityFuncInvoked)
				require.Equal(t, templateName, capturedActivity.Name)
				if tc.templateID == teamTemplateID {
					require.True(t, tt.ds.TeamLiteFuncInvoked)
					require.Equal(t, new(teamID), capturedActivity.TeamID)
					require.Equal(t, new(teamName), capturedActivity.TeamName)
				} else {
					require.False(t, tt.ds.TeamLiteFuncInvoked)
					require.Nil(t, capturedActivity.TeamID)
					require.Nil(t, capturedActivity.TeamName)
				}
			})
		}
	})

	t.Run("forbidden error", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			user       *automdm.User
			templateID uint
		}{
			{name: "global observer deleting a 'no team' template", user: globalObserver, templateID: noTeamTemplateID},
			{name: "global observer deleting a team template", user: globalObserver, templateID: teamTemplateID},
			{name: "global observer deleting a missing template", user: globalObserver, templateID: missingTemplateID},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tt := setup(t, tc.user)

				err := tt.svc.DeleteCertificateTemplate(tt.ctx, tc.templateID)
				require.Error(t, err)
				require.True(t, tt.authCtx.Checked())
				require.Contains(t, err.Error(), authz.ForbiddenErrorMessage)
				require.False(t, tt.ds.DeleteCertificateTemplateFuncInvoked)

				if tc.templateID == missingTemplateID {
					require.False(t, automdm.IsNotFound(err), "must not disclose that the template is missing")
				}
			})
		}
	})

	t.Run("not found error", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			user       *automdm.User
			templateID uint
		}{
			{name: "global admin deleting a missing template", user: globalAdmin, templateID: missingTemplateID},
			{name: "team admin deleting a template on another team", user: otherTeamAdmin, templateID: teamTemplateID},
			{name: "team admin on another team deleting a missing template", user: otherTeamAdmin, templateID: missingTemplateID},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tt := setup(t, tc.user)

				err := tt.svc.DeleteCertificateTemplate(tt.ctx, tc.templateID)
				require.Error(t, err)
				require.True(t, tt.authCtx.Checked(), "authorization must be checked even when the template is missing")
				require.True(t, automdm.IsNotFound(err))
				require.NotContains(t, err.Error(), authz.ForbiddenErrorMessage)
				require.False(t, tt.ds.DeleteCertificateTemplateFuncInvoked)
			})
		}
	})
}
