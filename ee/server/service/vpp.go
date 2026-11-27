package service

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	"github.com/anilyagiz/automdm/v4/server/mdm/android/service/androidmgmt"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/apple_apps"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/vpp"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
	"google.golang.org/api/androidmanagement/v1"
)

// Used for overriding the env var value in testing
var testSetEmptyPrivateKey bool

// vppTokenInfo bundles the encoded VPP token bytes and the country code of
// the storefront associated with the token, for callers that need both.
type vppTokenInfo struct {
	Secret  string
	Country string
}

// vppAddAnchor describes which token + storefront should be used to fetch
// metadata for an add-app operation, and whether the (adam_id, platform) row
// should be re-anchored to the adding team's country. anchorCountry is the
// country to write into vpp_apps.country_code when this is a first-add or a
// re-anchor; reAnchor is true when the row exists but had no eligible token
// in its previous anchored country.
type vppAddAnchor struct {
	region        string
	fetchSecret   string
	anchorCountry string
	reAnchor      bool
}

// resolveAddAnchor implements the first-add / subsequent-add / re-anchor
// decision tree from the design plan. The adding team's token+country is the
// fallback; if a vpp_apps row already exists and AutoMDM still has a token
// owning it in the anchored country, that token is used instead.
func (svc *Service) resolveAddAnchor(ctx context.Context, adamID string, platform automdm.InstallableDevicePlatform, addingTeam vppTokenInfo) (vppAddAnchor, error) {
	if addingTeam.Country == "" {
		return vppAddAnchor{}, automdm.NewInvalidArgumentError("vpp_token",
			"Could not determine the storefront country for the team's VPP token. Try renewing the token in Settings > Integrations > Apple Business.")
	}

	existing, err := svc.ds.GetVPPAppByAdamIDPlatform(ctx, adamID, platform)
	if err != nil {
		if automdm.IsNotFound(err) {
			// First add: anchor to adding team's country.
			return vppAddAnchor{
				region:        addingTeam.Country,
				fetchSecret:   addingTeam.Secret,
				anchorCountry: addingTeam.Country,
				reAnchor:      false,
			}, nil
		}
		return vppAddAnchor{}, ctxerr.Wrap(ctx, err, "looking up existing vpp app for anchor")
	}

	anchored := existing.CountryCode
	if anchored == "" {
		// Row exists but predates country_code (lazy-backfill case). Treat
		// it as a re-anchor to the adding team's country so the row gets a
		// value going forward.
		return vppAddAnchor{
			region:        addingTeam.Country,
			fetchSecret:   addingTeam.Secret,
			anchorCountry: addingTeam.Country,
			reAnchor:      true,
		}, nil
	}

	// Try to find a token in the anchored country that owns the app.
	tok, err := svc.ds.GetVPPTokenOwningAppInCountry(ctx, adamID, platform, anchored)
	if err != nil {
		if automdm.IsNotFound(err) {
			// Re-anchor self-heal: original anchor's tokens are gone.
			return vppAddAnchor{
				region:        addingTeam.Country,
				fetchSecret:   addingTeam.Secret,
				anchorCountry: addingTeam.Country,
				reAnchor:      true,
			}, nil
		}
		return vppAddAnchor{}, ctxerr.Wrap(ctx, err, "looking up owning token in anchored country")
	}

	// Subsequent add: fetch from anchored storefront using that token. The
	// row's country stays put.
	return vppAddAnchor{
		region:        anchored,
		fetchSecret:   tok.Token,
		anchorCountry: anchored,
		reAnchor:      false,
	}, nil
}

// ensureVPPTokenCountryDeadline keeps the user-facing /client/config retry
// short so add and refresh requests can't hang for the full GetConfig retry
// budget when Apple is unreachable.
const ensureVPPTokenCountryDeadline = 5 * time.Second

// ensureVPPTokenCountry populates country_code on a legacy token, mutating
// it in place so callers can use the value without a re-read. Bounded so
// user-facing callers don't block on Apple; the startup backfill covers
// anything that times out here.
func ensureVPPTokenCountry(ctx context.Context, ds automdm.Datastore, token *automdm.VPPTokenDB) error {
	if token == nil || token.CountryCode != "" {
		return nil
	}
	getCtx, cancel := context.WithTimeout(ctx, ensureVPPTokenCountryDeadline)
	defer cancel()
	cfg, err := vpp.GetConfig(getCtx, token.Token)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "backfilling country for vpp token")
	}
	if cfg.CountryCode == "" {
		return ctxerr.New(ctx, "Apple /client/config returned empty country code")
	}
	if err := ds.UpdateVPPTokenCountryCode(ctx, token.ID, cfg.CountryCode); err != nil {
		return ctxerr.Wrap(ctx, err, "persisting backfilled vpp token country")
	}
	token.CountryCode = cfg.CountryCode
	return nil
}

// getVPPTokenByID returns the VPP token an App Store app is bound to, ready for
// use in requests to Apple's VPP API. Returns an error if the token is missing
// or expired. Does NOT touch country_code: install-time callers only need the
// secret, and any synchronous Apple call here would block installs whenever
// Apple is unreachable. Country lookup happens in ensureVPPTokenCountry.
func (svc *Service) getVPPTokenByID(ctx context.Context, id *uint) (*automdm.VPPTokenDB, error) {
	noToken := automdm.NewUserMessageError(errors.New("No available VPP Token"), http.StatusUnprocessableEntity)
	if id == nil {
		return nil, noToken
	}
	token, err := svc.ds.GetVPPToken(ctx, *id)
	if err != nil {
		if automdm.IsNotFound(err) {
			return nil, noToken
		}
		return nil, ctxerr.Wrap(ctx, err, "fetching vpp token")
	}

	if time.Now().After(token.RenewDate) {
		return nil, automdm.NewUserMessageError(fmt.Errorf("Couldn't install. VPP token %q is expired. Please renew it and try again.", token.Location), http.StatusUnprocessableEntity)
	}

	return token, nil
}

// canReadVPPToken applies the visibility rule of GetVPPTokens to one token.
func (svc *Service) canReadVPPToken(ctx context.Context, tok *automdm.VPPTokenDB) bool {
	if svc.authz.Authorize(ctx, &automdm.VPPApp{}, automdm.ActionRead) == nil {
		return true
	}
	for _, tt := range tok.Teams {
		if svc.authz.Authorize(ctx, &automdm.VPPApp{TeamID: &tt.ID}, automdm.ActionRead) == nil {
			return true
		}
	}
	return false
}

// resolveAppStoreAppToken returns the requested token, or the team's only
// token when none is requested. errPrefix is "Couldn't list apps." or
// "Couldn't add software.".
func (svc *Service) resolveAppStoreAppToken(ctx context.Context, teamID, vppTokenID *uint, errPrefix string) (*automdm.VPPTokenDB, error) {
	tokens, err := svc.ds.ListVPPTokens(ctx)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "listing vpp tokens")
	}
	tmID := ptr.ValOrZero(teamID)

	var tok *automdm.VPPTokenDB
	if vppTokenID != nil {
		for _, t := range tokens {
			if t.ID == *vppTokenID {
				tok = t
				break
			}
		}
		// A token the caller can't see is reported as missing so its location and
		// existence don't leak.
		if tok == nil || (!tok.AvailableToTeam(tmID) && !svc.canReadVPPToken(ctx, tok)) {
			return nil, automdm.NewInvalidArgumentError("vpp_token_id",
				fmt.Sprintf("%s VPP token with ID %d doesn't exist.", errPrefix, *vppTokenID)).WithStatus(http.StatusNotFound)
		}
		if !tok.AvailableToTeam(tmID) {
			return nil, automdm.NewInvalidArgumentError("vpp_token_id",
				fmt.Sprintf("%s VPP token %q isn't assigned to this team.", errPrefix, tok.Location))
		}
	} else {
		var teamTokens []*automdm.VPPTokenDB
		for _, t := range tokens {
			if t.AvailableToTeam(tmID) {
				teamTokens = append(teamTokens, t)
			}
		}
		switch len(teamTokens) {
		case 0:
			return nil, automdm.NewUserMessageError(errors.New("No available VPP Token"), http.StatusUnprocessableEntity)
		case 1:
			tok = teamTokens[0]
		default:
			return nil, automdm.NewInvalidArgumentError("vpp_token_id",
				errPrefix+" This team has more than one VPP token. Please specify vpp_token_id.")
		}
	}

	if time.Now().After(tok.RenewDate) {
		return nil, automdm.NewInvalidArgumentError("vpp_token_id",
			fmt.Sprintf("%s VPP token %q is expired. Please renew it and try again.", errPrefix, tok.Location))
	}
	if err := ensureVPPTokenCountry(ctx, svc.ds, tok); err != nil {
		return nil, err
	}
	return tok, nil
}

var isAdamID = regexp.MustCompile(`^[0-9]+$`)

// The payload checks below need no team context, so BatchAssociateVPPApps and
// the dry run for a not-yet-created team share them. A check that needs the
// team (its config, labels, categories, or token) stays in BatchAssociateVPPApps.

// validateVPPBatchPayloadVersions rejects version names that are too long or
// too many per app, and an app listed both with and without versions, since the
// entry without versions would add a Default version next to the named ones.
func validateVPPBatchPayloadVersions(payloads []automdm.VPPBatchPayload) error {
	// An entry without versions sends one version with no name.
	versionNamesByApp := make(map[automdm.VPPAppID][]string)
	appsWithUnnamedVersion := make(map[automdm.VPPAppID]struct{})
	for _, payload := range payloads {
		appID := automdm.VPPAppID{AdamID: payload.AppStoreID, Platform: payload.Platform}
		versionName := strings.TrimSpace(payload.VersionName)
		if versionName == "" {
			appsWithUnnamedVersion[appID] = struct{}{}
			continue
		}

		if utf8.RuneCountInString(versionName) > automdm.MaxAppStoreAppVersionNameLength {
			return automdm.NewInvalidArgumentError("app_store_apps.name",
				fmt.Sprintf("Couldn't edit app store app (%s). The version name can't be longer than %d characters.", payload.AppStoreID, automdm.MaxAppStoreAppVersionNameLength))
		}

		versionNamesByApp[appID] = append(versionNamesByApp[appID], versionName)
		if len(versionNamesByApp[appID]) > automdm.MaxAppStoreAppVersions {
			return automdm.NewInvalidArgumentError("app_store_apps.versions",
				fmt.Sprintf("Couldn't edit app store app (%s). An app can have at most %d versions per team.", payload.AppStoreID, automdm.MaxAppStoreAppVersions))
		}
	}

	for appID := range appsWithUnnamedVersion {
		if len(versionNamesByApp[appID]) > 0 {
			return automdm.NewInvalidArgumentError("app_store_apps.versions",
				fmt.Sprintf("Couldn't edit app store app (%s). The app has an entry without versions and an entry with versions. Use one entry with versions.", appID.AdamID))
		}
	}
	return nil
}

// validateVPPBatchPayloadLocations rejects location on Android apps and an
// app listed for the same platform with different locations.
func validateVPPBatchPayloadLocations(payloads []automdm.VPPBatchPayload) error {
	locations := make(map[automdm.VPPAppID]string)
	for _, p := range payloads {
		for _, platform := range vppBatchPayloadPlatforms(p) {
			if platform == automdm.AndroidPlatform {
				if p.Location != "" {
					return automdm.NewInvalidArgumentError("app_store_apps.location",
						"Couldn't apply. location isn't supported for Android apps.")
				}
				continue
			}
			key := automdm.VPPAppID{AdamID: p.AppStoreID, Platform: platform}
			if prev, ok := locations[key]; ok && prev != p.Location {
				return automdm.NewInvalidArgumentError("app_store_apps.location",
					fmt.Sprintf("Couldn't apply. App %q is specified more than once with different location values.", p.AppStoreID))
			}
			locations[key] = p.Location
		}
	}
	return nil
}

// vppAppAlreadyAddedError reports a location that names a different token than
// the one an app is already added from. The caller adds the app name.
type vppAppAlreadyAddedError struct {
	from, to string
}

func (e vppAppAlreadyAddedError) Error() string {
	return fmt.Sprintf("app is already added from the %q VPP token, not %q", e.from, e.to)
}

// vppTokenByLocation matches locations with NFC equality, as validateVPPAssignments does.
func vppTokenByLocation(tokens []*automdm.VPPTokenDB, location string) *automdm.VPPTokenDB {
	location = norm.NFC.String(location)
	for _, t := range tokens {
		if norm.NFC.String(t.Location) == location {
			return t
		}
	}
	return nil
}

func vppLocationNotFoundError(location string) error {
	return automdm.NewInvalidArgumentError("app_store_apps", fmt.Sprintf("Couldn't apply. VPP token %q doesn't exist.", location))
}

func vppTokenExpiredError(tok *automdm.VPPTokenDB) error {
	return automdm.NewInvalidArgumentError("app_store_apps", fmt.Sprintf("Couldn't apply. VPP token %q is expired. Please renew it and try again.", tok.Location))
}

// vppMissingAsset is an app that its token's assets don't include.
type vppMissingAsset struct {
	adamID, location string
}

// vppAssetsMissingError returns one line per missing app, sorted by app store
// ID, or nil when nothing is missing.
func vppAssetsMissingError(missing []vppMissingAsset) error {
	if len(missing) == 0 {
		return nil
	}
	slices.SortFunc(missing, func(a, b vppMissingAsset) int {
		return cmp.Or(cmp.Compare(a.adamID, b.adamID), cmp.Compare(a.location, b.location))
	})
	lines := make([]string, 0, len(missing))
	for _, m := range missing {
		lines = append(lines, fmt.Sprintf(`Couldn't apply. App %q isn't available in the %q VPP token. Please purchase a license in Apple Business and try again.`, m.adamID, m.location))
	}
	return automdm.NewInvalidArgumentError("app_store_apps", strings.Join(lines, "\n"))
}

// vppAssetsMissingInAnyTokenError is the dry-run new-team equivalent of
// vppAssetsMissingError: before the team exists its token can't be known, so
// each missing app is blamed on the token set, not a specific location.
func vppAssetsMissingInAnyTokenError(adamIDs []string) error {
	if len(adamIDs) == 0 {
		return nil
	}
	sort.Strings(adamIDs)
	lines := make([]string, 0, len(adamIDs))
	for _, adamID := range adamIDs {
		lines = append(lines, fmt.Sprintf(`Couldn't apply. App %q isn't available in any VPP token. Please purchase a license in Apple Business and try again.`, adamID))
	}
	return automdm.NewInvalidArgumentError("app_store_apps", strings.Join(lines, "\n"))
}

// resolveBatchVPPToken picks the token for one Apple app in a GitOps apply. An
// existing app keeps its token, even an expired one.
func resolveBatchVPPToken(teamTokens, allTokens []*automdm.VPPTokenDB, existingTokenID *uint, location, automdmDisplayName string, dryRun bool) (*automdm.VPPTokenDB, error) {
	var located *automdm.VPPTokenDB
	if location != "" {
		located = vppTokenByLocation(allTokens, location)
		if located == nil {
			return nil, vppLocationNotFoundError(location)
		}
		// A dry run skips this check since a volume_purchasing_program change in
		// the same run isn't applied yet.
		if !dryRun && !slices.ContainsFunc(teamTokens, func(t *automdm.VPPTokenDB) bool { return t.ID == located.ID }) {
			return nil, automdm.NewInvalidArgumentError("app_store_apps",
				fmt.Sprintf(`Couldn't apply. VPP token %q isn't assigned to team %q. Add it under "org_settings.mdm.volume_purchasing_program".`, located.Location, automdmDisplayName))
		}
	}

	if existingTokenID != nil {
		i := slices.IndexFunc(allTokens, func(t *automdm.VPPTokenDB) bool { return t.ID == *existingTokenID })
		if i >= 0 && i < len(allTokens) {
			existing := allTokens[i]
			if located != nil && located.ID != existing.ID {
				return nil, vppAppAlreadyAddedError{from: existing.Location, to: location}
			}
			return existing, nil
		}
	}

	tok := located
	if tok == nil {
		if len(teamTokens) == 0 {
			return nil, automdm.NewUserMessageError(errors.New("No available VPP Token"), http.StatusUnprocessableEntity)
		}
		tok = teamTokens[0]
	}
	if time.Now().After(tok.RenewDate) {
		return nil, vppTokenExpiredError(tok)
	}
	return tok, nil
}

// vppBatchPayloadPlatforms returns the platforms a payload expands to in
// BatchAssociateVPPApps: an Apple app with no platform is added to every Apple
// platform, anything else with no platform defaults to macOS.
func vppBatchPayloadPlatforms(payload automdm.VPPBatchPayload) []automdm.InstallableDevicePlatform {
	switch {
	case payload.Platform != "":
		return []automdm.InstallableDevicePlatform{payload.Platform}
	case isAdamID.MatchString(payload.AppStoreID):
		return []automdm.InstallableDevicePlatform{automdm.MacOSPlatform, automdm.IOSPlatform, automdm.IPadOSPlatform}
	default:
		return []automdm.InstallableDevicePlatform{automdm.MacOSPlatform}
	}
}

// validateVPPAutoUpdateWindow checks the iOS and iPadOS update window. A nil
// enabled flag means disabled, and a disabled window with no times is valid.
func validateVPPAutoUpdateWindow(enabled *bool, start, end *string) error {
	if enabled == nil {
		enabled = new(false)
	}
	if !*enabled && start == nil && end == nil {
		return nil
	}
	schedule := automdm.SoftwareAutoUpdateSchedule{
		AutoUpdateEnabled:   enabled,
		AutoUpdateStartTime: start,
		AutoUpdateEndTime:   end,
	}
	return schedule.WindowIsValid()
}

// validateVPPVersionNamesUnderCollation compares the version names in MySQL,
// whose collation also ignores accents, and fails on two equal names for one app.
func (svc *Service) validateVPPVersionNamesUnderCollation(ctx context.Context, versions []automdm.VPPAppTeam) error {
	versionNames := make([]string, 0, len(versions))
	for _, version := range versions {
		versionNames = append(versionNames, cmp.Or(version.VersionName, automdm.DefaultAppStoreAppVersionName))
	}

	equalNameGroups, err := svc.ds.GetDuplicateStringGroupsUnderCollation(ctx, versionNames)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "comparing app store app version names")
	}

	for _, group := range equalNameGroups {
		appsInGroup := make(map[automdm.VPPAppID]struct{}, len(group.Indices))
		for _, nameIndex := range group.Indices {
			if _, ok := appsInGroup[versions[nameIndex].VPPAppID]; ok {
				return automdm.NewInvalidArgumentError("app_store_apps.name",
					fmt.Sprintf("Couldn't edit app store app (%s). More than one version is named %q.", versions[nameIndex].AdamID, versionNames[nameIndex]))
			}
			appsInGroup[versions[nameIndex].VPPAppID] = struct{}{}
		}
	}
	return nil
}

// validateVPPBatchPayloadPlatform rejects a platform that cannot carry App
// Store apps and the AutoMDM agent package, which AutoMDM manages itself.
func validateVPPBatchPayloadPlatform(platform automdm.InstallableDevicePlatform, appStoreID string) error {
	if !platform.SupportsAppStoreApps() {
		return automdm.NewInvalidArgumentError("app_store_apps.platform",
			fmt.Sprintf("platform must be one of '%s', '%s', '%s', or '%s'", automdm.IOSPlatform, automdm.IPadOSPlatform, automdm.MacOSPlatform, automdm.AndroidPlatform))
	}

	if platform == automdm.AndroidPlatform && strings.HasPrefix(appStoreID, automdmAgentPackagePrefix) {
		return automdm.NewInvalidArgumentError("app_store_id", "The AutoMDM agent cannot be added manually. "+
			"It is automatically managed by AutoMDM when Android MDM is enabled.")
	}
	return nil
}

func (svc *Service) getAndroidAppsFromPlayStore(ctx context.Context, enterpriseName string, apps []automdm.VPPAppTeam, teamID *uint) ([]*automdm.VPPApp, error) {
	var androidApps []*automdm.VPPApp
	seenWebAppNames := make(map[string]struct{})
	addedAndroidAdamIDs := make(map[string]struct{})
	for _, a := range apps {
		if _, ok := addedAndroidAdamIDs[a.AdamID]; ok {
			continue
		}
		addedAndroidAdamIDs[a.AdamID] = struct{}{}

		androidApp, err := svc.androidModule.EnterprisesApplications(ctx, enterpriseName, a.AdamID)
		if err != nil {
			if automdm.IsNotFound(err) {
				return nil, automdm.NewInvalidArgumentError("app_store_id", fmt.Sprintf("Couldn't add software. The application ID %q isn't available in Play Store. Please find ID on the Play Store and try again.", a.AdamID))
			}
			return nil, ctxerr.Wrap(ctx, err, "bulk add app store apps: check if android app exists")
		}

		if strings.HasPrefix(a.AdamID, automdm.AndroidWebAppPrefix) {
			lowerTitle := strings.ToLower(androidApp.Title)
			if _, ok := seenWebAppNames[lowerTitle]; ok {
				return nil, automdm.ConflictError{
					Message: fmt.Sprintf("Couldn't add. Web app with this name (%q) already exists in this team. Please add a web app with a different name or delete the existing app and try again.", androidApp.Title),
				}
			}
			seenWebAppNames[lowerTitle] = struct{}{}
		}

		androidApps = append(androidApps, &automdm.VPPApp{
			VPPAppTeam:       a,
			BundleIdentifier: a.AdamID,
			IconURL:          androidApp.IconUrl,
			Name:             androidApp.Title,
			TeamID:           teamID,
		})
	}
	return androidApps, nil
}

// dryRunValidateVPPAssetsForNewTeam is the dry-run stand-in for
// BatchAssociateVPPApps when the target team does not exist yet. It runs every
// payload check that needs no team. An Apple app with a location is checked
// against that token only. Any other Apple app is checked against the assets of
// every unexpired VPP token, since the team's own token cannot be known before
// the team is created. A missing app is reported with the same error a real
// apply returns. Play Store entries are looked up once per application ID in the
// Android enterprise.
func (svc *Service) dryRunValidateVPPAssetsForNewTeam(ctx context.Context, payloads []automdm.VPPBatchPayload) error {
	if err := validateVPPBatchPayloadVersions(payloads); err != nil {
		return err
	}
	if err := validateVPPBatchPayloadLocations(payloads); err != nil {
		return err
	}

	type locatedApp struct{ location, adamID string }
	var located []locatedApp
	seenLocated := map[locatedApp]struct{}{}
	var wanted []string
	var androidApps []automdm.VPPAppTeam
	var versions []automdm.VPPAppTeam
	seen := map[string]struct{}{}
	for _, payload := range payloads {
		if err := trimAndValidateCategories(ctx, payload.Categories); err != nil {
			return ctxerr.Wrap(ctx, err, "validating app store app categories")
		}
		for _, platform := range vppBatchPayloadPlatforms(payload) {
			if err := validateVPPBatchPayloadPlatform(platform, payload.AppStoreID); err != nil {
				return err
			}
			if platform == automdm.IOSPlatform || platform == automdm.IPadOSPlatform {
				if err := validateVPPAutoUpdateWindow(payload.AutoUpdateEnabled, payload.AutoUpdateStartTime, payload.AutoUpdateEndTime); err != nil {
					return ctxerr.Wrap(ctx, err, "invalid auto-update window for vpp app")
				}
			}
			versions = append(versions, automdm.VPPAppTeam{
				VPPAppID:    automdm.VPPAppID{AdamID: payload.AppStoreID, Platform: platform},
				VersionName: payload.VersionName,
			})
			if platform == automdm.AndroidPlatform {
				androidApps = append(androidApps, automdm.VPPAppTeam{AdamID: payload.AppStoreID, Platform: platform})
				continue
			}
			if payload.Location != "" {
				app := locatedApp{location: payload.Location, adamID: payload.AppStoreID}
				if _, dup := seenLocated[app]; !dup {
					seenLocated[app] = struct{}{}
					located = append(located, app)
				}
				continue
			}
			if _, dup := seen[payload.AppStoreID]; !dup {
				seen[payload.AppStoreID] = struct{}{}
				wanted = append(wanted, payload.AppStoreID)
			}
		}
	}
	if len(versions) > 0 {
		if err := svc.validateVPPVersionNamesUnderCollation(ctx, versions); err != nil {
			return err
		}
	}

	if len(androidApps) > 0 {
		enterprise, err := svc.ds.GetEnterprise(ctx)
		if err != nil {
			if automdm.IsNotFound(err) {
				return &automdm.BadRequestError{Message: "Android MDM is not enabled", InternalErr: err}
			}
			return ctxerr.Wrap(ctx, err, "get android enterprise")
		}
		_, err = svc.getAndroidAppsFromPlayStore(ctx, enterprise.Name(), androidApps, nil)
		if err != nil {
			return err
		}
	}

	if len(wanted) == 0 && len(located) == 0 {
		return nil
	}
	if err := svc.VerifyMDMAppleConfigured(ctx); err != nil {
		return ctxerr.Wrap(ctx, err, "check Apple MDM enabled for VPP apps")
	}

	tokens, err := svc.ds.ListVPPTokens(ctx)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "listing vpp tokens")
	}

	ownedByToken := map[uint]map[string]struct{}{}
	var missingLocated []vppMissingAsset
	for _, app := range located {
		tok := vppTokenByLocation(tokens, app.location)
		if tok == nil {
			return vppLocationNotFoundError(app.location)
		}
		if time.Now().After(tok.RenewDate) {
			return vppTokenExpiredError(tok)
		}
		owned, ok := ownedByToken[tok.ID]
		if !ok {
			assets, err := vpp.GetAssets(ctx, tok.Token, nil)
			if err != nil {
				return ctxerr.Wrap(ctx, err, "unable to retrieve assets")
			}
			owned = make(map[string]struct{}, len(assets))
			for _, asset := range assets {
				owned[asset.AdamID] = struct{}{}
			}
			ownedByToken[tok.ID] = owned
		}
		if _, ok := owned[app.adamID]; !ok {
			missingLocated = append(missingLocated, vppMissingAsset{adamID: app.adamID, location: tok.Location})
		}
	}
	if err := vppAssetsMissingError(missingLocated); err != nil {
		return err
	}

	if len(wanted) == 0 {
		return nil
	}
	if len(tokens) == 0 {
		// A team is assigned a token only after it exists, and a new instance is
		// often dry-run before any token is uploaded, so a missing token is left
		// to the real apply, which reports it. When every token is expired the
		// dry run fails instead, since whichever one the team gets cannot serve apps.
		return nil
	}

	// A token that cannot be read is skipped while another one answers, since
	// the team may never be assigned to it. The dry run fails on it only when no
	// token answers.
	available := map[string]struct{}{}
	var unexpired, readable int
	var fetchErr error
	for _, token := range tokens {
		if time.Now().After(token.RenewDate) {
			continue
		}
		unexpired++
		if owned, ok := ownedByToken[token.ID]; ok {
			readable++
			for adamID := range owned {
				available[adamID] = struct{}{}
			}
			continue
		}
		assets, err := vpp.GetAssets(ctx, token.Token, nil)
		if err != nil {
			svc.logger.WarnContext(ctx, "skipping vpp token in dry run, unable to retrieve assets", "org_name", token.OrgName, "err", err)
			fetchErr = err
			continue
		}
		readable++
		for _, asset := range assets {
			available[asset.AdamID] = struct{}{}
		}
	}
	if unexpired == 0 {
		return automdm.NewInvalidArgumentError("app_store_apps", "Couldn't apply. All VPP tokens are expired. Please renew one and try again.")
	}
	if readable == 0 {
		return ctxerr.Wrap(ctx, fetchErr, "unable to retrieve assets")
	}

	var missing []string
	for _, adamID := range wanted {
		if _, ok := available[adamID]; !ok {
			missing = append(missing, adamID)
		}
	}
	return vppAssetsMissingInAnyTokenError(missing)
}

func (svc *Service) BatchAssociateVPPApps(ctx context.Context, teamName string, payloads []automdm.VPPBatchPayload, dryRun bool) ([]automdm.VPPAppResponse, []string, error) {
	if err := svc.authz.Authorize(ctx, &automdm.Team{}, automdm.ActionRead); err != nil {
		return nil, nil, err
	}

	var teamID *uint
	var manualAgentInstall bool
	if teamName != "" {
		tm, err := svc.ds.TeamByName(ctx, teamName)
		if err != nil {
			// If this is a dry run, the team may not have been created yet.
			if dryRun && automdm.IsNotFound(err) {
				// The team-scoped write check below needs a team ID, so require
				// global VPP app write access before reading any VPP assets.
				if err := svc.authz.Authorize(ctx, &automdm.VPPApp{}, automdm.ActionWrite); err != nil {
					return nil, nil, ctxerr.Wrap(ctx, err, "validating authorization")
				}
				return nil, nil, svc.dryRunValidateVPPAssetsForNewTeam(ctx, payloads)
			}
			return nil, nil, err
		}
		teamID = &tm.ID
		manualAgentInstall = tm.Config.MDM.MacOSSetup.ManualAgentInstall.Value
	} else {
		ac, err := svc.ds.AppConfig(ctx)
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "getting app config")
		}
		manualAgentInstall = ac.MDM.MacOSSetup.ManualAgentInstall.Value
	}

	if err := svc.authz.Authorize(ctx, &automdm.SoftwareInstaller{TeamID: teamID}, automdm.ActionWrite); err != nil {
		return nil, nil, ctxerr.Wrap(ctx, err, "validating authorization")
	}

	// Adding VPP apps will add them to all available platforms per decision:
	// https://github.com/fleetdm/fleet/issues/19447#issuecomment-2256598681
	// The code is already here to support individual platforms, so we can easily enable it later.

	if err := validateVPPBatchPayloadVersions(payloads); err != nil {
		return nil, nil, err
	}
	if err := validateVPPBatchPayloadLocations(payloads); err != nil {
		return nil, nil, err
	}

	var categoryNames []string
	// expandedPlatforms are the platforms of entries that had no platform.
	expandedPlatforms := make(map[automdm.VPPAppID]struct{})
	payloadsWithPlatform := make([]automdm.VPPBatchPayloadWithPlatform, 0, len(payloads))
	for _, payload := range payloads {
		if err := trimAndValidateCategories(ctx, payload.Categories); err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "validating app store app categories")
		}
		categoryNames = append(categoryNames, payload.Categories...)

		payload.VersionName = strings.TrimSpace(payload.VersionName)

		if payload.Platform == "" && isAdamID.MatchString(payload.AppStoreID) {
			// add all possible Apple platforms, we'll remove the ones that this app doesn't support later
			for _, p := range []automdm.InstallableDevicePlatform{automdm.MacOSPlatform, automdm.IOSPlatform, automdm.IPadOSPlatform} {
				expandedPlatforms[automdm.VPPAppID{AdamID: payload.AppStoreID, Platform: p}] = struct{}{}
			}
			payloadsWithPlatform = append(payloadsWithPlatform,
				automdm.VPPBatchPayloadWithPlatform{
					AppStoreID:          payload.AppStoreID,
					VersionName:         payload.VersionName,
					SelfService:         payload.SelfService,
					InstallDuringSetup:  payload.InstallDuringSetup,
					Platform:            automdm.MacOSPlatform,
					Location:            payload.Location,
					LabelsExcludeAny:    payload.LabelsExcludeAny,
					LabelsIncludeAny:    payload.LabelsIncludeAny,
					LabelsIncludeAll:    payload.LabelsIncludeAll,
					Categories:          payload.Categories,
					DisplayName:         payload.DisplayName,
					AutoUpdateEnabled:   payload.AutoUpdateEnabled,
					AutoUpdateStartTime: payload.AutoUpdateStartTime,
					AutoUpdateEndTime:   payload.AutoUpdateEndTime,
				},
				automdm.VPPBatchPayloadWithPlatform{
					AppStoreID:          payload.AppStoreID,
					VersionName:         payload.VersionName,
					SelfService:         payload.SelfService,
					InstallDuringSetup:  payload.InstallDuringSetup,
					Platform:            automdm.IOSPlatform,
					Location:            payload.Location,
					LabelsExcludeAny:    payload.LabelsExcludeAny,
					LabelsIncludeAny:    payload.LabelsIncludeAny,
					LabelsIncludeAll:    payload.LabelsIncludeAll,
					Categories:          payload.Categories,
					DisplayName:         payload.DisplayName,
					Configuration:       payload.Configuration,
					AutoUpdateEnabled:   payload.AutoUpdateEnabled,
					AutoUpdateStartTime: payload.AutoUpdateStartTime,
					AutoUpdateEndTime:   payload.AutoUpdateEndTime,
				},
				automdm.VPPBatchPayloadWithPlatform{
					AppStoreID:          payload.AppStoreID,
					VersionName:         payload.VersionName,
					SelfService:         payload.SelfService,
					InstallDuringSetup:  payload.InstallDuringSetup,
					Platform:            automdm.IPadOSPlatform,
					Location:            payload.Location,
					LabelsExcludeAny:    payload.LabelsExcludeAny,
					LabelsIncludeAny:    payload.LabelsIncludeAny,
					LabelsIncludeAll:    payload.LabelsIncludeAll,
					Categories:          payload.Categories,
					DisplayName:         payload.DisplayName,
					Configuration:       payload.Configuration,
					AutoUpdateEnabled:   payload.AutoUpdateEnabled,
					AutoUpdateStartTime: payload.AutoUpdateStartTime,
					AutoUpdateEndTime:   payload.AutoUpdateEndTime,
				},
			)
			continue
		}

		payloadsWithPlatform = append(payloadsWithPlatform, automdm.VPPBatchPayloadWithPlatform{
			AppStoreID:          payload.AppStoreID,
			Location:            payload.Location,
			VersionName:         payload.VersionName,
			SelfService:         payload.SelfService,
			InstallDuringSetup:  payload.InstallDuringSetup,
			Platform:            payload.Platform,
			LabelsExcludeAny:    payload.LabelsExcludeAny,
			LabelsIncludeAny:    payload.LabelsIncludeAny,
			LabelsIncludeAll:    payload.LabelsIncludeAll,
			Categories:          payload.Categories,
			DisplayName:         payload.DisplayName,
			Configuration:       payload.Configuration,
			AutoUpdateEnabled:   payload.AutoUpdateEnabled,
			AutoUpdateStartTime: payload.AutoUpdateStartTime,
			AutoUpdateEndTime:   payload.AutoUpdateEndTime,
		})

	}

	var allTokens, teamTokens []*automdm.VPPTokenDB
	tokensByID := make(map[uint]*automdm.VPPTokenDB)
	var assigned map[automdm.VPPAppID]automdm.VPPAppTeam
	automdmDisplayName := cmp.Or(teamName, automdm.DisplayNameNoTeam)

	// Checked before anything is written, so a rejected batch doesn't leave new categories behind.
	for _, payload := range payloadsWithPlatform {
		if payload.Platform == "" || payload.Platform.IsApplePlatform() {
			if err := svc.VerifyMDMAppleConfigured(ctx); err != nil {
				return nil, nil, ctxerr.Wrap(ctx, err, "check Apple MDM enabled for VPP apps")
			}

			var err error
			allTokens, err = svc.ds.ListVPPTokens(ctx)
			if err != nil {
				return nil, nil, ctxerr.Wrap(ctx, err, "listing vpp tokens")
			}
			for _, t := range allTokens {
				tokensByID[t.ID] = t
				if t.AvailableToTeam(ptr.ValOrZero(teamID)) {
					teamTokens = append(teamTokens, t)
				}
			}
			assigned, err = svc.ds.GetAssignedVPPApps(ctx, teamID)
			if err != nil {
				return nil, nil, ctxerr.Wrap(ctx, err, "getting assigned vpp apps")
			}
			break
		}
	}

	// since we actually add categories here, it's possible they can stay orphaned until the next run if the run fails
	categories, err := svc.batchAddSelfServiceCategories(ctx, teamID, categoryNames, dryRun)
	if err != nil {
		return nil, nil, err
	}

	var incomingAppleApps, incomingAndroidApps []automdm.VPPAppTeam
	// An existing app on an expired token keeps its row, but skips the asset check and metadata fetch.
	var appleAppsToFetch, expiredExistingApps []automdm.VPPAppTeam
	// Don't check for token if we're only disassociating assets
	if len(payloads) > 0 {
		for _, payload := range payloadsWithPlatform {
			if payload.Platform == "" {
				payload.Platform = automdm.MacOSPlatform
			}
			if err := validateVPPBatchPayloadPlatform(payload.Platform, payload.AppStoreID); err != nil {
				return nil, nil, err
			}

			if payload.Platform == automdm.MacOSPlatform && ptr.ValOrZero(payload.InstallDuringSetup) && manualAgentInstall {
				return nil, nil, automdm.NewUserMessageError(
					errors.New(`Couldn't edit software. "setup_experience" cannot be used for macOS software if "macos_manual_agent_install" is enabled.`),
					http.StatusUnprocessableEntity)
			}

			var err error
			var vppTokenID *uint
			if payload.Platform.IsApplePlatform() {
				var existingTokenID *uint
				if existing, ok := assigned[automdm.VPPAppID{AdamID: payload.AppStoreID, Platform: payload.Platform}]; ok {
					existingTokenID = existing.VPPTokenID
				}
				// A platform the app doesn't support has no row. It's skipped if the app's
				// first-added row is on an expired token. Without a location, it also
				// follows that row's token.
				if _, expanded := expandedPlatforms[automdm.VPPAppID{AdamID: payload.AppStoreID, Platform: payload.Platform}]; expanded && existingTokenID == nil {
					var first *automdm.VPPAppTeam
					for id, row := range assigned {
						if id.AdamID == payload.AppStoreID && (first == nil || row.AppTeamID < first.AppTeamID) {
							first = &row
						}
					}
					if first != nil && first.VPPTokenID != nil {
						if tok := tokensByID[*first.VPPTokenID]; tok != nil && time.Now().After(tok.RenewDate) {
							continue
						}
						if payload.Location == "" {
							existingTokenID = first.VPPTokenID
						}
					}
				}
				tok, err := resolveBatchVPPToken(teamTokens, allTokens, existingTokenID, payload.Location, automdmDisplayName, dryRun)
				if err != nil {
					var added vppAppAlreadyAddedError
					if !errors.As(err, &added) {
						return nil, nil, err
					}
					appName := payload.AppStoreID
					if app, err := svc.ds.GetVPPAppByAdamIDPlatform(ctx, payload.AppStoreID, payload.Platform); err == nil {
						appName = app.Name
					} else if !automdm.IsNotFound(err) {
						return nil, nil, ctxerr.Wrap(ctx, err, "getting vpp app name")
					}
					return nil, nil, automdm.ConflictError{Message: fmt.Sprintf(
						`Couldn't apply. %s is already added from the %q VPP token. To add it from %q, remove it, run GitOps, and then add it back with the new location.`,
						appName, added.from, added.to)}
				}
				vppTokenID = new(tok.ID)
			}

			var validatedLabels *automdm.LabelIdentsWithScope
			if !dryRun {
				validatedLabels, err = ValidateSoftwareLabels(ctx, svc, teamID, payload.LabelsIncludeAny, payload.LabelsExcludeAny, payload.LabelsIncludeAll)
				if err != nil {
					return nil, nil, ctxerr.Wrap(ctx, err, "validating software labels for batch adding vpp app")
				}
			}

			categories, catIDs, err := svc.removeDuplicateOrMissingCategories(ctx, ptr.ValOrZero(teamID), payload.Categories)
			if err != nil {
				return nil, nil, ctxerr.Wrap(ctx, err, "filtering vpp app categories")
			}
			payload.Categories = categories

			appStoreApp := automdm.VPPAppTeam{
				VPPAppID: automdm.VPPAppID{
					AdamID:   payload.AppStoreID,
					Platform: payload.Platform,
				},
				VPPTokenID:          vppTokenID,
				VersionName:         payload.VersionName,
				SelfService:         payload.SelfService,
				InstallDuringSetup:  payload.InstallDuringSetup,
				ValidatedLabels:     validatedLabels,
				CategoryIDs:         catIDs,
				DisplayName:         ptr.String(payload.DisplayName),
				AutoUpdateEnabled:   payload.AutoUpdateEnabled,
				AutoUpdateStartTime: payload.AutoUpdateStartTime,
				AutoUpdateEndTime:   payload.AutoUpdateEndTime,
			}
			if payload.Platform == automdm.IOSPlatform || payload.Platform == automdm.IPadOSPlatform {
				if appStoreApp.AutoUpdateEnabled == nil {
					appStoreApp.AutoUpdateEnabled = new(false)
				}
				if err := validateVPPAutoUpdateWindow(appStoreApp.AutoUpdateEnabled, appStoreApp.AutoUpdateStartTime, appStoreApp.AutoUpdateEndTime); err != nil {
					return nil, nil, ctxerr.Wrap(ctx, err, "invalid auto-update window for vpp app")
				}
			} else {
				// Clear auto-update settings on other platforms, only iOS and iPadOS support them
				appStoreApp.AutoUpdateEnabled = nil
				appStoreApp.AutoUpdateStartTime = nil
				appStoreApp.AutoUpdateEndTime = nil
			}
			switch payload.Platform {
			case automdm.AndroidPlatform:
				if strings.HasPrefix(payload.AppStoreID, automdm.AndroidWebAppPrefix) && payload.Configuration != nil {
					return nil, nil, automdm.NewInvalidArgumentError("configuration", "Couldn't edit. Android web apps don't support configurations.")
				}

				appStoreApp.SelfService = true
				if payload.Configuration != nil {
					if err := automdm.ValidateAndroidAppConfiguration(payload.Configuration); err != nil {
						return nil, nil, err
					}
					if err := svc.validateAppConfigCustomHostVitals(ctx, payload.Configuration); err != nil {
						return nil, nil, err
					}
				}
				appStoreApp.Configuration = payload.Configuration
				incomingAndroidApps = append(incomingAndroidApps, appStoreApp)
			case automdm.IOSPlatform, automdm.IPadOSPlatform, automdm.MacOSPlatform:
				if payload.Configuration != nil && payload.Platform != automdm.MacOSPlatform {
					var plist string
					if err := json.Unmarshal(payload.Configuration, &plist); err != nil {
						return nil, nil, automdm.NewInvalidArgumentError("configuration", "expected configuration as a JSON string containing the XML")
					}
					if err := automdm.ValidateAppleAppConfiguration([]byte(plist)); err != nil {
						return nil, nil, err
					}
					if err := svc.validateAppConfigCustomHostVitals(ctx, []byte(plist)); err != nil {
						return nil, nil, err
					}
					appStoreApp.Configuration = []byte(plist)
				}
				incomingAppleApps = append(incomingAppleApps, appStoreApp)
			}

		}

		if err := svc.validateVPPVersionNamesUnderCollation(ctx, slices.Concat(incomingAppleApps, incomingAndroidApps)); err != nil {
			return nil, nil, err
		}

		if len(incomingAppleApps) > 0 {
			// incomingAppleApps has one entry per (AdamID, platform); the sets
			// dedup an app listed for several platforms.
			adamIDsByToken := make(map[uint]map[string]struct{})
			for _, app := range incomingAppleApps {
				tok, ok := tokensByID[*app.VPPTokenID]
				if !ok {
					return nil, nil, ctxerr.New(ctx, "resolved VPP token not found")
				}
				if time.Now().After(tok.RenewDate) {
					expiredExistingApps = append(expiredExistingApps, app)
					continue
				}
				appleAppsToFetch = append(appleAppsToFetch, app)
				if adamIDsByToken[tok.ID] == nil {
					adamIDsByToken[tok.ID] = make(map[string]struct{})
				}
				adamIDsByToken[tok.ID][app.AdamID] = struct{}{}
			}

			var missing []vppMissingAsset
			for _, tokenID := range slices.Sorted(maps.Keys(adamIDsByToken)) {
				tok, ok := tokensByID[tokenID]
				if !ok {
					return nil, nil, ctxerr.New(ctx, "resolved VPP token not found")
				}
				assets, err := vpp.GetAssets(ctx, tok.Token, nil)
				if err != nil {
					return nil, nil, ctxerr.Wrap(ctx, err, "unable to retrieve assets")
				}
				owned := make(map[string]struct{}, len(assets))
				for _, asset := range assets {
					owned[asset.AdamID] = struct{}{}
				}
				for adamID := range adamIDsByToken[tokenID] {
					if _, ok := owned[adamID]; !ok {
						missing = append(missing, vppMissingAsset{adamID: adamID, location: tok.Location})
					}
				}
			}
			if err := vppAssetsMissingError(missing); err != nil {
				return nil, nil, err
			}
		}
	}

	// Look up Android apps before the dry run returns, so a dry run rejects the same apps the
	// apply would and the apply does not repeat the AMAPI calls.
	var androidAppStoreApps []*automdm.VPPApp
	if len(incomingAndroidApps) > 0 {
		var enterprise *android.Enterprise
		enterprise, err = svc.ds.GetEnterprise(ctx)
		if err != nil {
			if automdm.IsNotFound(err) {
				return nil, nil, &automdm.BadRequestError{Message: "Android MDM is not enabled", InternalErr: err}
			}
			return nil, nil, ctxerr.Wrap(ctx, err, "get android enterprise")
		}

		androidAppStoreApps, err = svc.getAndroidAppsFromPlayStore(ctx, enterprise.Name(), incomingAndroidApps, teamID)
		if err != nil {
			return nil, nil, err
		}
	}

	if dryRun {
		// Stop after the read-only App Store and Play Store checks, so a dry run makes no changes.
		return nil, categories, nil
	}

	allPlatformApps := slices.Concat(incomingAppleApps, incomingAndroidApps)

	var appStoreApps []*automdm.VPPApp

	var pendingReAnchors []vppReAnchor
	if len(appleAppsToFetch) > 0 {
		tokenInfoByID := make(map[uint]vppTokenInfo)
		for _, app := range appleAppsToFetch {
			if _, ok := tokenInfoByID[*app.VPPTokenID]; ok {
				continue
			}
			tok, ok := tokensByID[*app.VPPTokenID]
			if !ok {
				return nil, nil, ctxerr.New(ctx, "resolved VPP token not found")
			}
			if err := ensureVPPTokenCountry(ctx, svc.ds, tok); err != nil {
				return nil, nil, err
			}
			tokenInfoByID[tok.ID] = vppTokenInfo{Secret: tok.Token, Country: tok.CountryCode}
		}

		apps, reAnchors, err := svc.getAnchoredVPPAppsMetadata(ctx, appleAppsToFetch, tokenInfoByID)
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "refreshing VPP app metadata")
		}
		if len(apps) == 0 {
			return nil, nil, automdm.NewInvalidArgumentError("app_store_apps",
				"no valid apps found matching the provided app store IDs and platforms")
		}

		appStoreApps = append(appStoreApps, apps...)
		pendingReAnchors = reAnchors
	}

	appStoreApps = append(appStoreApps, androidAppStoreApps...)

	// Filter out the apps with invalid platforms
	validAppIDs := make(map[automdm.VPPAppID]struct{}, len(appStoreApps)+len(expiredExistingApps))
	for _, app := range appStoreApps {
		validAppIDs[app.VPPAppID] = struct{}{}
	}
	for _, app := range expiredExistingApps {
		validAppIDs[app.VPPAppID] = struct{}{}
	}
	validPlatformApps := make([]automdm.VPPAppTeam, 0, len(allPlatformApps))
	macOSVersionCountByAdamID := make(map[string]int)
	for _, app := range allPlatformApps {
		if _, ok := validAppIDs[app.VPPAppID]; !ok {
			continue
		}
		validPlatformApps = append(validPlatformApps, app)

		// Count macOS versions after the metadata lookup, an entry without a platform adds macOS only when the app supports it
		if app.Platform != automdm.MacOSPlatform {
			continue
		}
		macOSVersionCountByAdamID[app.AdamID]++
		if macOSVersionCountByAdamID[app.AdamID] > 1 {
			return nil, nil, automdm.NewInvalidArgumentError("app_store_apps.versions",
				fmt.Sprintf("Couldn't edit app store app (%s). macOS App Store apps can only have one version. Set \"platform\" to add versions for iOS or iPadOS only.", app.AdamID))
		}
	}
	allPlatformApps = validPlatformApps

	if len(appStoreApps) > 0 {
		if err := svc.ds.BatchInsertVPPApps(ctx, appStoreApps); err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "inserting vpp app metadata")
		}
	}

	// Re-anchor only after the insert so a row's stored country never
	// outpaces the metadata fetched from that country.
	for _, ra := range pendingReAnchors {
		if err := svc.ds.UpdateVPPAppCountryCode(ctx, ra.AdamID, ra.Platform, ra.CountryCode); err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "re-anchoring vpp app country in batch")
		}
	}

	appStoreIDToTitleID := make(map[string]uint, len(appStoreApps))
	for _, a := range appStoreApps {
		// The string representation includes the adam ID AND the platform, so it's unique per software title.
		appStoreIDToTitleID[a.VPPAppID.String()] = a.TitleID
	}
	for _, app := range expiredExistingApps {
		existing, err := svc.ds.GetVPPAppByAdamIDPlatform(ctx, app.AdamID, app.Platform)
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "getting vpp app for expired token")
		}
		appStoreIDToTitleID[app.VPPAppID.String()] = existing.TitleID
	}

	setupExperienceChanged, err := svc.ds.SetTeamVPPApps(ctx, teamID, allPlatformApps, appStoreIDToTitleID)
	if err != nil {
		return nil, nil, ctxerr.Wrap(ctx, err, "set team vpp assets")
	}

	// Do cleanup here because this is API call 2 of 2 for setting software from GitOps
	var tmID uint
	if teamID != nil {
		tmID = *teamID
	}

	if err := svc.ds.DeleteIconsAssociatedWithTitlesWithoutInstallers(ctx, tmID); err != nil {
		return nil, nil, err // returned error already includes context that we could include here
	}

	addedApps, err := svc.ds.GetVPPApps(ctx, teamID)
	if err != nil {
		return nil, nil, err
	}

	if setupExperienceChanged {
		err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityEditedSetupExperienceSoftware{TeamID: ptr.ValOrZero(teamID), TeamName: teamName})
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "create edited setup experience activity")
		}
	}

	if len(addedApps) == 0 {
		return []automdm.VPPAppResponse{}, categories, nil
	}
	return addedApps, categories, nil
}

func (svc *Service) GetAppStoreApps(ctx context.Context, teamID *uint, vppTokenID *uint) ([]*automdm.VPPApp, error) {
	if err := svc.authz.Authorize(ctx, &automdm.VPPApp{TeamID: teamID}, automdm.ActionRead); err != nil {
		return nil, err
	}
	if err := svc.VerifyMDMAppleConfigured(ctx); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "check Apple MDM enabled for VPP apps")
	}

	tok, err := svc.resolveAppStoreAppToken(ctx, teamID, vppTokenID, "Couldn't list apps.")
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "retrieving VPP token")
	}
	vppToken := tok.Token

	assets, err := vpp.GetAssets(ctx, vppToken, nil)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "fetching Apple VPP assets")
	}

	if len(assets) == 0 {
		return []*automdm.VPPApp{}, nil
	}

	var adamIDs []string
	for _, a := range assets {
		adamIDs = append(adamIDs, a.AdamID)
	}

	// The picker UI shows apps the team's token can add, so it's correct to
	// fetch metadata from the team's storefront.
	metadata, err := apple_apps.GetMetadata(adamIDs, tok.CountryCode, vppToken, svc.getVPPConfig(ctx))
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "fetching VPP asset metadata")
	}

	assignedApps, err := svc.ds.GetAssignedVPPApps(ctx, teamID)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "retrieving assigned VPP apps")
	}

	// Don't write metadata back here. The team's current storefront can
	// differ from a row's anchored country, so a writeback would leak
	// foreign metadata onto an anchored row. RefreshVersions handles the
	// hourly refresh from each row's anchored storefront.
	var apps []*automdm.VPPApp
	for _, a := range assets {
		m, ok := metadata[a.AdamID]
		if !ok {
			// Then this adam_id is not a VPP software entity, so skip it.
			continue
		}

		for _, app := range apple_apps.ToVPPApps(m) {
			if _, assigned := assignedApps[app.VPPAppID]; assigned {
				continue
			}
			apps = append(apps, &app)
		}
	}

	// Sort apps by name and by platform
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Name != apps[j].Name {
			return apps[i].Name < apps[j].Name
		}
		return apps[i].Platform < apps[j].Platform
	})

	return apps, nil
}

var androidApplicationID = regexp.MustCompile(`^([A-Za-z]{1}[A-Za-z\d_]*\.)+[A-Za-z][A-Za-z\d_]*$`)

// automdmAgentPackagePrefix is the package prefix for AutoMDM Android agent.
// IT admins should not be able to add this app manually via the Software page as it is managed automatically by AutoMDM.
const automdmAgentPackagePrefix = "com.fleetdm.agent"

func (svc *Service) AddAppStoreApp(ctx context.Context, teamID *uint, appID automdm.VPPAppTeam) (*automdm.VPPApp, error) {
	if err := svc.authz.Authorize(ctx, &automdm.VPPApp{TeamID: teamID}, automdm.ActionWrite); err != nil {
		return nil, err
	}
	if appID.AddAutoInstallPolicy {
		// Currently, same write permissions are applied on software and policies,
		// but leaving this here in case it changes in the future.
		if err := svc.authz.Authorize(ctx, &automdm.Policy{PolicyData: automdm.PolicyData{TeamID: teamID}}, automdm.ActionWrite); err != nil {
			return nil, err
		}
	}

	// Validate platform
	if appID.Platform == "" {
		appID.Platform = automdm.MacOSPlatform
	}

	if !appID.Platform.SupportsAppStoreApps() {
		return nil, automdm.NewInvalidArgumentError("platform",
			fmt.Sprintf("platform must be one of '%s', '%s', '%s', or '%s'", automdm.IOSPlatform, automdm.IPadOSPlatform, automdm.MacOSPlatform, automdm.AndroidPlatform))
	}
	if appID.Platform == automdm.AndroidPlatform && appID.VPPTokenID != nil {
		return nil, automdm.NewInvalidArgumentError("vpp_token_id", "Couldn't add software. vpp_token_id isn't supported for Android apps.")
	}
	if appID.Platform.IsApplePlatform() {
		if err := svc.VerifyMDMAppleConfigured(ctx); err != nil {
			return nil, ctxerr.Wrap(ctx, err, "check Apple MDM enabled for VPP app")
		}
	}

	// Auto-update is iOS/iPadOS only. Validate here and clear on other platforms; the datastore stores whatever strings arrive, and gitops/API bypass the FE validator.
	if appID.Platform == automdm.IOSPlatform || appID.Platform == automdm.IPadOSPlatform {
		if appID.AutoUpdateEnabled != nil && *appID.AutoUpdateEnabled {
			schedule := automdm.SoftwareAutoUpdateSchedule{
				AutoUpdateEnabled:   appID.AutoUpdateEnabled,
				AutoUpdateStartTime: appID.AutoUpdateStartTime,
				AutoUpdateEndTime:   appID.AutoUpdateEndTime,
			}
			if err := schedule.WindowIsValid(); err != nil {
				return nil, ctxerr.Wrap(ctx, err, "validating auto-update schedule")
			}
		}
	} else {
		appID.AutoUpdateEnabled = nil
		appID.AutoUpdateStartTime = nil
		appID.AutoUpdateEndTime = nil
	}

	validatedLabels, err := ValidateSoftwareLabels(ctx, svc, teamID, appID.LabelsIncludeAny, appID.LabelsExcludeAny, appID.LabelsIncludeAll)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "validating software labels for adding vpp app")
	}

	teamName := automdm.DisplayNameNoTeam
	if teamID != nil && *teamID != 0 {
		tm, err := svc.ds.TeamLite(ctx, *teamID)
		if automdm.IsNotFound(err) {
			return nil, automdm.NewInvalidArgumentError("team_id", fmt.Sprintf("team %d does not exist", *teamID)).
				WithStatus(http.StatusNotFound)
		} else if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "checking if team exists")
		}

		teamName = tm.Name
	}

	if appID.AddAutoInstallPolicy && appID.Platform != automdm.MacOSPlatform {
		return nil, automdm.NewUserMessageError(errors.New("Currently, automatic install is only supported on macOS, Windows, and Linux. Please add the app without automatic_install and manually install it on the Host details page."), http.StatusBadRequest)
	}

	appID.VersionName = strings.TrimSpace(appID.VersionName)
	if appID.VersionName == "" {
		appID.VersionName = automdm.DefaultAppStoreAppVersionName
	}
	if utf8.RuneCountInString(appID.VersionName) > automdm.MaxAppStoreAppVersionNameLength {
		return nil, automdm.NewInvalidArgumentError("name", fmt.Sprintf("Couldn't add. The version name can't be longer than %d characters.", automdm.MaxAppStoreAppVersionNameLength))
	}

	// Check if an existing version uses this name
	existingVersionCount, versionNameExists, err := svc.ds.GetAppStoreAppVersionCount(ctx, teamID, appID.VPPAppID, appID.VersionName)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "getting existing app store app versions")
	}

	// Every version of an app on a team shares the token of the first one added.
	if existingVersionCount > 0 && appID.Platform.IsApplePlatform() {
		existing, err := svc.ds.GetVPPAppMetadataByAdamIDPlatformTeamID(ctx, appID.AdamID, appID.Platform, teamID)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "getting first added app store app version")
		}
		switch {
		case existing.VPPTokenID == nil:
		case appID.VPPTokenID == nil:
			appID.VPPTokenID = existing.VPPTokenID
		case *appID.VPPTokenID != *existing.VPPTokenID:
			existingTok, err := svc.ds.GetVPPToken(ctx, *existing.VPPTokenID)
			if err != nil {
				return nil, ctxerr.Wrap(ctx, err, "getting vpp token of first added app store app version")
			}
			return nil, automdm.ConflictError{Message: fmt.Sprintf(
				"Couldn't add software. %s (%s) is already added to this team from the %q VPP token.",
				existing.Name, appID.Platform.DisplayName(), existingTok.Location)}
		}
	}

	if versionNameExists {
		// FE routes the duplicate-name conflict to the Name field by matching
		// the "a version named" substring (routeVersionNameError in
		// frontend/pages/SoftwarePage/helpers.tsx). Keep the phrase if you
		// reword this message.
		return nil, ctxerr.Wrap(ctx, automdm.ConflictError{
			Message: fmt.Sprintf("Couldn't add. A version named %q already exists for this app in the %s team.", appID.VersionName, teamName),
		}, "adding app store app version")
	}

	if appID.Platform == automdm.MacOSPlatform && existingVersionCount > 0 {
		return nil, ctxerr.Wrap(ctx, automdm.ConflictError{
			Message: fmt.Sprintf("Couldn't add. macOS App Store apps can only have one version in the %s team.", teamName),
		}, "adding app store app version")
	}

	if existingVersionCount >= automdm.MaxAppStoreAppVersions {
		return nil, ctxerr.Wrap(ctx, &automdm.BadRequestError{
			Message: fmt.Sprintf("Couldn't add. An app can have at most %d versions per team.", automdm.MaxAppStoreAppVersions),
		}, "adding app store app version")
	}

	isAndroidAppID := androidApplicationID.MatchString(appID.AdamID)

	var app *automdm.VPPApp
	var androidEnterpriseName string
	var anchor vppAddAnchor
	var vppTok *automdm.VPPTokenDB

	// Different flows based on platform
	switch appID.Platform {
	case automdm.AndroidPlatform:
		if !isAndroidAppID {
			return nil, automdm.NewInvalidArgumentError("app_store_id", "Application ID must be a valid Android application ID")
		}
		if strings.HasPrefix(appID.AdamID, automdmAgentPackagePrefix) {
			return nil, automdm.NewInvalidArgumentError("app_store_id", "The AutoMDM agent cannot be added manually. "+
				"It is automatically managed by AutoMDM when Android MDM is enabled.")
		}

		if strings.HasPrefix(appID.AdamID, automdm.AndroidWebAppPrefix) && appID.Configuration != nil {
			return nil, automdm.NewInvalidArgumentError("configuration", "Couldn't add. Android web apps don't support configurations.")
		}

		if appID.Configuration != nil {
			if err := svc.validateAppConfigCustomHostVitals(ctx, appID.Configuration); err != nil {
				return nil, err
			}
		}

		appID.SelfService = true
		appID.AddAutoInstallPolicy = false
		appID.ValidatedLabels = validatedLabels

		enterprise, err := svc.ds.GetEnterprise(ctx)
		if err != nil {
			return nil, &automdm.BadRequestError{Message: "Android MDM is not enabled", InternalErr: err}
		}
		androidEnterpriseName = enterprise.Name()

		androidApp, err := svc.androidModule.EnterprisesApplications(ctx, androidEnterpriseName, appID.AdamID)
		if err != nil {
			if automdm.IsNotFound(err) {
				return nil, automdm.NewInvalidArgumentError("app_store_id", fmt.Sprintf("Couldn't add software. The application ID %q isn't available in Play Store. Please find ID on the Play Store and try again.", appID.AdamID))
			}
			return nil, ctxerr.Wrap(ctx, err, "add app store app: check if android app exists")
		}

		app = &automdm.VPPApp{
			VPPAppTeam:       appID,
			BundleIdentifier: appID.AdamID,
			IconURL:          androidApp.IconUrl,
			Name:             androidApp.Title,
			TeamID:           teamID,
		}

		if strings.HasPrefix(appID.AdamID, automdm.AndroidWebAppPrefix) {
			exists, err := svc.ds.CheckAndroidWebAppNameExistsOnTeam(ctx, teamID, androidApp.Title, appID.AdamID)
			if err != nil {
				return nil, ctxerr.Wrap(ctx, err, "checking for duplicate android web app name")
			}
			if exists {
				return nil, automdm.ConflictError{
					Message: fmt.Sprintf("Couldn't add. Web app with this name (%q) already exists in this team. Please add a web app with a different name or delete the existing app and try again.", androidApp.Title),
				}
			}
		}

	default:
		if isAndroidAppID {
			return nil, automdm.NewInvalidArgumentError(
				"app_store_id",
				fmt.Sprintf(
					"Couldn't add software. %q isn't available in Apple Business or Play Store. Please purchase a license in Apple Business or find the app in Play Store and try again.",
					appID.AdamID,
				),
			)
		}

		vppTok, err = svc.resolveAppStoreAppToken(ctx, teamID, appID.VPPTokenID, "Couldn't add software.")
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "retrieving VPP token")
		}
		teamTokenInfo := vppTokenInfo{Secret: vppTok.Token, Country: vppTok.CountryCode}

		assets, err := vpp.GetAssets(ctx, vppTok.Token, &vpp.AssetFilter{AdamID: appID.AdamID})
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "retrieving VPP asset")
		}

		if len(assets) == 0 {
			return nil, automdm.NewInvalidArgumentError("app_store_id",
				fmt.Sprintf("Couldn't add software. %q isn't available in the %q VPP token. Please purchase a license in Apple Business and try again.", appID.AdamID, vppTok.Location))
		}

		asset := assets[0]

		// Decide which storefront to fetch metadata from based on whether this
		// is a first-add, a subsequent add against an existing anchor, or a
		// re-anchor self-heal.
		anchor, err = svc.resolveAddAnchor(ctx, asset.AdamID, appID.Platform, teamTokenInfo)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "resolving anchor for vpp app add")
		}

		assetMetadata, err := apple_apps.GetMetadata([]string{asset.AdamID}, anchor.region, anchor.fetchSecret, svc.getVPPConfig(ctx))
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "fetching VPP asset metadata")
		}

		assetMD := assetMetadata[asset.AdamID]

		if appID.Platform == automdm.MacOSPlatform {
			appID.Configuration = nil
		}

		platforms := apple_apps.ToVPPApps(assetMD)
		appFromApple, ok := platforms[appID.Platform]
		if !ok {
			return nil, automdm.NewInvalidArgumentError("app_store_id", fmt.Sprintf("%s isn't available for %s", assetMD.Attributes.Name, appID.Platform))
		}

		if appID.Platform == automdm.MacOSPlatform {
			exists, err := svc.ds.CheckConflictingInstallerExists(ctx, teamID, appFromApple.BundleIdentifier, string(appID.Platform))
			if err != nil {
				return nil, ctxerr.Wrap(ctx, err, "checking existence of conflicting installer")
			}

			if exists {
				return nil, ctxerr.Wrap(ctx, automdm.ConflictError{
					Message: fmt.Sprintf(automdm.CantAddSoftwareConflictMessage,
						assetMD.Attributes.Name, teamName),
				}, "vpp app conflicts with existing software installer")
			}
		} else if appID.Platform == automdm.IOSPlatform || appID.Platform == automdm.IPadOSPlatform {
			// Check if an in-house app (IPA) with the same bundle identifier already exists
			exists, err := svc.ds.CheckConflictingInHouseAppExists(ctx, teamID, appFromApple.BundleIdentifier, string(appID.Platform))
			if err != nil {
				return nil, ctxerr.Wrap(ctx, err, "checking existence of conflicting installer")
			}

			if exists {
				return nil, ctxerr.Wrap(ctx, automdm.ConflictError{
					Message: fmt.Sprintf(automdm.CantAddSoftwareConflictMessage,
						assetMD.Attributes.Name, teamName),
				}, "vpp app conflicts with existing in-house app")
			}
		}

		appID.ValidatedLabels = validatedLabels

		categories, catIDs, err := svc.removeDuplicateOrMissingCategories(ctx, ptr.ValOrZero(teamID), appID.Categories)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "filtering vpp app categories")
		}
		appID.Categories = categories
		appID.CategoryIDs = catIDs
		appID.VPPTokenID = &vppTok.ID
		app = &appFromApple
		app.VPPAppTeam = appID
		// Always set the anchored country on the app payload. For first-adds
		// this populates the new row; for subsequent adds the value is
		// ignored by insertVPPApps (country_code is INSERT-only). For the
		// re-anchor case we explicitly overwrite via UpdateVPPAppCountryCode
		// after insert.
		app.CountryCode = anchor.anchorCountry
	}

	// note that if appID.Configuration is nil, InsertVPPAppWithTeam will ignore it (it will not
	// update or remove it), so here we ignore it too if it is nil.
	if appID.Configuration != nil {
		switch appID.Platform {
		case automdm.IOSPlatform, automdm.IPadOSPlatform:
			var plist string
			if err := json.Unmarshal(appID.Configuration, &plist); err != nil {
				return nil, automdm.NewInvalidArgumentError("configuration", "expected configuration as a JSON string containing the XML")
			}
			if err := automdm.ValidateAppleAppConfiguration([]byte(plist)); err != nil {
				return nil, err
			}
			if err := svc.validateAppConfigCustomHostVitals(ctx, []byte(plist)); err != nil {
				return nil, err
			}
			app.Configuration = []byte(plist)
		}
	}

	addedApp, err := svc.ds.InsertVPPAppWithTeam(ctx, app, teamID, nil)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "writing VPP app to db")
	}
	// If the original anchored country was orphaned (no token left), we
	// re-anchor to the adding team's country. The country_code column is
	// INSERT-only on insertVPPApps, so we explicitly UPDATE here.
	if appID.Platform != automdm.AndroidPlatform && anchor.reAnchor && anchor.anchorCountry != "" {
		if err := svc.ds.UpdateVPPAppCountryCode(ctx, app.AdamID, app.Platform, anchor.anchorCountry); err != nil {
			return nil, ctxerr.Wrap(ctx, err, "re-anchoring vpp app country")
		}
	}
	actLabelsInclAny, actLabelsExclAny, actLabelsInclAll := activitySoftwareLabelsFromValidatedLabels(addedApp.ValidatedLabels)

	// Only log the window when enabled=true; insertVPPAppTeams drops it otherwise, and the activity shouldn't claim stored values that weren't.
	act := automdm.ActivityAddedAppStoreApp{
		AppStoreID:       app.AdamID,
		Platform:         app.Platform,
		TeamName:         &teamName,
		SoftwareTitle:    app.Name,
		SoftwareTitleId:  addedApp.TitleID,
		TeamID:           teamID,
		SelfService:      app.SelfService,
		LabelsIncludeAny: actLabelsInclAny,
		LabelsExcludeAny: actLabelsExclAny,
		LabelsIncludeAll: actLabelsInclAll,
		Configuration:    json.RawMessage(appID.Configuration),
		VersionName:      addedApp.VersionName,
	}
	if vppTok != nil {
		act.VPPToken = &automdm.ActivityVPPToken{ID: vppTok.ID, Location: vppTok.Location, CountryCode: vppTok.CountryCode}
	}
	if appID.AutoUpdateEnabled != nil {
		act.AutoUpdateEnabled = appID.AutoUpdateEnabled
		if *appID.AutoUpdateEnabled {
			act.AutoUpdateStartTime = appID.AutoUpdateStartTime
			act.AutoUpdateEndTime = appID.AutoUpdateEndTime
		}
	}

	if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), act); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "create activity for add app store app")
	}

	if appID.AddAutoInstallPolicy && app.AddedAutomaticInstallPolicy != nil {
		policyAct := createdAutomaticInstallPolicyActivity(app.AddedAutomaticInstallPolicy, &teamName)

		if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), policyAct); err != nil {
			svc.logger.WarnContext(ctx, "failed to create activity for create automatic install policy for app store app", "err", err)
		}

	}

	return addedApp, nil
}

func (svc *Service) getVPPConfig(ctx context.Context) apple_apps.Config {
	return apple_apps.Configure(ctx, svc.ds, svc.config.License.Key, svc.config.MDM.AppleConnectJWT)
}

// getAnchoredVPPAppsMetadata is the anchoring-aware metadata fetcher used by
// the batch (GitOps) path. For each (adam_id, platform) in ids it consults
// resolveAddAnchor to decide which token+storefront to use, bundles adamIDs
// by their resolved (region, secret) so each unique pair produces at most one
// Apple call, and returns the constructed VPPApp slice along with the list
// of (adam_id, platform, country) tuples that need a re-anchor UPDATE after
// insert.
type vppReAnchor struct {
	AdamID      string
	Platform    automdm.InstallableDevicePlatform
	CountryCode string
}

func (svc *Service) getAnchoredVPPAppsMetadata(ctx context.Context, ids []automdm.VPPAppTeam, tokenInfoByID map[uint]vppTokenInfo) ([]*automdm.VPPApp, []vppReAnchor, error) {
	type bundleKey struct {
		region string
		secret string
	}
	type appKey struct {
		adamID   string
		platform automdm.InstallableDevicePlatform
	}
	type perKey struct {
		props  automdm.VPPAppTeam
		anchor vppAddAnchor
	}

	// Anchoring is per (adam_id, platform); different platforms of the same
	// app can have diverged anchor histories. reAnchors is populated only
	// when the corresponding row makes it into apps below, so the country
	// update never runs for a row whose metadata fetch was skipped.
	byKey := make(map[appKey]*perKey)
	var reAnchors []vppReAnchor

	for _, id := range ids {
		key := appKey{adamID: id.AdamID, platform: id.Platform}
		if _, exists := byKey[key]; exists {
			continue
		}
		anchor, err := svc.resolveAddAnchor(ctx, id.AdamID, id.Platform, tokenInfoByID[*id.VPPTokenID])
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "resolving anchor for batch vpp add")
		}
		byKey[key] = &perKey{props: id, anchor: anchor}
	}

	// Bundle adamIDs by (region, secret). Same adamID may appear in multiple
	// bundles if its platforms anchor to different storefronts.
	bundles := make(map[bundleKey]map[string]struct{})
	for k, e := range byKey {
		bk := bundleKey{region: e.anchor.region, secret: e.anchor.fetchSecret}
		if bundles[bk] == nil {
			bundles[bk] = make(map[string]struct{})
		}
		bundles[bk][k.adamID] = struct{}{}
	}

	// Per-bundle fetch. Index results by (bundleKey, adamID) so a row's
	// metadata is matched to the storefront its platform anchored to.
	type metaIndex struct {
		bk     bundleKey
		adamID string
	}
	allMetadata := make(map[metaIndex]apple_apps.Metadata)
	cfg := svc.getVPPConfig(ctx)
	for bk, adamSet := range bundles {
		adamIDs := make([]string, 0, len(adamSet))
		for adamID := range adamSet {
			adamIDs = append(adamIDs, adamID)
		}
		md, err := apple_apps.GetMetadata(adamIDs, bk.region, bk.secret, cfg)
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "fetching VPP asset metadata")
		}
		for adamID, m := range md {
			allMetadata[metaIndex{bk: bk, adamID: adamID}] = m
		}
	}

	var apps []*automdm.VPPApp
	for k, e := range byKey {
		bk := bundleKey{region: e.anchor.region, secret: e.anchor.fetchSecret}
		metadata, ok := allMetadata[metaIndex{bk: bk, adamID: k.adamID}]
		if !ok {
			continue
		}
		retrieved, ok := apple_apps.ToVPPApps(metadata)[k.platform]
		if !ok {
			continue
		}
		// Apple occasionally returns blanks for transiently-degraded apps,
		// so guard against a GitOps apply silently shipping a row with no
		// Name, IconURL, or LatestVersion.
		if retrieved.Name == "" || retrieved.LatestVersion == "" || retrieved.IconURL == "" {
			svc.logger.WarnContext(ctx, "skipping vpp app with empty metadata from Apple",
				"adam_id", k.adamID,
				"platform", k.platform,
				"region", e.anchor.region)
			continue
		}
		props := e.props
		apps = append(apps, &automdm.VPPApp{
			VPPAppTeam: automdm.VPPAppTeam{
				VPPAppID: automdm.VPPAppID{
					AdamID:   k.adamID,
					Platform: k.platform,
				},
				VPPTokenID:          props.VPPTokenID,
				SelfService:         props.SelfService,
				InstallDuringSetup:  props.InstallDuringSetup,
				ValidatedLabels:     props.ValidatedLabels,
				AppTeamID:           props.AppTeamID,
				Categories:          props.Categories,
				CategoryIDs:         props.CategoryIDs,
				DisplayName:         props.DisplayName,
				Configuration:       props.Configuration,
				AutoUpdateEnabled:   props.AutoUpdateEnabled,
				AutoUpdateStartTime: props.AutoUpdateStartTime,
				AutoUpdateEndTime:   props.AutoUpdateEndTime,
			},
			BundleIdentifier: retrieved.BundleIdentifier,
			IconURL:          retrieved.IconURL,
			Name:             retrieved.Name,
			LatestVersion:    retrieved.LatestVersion,
			CountryCode:      e.anchor.anchorCountry,
		})
		if e.anchor.reAnchor {
			reAnchors = append(reAnchors, vppReAnchor{
				AdamID:      k.adamID,
				Platform:    k.platform,
				CountryCode: e.anchor.anchorCountry,
			})
		}
	}

	return apps, reAnchors, nil
}

func (svc *Service) UpdateAppStoreApp(ctx context.Context, titleID uint, teamID *uint, payload automdm.AppStoreAppUpdatePayload) (*automdm.VPPAppStoreApp, *automdm.ActivityEditedAppStoreApp, error) {
	if err := svc.authz.Authorize(ctx, &automdm.VPPApp{TeamID: teamID}, automdm.ActionWrite); err != nil {
		return nil, nil, err
	}

	// If there's an auto-update config, validate it.
	// Note that applying this config is done in a separate service method.
	schedule := automdm.SoftwareAutoUpdateSchedule{
		SoftwareAutoUpdateConfig: automdm.SoftwareAutoUpdateConfig{
			AutoUpdateEnabled:   payload.AutoUpdateEnabled,
			AutoUpdateStartTime: payload.AutoUpdateStartTime,
			AutoUpdateEndTime:   payload.AutoUpdateEndTime,
		},
	}

	if payload.AutoUpdateEnabled != nil && *payload.AutoUpdateEnabled {
		if err := schedule.WindowIsValid(); err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "UpdateAppStoreApp: validating auto-update schedule")
		}
	}

	var teamName string
	if teamID != nil && *teamID != 0 {
		tm, err := svc.ds.TeamLite(ctx, *teamID)
		if automdm.IsNotFound(err) {
			return nil, nil, automdm.NewInvalidArgumentError("team_id", fmt.Sprintf("team %d does not exist", *teamID)).
				WithStatus(http.StatusNotFound)
		} else if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "UpdateAppStoreApp: checking if team exists")
		}

		teamName = tm.Name
	}

	var validatedLabels *automdm.LabelIdentsWithScope
	if payload.LabelsExcludeAny != nil || payload.LabelsIncludeAny != nil || payload.LabelsIncludeAll != nil {
		var err error
		validatedLabels, err = ValidateSoftwareLabels(ctx, svc, teamID, payload.LabelsIncludeAny, payload.LabelsExcludeAny, payload.LabelsIncludeAll)
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "UpdateAppStoreApp: validating software labels")
		}
	}

	versions, err := svc.ds.GetAppStoreAppVersionsByTeamAndTitleID(ctx, ptr.ValOrZero(teamID), titleID)
	if err != nil {
		return nil, nil, ctxerr.Wrap(ctx, err, "UpdateAppStoreApp: getting vpp app metadata")
	}
	// Require a version id when the app has more than one version so an edit can't change the wrong version
	if payload.VersionID == nil && len(versions) > 1 {
		return nil, nil, &automdm.BadRequestError{Message: "Couldn't edit. version_id is required when the app has more than one version in this team."}
	}
	var meta *automdm.VPPAppStoreApp
	for _, version := range versions {
		if payload.VersionID == nil || version.VPPAppsTeamsID == *payload.VersionID {
			meta = version
			break
		}
	}
	if meta == nil {
		return nil, nil, ctxerr.Wrapf(ctx, &notFoundError{}, "app store app version %d does not belong to this title and team", ptr.ValOrZero(payload.VersionID))
	}

	versionName := meta.VersionName
	if payload.VersionName != nil {
		versionName = strings.TrimSpace(*payload.VersionName)
		if versionName == "" {
			return nil, nil, automdm.NewInvalidArgumentError("name", "Couldn't edit. The version name can't be empty.")
		}
		if utf8.RuneCountInString(versionName) > automdm.MaxAppStoreAppVersionNameLength {
			return nil, nil, automdm.NewInvalidArgumentError("name", fmt.Sprintf("Couldn't edit. The version name can't be longer than %d characters.", automdm.MaxAppStoreAppVersionNameLength))
		}
	}

	if payload.DisplayName != nil && *payload.DisplayName != meta.DisplayName {
		trimmed := strings.TrimSpace(*payload.DisplayName)
		if trimmed == "" && *payload.DisplayName != "" {
			return nil, nil, automdm.NewInvalidArgumentError("display_name", "Cannot have a display name that is all whitespace.")
		}

		*payload.DisplayName = trimmed
	}

	selfServiceVal := meta.SelfService
	if payload.SelfService != nil && meta.Platform != automdm.AndroidPlatform {
		selfServiceVal = *payload.SelfService
	}
	if meta.Platform == automdm.MacOSPlatform {
		payload.Configuration = nil
	}

	// datastoreConfig holds the decoded plist for iOS/iPadOS; payload.Configuration stays in its incoming form for the activity below.
	datastoreConfig := payload.Configuration
	if payload.Configuration != nil && (meta.Platform == automdm.IOSPlatform || meta.Platform == automdm.IPadOSPlatform) {
		var plist string
		if err := json.Unmarshal(payload.Configuration, &plist); err != nil {
			return nil, nil, automdm.NewInvalidArgumentError("configuration", "expected configuration as a JSON string containing the XML")
		}
		if err := automdm.ValidateAppleAppConfiguration([]byte(plist)); err != nil {
			return nil, nil, err
		}
		if err := svc.validateAppConfigCustomHostVitals(ctx, []byte(plist)); err != nil {
			return nil, nil, err
		}
		datastoreConfig = []byte(plist)
	}

	appToWrite := &automdm.VPPApp{
		VPPAppTeam: automdm.VPPAppTeam{
			VPPAppID: automdm.VPPAppID{
				AdamID: meta.AdamID, Platform: meta.Platform,
			},
			VersionName:         versionName,
			SelfService:         selfServiceVal,
			ValidatedLabels:     validatedLabels,
			DisplayName:         payload.DisplayName,
			Configuration:       datastoreConfig,
			AutoUpdateEnabled:   payload.AutoUpdateEnabled,
			AutoUpdateStartTime: payload.AutoUpdateStartTime,
			AutoUpdateEndTime:   payload.AutoUpdateEndTime,
		},
		TeamID:           teamID,
		TitleID:          titleID,
		BundleIdentifier: meta.BundleIdentifier,
		Name:             meta.Name,
		LatestVersion:    meta.LatestVersion,
	}
	if meta.IconURL != nil {
		appToWrite.IconURL = *meta.IconURL
	}

	if payload.Categories != nil {
		categories, catIDs, err := svc.removeDuplicateOrMissingCategories(ctx, ptr.ValOrZero(teamID), payload.Categories)
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "filtering vpp app categories")
		}
		payload.Categories = categories
		appToWrite.CategoryIDs = catIDs
	}

	// check if labels have changed
	var existingLabels automdm.LabelIdentsWithScope
	switch {
	case len(meta.LabelsExcludeAny) > 0:
		existingLabels.LabelScope = automdm.LabelScopeExcludeAny
		existingLabels.ByName = make(map[string]automdm.LabelIdent, len(meta.LabelsExcludeAny))
		for _, l := range meta.LabelsExcludeAny {
			existingLabels.ByName[l.LabelName] = automdm.LabelIdent{LabelName: l.LabelName, LabelID: l.LabelID}
		}

	case len(meta.LabelsIncludeAny) > 0:
		existingLabels.LabelScope = automdm.LabelScopeIncludeAny
		existingLabels.ByName = make(map[string]automdm.LabelIdent, len(meta.LabelsIncludeAny))
		for _, l := range meta.LabelsIncludeAny {
			existingLabels.ByName[l.LabelName] = automdm.LabelIdent{LabelName: l.LabelName, LabelID: l.LabelID}
		}

	case len(meta.LabelsIncludeAll) > 0:
		existingLabels.LabelScope = automdm.LabelScopeIncludeAll
		existingLabels.ByName = make(map[string]automdm.LabelIdent, len(meta.LabelsIncludeAll))
		for _, l := range meta.LabelsIncludeAll {
			existingLabels.ByName[l.LabelName] = automdm.LabelIdent{LabelName: l.LabelName, LabelID: l.LabelID}
		}
	}
	var labelsChanged bool
	if validatedLabels != nil {
		labelsChanged = !validatedLabels.Equal(&existingLabels)
	}

	// Get the hosts that are NOT in label scope currently (before the update happens)
	var hostsNotInScope map[uint]struct{}
	if labelsChanged {
		hostsNotInScope, err = svc.ds.GetExcludedHostIDMapForVPPApp(ctx, meta.VPPAppsTeamsID)
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "UpdateAppStoreApp: getting hosts not in scope for vpp app")
		}
	}

	// note that if appID.Configuration is nil, InsertVPPAppWithTeam will ignore it (it will not
	// update or remove it), so here we ignore it too if it is nil.
	if payload.Configuration != nil && meta.Platform == automdm.AndroidPlatform {
		if strings.HasPrefix(meta.AdamID, automdm.AndroidWebAppPrefix) {
			return nil, nil, automdm.NewInvalidArgumentError("configuration", "Couldn't edit. Android web apps don't support configurations.")
		}

		if err := svc.validateAppConfigCustomHostVitals(ctx, payload.Configuration); err != nil {
			return nil, nil, err
		}
	}

	actVPPToken, err := automdm.ActivityVPPTokenByID(ctx, svc.ds, meta.VPPTokenID)
	if err != nil {
		return nil, nil, ctxerr.Wrap(ctx, err, "getting vpp token for edited activity")
	}

	// Update the app
	_, err = svc.ds.InsertVPPAppWithTeam(ctx, appToWrite, teamID, &meta.VPPAppsTeamsID)
	if err != nil {
		return nil, nil, ctxerr.Wrap(ctx, err, "UpdateAppStoreApp: write app to db")
	}

	if labelsChanged {
		// Get the hosts that are now IN label scope (after the update)
		hostsInScope, err := svc.ds.GetIncludedHostIDMapForVPPApp(ctx, meta.VPPAppsTeamsID)
		if err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "UpdateAppStoreApp: getting hosts in scope for vpp app")
		}

		var hostsToClear []uint
		for id := range hostsInScope {
			if _, ok := hostsNotInScope[id]; ok {
				// it was not in scope but now it is, so we should clear policy status
				hostsToClear = append(hostsToClear, id)
			}
		}

		// We clear the policy status here because otherwise the policy automation machinery
		// won't pick this up and the software won't install.
		if err := svc.ds.ClearVPPAppAutoInstallPolicyStatusForHosts(ctx, meta.VPPAppsTeamsID, hostsToClear); err != nil {
			return nil, nil, ctxerr.Wrap(ctx, err, "failed to clear auto install policy status for host")
		}
	}

	// Read the edited version again so the response and the activity show its stored values
	updatedVersions, err := svc.ds.GetAppStoreAppVersionsByTeamAndTitleID(ctx, ptr.ValOrZero(teamID), titleID)
	if err != nil {
		return nil, nil, ctxerr.Wrap(ctx, err, "UpdateAppStoreApp: getting updated app metadata")
	}
	var updatedAppMeta *automdm.VPPAppStoreApp
	for _, version := range updatedVersions {
		if version.VPPAppsTeamsID == meta.VPPAppsTeamsID {
			updatedAppMeta = version
		}
	}
	if updatedAppMeta == nil {
		return nil, nil, ctxerr.Wrapf(ctx, &notFoundError{}, "app store app version %d was not found after the update", meta.VPPAppsTeamsID)
	}
	if updatedAppMeta.VPPTokenID != nil {
		var tok *automdm.VPPTokenDB
		tok, err = svc.ds.GetVPPToken(ctx, *updatedAppMeta.VPPTokenID)
		if err != nil && !automdm.IsNotFound(err) {
			return nil, nil, ctxerr.Wrap(ctx, err, "UpdateAppStoreApp: getting vpp token")
		}
		if tok != nil {
			updatedAppMeta.VPPToken = &automdm.VPPTokenSummary{ID: tok.ID, OrgName: tok.OrgName, Location: tok.Location, CountryCode: tok.CountryCode}
		}
	}

	// Build the activity's labels and configuration from the pre-update read and the request, not from
	// the re-read above (which can hit a lagging replica): a partial edit keeps the stored values.
	actLabelsInclAny, actLabelsExclAny, actLabelsInclAll := activitySoftwareLabelsFromSoftwareScopeLabels(meta.LabelsIncludeAny, meta.LabelsExcludeAny, meta.LabelsIncludeAll)
	if validatedLabels != nil {
		actLabelsInclAny, actLabelsExclAny, actLabelsInclAll = activitySoftwareLabelsFromValidatedLabels(validatedLabels)
	}
	actConfiguration := payload.Configuration
	if actConfiguration == nil && len(meta.Configuration) > 0 {
		actConfiguration = meta.Configuration
		switch meta.Platform {
		case automdm.IOSPlatform, automdm.IPadOSPlatform:
			wrapped, err := json.Marshal(string(meta.Configuration))
			if err != nil {
				return nil, nil, ctxerr.Wrap(ctx, err, "wrapping configuration for activity")
			}
			actConfiguration = wrapped
		}
	}

	act := automdm.ActivityEditedAppStoreApp{
		VPPToken:            actVPPToken,
		TeamName:            &teamName,
		TeamID:              teamID,
		SelfService:         selfServiceVal,
		SoftwareTitleID:     titleID,
		SoftwareTitle:       meta.Name,
		AppStoreID:          meta.AdamID,
		Platform:            meta.Platform,
		LabelsIncludeAny:    actLabelsInclAny,
		LabelsExcludeAny:    actLabelsExclAny,
		LabelsIncludeAll:    actLabelsInclAll,
		SoftwareIconURL:     meta.IconURL,
		SoftwareDisplayName: updatedAppMeta.DisplayName,
		Configuration:       actConfiguration,
	}

	act.VersionName = updatedAppMeta.VersionName
	if updatedAppMeta.AutoUpdateEnabled != nil {
		act.AutoUpdateEnabled = updatedAppMeta.AutoUpdateEnabled
		if *updatedAppMeta.AutoUpdateEnabled {
			act.AutoUpdateStartTime = updatedAppMeta.AutoUpdateStartTime
			act.AutoUpdateEndTime = updatedAppMeta.AutoUpdateEndTime
		}
	}

	// Wrap iOS / iPadOS plist as a JSON string for the response.
	if len(updatedAppMeta.Configuration) > 0 {
		switch updatedAppMeta.Platform {
		case automdm.IOSPlatform, automdm.IPadOSPlatform:
			wrapped, err := json.Marshal(string(updatedAppMeta.Configuration))
			if err != nil {
				return nil, nil, ctxerr.Wrap(ctx, err, "wrapping configuration for response")
			}
			updatedAppMeta.Configuration = wrapped
		}
	}

	return updatedAppMeta, &act, nil
}

func (svc *Service) UploadVPPToken(ctx context.Context, token io.ReadSeeker) (*automdm.VPPTokenDB, error) {
	if err := svc.authz.Authorize(ctx, &automdm.AppleCSR{}, automdm.ActionWrite); err != nil {
		return nil, err
	}

	privateKey := svc.config.Server.PrivateKey
	if testSetEmptyPrivateKey {
		privateKey = ""
	}

	if len(privateKey) == 0 {
		return nil, ctxerr.New(ctx, "Couldn't add content token. Missing required private key. Learn how to configure the private key here: https://automdm.com/learn-more-about/automdm-server-private-key")
	}

	if token == nil {
		return nil, ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("token", "Invalid token. Please provide a valid content token from Apple Business."))
	}

	tokenBytes, err := io.ReadAll(token)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "reading VPP token")
	}

	clientCfg, err := vpp.GetConfig(ctx, string(tokenBytes))
	if err != nil {
		var vppErr *vpp.ErrorResponse
		if errors.As(err, &vppErr) {
			// Per https://developer.apple.com/documentation/devicemanagement/app_and_book_management/app_and_book_management_legacy/interpreting_error_codes
			if vppErr.ErrorNumber == 9622 {
				return nil, automdm.NewInvalidArgumentError("token", "Invalid token. Please provide a valid content token from Apple Business.")
			}
		}
		return nil, ctxerr.Wrap(ctx, err, "validating VPP token with Apple")
	}
	if clientCfg.CountryCode == "" {
		return nil, automdm.NewInvalidArgumentError("token", "Invalid token. Could not determine the storefront country from Apple Business.")
	}

	data := automdm.VPPTokenData{
		Token:       string(tokenBytes),
		Location:    clientCfg.LocationName,
		CountryCode: clientCfg.CountryCode,
	}

	tok, err := svc.ds.InsertVPPToken(ctx, &data)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "writing VPP token to db")
	}

	if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityEnabledVPP{
		Location: clientCfg.LocationName,
	}); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "create activity for upload VPP token")
	}

	return tok, nil
}

func (svc *Service) UpdateVPPToken(ctx context.Context, tokenID uint, token io.ReadSeeker) (*automdm.VPPTokenDB, error) {
	if err := svc.authz.Authorize(ctx, &automdm.AppleCSR{}, automdm.ActionWrite); err != nil {
		return nil, err
	}

	privateKey := svc.config.Server.PrivateKey
	if testSetEmptyPrivateKey {
		privateKey = ""
	}

	if len(privateKey) == 0 {
		return nil, ctxerr.New(ctx, "Couldn't add content token. Missing required private key. Learn how to configure the private key here: https://automdm.com/learn-more-about/automdm-server-private-key")
	}

	if token == nil {
		return nil, ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("token", "Invalid token. Please provide a valid content token from Apple Business."))
	}

	tokenBytes, err := io.ReadAll(token)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "reading VPP token")
	}

	clientCfg, err := vpp.GetConfig(ctx, string(tokenBytes))
	if err != nil {
		var vppErr *vpp.ErrorResponse
		if errors.As(err, &vppErr) {
			// Per https://developer.apple.com/documentation/devicemanagement/app_and_book_management/app_and_book_management_legacy/interpreting_error_codes
			if vppErr.ErrorNumber == 9622 {
				return nil, automdm.NewInvalidArgumentError("token", "Invalid token. Please provide a valid content token from Apple Business.")
			}
		}
		return nil, ctxerr.Wrap(ctx, err, "validating VPP token with Apple")
	}
	if clientCfg.CountryCode == "" {
		return nil, automdm.NewInvalidArgumentError("token", "Invalid token. Could not determine the storefront country from Apple Business.")
	}

	data := automdm.VPPTokenData{
		Token:       string(tokenBytes),
		Location:    clientCfg.LocationName,
		CountryCode: clientCfg.CountryCode,
	}

	tok, err := svc.ds.UpdateVPPToken(ctx, tokenID, &data)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "updating vpp token")
	}

	return tok, nil
}

func (svc *Service) UpdateVPPTokenTeams(ctx context.Context, tokenID uint, teamIDs []uint) (*automdm.VPPTokenDB, error) {
	if err := svc.authz.Authorize(ctx, &automdm.AppleCSR{}, automdm.ActionWrite); err != nil {
		return nil, err
	}

	// Add VPP assigns teams right after the upload, before a replica may have the token.
	if _, err := svc.ds.GetVPPToken(ctxdb.RequirePrimary(ctx, true), tokenID); err != nil {
		if automdm.IsNotFound(err) {
			return nil, automdm.NewInvalidArgumentError("id",
				fmt.Sprintf("Couldn't edit. VPP token with ID %d doesn't exist.", tokenID)).WithStatus(http.StatusNotFound)
		}
		return nil, ctxerr.Wrap(ctx, err, "get vpp token")
	}

	if slices.ContainsFunc(teamIDs, func(id uint) bool { return id != 0 }) {
		teams, err := svc.ds.TeamsSummary(ctx)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "get teams summary")
		}
		for _, id := range teamIDs {
			if id != 0 && !slices.ContainsFunc(teams, func(t *automdm.TeamSummary) bool { return t.ID == id }) {
				return nil, automdm.NewInvalidArgumentError("teams",
					fmt.Sprintf("Couldn't edit. AutoMDM with ID %d doesn't exist.", id)).WithStatus(http.StatusNotFound)
			}
		}
	}

	tok, err := svc.ds.UpdateVPPTokenTeams(ctx, tokenID, teamIDs)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "updating vpp token team")
	}

	return tok, nil
}

func (svc *Service) GetVPPTokens(ctx context.Context) ([]*automdm.VPPTokenDB, error) {
	// The Add software > App Store picker reads the token list, so this must be
	// readable by the same roles that can read App Store apps
	// (automdm.VPPApp/installable_entity), not just admins. Global roles can read
	// every token; team-scoped users can only read tokens assigned to a team
	// where they have read access (plus "All teams" tokens).
	globalRead := svc.authz.Authorize(ctx, &automdm.VPPApp{}, automdm.ActionRead)

	// Collect the teams where a team-scoped user has read access. Left empty for
	// global readers since they can see everything.
	readableTeams := make(map[uint]struct{})
	if globalRead != nil {
		if user := authz.UserFromContext(ctx); user != nil {
			for _, t := range user.Teams {
				teamID := t.ID
				if err := svc.authz.Authorize(ctx, &automdm.VPPApp{TeamID: &teamID}, automdm.ActionRead); err == nil {
					readableTeams[teamID] = struct{}{}
				}
			}
		}

		// No read access globally or on any team: return the forbidden error.
		if len(readableTeams) == 0 {
			return nil, globalRead
		}
	}

	tokens, err := svc.ds.ListVPPTokens(ctx)
	if err != nil {
		return nil, err
	}

	// Global readers get every token unchanged.
	if globalRead == nil {
		return tokens, nil
	}

	filtered := make([]*automdm.VPPTokenDB, 0, len(tokens))
	for _, tok := range tokens {
		// A non-nil, empty Teams slice means the token is assigned to "All
		// teams" and is visible to any user with read access. A nil slice means
		// the token is unassigned and only global readers should see it.
		if tok.Teams != nil && len(tok.Teams) == 0 {
			filtered = append(filtered, tok)
			continue
		}
		for _, tt := range tok.Teams {
			if _, ok := readableTeams[tt.ID]; ok {
				filtered = append(filtered, tok)
				break
			}
		}
	}

	return filtered, nil
}

func (svc *Service) DeleteVPPToken(ctx context.Context, tokenID uint) error {
	if err := svc.authz.Authorize(ctx, &automdm.AppleCSR{}, automdm.ActionWrite); err != nil {
		return err
	}
	tok, err := svc.ds.GetVPPToken(ctx, tokenID)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "getting vpp token")
	}
	if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityDisabledVPP{
		Location: tok.Location,
	}); err != nil {
		return ctxerr.Wrap(ctx, err, "create activity for delete VPP token")
	}

	return svc.ds.DeleteVPPToken(ctx, tokenID)
}

func (svc *Service) CreateAndroidWebApp(ctx context.Context, title, startURL string, icon io.Reader) (string, error) {
	// Authorization for this endpoint is a bit different - basically we want the same
	// write permissions as for App Store apps (automdm.VPPApp struct), but there is no
	// team id available when this endpoint is called, so we allow any team user to
	// call it as long as they have the acceptable role. To achieve this, we grab the
	// first team id from the user's list of teams, if they have a non-global role.
	var teamID *uint
	if user := authz.UserFromContext(ctx); user != nil {
		if len(user.Teams) > 0 {
			teamID = &user.Teams[0].ID
		}
	}

	if err := svc.authz.Authorize(ctx, &automdm.VPPApp{TeamID: teamID}, automdm.ActionWrite); err != nil {
		return "", err
	}

	// title and startURL are required but are already validated during the DecodeRequest implementation.
	if parsedURL, err := url.Parse(startURL); err != nil || !parsedURL.IsAbs() {
		return "", &automdm.BadRequestError{Message: "Couldn't create. The start URL must be a valid absolute URL.", InternalErr: err}
	}

	// icon, if provided, must be a .png file and must be square and at least 512x512 pixels.
	var iconData []byte
	if icon != nil {
		const invalidIconErrMsg = `Couldn't create. The icon must be a PNG file and square, with dimensions of at least 512 x 512px.`

		b, err := io.ReadAll(icon)
		if err != nil {
			return "", &automdm.BadRequestError{Message: invalidIconErrMsg, InternalErr: err}
		}
		iconData = b

		// decoding errors if it is not a valid png
		cfg, err := png.DecodeConfig(bytes.NewReader(iconData))
		if err != nil {
			return "", &automdm.BadRequestError{Message: invalidIconErrMsg, InternalErr: err}
		}

		// check that it is square
		if cfg.Width != cfg.Height {
			return "", &automdm.BadRequestError{Message: invalidIconErrMsg}
		}

		// check minimal size requirement (only needs to test one, as at this point it is square)
		if cfg.Width < 512 {
			return "", &automdm.BadRequestError{Message: invalidIconErrMsg}
		}
	}

	enterprise, err := svc.ds.GetEnterprise(ctx)
	if err != nil {
		return "", ctxerr.Wrap(ctx, err, "get android enterprise")
	}

	webApp := &androidmanagement.WebApp{
		DisplayMode: "STANDALONE", // always standalone for now per spec
		Title:       title,
		StartUrl:    startURL,
	}
	if len(iconData) > 0 {
		// must be the "actual bytes of the image in a base64url encoded string"
		b64Icon := base64.URLEncoding.EncodeToString(iconData)
		webApp.Icons = append(webApp.Icons, &androidmanagement.WebAppIcon{ImageData: b64Icon})
	}
	createdApp, err := svc.androidModule.CreateAndroidWebApp(ctx, enterprise.Name(), webApp)
	if err != nil {
		if androidmgmt.IsBadRequestError(err) {
			return "", &automdm.BadRequestError{Message: "Couldn't create. Please check the provided data and try again.", InternalErr: err}
		}
		return "", ctxerr.Wrap(ctx, err, "creating android web app")
	}

	packageName := strings.TrimPrefix(createdApp.Name, fmt.Sprintf("%s/webApps/", enterprise.Name()))
	if packageName == createdApp.Name || !strings.HasPrefix(packageName, automdm.AndroidWebAppPrefix) {
		// logging this as an error, because the frontend uses the package name to hide some actions
		// not available to WebApps, we must know if somehow android changes how those get named.
		svc.logger.ErrorContext(ctx, "created Android webApp does not have expected package name format", "package_name", createdApp.Name)
	}

	return packageName, nil
}

// Check this many iOS and iPadOS hosts per run, the next run continues after the last one
const appStoreAppConfigurationReconcileBatchSize = 1000

func ReconcileAppStoreAppConfigurations(ctx context.Context, ds automdm.Datastore, vppInstaller automdm.AppleMDMVPPInstaller, retryCounter automdm.SoftwareInstallAttemptCounter,
	logger *slog.Logger,
) error {
	if !license.IsPremium(ctx) {
		return nil
	}
	appConfig, err := ds.AppConfig(ctx)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "reading app config")
	}
	if !appConfig.MDM.EnabledAndConfigured {
		return nil
	}

	// Start from the first host when the cursor can't be read, checking hosts again only costs reads
	cursor, err := ds.GetAppStoreAppConfigurationReconcileCursor(ctx)
	if err != nil {
		logger.WarnContext(ctx, "failed to read app store app configuration reconcile cursor, starting from the first host", "err", err)
		cursor = 0
	}
	hosts, err := ds.ListIOSAndIPadOSMDMHostsAfterID(ctx, cursor, appStoreAppConfigurationReconcileBatchSize)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "list iOS and iPadOS hosts")
	}
	hostIDsByTeamID := make(map[uint][]uint)
	hostByID := make(map[uint]automdm.Host, len(hosts))
	for _, host := range hosts {
		automdmID := ptr.ValOrZero(host.TeamID)
		hostIDsByTeamID[automdmID] = append(hostIDsByTeamID[automdmID], host.ID)
		hostByID[host.ID] = *host
	}

	// Read every team's versions, so a host moved from another team gets an install only when the configuration differs
	platforms := []automdm.InstallableDevicePlatform{automdm.IOSPlatform, automdm.IPadOSPlatform}
	versionsByTitleIDByTeamID, err := ds.GetAppStoreAppVersionsByTeamAndTitle(ctx, platforms)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "get app store app versions")
	}
	versionByID := make(map[uint]*automdm.VPPAppStoreApp)
	for _, versionsByTitleID := range versionsByTitleIDByTeamID {
		for _, versions := range versionsByTitleID {
			for _, version := range versions {
				versionByID[version.VPPAppsTeamsID] = version
			}
		}
	}

	// Each version installs with the token it was added from, so a team's versions on an expired token wait for it to be
	// renewed while its versions on other tokens still go out
	tokens, err := ds.ListVPPTokens(ctx)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "list vpp tokens")
	}
	usableTokenIDs := make(map[uint]struct{}, len(tokens))
	for _, tok := range tokens {
		if time.Now().Before(tok.RenewDate) {
			usableTokenIDs[tok.ID] = struct{}{}
		}
	}

	var installByHostIDByTitleID map[uint]map[uint]automdm.HostVPPInstallWithUpcoming
	for automdmID, hostIDs := range hostIDsByTeamID {
		versionsByTitleID := versionsByTitleIDByTeamID[automdmID]
		if len(versionsByTitleID) == 0 {
			continue
		}
		installByHostIDByTitleID, err = ds.ListHostAppStoreAppInstallVersions(ctx, automdmID, hostIDs, platforms)
		if err != nil {
			logger.ErrorContext(ctx, "list app store app installs for app store app configurations", "team_id", automdmID, "err", err)
			continue
		}

		for titleID, versions := range versionsByTitleID {
			// Log the error and move on to the next title, so one title doesn't stop the others from being reconciled
			err = reconcileAppStoreAppConfigurations(ctx, ds, vppInstaller, retryCounter, logger, automdmID, titleID, versions, versionByID,
				installByHostIDByTitleID[titleID], usableTokenIDs, hostByID)
			if err != nil {
				logger.ErrorContext(ctx, "reconcile app store app configurations", "team_id", automdmID, "title_id", titleID, "err", err)
			}
		}
	}

	// Skip saving the cursor when the run timed out, so the next run checks this batch again
	if ctx.Err() != nil {
		return ctxerr.Wrap(ctx, ctx.Err(), "reconcile app store app configurations")
	}

	// Continue after this batch on the next run, or start from the first host again after the last batch
	var nextCursor uint
	if len(hosts) == appStoreAppConfigurationReconcileBatchSize {
		nextCursor = hosts[len(hosts)-1].ID
	}
	err = ds.SetAppStoreAppConfigurationReconcileCursor(ctx, nextCursor)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "set app store app configuration reconcile cursor")
	}
	return nil
}

func reconcileAppStoreAppConfigurations(ctx context.Context, ds automdm.Datastore, vppInstaller automdm.AppleMDMVPPInstaller, retryCounter automdm.SoftwareInstallAttemptCounter,
	logger *slog.Logger, automdmID uint, titleID uint,
	versions []*automdm.VPPAppStoreApp, versionByID map[uint]*automdm.VPPAppStoreApp, installByHostID map[uint]automdm.HostVPPInstallWithUpcoming, usableTokenIDs map[uint]struct{},
	hostByID map[uint]automdm.Host,
) error {
	var err error

	// Go through the versions in the order they were added, so each host gets the first version it's in scope for
	hostIDsWithoutVersion := slices.Collect(maps.Keys(installByHostID))
	var includedHostIDs map[uint]struct{}
	var hostIDsWithRecentFailedInstall map[uint]struct{}
	var hostIDsToRetry map[uint]struct{}
	var retries int
	var vppApp *automdm.VPPApp
	for _, version := range versions {
		if len(hostIDsWithoutVersion) == 0 {
			break
		}
		// A version without labels includes every host left, so skip the label scope query for it
		if !version.HasLabels {
			includedHostIDs = make(map[uint]struct{}, len(hostIDsWithoutVersion))
			for _, hostID := range hostIDsWithoutVersion {
				includedHostIDs[hostID] = struct{}{}
			}
		} else {
			includedHostIDs, err = ds.GetIncludedHostIDMapForVPPAppHosts(ctx, version.VPPAppsTeamsID, hostIDsWithoutVersion)
			if err != nil {
				return ctxerr.Wrapf(ctx, err, "get hosts in scope of app store app version %d", version.VPPAppsTeamsID)
			}
		}
		hostIDsWithoutVersion = slices.DeleteFunc(hostIDsWithoutVersion, func(hostID uint) bool {
			_, included := includedHostIDs[hostID]
			return included
		})
		// Hosts in scope for a version on an expired or deleted token keep that version and get nothing until it's renewed
		if version.VPPTokenID == nil {
			continue
		}
		if _, usable := usableTokenIDs[*version.VPPTokenID]; !usable {
			continue
		}

		// Collect the hosts whose latest install didn't send this version's configuration
		var hostIDs []uint
		for hostID := range includedHostIDs {
			install := installByHostID[hostID]
			// Leave the host to its upcoming install of this version, which reads the current configuration when it activates
			if ptr.Equal(install.UpcomingVPPAppTeamID, &version.VPPAppsTeamsID) {
				continue
			}
			switch {
			case !ptr.Equal(install.VPPAppTeamID, &version.VPPAppsTeamsID):
				// Re-send when the host's latest install is of another version, unless both versions have the same configuration
				if install.VPPAppTeamID != nil {
					installedVersion, found := versionByID[*install.VPPAppTeamID]
					if found && !install.NeedsResend && bytes.Equal(installedVersion.Configuration, version.Configuration) {
						// Skip the host when the installed version's configuration didn't change after the install
						if installedVersion.ConfigurationUpdatedAt == nil || installedVersion.ConfigurationUpdatedAt.Before(install.CreatedAt) {
							continue
						}
					}
				}
				hostIDs = append(hostIDs, hostID)
			case install.NeedsResend:
				// Re-send when an AutoMDM variable the configuration uses changed
				hostIDs = append(hostIDs, hostID)
			case version.ConfigurationUpdatedAt != nil && install.CreatedAt.Before(*version.ConfigurationUpdatedAt):
				// Re-send when the configuration changed after the install, counting an install in the same second as before the change
				hostIDs = append(hostIDs, hostID)
			}
		}
		if len(hostIDs) == 0 {
			continue
		}

		// Wait before retrying an install of this version that failed after its configuration last changed, counting a failure in the same second as the change as after it
		failedSince := time.Now().Add(-automdm.VPPAutomaticInstallWaitSeconds * time.Second)
		if version.ConfigurationUpdatedAt != nil && version.ConfigurationUpdatedAt.After(failedSince) {
			failedSince = version.ConfigurationUpdatedAt.Truncate(time.Second)
		}
		hostIDsWithRecentFailedInstall, err = ds.GetHostIDsWithRecentFailedVPPAppInstall(ctx, version.VPPAppsTeamsID, failedSince, hostIDs)
		if err != nil {
			return ctxerr.Wrapf(ctx, err, "get hosts with a recent failed install of app store app version %d", version.VPPAppsTeamsID)
		}

		// A send to a host whose install of this version failed in the retry timeout window, after the configuration last changed, is a retry
		retriedSince := time.Now().Add(-automdm.VPPAutomaticInstallRetryTimeout)
		if version.ConfigurationUpdatedAt != nil && version.ConfigurationUpdatedAt.After(retriedSince) {
			retriedSince = version.ConfigurationUpdatedAt.Truncate(time.Second)
		}
		hostIDsToRetry, err = ds.GetHostIDsWithRecentFailedVPPAppInstall(ctx, version.VPPAppsTeamsID, retriedSince, hostIDs)
		if err != nil {
			return ctxerr.Wrapf(ctx, err, "get hosts with a failed install of app store app version %d", version.VPPAppsTeamsID)
		}

		// Skip a version deleted since the versions were read
		vppApp, err = ds.GetVPPAppByTeamAndTitleID(ctx, &automdmID, titleID, version.VPPAppsTeamsID)
		if err != nil {
			if automdm.IsNotFound(err) {
				continue
			}
			return ctxerr.Wrapf(ctx, err, "get app store app version %d", version.VPPAppsTeamsID)
		}
		for _, hostID := range hostIDs {
			if _, failed := hostIDsWithRecentFailedInstall[hostID]; failed {
				continue
			}
			// Count each retry, and stop retrying the host after MaxVPPAutomaticInstallRetries until the count expires
			_, isRetry := hostIDsToRetry[hostID]
			if isRetry && retryCounter != nil {
				retries, err = retryCounter.CountAttempts(ctx, hostID, version.VPPAppsTeamsID)
				if err != nil {
					logger.WarnContext(ctx, "failed to count app store app configuration retries", "host_id", hostID, "vpp_app_team_id", version.VPPAppsTeamsID, "err", err)
				}
				if retries >= automdm.MaxVPPAutomaticInstallRetries {
					continue
				}
				// Record the retry before queueing the VPP install, since the MDM command result and install
				// verification can't tell it was a configuration resend
				_, err = retryCounter.RecordAttempt(ctx, hostID, version.VPPAppsTeamsID, automdm.VPPAutomaticInstallRetryTimeout)
				if err != nil {
					logger.WarnContext(ctx, "failed to record app store app configuration retry", "host_id", hostID, "vpp_app_team_id", version.VPPAppsTeamsID, "err", err)
				}
			}
			host, ok := hostByID[hostID]
			if !ok {
				continue
			}
			_, err = vppInstaller.InstallVPPAppPostValidation(ctx, &host, vppApp, automdm.HostSoftwareInstallOptions{
				ForConfigurationResend: true,
			})
			if err != nil {
				logger.WarnContext(ctx, "failed to send app store app configuration", "host_id", hostID, "vpp_app_team_id", version.VPPAppsTeamsID, "err", err)
				// Record a failed install even though no attempt was made, so the next cron run waits 1 hour before retrying.
				_, _, err = ds.RecordFailedVPPAppInstall(ctx, hostID, vppApp.VPPAppID, uuid.NewString(), err.Error(), automdm.HostSoftwareInstallOptions{
					VPPAppTeamID:           vppApp.AppTeamID,
					ForConfigurationResend: true,
				})
				if err != nil {
					logger.WarnContext(ctx, "failed to record failed app store app configuration install", "host_id", hostID, "vpp_app_team_id", version.VPPAppsTeamsID, "err", err)
				}
			}
		}
	}
	return nil
}

func (svc *Service) validateAppConfigCustomHostVitals(ctx context.Context, config []byte) error {
	if err := svc.ds.ValidateReferencedCustomHostVitals(ctx, []string{string(config)}); err != nil {
		if !automdm.IsInvalidReferencedCustomHostVitalsError(err) {
			return ctxerr.Wrap(ctx, err, "validating referenced custom host vitals")
		}
		return ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("configuration", err.Error()))
	}
	return nil
}
