package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anilyagiz/automdm/v4/pkg/automdmhttp"
	"github.com/anilyagiz/automdm/v4/pkg/mdm/profilevalidation"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	platform_errors "github.com/anilyagiz/automdm/v4/server/platform/errors"
	"golang.org/x/sync/singleflight"
)

const (
	autofillConfigProfileDefaultBaseURL = "https://automdm.com"
	// Lets developers and QA point AutoMDM at a local or preview website.
	autofillConfigProfileBaseURLEnvVar = "AUTOMDM_DEV_AI_AUTOFILL_BASE_URL"

	autofillConfigProfileRegisterPath        = "/api/profile-generator/v1/register"
	autofillConfigProfileNameDescriptionPath = "/api/profile-generator/v1/name-and-description"
	autofillConfigProfileContentPath         = "/api/profile-generator/v1/profile"

	// Generated profiles carry ADMX templates of several hundred KB, so this is well above any real response.
	autofillConfigProfileMaxResponseBytes = 10 << 20

	// The Windows checks need a name, and the name the admin picks is checked again at save.
	autofillWindowsProfileCheckName = "AI generated profile"
)

// Just inside the UI's 10-second cap, for registering and generating together.  A var so tests can shorten it.
var autofillConfigProfileTimeout = 9500 * time.Millisecond

// Registering is a database write on automdm.com, so it gets a small part of the budget.
const autofillConfigProfileRegistrationTimeout = 3 * time.Second

////////////////////////////////////////////////////////////////////////////////
// Autofill configuration profile name and description
////////////////////////////////////////////////////////////////////////////////

type autofillConfigProfileNameDescriptionRequest struct {
	NaturalLanguageInstructions string `json:"natural_language_instructions"`
	Platform                    string `json:"platform"`
}

type autofillConfigProfileNameDescriptionResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Err         error  `json:"error,omitempty"`
}

func (r autofillConfigProfileNameDescriptionResponse) Error() error { return r.Err }

func autofillConfigProfileNameDescriptionEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*autofillConfigProfileNameDescriptionRequest)
	name, description, err := svc.AutofillConfigProfileNameDescription(ctx, req.NaturalLanguageInstructions, req.Platform)
	if err != nil {
		return autofillConfigProfileNameDescriptionResponse{Err: err}, nil
	}
	return autofillConfigProfileNameDescriptionResponse{Name: name, Description: description}, nil
}

func (svc *Service) AutofillConfigProfileNameDescription(ctx context.Context, instructions, platform string) (string, string, error) {
	if err := svc.authorizeConfigProfileWriteOnAnyTeam(ctx); err != nil {
		return "", "", err
	}

	instructions, err := svc.validateAutofillConfigProfileRequest(ctx, instructions, platform, automdm.AutofillConfigProfileNameDescriptionErrorMessage)
	if err != nil {
		return "", "", err
	}

	callCtx, cancel := context.WithTimeout(ctx, autofillConfigProfileTimeout)
	defer cancel()
	credential, err := svc.profileGeneratorCredential(callCtx, automdm.AutofillConfigProfileNameDescriptionErrorMessage)
	if err != nil {
		return "", "", err
	}

	status, body, err := postToConfigProfileGenerator(callCtx, autofillConfigProfileNameDescriptionPath, instructions, platform, credential)
	if err != nil {
		return "", "", autofillUpstreamError(ctx, automdm.AutofillConfigProfileNameDescriptionErrorMessage, err)
	}
	if limitErr := autofillLimitError(ctx, automdm.AutofillConfigProfileNameDescriptionErrorMessage, status, body, time.Now()); limitErr != nil {
		return "", "", limitErr
	}
	if status != http.StatusOK {
		return "", "", autofillUnavailableError(ctx, automdm.AutofillConfigProfileNameDescriptionErrorMessage, status)
	}

	var result struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Name == "" {
		return "", "", autofillUnreadableError(ctx, automdm.AutofillConfigProfileNameDescriptionErrorMessage, err)
	}
	return result.Name, result.Description, nil
}

////////////////////////////////////////////////////////////////////////////////
// Autofill configuration profile content
////////////////////////////////////////////////////////////////////////////////

type autofillConfigProfileContentRequest struct {
	NaturalLanguageInstructions string `json:"natural_language_instructions"`
	Platform                    string `json:"platform"`
	AutoMDMID                   uint   `json:"team_id" premium:"true"`
}

type autofillConfigProfileContentResponse struct {
	*automdm.AutofilledConfigProfile
	Err error `json:"error,omitempty"`
}

func (r autofillConfigProfileContentResponse) Error() error { return r.Err }

func autofillConfigProfileContentEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*autofillConfigProfileContentRequest)
	profile, err := svc.AutofillConfigProfileContent(ctx, req.NaturalLanguageInstructions, req.Platform, req.AutoMDMID)
	if err != nil {
		return autofillConfigProfileContentResponse{Err: err}, nil
	}
	return autofillConfigProfileContentResponse{AutofilledConfigProfile: profile}, nil
}

func (svc *Service) AutofillConfigProfileContent(ctx context.Context, instructions, platform string, teamID uint) (*automdm.AutofilledConfigProfile, error) {
	if err := svc.authz.Authorize(ctx, &automdm.MDMConfigProfileAuthz{TeamID: &teamID}, automdm.ActionWrite); err != nil {
		return nil, ctxerr.Wrap(ctx, err)
	}

	instructions, err := svc.validateAutofillConfigProfileRequest(ctx, instructions, platform, automdm.AutofillConfigProfileContentErrorMessage)
	if err != nil {
		return nil, err
	}

	if teamID > 0 {
		// the request decoder checks the license if "team_id" is set
		if _, err := svc.EnterpriseOverrides.TeamByIDOrName(ctx, &teamID, nil); err != nil {
			return nil, ctxerr.Wrap(ctx, err, "get team")
		}
	}

	// Only the calls to automdm.com share the budget, not the checks that run on the profile afterward.
	callCtx, cancel := context.WithTimeout(ctx, autofillConfigProfileTimeout)
	defer cancel()
	credential, err := svc.profileGeneratorCredential(callCtx, automdm.AutofillConfigProfileContentErrorMessage)
	if err != nil {
		return nil, err
	}

	status, body, err := postToConfigProfileGenerator(callCtx, autofillConfigProfileContentPath, instructions, platform, credential)
	if err != nil {
		return nil, autofillUpstreamError(ctx, automdm.AutofillConfigProfileContentErrorMessage, err)
	}
	if limitErr := autofillLimitError(ctx, automdm.AutofillConfigProfileContentErrorMessage, status, body, time.Now()); limitErr != nil {
		return nil, limitErr
	}

	if status == http.StatusBadRequest {
		var decline struct {
			Error  string `json:"error"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(body, &decline) == nil && decline.Error == "couldNotGenerateProfile" {
			reason := decline.Reason
			if reason == "" {
				reason = "The AI couldn't generate a configuration profile from these instructions."
			}
			return nil, ctxerr.Wrap(ctx, &automdm.AutofillConfigProfileError{
				Message: automdm.AutofillConfigProfileContentErrorMessage,
				Field:   "natural_language_instructions",
				Reason:  reason,
			}, "generator declined")
		}
	}
	if status != http.StatusOK {
		return nil, autofillUnavailableError(ctx, automdm.AutofillConfigProfileContentErrorMessage, status)
	}

	var generated struct {
		ProfileType        string            `json:"profileType"`
		Profile            string            `json:"profile"`
		DeliveryNotes      string            `json:"deliveryNotes"`
		AdditionalProfiles []json.RawMessage `json:"additionalProfiles"`
	}
	if err := json.Unmarshal(body, &generated); err != nil || generated.Profile == "" {
		return nil, autofillUnreadableError(ctx, automdm.AutofillConfigProfileContentErrorMessage, err)
	}

	if len(generated.AdditionalProfiles) > 0 {
		return nil, ctxerr.Wrap(ctx, &automdm.AutofillConfigProfileError{
			Message: automdm.AutofillConfigProfileContentErrorMessage,
			Field:   "natural_language_instructions",
			Reason:  "This configuration profile needs more than one file. Create it manually instead.",
		}, "generator returned additional profiles")
	}

	result := &automdm.AutofilledConfigProfile{
		Content:       generated.Profile,
		DeliveryNotes: generated.DeliveryNotes,
	}
	data := []byte(generated.Profile)
	// Generated output is always checked strictly, regardless of the setting, and
	// never leniently, so the toggle hint can't appear.
	strict := new(profilevalidation.ModeStrict)
	switch {
	case generated.ProfileType == "mobileconfig" && platform == automdm.AutofillConfigProfilePlatformApple:
		result.Type = automdm.AutofillConfigProfileTypeMobileconfig
		var cp *automdm.MDMAppleConfigProfile
		cp, _, _, err = svc.parseAndValidateAppleConfigProfile(ctx, teamID, "", data, nil, automdm.LabelsIncludeAll, nil, strict)
		if err == nil {
			result.Name = &cp.Name
		}
	case generated.ProfileType == "ddm" && platform == automdm.AutofillConfigProfilePlatformApple:
		result.Type = automdm.AutofillConfigProfileTypeDeclaration
		_, _, _, err = svc.parseAndValidateAppleDeclaration(ctx, teamID, "", data, nil, automdm.LabelsIncludeAll, nil, strict)
	case generated.ProfileType == "csp" && platform == automdm.AutofillConfigProfilePlatformWindows:
		result.Type = automdm.AutofillConfigProfileTypeWindows
		_, _, _, err = svc.parseAndValidateWindowsConfigProfile(ctx, teamID, autofillWindowsProfileCheckName, data, nil, automdm.LabelsIncludeAll, nil, "Couldn't add. ", strict)
	default:
		return nil, autofillUnreadableError(ctx, automdm.AutofillConfigProfileContentErrorMessage,
			errors.New("unexpected profile type "+generated.ProfileType+" for platform "+platform))
	}
	if pvErr, ok := errors.AsType[*profileValidationError](err); ok {
		// "base" like an AI service failure: the model wrote the bad profile, so the
		// decline banner under natural_language_instructions would blame the admin.
		reasons := pvErr.sentences()
		return nil, ctxerr.Wrap(ctx, &automdm.AutofillConfigProfileError{
			Message:     automdm.AutofillConfigProfileContentErrorMessage,
			Field:       "base",
			Reason:      reasons[0],
			MoreReasons: reasons[1:],
		}, "generated profile failed schema validation")
	}
	if err != nil {
		return nil, autofillCheckError(ctx, err)
	}

	// Usage statistics only: the admin still gets the profile.
	if err := svc.ds.IncrementAIGeneratedProfileCount(ctx, platform); err != nil {
		svc.logger.ErrorContext(ctx, "failed to count AI generated configuration profile", "err", err, "platform", platform)
	}

	return result, nil
}

////////////////////////////////////////////////////////////////////////////////
// Helpers
////////////////////////////////////////////////////////////////////////////////

// authorizeConfigProfileWriteOnAnyTeam mirrors AutofillPolicySql: the request names no team, so a user who can
// write profiles on any team may ask.
func (svc *Service) authorizeConfigProfileWriteOnAnyTeam(ctx context.Context) error {
	vc, ok := viewer.FromContext(ctx)
	if !ok {
		svc.authz.SkipAuthorization(ctx)
		return automdm.ErrNoContext
	}

	if vc.User.GlobalRole != nil || len(vc.User.Teams) == 0 {
		return ctxerr.Wrap(ctx, svc.authz.Authorize(ctx, &automdm.MDMConfigProfileAuthz{}, automdm.ActionWrite))
	}
	teamID := vc.User.Teams[0].Team.ID
	for _, teamUser := range vc.User.Teams {
		if teamUser.Role == automdm.RoleAdmin || teamUser.Role == automdm.RoleMaintainer || teamUser.Role == automdm.RoleGitOps {
			teamID = teamUser.Team.ID
			break
		}
	}
	return ctxerr.Wrap(ctx, svc.authz.Authorize(ctx, &automdm.MDMConfigProfileAuthz{TeamID: &teamID}, automdm.ActionWrite))
}

// validateAutofillConfigProfileRequest returns the trimmed instructions, or the error that stops the request
// before anything is sent to automdm.com.
func (svc *Service) validateAutofillConfigProfileRequest(ctx context.Context, instructions, platform, errMessage string) (string, error) {
	appConfig, err := svc.ds.AppConfig(ctx)
	if err != nil {
		return "", ctxerr.Wrap(ctx, err, "get app config")
	}
	if appConfig.ServerSettings.AIFeaturesDisabled {
		return "", ctxerr.Wrap(ctx, &automdm.BadRequestError{Message: "AI features are disabled (server_settings.ai_features_disabled)"})
	}

	var mdmEnabled bool
	var mdmOffMessage string
	switch platform {
	case automdm.AutofillConfigProfilePlatformApple:
		mdmEnabled, mdmOffMessage = appConfig.MDM.EnabledAndConfigured, automdm.AppleMDMNotConfiguredMessage
	case automdm.AutofillConfigProfilePlatformWindows:
		mdmEnabled, mdmOffMessage = appConfig.MDM.WindowsEnabledAndConfigured, automdm.WindowsMDMNotConfiguredMessage
	default:
		return "", ctxerr.Wrap(ctx, &automdm.BadRequestError{Message: `'platform' must be "apple" or "windows"`})
	}

	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return "", ctxerr.Wrap(ctx, &automdm.BadRequestError{Message: "'natural_language_instructions' cannot be empty"})
	}
	if utf8.RuneCountInString(instructions) > automdm.MaxAutofillConfigProfileInstructionsLength {
		return "", ctxerr.Wrap(ctx, &automdm.BadRequestError{Message: "'natural_language_instructions' must be 2,000 characters or fewer"})
	}

	// Checked before calling automdm.com, since the profile checks would fail anyway and the generation takes
	// up to 10 seconds.
	if !mdmEnabled {
		return "", ctxerr.Wrap(ctx, &automdm.AutofillConfigProfileError{
			Message: errMessage,
			Field:   "base",
			Reason:  mdmOffMessage,
		}, "platform MDM is off")
	}

	return instructions, nil
}

// autofillConfigProfileBaseURL is automdm.com unless dev mode overrides it.
func autofillConfigProfileBaseURL() string {
	if baseURL := dev_mode.Env(autofillConfigProfileBaseURLEnvVar); baseURL != "" {
		return strings.TrimSuffix(baseURL, "/")
	}
	return autofillConfigProfileDefaultBaseURL
}

// postToConfigProfileGenerator sends the instructions to automdm.com and returns the status and body.  It uses
// the request's context, so a client that disconnects cancels the call.
func postToConfigProfileGenerator(ctx context.Context, path, instructions, platform string, credential profileGeneratorCredential) (int, []byte, error) {
	reqBody := map[string]any{
		"naturalLanguageInstructions": instructions,
		"platform":                    platform,
	}
	if credential.licenseKey != "" {
		reqBody["automdmLicenseKey"] = credential.licenseKey
	}
	return postJSONToConfigProfileGenerator(ctx, path, reqBody, credential.secret)
}

func postJSONToConfigProfileGenerator(ctx context.Context, path string, reqBody map[string]any, bearerToken string) (int, []byte, error) {
	encodedBody, err := json.Marshal(reqBody)
	if err != nil {
		return 0, nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, autofillConfigProfileTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, autofillConfigProfileBaseURL()+path, bytes.NewReader(encodedBody))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := automdmhttp.NewClient().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, autofillConfigProfileMaxResponseBytes))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// profileGeneratorCredential is what AutoMDM authenticates to the automdm.com configuration profile generator with:
// its license key, or the secret it got by registering.
type profileGeneratorCredential struct {
	licenseKey string
	secret     string
}

// Concurrent first requests in one process share a registration: the UI asks for a name and a profile at once.
var profileGeneratorRegistrations singleflight.Group

// profileGeneratorCredential returns the license key for an unexpired Premium license, and otherwise the stored
// registration secret, registering first if there is none.
func (svc *Service) profileGeneratorCredential(ctx context.Context, errMessage string) (profileGeneratorCredential, error) {
	// automdm.com treats an expired license as AutoMDM Free, but IsPremium is still true for one.
	if lic, ok := license.FromContext(ctx); ok {
		if licInfo, ok := lic.(*automdm.LicenseInfo); ok && licInfo.IsPremium() && !licInfo.IsExpired() && svc.config.License.Key != "" {
			return profileGeneratorCredential{licenseKey: svc.config.License.Key}, nil
		}
	}

	// The secret is stored encrypted with the server private key.
	privateKey := svc.config.Server.PrivateKey
	if testSetEmptyPrivateKey {
		privateKey = ""
	}
	if privateKey == "" {
		return profileGeneratorCredential{}, ctxerr.Wrap(ctx, &automdm.AutofillConfigProfileError{
			Message: errMessage,
			Field:   "base",
			Reason:  automdm.AutofillConfigProfileMissingPrivateKeyReason,
		}, "no server private key to store the profile generator secret")
	}

	secret, err := svc.storedProfileGeneratorSecret(ctx)
	if err != nil {
		return profileGeneratorCredential{}, err
	}
	if secret != "" {
		return profileGeneratorCredential{secret: secret}, nil
	}

	// Not tied to one request's cancellation, since the requests sharing it would all fail with it, but each
	// request stops waiting when its own context ends.
	registration := profileGeneratorRegistrations.DoChan("register", func() (any, error) {
		registerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), autofillConfigProfileRegistrationTimeout)
		defer cancel()
		return svc.registerWithProfileGenerator(registerCtx)
	})
	var result any
	select {
	case res := <-registration:
		result, err = res.Val, res.Err
	case <-ctx.Done():
		return profileGeneratorCredential{}, autofillUpstreamError(ctx, errMessage, ctx.Err())
	}
	if err != nil {
		if regErr, ok := errors.AsType[*profileGeneratorRegistrationError](err); ok {
			switch {
			case regErr.status != 0:
				return profileGeneratorCredential{}, autofillUnavailableError(ctx, errMessage, regErr.status)
			case regErr.unreadable:
				return profileGeneratorCredential{}, autofillUnreadableError(ctx, errMessage, regErr.err)
			default:
				return profileGeneratorCredential{}, autofillUpstreamError(ctx, errMessage, regErr.err)
			}
		}
		return profileGeneratorCredential{}, err
	}
	return profileGeneratorCredential{secret: result.(string)}, nil
}

func (svc *Service) storedProfileGeneratorSecret(ctx context.Context) (string, error) {
	assets, err := svc.ds.GetAllMDMConfigAssetsByName(ctx, []automdm.MDMAssetName{automdm.MDMAssetProfileGeneratorAutoMDMServerSecret}, nil)
	if err != nil {
		if automdm.IsNotFound(err) {
			return "", nil
		}
		return "", ctxerr.Wrap(ctx, err, "get profile generator secret")
	}
	return string(assets[automdm.MDMAssetProfileGeneratorAutoMDMServerSecret].Value), nil
}

// profileGeneratorRegistrationError is a failed registration, mapped to an autofill error by each request that
// shared it, since each reports it under its own message.
type profileGeneratorRegistrationError struct {
	status     int
	unreadable bool
	err        error
}

func (e *profileGeneratorRegistrationError) Error() string {
	if e.status != 0 {
		return "automdm.com returned " + http.StatusText(e.status) + " to registration"
	}
	return "registering with automdm.com: " + e.err.Error()
}

// registerWithProfileGenerator registers this AutoMDM server with the automdm.com configuration profile generator
// and stores the secret it returns.  A rejected secret isn't registered again.
func (svc *Service) registerWithProfileGenerator(ctx context.Context) (string, error) {
	// Another request may have stored a secret since this one looked.
	secret, err := svc.storedProfileGeneratorSecret(ctx)
	if err != nil || secret != "" {
		return secret, err
	}

	appConfig, err := svc.ds.AppConfig(ctx)
	if err != nil {
		return "", ctxerr.Wrap(ctx, err, "get app config")
	}
	status, body, err := postJSONToConfigProfileGenerator(ctx, autofillConfigProfileRegisterPath,
		map[string]any{"automdmServerUrl": appConfig.ServerSettings.ServerURL}, "")
	if err != nil {
		return "", &profileGeneratorRegistrationError{err: err}
	}
	if status != http.StatusOK {
		return "", &profileGeneratorRegistrationError{status: status}
	}
	var registration struct {
		AutoMDMServerSecret string `json:"automdmServerSecret"`
	}
	if err := json.Unmarshal(body, &registration); err != nil || registration.AutoMDMServerSecret == "" {
		return "", &profileGeneratorRegistrationError{unreadable: true, err: err}
	}

	if err := svc.ds.InsertOrReplaceMDMConfigAsset(ctx, automdm.MDMConfigAsset{
		Name:  automdm.MDMAssetProfileGeneratorAutoMDMServerSecret,
		Value: []byte(registration.AutoMDMServerSecret),
	}); err != nil {
		return "", ctxerr.Wrap(ctx, err, "store profile generator secret")
	}
	svc.logger.InfoContext(ctx, "registered with the automdm.com configuration profile generator")
	return registration.AutoMDMServerSecret, nil
}

// autofillLimitError reports automdm.com's AutoMDM Free limit as the endpoint's own 429, or returns nil for any
// other response.
func autofillLimitError(ctx context.Context, message string, status int, body []byte, now time.Time) error {
	if status != http.StatusTooManyRequests {
		return nil
	}
	var limit struct {
		Error    string    `json:"error"`
		ResetsAt time.Time `json:"resetsAt"`
	}
	if json.Unmarshal(body, &limit) != nil || limit.Error != "generationLimitReached" {
		return nil
	}
	// The limit resets on the 1st of the month (UTC), the fallback when automdm.com sends no date.
	resetsAt := limit.ResetsAt
	if resetsAt.IsZero() {
		nowUTC := now.UTC()
		resetsAt = time.Date(nowUTC.Year(), nowUTC.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
	// A reset that has already passed, from clock skew or a response that arrived late, means try again now.
	retryAfter := max(int(math.Ceil(resetsAt.Sub(now).Seconds())), 1)
	return ctxerr.Wrap(ctx, &automdm.AutofillConfigProfileError{
		Message:           message,
		Field:             "base",
		Reason:            automdm.AutofillConfigProfileLimitReachedReason,
		Status:            http.StatusTooManyRequests,
		RetryAfterSeconds: retryAfter,
	}, "profile generator limit reached")
}

func autofillUpstreamError(ctx context.Context, message string, err error) error {
	// The admin left: return the cancellation itself rather than blaming the AI service.
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return ctxerr.Wrap(ctx, ctx.Err(), "autofill request canceled")
	}
	reason := "The AI service couldn't be reached."
	if errors.Is(err, context.DeadlineExceeded) {
		reason = "The AI service timed out."
	}
	return ctxerr.Wrap(ctx, &automdm.AutofillConfigProfileError{
		Message:     message,
		Field:       "base",
		Reason:      reason,
		InternalErr: err,
	}, "calling automdm.com")
}

func autofillUnavailableError(ctx context.Context, message string, status int) error {
	return ctxerr.Wrap(ctx, &automdm.AutofillConfigProfileError{
		Message:     message,
		Field:       "base",
		Reason:      "The AI service is unavailable.",
		InternalErr: errors.New("automdm.com returned " + http.StatusText(status)),
	}, "calling automdm.com")
}

func autofillUnreadableError(ctx context.Context, message string, err error) error {
	if err == nil {
		err = errors.New("response is missing a required field")
	}
	return ctxerr.Wrap(ctx, &automdm.AutofillConfigProfileError{
		Message:     message,
		Field:       "base",
		Reason:      "The AI service returned a response AutoMDM couldn't read.",
		InternalErr: err,
	}, "reading automdm.com response")
}

// autofillCheckError reports a profile the add checks reject the way the autofill API documents it, with the
// check's own message.  Anything that isn't the client's fault, such as a database error, is returned as-is.
func autofillCheckError(ctx context.Context, err error) error {
	cause := ctxerr.Cause(err)
	var clientErr platform_errors.ErrWithIsClientError
	if !errors.As(cause, &clientErr) || !clientErr.IsClientError() {
		return err
	}

	reason := cause.Error()
	var invalid interface{ Invalid() []map[string]string }
	if errors.As(cause, &invalid) && len(invalid.Invalid()) > 0 {
		reason = invalid.Invalid()[0]["reason"]
	}
	return ctxerr.Wrap(ctx, &automdm.AutofillConfigProfileError{
		Message: automdm.AutofillConfigProfileContentErrorMessage,
		Field:   "natural_language_instructions",
		Reason:  reason,
	}, "generated profile failed checks")
}
