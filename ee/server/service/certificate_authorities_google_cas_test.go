package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mock"
	googlecas_mock "github.com/anilyagiz/automdm/v4/server/mock/googlecas"
	common_mysql "github.com/anilyagiz/automdm/v4/server/platform/mysql"
	"github.com/stretchr/testify/require"
)

const testGoogleCASKey = `{"type":"service_account","client_email":"sa@my-project.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----"}`

func goodGoogleCAS() *automdm.GoogleCASCA {
	return &automdm.GoogleCASCA{Name: "GOOGLE_WIFI", Project: "my-project", Location: "us-central1", CAPool: "my-ca-pool", APIKeyJSON: testGoogleCASKey}
}

// newGoogleCASTestService builds a service whose datastore records the CA it is asked to create or update and then fails, which keeps
// the activity recording, which can't be mocked here, from running.
func newGoogleCASTestService(t *testing.T, stored *automdm.CertificateAuthority) (*Service, *mock.Store, *googlecas_mock.Service, context.Context) {
	ds := new(mock.Store)
	ds.NewCertificateAuthorityFunc = func(ctx context.Context, ca *automdm.CertificateAuthority) (*automdm.CertificateAuthority, error) {
		return nil, errors.New("mock error to avoid NewActivity panic")
	}
	ds.GetCertificateAuthorityByIDFunc = func(ctx context.Context, id uint, includeSecrets bool) (*automdm.CertificateAuthority, error) {
		if stored != nil && stored.ID == id {
			return stored, nil
		}
		return nil, common_mysql.NotFound("certificate authority")
	}
	ds.UpdateCertificateAuthorityByIDFunc = func(ctx context.Context, id uint, ca *automdm.CertificateAuthority) error {
		return errors.New("mock error to avoid NewActivity panic")
	}
	googleCAS := &googlecas_mock.Service{VerifyCAPoolFunc: func(ctx context.Context, ca automdm.GoogleCASCA) error { return nil }}
	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	svc := &Service{
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
		ds:               ds,
		authz:            authorizer,
		googleCASService: googleCAS,
	}
	svc.config.Server.PrivateKey = "supersecret"
	ctx := viewer.NewContext(t.Context(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
	return svc, ds, googleCAS, ctx
}

func TestCreateGoogleCASCertificateAuthority(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		svc, ds, googleCAS, ctx := newGoogleCASTestService(t, nil)
		var created *automdm.CertificateAuthority
		ds.NewCertificateAuthorityFunc = func(ctx context.Context, ca *automdm.CertificateAuthority) (*automdm.CertificateAuthority, error) {
			created = ca
			return nil, errors.New("mock error to avoid NewActivity panic")
		}
		var verified automdm.GoogleCASCA
		googleCAS.VerifyCAPoolFunc = func(ctx context.Context, ca automdm.GoogleCASCA) error {
			verified = ca
			return nil
		}

		in := goodGoogleCAS()
		in.Name, in.Project = " GOOGLE_WIFI ", " my-project " // trimmed by Preprocess
		_, err := svc.NewCertificateAuthority(ctx, automdm.CertificateAuthorityPayload{GoogleCAS: in})
		require.EqualError(t, err, "mock error to avoid NewActivity panic")

		require.Equal(t, *goodGoogleCAS(), verified)
		require.Equal(t, string(automdm.CATypeGoogleCAS), created.Type)
		require.Equal(t, "GOOGLE_WIFI", *created.Name)
		require.Empty(t, *created.URL) // the column is NOT NULL
		require.Equal(t, "my-project", *created.GoogleCASProject)
		require.Equal(t, "us-central1", *created.GoogleCASLocation)
		require.Equal(t, "my-ca-pool", *created.GoogleCASCAPool)
		require.JSONEq(t, testGoogleCASKey, *created.GoogleCASAPIKeyJSON)
	})

	t.Run("validation", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			mutate  func(ca *automdm.GoogleCASCA)
			wantErr string
		}{
			{"bad name", func(ca *automdm.GoogleCASCA) { ca.Name = "a/b" }, "Only letters, numbers and underscores allowed"},
			{"empty project", func(ca *automdm.GoogleCASCA) { ca.Project = " " }, "The Google Cloud project ID cannot be empty."},
			{"empty location", func(ca *automdm.GoogleCASCA) { ca.Location = "" }, "The location cannot be empty."},
			{"empty ca pool", func(ca *automdm.GoogleCASCA) { ca.CAPool = "" }, "The CA pool cannot be empty."},
			{"empty key", func(ca *automdm.GoogleCASCA) { ca.APIKeyJSON = "" }, "The service account key cannot be empty."},
			{"masked key", func(ca *automdm.GoogleCASCA) { ca.APIKeyJSON = automdm.MaskedPassword }, "The service account key must be provided in full, not masked."},
			{"key is not json", func(ca *automdm.GoogleCASCA) { ca.APIKeyJSON = "nope" }, "The service account key must be a JSON object."},
			{"key too large", func(ca *automdm.GoogleCASCA) { ca.APIKeyJSON = automdm.GoogleCASKeyJSON(strings.Repeat("a", 16<<10+1)) }, "The service account key is too large."},
			{"pool with a slash", func(ca *automdm.GoogleCASCA) { ca.CAPool = "pool/certificates/other" }, "The CA pool can only contain letters, numbers, hyphens and underscores."},
			{"location with a slash", func(ca *automdm.GoogleCASCA) { ca.Location = "us-central1/../x" }, "The location can only contain lowercase letters, numbers and hyphens."},
			{"project with a query", func(ca *automdm.GoogleCASCA) { ca.Project = "p?x=1" }, "The Google Cloud project ID can only contain"},
			{"key is not a service account", func(ca *automdm.GoogleCASCA) { ca.APIKeyJSON = `{"type":"authorized_user"}` }, `"type" set to "service_account"`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc, _, googleCAS, ctx := newGoogleCASTestService(t, nil)
				in := goodGoogleCAS()
				tc.mutate(in)
				_, err := svc.NewCertificateAuthority(ctx, automdm.CertificateAuthorityPayload{GoogleCAS: in})
				require.ErrorContains(t, err, "Couldn't add certificate authority. ")
				require.ErrorContains(t, err, tc.wantErr)
				require.False(t, googleCAS.VerifyCAPoolFuncInvoked, "Google must not be contacted for a CA that fails validation")
			})
		}
	})

	t.Run("CA pool Google rejects is not stored", func(t *testing.T) {
		svc, ds, googleCAS, ctx := newGoogleCASTestService(t, nil)
		googleCAS.VerifyCAPoolFunc = func(ctx context.Context, ca automdm.GoogleCASCA) error {
			return errors.New(`CA pool "my-ca-pool" wasn't found in project "my-project", location "us-central1".`)
		}
		_, err := svc.NewCertificateAuthority(ctx, automdm.CertificateAuthorityPayload{GoogleCAS: goodGoogleCAS()})
		require.EqualError(t, err, `Couldn't add certificate authority. CA pool "my-ca-pool" wasn't found in project "my-project", location "us-central1".`)
		require.False(t, ds.NewCertificateAuthorityFuncInvoked)
	})
}

func TestUpdateGoogleCASCertificateAuthority(t *testing.T) {
	t.Parallel()

	stored := func() *automdm.CertificateAuthority {
		return &automdm.CertificateAuthority{
			ID:                  1,
			Type:                string(automdm.CATypeGoogleCAS),
			Name:                new("GOOGLE_WIFI"),
			URL:                 new(""),
			GoogleCASProject:    new("my-project"),
			GoogleCASLocation:   new("us-central1"),
			GoogleCASCAPool:     new("my-ca-pool"),
			GoogleCASAPIKeyJSON: new(testGoogleCASKey),
		}
	}
	update := func(p automdm.GoogleCASCAUpdatePayload) automdm.CertificateAuthorityUpdatePayload {
		return automdm.CertificateAuthorityUpdatePayload{GoogleCASCAUpdatePayload: &p}
	}

	t.Run("name change is stored without asking Google, even with the other values sent unchanged", func(t *testing.T) {
		svc, ds, googleCAS, ctx := newGoogleCASTestService(t, stored())
		var updated *automdm.CertificateAuthority
		ds.UpdateCertificateAuthorityByIDFunc = func(ctx context.Context, id uint, ca *automdm.CertificateAuthority) error {
			updated = ca
			return errors.New("mock error to avoid NewActivity panic")
		}
		err := svc.UpdateCertificateAuthority(ctx, 1, update(automdm.GoogleCASCAUpdatePayload{
			Name: new("GOOGLE_VPN"), Project: new("my-project"), Location: new("us-central1"), CAPool: new("my-ca-pool"),
			APIKeyJSON: new(automdm.GoogleCASKeyJSON(automdm.MaskedPassword)),
		}))
		require.EqualError(t, err, "mock error to avoid NewActivity panic")
		require.False(t, googleCAS.VerifyCAPoolFuncInvoked)
		require.Equal(t, &automdm.CertificateAuthority{Type: string(automdm.CATypeGoogleCAS), Name: new("GOOGLE_VPN")}, updated)
	})

	t.Run("pool change is verified with the stored key and only the change is stored", func(t *testing.T) {
		svc, ds, googleCAS, ctx := newGoogleCASTestService(t, stored())
		var verified automdm.GoogleCASCA
		googleCAS.VerifyCAPoolFunc = func(ctx context.Context, ca automdm.GoogleCASCA) error {
			verified = ca
			return nil
		}
		var updated *automdm.CertificateAuthority
		ds.UpdateCertificateAuthorityByIDFunc = func(ctx context.Context, id uint, ca *automdm.CertificateAuthority) error {
			updated = ca
			return errors.New("mock error to avoid NewActivity panic")
		}
		err := svc.UpdateCertificateAuthority(ctx, 1, update(automdm.GoogleCASCAUpdatePayload{CAPool: new("other-pool"), APIKeyJSON: new(automdm.GoogleCASKeyJSON(automdm.MaskedPassword))}))
		require.EqualError(t, err, "mock error to avoid NewActivity panic")
		want := *goodGoogleCAS()
		want.CAPool = "other-pool"
		require.Equal(t, want, verified)
		require.Equal(t, &automdm.CertificateAuthority{Type: string(automdm.CATypeGoogleCAS), GoogleCASCAPool: new("other-pool")}, updated)
	})

	t.Run("rejected by Google is not stored", func(t *testing.T) {
		svc, ds, googleCAS, ctx := newGoogleCASTestService(t, stored())
		googleCAS.VerifyCAPoolFunc = func(ctx context.Context, ca automdm.GoogleCASCA) error { return errors.New("no access") }
		rotated := strings.Replace(testGoogleCASKey, `"sa@`, `"rotated@`, 1)
		err := svc.UpdateCertificateAuthority(ctx, 1, update(automdm.GoogleCASCAUpdatePayload{APIKeyJSON: new(automdm.GoogleCASKeyJSON(rotated))}))
		require.EqualError(t, err, "Couldn't edit certificate authority. no access")
		require.False(t, ds.UpdateCertificateAuthorityByIDFuncInvoked)
	})

	t.Run("nothing changed is neither verified nor stored", func(t *testing.T) {
		var reformattedKey bytes.Buffer
		require.NoError(t, json.Indent(&reformattedKey, []byte(testGoogleCASKey), "", "  "))
		for _, tc := range []struct {
			name    string
			payload automdm.GoogleCASCAUpdatePayload
		}{
			{"empty payload", automdm.GoogleCASCAUpdatePayload{}},
			{"masked key alone", automdm.GoogleCASCAUpdatePayload{APIKeyJSON: new(automdm.GoogleCASKeyJSON(automdm.MaskedPassword))}},
			{"stored values sent again", automdm.GoogleCASCAUpdatePayload{
				Name: new("GOOGLE_WIFI"), Project: new(" my-project "), Location: new("us-central1"), CAPool: new("my-ca-pool"),
				APIKeyJSON: new(automdm.GoogleCASKeyJSON(reformattedKey.String())),
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc, ds, googleCAS, ctx := newGoogleCASTestService(t, stored())
				require.NoError(t, svc.UpdateCertificateAuthority(ctx, 1, update(tc.payload)))
				require.False(t, googleCAS.VerifyCAPoolFuncInvoked)
				require.False(t, ds.UpdateCertificateAuthorityByIDFuncInvoked)
			})
		}
	})

	t.Run("validation", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			payload automdm.GoogleCASCAUpdatePayload
			wantErr string
		}{
			{"bad name", automdm.GoogleCASCAUpdatePayload{Name: new("NDES")}, "CA name cannot be NDES"},
			{"empty project", automdm.GoogleCASCAUpdatePayload{Project: new("")}, "The Google Cloud project ID cannot be empty."},
			{"bad key", automdm.GoogleCASCAUpdatePayload{APIKeyJSON: new(automdm.GoogleCASKeyJSON("{}"))}, `"type" set to "service_account"`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc, ds, _, ctx := newGoogleCASTestService(t, stored())
				err := svc.UpdateCertificateAuthority(ctx, 1, update(tc.payload))
				require.ErrorContains(t, err, "Couldn't edit certificate authority. ")
				require.ErrorContains(t, err, tc.wantErr)
				require.False(t, ds.UpdateCertificateAuthorityByIDFuncInvoked)
			})
		}
	})

	t.Run("type cannot change", func(t *testing.T) {
		hydrant := stored()
		hydrant.Type = string(automdm.CATypeHydrant)
		svc, _, _, ctx := newGoogleCASTestService(t, hydrant)
		err := svc.UpdateCertificateAuthority(ctx, 1, update(automdm.GoogleCASCAUpdatePayload{Name: new("GOOGLE_VPN")}))
		require.EqualError(t, err, "Couldn't edit certificate authority. The certificate authority types must be the same.")
	})
}

func TestProcessGoogleCASCAs(t *testing.T) {
	t.Parallel()
	svc, _, googleCAS, ctx := newGoogleCASTestService(t, nil)

	existing := []automdm.GoogleCASCA{*goodGoogleCAS(), {Name: "GONE", Project: "p", Location: "l", CAPool: "c", APIKeyJSON: testGoogleCASKey}}
	changed := *goodGoogleCAS()
	changed.CAPool = "renewed-pool"
	added := automdm.GoogleCASCA{Name: "NEW", Project: "p", Location: "l", CAPool: "c", APIKeyJSON: testGoogleCASKey}

	var verified []string
	googleCAS.VerifyCAPoolFunc = func(ctx context.Context, ca automdm.GoogleCASCA) error {
		verified = append(verified, ca.Name)
		return nil
	}
	// An unchanged CA is neither verified nor updated, so GitOps records no edited activity for it.
	unchanged := existing[0]
	unchanged.ID = 0 // incoming CAs from YAML have no ID
	ops := &automdm.CertificateAuthoritiesBatchOperations{}
	require.NoError(t, svc.processGoogleCASCAs(ctx, ops, []automdm.GoogleCASCA{unchanged, added}, existing))
	require.Equal(t, []string{"NEW"}, verified)
	require.Empty(t, ops.Update)

	verified = nil
	ops = &automdm.CertificateAuthoritiesBatchOperations{}
	require.NoError(t, svc.processGoogleCASCAs(ctx, ops, []automdm.GoogleCASCA{changed, added}, existing))
	require.ElementsMatch(t, []string{"GOOGLE_WIFI", "NEW"}, verified)

	names := func(cas []*automdm.CertificateAuthority) []string {
		var out []string
		for _, ca := range cas {
			require.Equal(t, string(automdm.CATypeGoogleCAS), ca.Type)
			require.Empty(t, *ca.URL)
			out = append(out, *ca.Name)
		}
		return out
	}
	require.Equal(t, []string{"NEW"}, names(ops.Add))
	require.Equal(t, []string{"GOOGLE_WIFI"}, names(ops.Update))
	require.Equal(t, "renewed-pool", *ops.Update[0].GoogleCASCAPool)
	require.Equal(t, []string{"GONE"}, names(ops.Delete))

	// A CA that fails validation stops the batch, and a masked key is refused in YAML since the full key is the intended input.
	for key, wantErr := range map[string]string{
		"nope":                 "certificate_authorities.google_cas: The service account key must be a JSON object.",
		automdm.MaskedPassword: "certificate_authorities.google_cas: The service account key must be provided in full, not masked.",
	} {
		bad := added
		bad.APIKeyJSON = automdm.GoogleCASKeyJSON(key)
		err := svc.processGoogleCASCAs(ctx, &automdm.CertificateAuthoritiesBatchOperations{}, []automdm.GoogleCASCA{bad}, nil)
		require.ErrorContains(t, err, wantErr)
	}
}
