package android

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	licensectx "github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	"github.com/anilyagiz/automdm/v4/server/mdm/android/service/androidmgmt"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"google.golang.org/api/androidmanagement/v1"
)

var _ android.Service = (*Service)(nil)

// zeroTouchTokenDuration is ~100 years in seconds.
const zeroTouchTokenDuration = "31536000000s"

// Service wraps a core android.Service with premium feature implementations.
type Service struct {
	android.Service
	ds        automdm.AndroidDatastore
	automdmDS automdm.Datastore
	apiClient androidmgmt.Client
	authz     *authz.Authorizer
	logger    *slog.Logger
}

func NewService(
	svc android.Service,
	ds automdm.AndroidDatastore,
	automdmDS automdm.Datastore,
	apiClient androidmgmt.Client,
	logger *slog.Logger,
) (*Service, error) {
	authorizer, err := authz.NewAuthorizer()
	if err != nil {
		return nil, fmt.Errorf("new authorizer: %w", err)
	}
	return &Service{
		Service:   svc,
		ds:        ds,
		automdmDS: automdmDS,
		apiClient: apiClient,
		authz:     authorizer,
		logger:    logger,
	}, nil
}

// teamEnrollmentRequest is the payload embedded in AMAPI enrollment token additionalData.
type teamEnrollmentRequest struct {
	TeamID *uint `json:"team_id"`
}

func (svc *Service) GetZeroTouchConfiguration(ctx context.Context, teamID *uint) (*android.ZeroTouchConfigurationResponse, error) {
	if !licensectx.IsPremium(ctx) {
		svc.authz.SkipAuthorization(ctx)
		return nil, automdm.ErrMissingLicense
	}

	if err := svc.authz.Authorize(ctx, &android.Enterprise{}, automdm.ActionWrite); err != nil {
		return nil, err
	}

	// Android MDM must be configured
	appConfig, err := svc.ds.AppConfig(ctx)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "getting app config")
	}
	if !appConfig.MDM.AndroidEnabledAndConfigured {
		return nil, automdm.NewInvalidArgumentError("android",
			"Android MDM is NOT configured").WithStatus(http.StatusNotFound)
	}

	// 0 means Unassigned; storing it as team_id would violate the teams foreign key.
	if teamID != nil && *teamID == 0 {
		teamID = nil
	}
	if teamID != nil {
		exists, err := svc.automdmDS.TeamExists(ctx, *teamID)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "checking if team exists")
		}
		if !exists {
			return nil, automdm.NewInvalidArgumentError("team_id", fmt.Sprintf("team %d does not exist", *teamID)).
				WithStatus(http.StatusNotFound)
		}
	}

	ctx = ctxdb.RequirePrimary(ctx, true)

	// Check if a token already exists
	existing, err := svc.ds.GetZeroTouchEnrollmentToken(ctx, teamID)
	if err != nil && !automdm.IsNotFound(err) {
		return nil, ctxerr.Wrap(ctx, err, "getting existing zero-touch token")
	}
	if existing != nil && existing.UsesAutoMDMPolicy {
		return buildDPCExtrasResponse(existing), nil
	}

	// A team without a token falls back to the global default policy rather than having none; the next
	// view replaces it once the team's policy can be created. An existing token is simply kept.
	token, err := svc.newZeroTouchToken(ctx, teamID, existing == nil)
	if err != nil {
		if existing == nil {
			return nil, err
		}
		svc.logger.WarnContext(ctx, "failed to replace the zero-touch token created against the global default policy",
			"team_id", ptr.ValOrZero(teamID), "err", err)
		return buildDPCExtrasResponse(existing), nil
	}

	if existing == nil {
		token, err = svc.ds.CreateZeroTouchEnrollmentToken(ctx, token)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "persisting zero-touch enrollment token")
		}
		return buildDPCExtrasResponse(token), nil
	}

	// The old token isn't deleted from AMAPI, so devices already set up with its DPC extras in the
	// zero-touch portal keep enrolling; they just get the passcode after setup instead of during it.
	replaced, err := svc.ds.ReplaceZeroTouchEnrollmentToken(ctx, existing.TokenName, token)
	if err != nil {
		svc.logger.WarnContext(ctx, "failed to save the zero-touch token that replaces the one created against the global default policy",
			"team_id", ptr.ValOrZero(teamID), "err", err)
		return buildDPCExtrasResponse(existing), nil
	}
	return buildDPCExtrasResponse(replaced), nil
}

// newZeroTouchToken creates a zero-touch enrollment token in AMAPI that points at the team's default policy,
// or, if that policy can't be created and allowGlobalPolicy is set, at the global default policy.
func (svc *Service) newZeroTouchToken(ctx context.Context, teamID *uint, allowGlobalPolicy bool) (*android.ZeroTouchToken, error) {
	enterprise, err := svc.ds.GetEnterprise(ctx)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "getting enterprise")
	}

	secret, err := svc.getClientAuthenticationSecret(ctx)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "getting client authentication secret")
	}
	_ = svc.apiClient.SetAuthenticationSecret(secret)

	var policyTeamID uint
	if teamID != nil {
		policyTeamID = *teamID
	}
	usesAutoMDMPolicy := true
	policyName, err := svc.EnsureAutoMDMDefaultPolicy(ctx, enterprise.Name(), policyTeamID)
	if err != nil {
		if !allowGlobalPolicy {
			return nil, ctxerr.Wrap(ctx, err, "ensuring the team's default policy")
		}
		svc.logger.ErrorContext(ctx, "failed to ensure the team's android default policy, creating the zero-touch token on the global one",
			"team_id", policyTeamID, "err", err)
		usesAutoMDMPolicy = false
		policyName = fmt.Sprintf("%s/policies/%d", enterprise.Name(), android.DefaultAndroidPolicyID)
	}

	additionalData, err := json.Marshal(teamEnrollmentRequest{
		TeamID: teamID,
	})
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "marshalling zero-touch additional data")
	}

	// Zero-touch intentionally bypasses end-user authentication (RequiresEnrollOTAAuthentication)
	// because there is no browser-based IdP flow during zero-touch provisioning.
	amapiToken := &androidmanagement.EnrollmentToken{
		Duration:           zeroTouchTokenDuration,
		AdditionalData:     string(additionalData),
		AllowPersonalUsage: "PERSONAL_USAGE_DISALLOWED",
		PolicyName:         policyName,
		OneTimeOnly:        false,
	}
	amapiToken, err = svc.apiClient.EnterprisesEnrollmentTokensCreate(ctx, enterprise.Name(), amapiToken)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "creating zero-touch enrollment token via AMAPI")
	}

	expiresAt, err := time.Parse(time.RFC3339, amapiToken.ExpirationTimestamp)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "parsing token expiration timestamp")
	}

	return &android.ZeroTouchToken{
		TeamID:            teamID,
		TokenName:         amapiToken.Name,
		TokenValue:        amapiToken.Value,
		ExpiresAt:         expiresAt,
		UsesAutoMDMPolicy: usesAutoMDMPolicy,
	}, nil
}

func (svc *Service) getClientAuthenticationSecret(ctx context.Context) (string, error) {
	assets, err := svc.ds.GetAllMDMConfigAssetsByName(ctx, []automdm.MDMAssetName{automdm.MDMAssetAndroidAutoMDMServerSecret}, nil)
	switch {
	case automdm.IsNotFound(err):
		return "", nil
	case err != nil:
		return "", ctxerr.Wrap(ctx, err, "getting Android authentication secret")
	}
	return string(assets[automdm.MDMAssetAndroidAutoMDMServerSecret].Value), nil
}

type dpcExtras struct {
	AdminExtrasBundle adminExtrasBundle `json:"android.app.extra.PROVISIONING_ADMIN_EXTRAS_BUNDLE"`
}

type adminExtrasBundle struct {
	EnrollmentToken string `json:"com.google.android.apps.work.clouddpc.EXTRA_ENROLLMENT_TOKEN"`
}

func buildDPCExtrasResponse(token *android.ZeroTouchToken) *android.ZeroTouchConfigurationResponse {
	raw, _ := json.Marshal(dpcExtras{
		AdminExtrasBundle: adminExtrasBundle{
			EnrollmentToken: token.TokenValue,
		},
	})
	return &android.ZeroTouchConfigurationResponse{
		DPCExtras: raw,
	}
}
