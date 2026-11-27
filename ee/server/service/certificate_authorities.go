package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/anilyagiz/automdm/v4/ee/server/service/scep"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/variables"
)

func (svc *Service) GetCertificateAuthority(ctx context.Context, id uint) (*automdm.CertificateAuthority, error) {
	if err := svc.authz.Authorize(ctx, &automdm.CertificateAuthority{}, automdm.ActionRead); err != nil {
		return nil, err
	}

	ca, err := svc.ds.GetCertificateAuthorityByID(ctx, id, false)
	if err != nil {
		return nil, err
	}

	return ca, nil
}

func (svc *Service) ListCertificateAuthorities(ctx context.Context) ([]*automdm.CertificateAuthoritySummary, error) {
	if err := svc.authz.Authorize(ctx, &automdm.CertificateAuthority{}, automdm.ActionList); err != nil {
		return nil, err
	}
	cas, err := svc.ds.ListCertificateAuthorities(ctx)
	if err != nil {
		return nil, err
	}

	return cas, nil
}

func (svc *Service) NewCertificateAuthority(ctx context.Context, p automdm.CertificateAuthorityPayload) (*automdm.CertificateAuthority, error) {
	if err := svc.authz.Authorize(ctx, &automdm.CertificateAuthority{}, automdm.ActionWrite); err != nil {
		return nil, err
	}

	errPrefix := "Couldn't add certificate authority. "

	if err := svc.validatePayload(&p, errPrefix); err != nil {
		return nil, err
	}

	caToCreate := &automdm.CertificateAuthority{}

	var activity automdm.ActivityDetails

	if p.DigiCert != nil {
		p.DigiCert.Preprocess()
		if err := svc.validateDigicert(ctx, p.DigiCert, errPrefix); err != nil {
			return nil, err
		}
		caToCreate.Type = string(automdm.CATypeDigiCert)
		caToCreate.Name = &p.DigiCert.Name
		caToCreate.URL = &p.DigiCert.URL
		caToCreate.APIToken = &p.DigiCert.APIToken
		caToCreate.ProfileID = ptr.String(p.DigiCert.ProfileID)
		caToCreate.CertificateCommonName = &p.DigiCert.CertificateCommonName
		caToCreate.CertificateUserPrincipalNames = &p.DigiCert.CertificateUserPrincipalNames
		caToCreate.CertificateSeatID = &p.DigiCert.CertificateSeatID
		activity = automdm.ActivityAddedDigiCert{Name: p.DigiCert.Name}
	}

	if p.Hydrant != nil {
		p.Hydrant.Preprocess()

		if err := svc.validateHydrant(ctx, p.Hydrant, errPrefix); err != nil {
			return nil, err
		}

		caToCreate.Type = string(automdm.CATypeHydrant)
		caToCreate.Name = &p.Hydrant.Name
		caToCreate.URL = &p.Hydrant.URL
		caToCreate.ClientID = &p.Hydrant.ClientID
		caToCreate.ClientSecret = &p.Hydrant.ClientSecret
		activity = automdm.ActivityAddedHydrant{Name: p.Hydrant.Name}
	}

	if p.CustomESTProxy != nil {
		p.CustomESTProxy.Preprocess()

		if err := svc.validateEST(ctx, p.CustomESTProxy, errPrefix); err != nil {
			return nil, err
		}

		caToCreate.Type = string(automdm.CATypeCustomESTProxy)
		caToCreate.Name = &p.CustomESTProxy.Name
		caToCreate.URL = &p.CustomESTProxy.URL
		caToCreate.Username = &p.CustomESTProxy.Username
		caToCreate.Password = &p.CustomESTProxy.Password
		activity = automdm.ActivityAddedCustomESTProxy{Name: p.CustomESTProxy.Name}
	}

	if p.NDESSCEPProxy != nil {
		// The NDES CA is always named NDES, so this is checked by type. Otherwise a second NDES CA would get the error for a name in use.
		cas, err := svc.ds.ListCertificateAuthorities(ctx)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(cas, func(ca *automdm.CertificateAuthoritySummary) bool {
			return ca.Type == string(automdm.CATypeNDESSCEPProxy)
		}) {
			return nil, &automdm.BadRequestError{Message: errPrefix + "Only a single NDES CA can be added."}
		}

		p.NDESSCEPProxy.Preprocess()

		if err := svc.validateNDESSCEPProxy(ctx, p.NDESSCEPProxy, errPrefix); err != nil {
			return nil, err
		}

		caToCreate.Name = ptr.String("NDES")
		caToCreate.Type = string(automdm.CATypeNDESSCEPProxy)
		caToCreate.URL = &p.NDESSCEPProxy.URL
		caToCreate.AdminURL = ptr.String(p.NDESSCEPProxy.AdminURL)
		caToCreate.Username = ptr.String(p.NDESSCEPProxy.Username)
		caToCreate.Password = &p.NDESSCEPProxy.Password
		activity = automdm.ActivityAddedNDESSCEPProxy{}
	}

	if p.CustomSCEPProxy != nil {
		p.CustomSCEPProxy.Preprocess()

		if err := svc.validateCustomSCEPProxy(ctx, p.CustomSCEPProxy, errPrefix); err != nil {
			return nil, err
		}

		caToCreate.Type = string(automdm.CATypeCustomSCEPProxy)
		caToCreate.Name = &p.CustomSCEPProxy.Name
		caToCreate.URL = &p.CustomSCEPProxy.URL
		caToCreate.Challenge = &p.CustomSCEPProxy.Challenge
		activity = automdm.ActivityAddedCustomSCEPProxy{Name: p.CustomSCEPProxy.Name}
	}

	if p.Smallstep != nil {
		p.Smallstep.Preprocess()

		if err := svc.validateSmallstepSCEPProxy(ctx, p.Smallstep, errPrefix); err != nil {
			return nil, err
		}

		caToCreate.Type = string(automdm.CATypeSmallstep)
		caToCreate.Name = &p.Smallstep.Name
		caToCreate.URL = &p.Smallstep.URL
		caToCreate.ChallengeURL = &p.Smallstep.ChallengeURL
		caToCreate.Username = &p.Smallstep.Username
		caToCreate.Password = &p.Smallstep.Password
		activity = automdm.ActivityAddedSmallstep{Name: p.Smallstep.Name}
	}

	if p.EJBCA != nil {
		p.EJBCA.Preprocess()
		if err := svc.validateEJBCA(ctx, p.EJBCA, errPrefix); err != nil {
			return nil, err
		}
		caToCreate = p.EJBCA.CertificateAuthority()
		activity = automdm.ActivityAddedEJBCA{Name: p.EJBCA.Name}
	}

	if p.GoogleCAS != nil {
		p.GoogleCAS.Preprocess()
		if err := svc.validateGoogleCAS(ctx, p.GoogleCAS, errPrefix); err != nil {
			return nil, err
		}
		caToCreate = googleCASToCertificateAuthority(*p.GoogleCAS)
		activity = automdm.ActivityAddedGoogleCAS{Name: p.GoogleCAS.Name}
	}

	createdCA, err := svc.ds.NewCertificateAuthority(ctx, caToCreate)
	if err != nil {
		return nil, err
	}

	if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), activity); err != nil {
		return nil, fmt.Errorf("recording activity for new %s certificate authority %s: %w", caToCreate.Type, *caToCreate.Name, err)
	}

	return createdCA, nil
}

func (svc *Service) validatePayload(p *automdm.CertificateAuthorityPayload, errPrefix string) error {
	casToCreate := 0
	if p.DigiCert != nil {
		casToCreate++
	}
	if p.Hydrant != nil {
		casToCreate++
	}
	if p.NDESSCEPProxy != nil {
		casToCreate++
	}
	if p.CustomSCEPProxy != nil {
		casToCreate++
	}
	if p.Smallstep != nil {
		casToCreate++
	}
	if p.CustomESTProxy != nil {
		casToCreate++
	}
	if p.EJBCA != nil {
		casToCreate++
	}
	if p.GoogleCAS != nil {
		casToCreate++
	}
	if casToCreate == 0 {
		return &automdm.BadRequestError{Message: fmt.Sprintf("%sA certificate authority must be specified", errPrefix)}
	}
	if casToCreate > 1 {
		return &automdm.BadRequestError{Message: fmt.Sprintf("%sOnly one certificate authority can be created at a time", errPrefix)}
	}

	if len(svc.config.Server.PrivateKey) == 0 {
		return &automdm.BadRequestError{Message: fmt.Sprintf("%sPrivate key must be configured. Learn more: https://automdm.com/learn-more-about/automdm-server-private-key", errPrefix)}
	}
	return nil
}

func (svc *Service) validateDigicert(ctx context.Context, digicertCA *automdm.DigiCertCA, errPrefix string) error {
	if err := validateURL(digicertCA.URL, "DigiCert", errPrefix); err != nil {
		return err
	}
	if digicertCA.APIToken == "" || digicertCA.APIToken == automdm.MaskedPassword {
		return automdm.NewInvalidArgumentError("api_token", fmt.Sprintf("%sInvalid API token. Please correct and try again.", errPrefix))
	}
	if digicertCA.ProfileID == "" {
		return automdm.NewInvalidArgumentError("profile_id", fmt.Sprintf("%sInvalid profile GUID. Please correct and try again.", errPrefix))
	}
	if err := validateCAName(digicertCA.Name, errPrefix); err != nil {
		return err
	}
	if err := validateDigicertCACN(digicertCA.CertificateCommonName, errPrefix); err != nil {
		return err
	}
	if err := validateDigicertUserPrincipalNames(digicertCA.CertificateUserPrincipalNames, errPrefix); err != nil {
		return err
	}
	if err := validateDigicertSeatID(digicertCA.CertificateSeatID, errPrefix); err != nil {
		return err
	}

	if err := svc.digiCertService.VerifyProfileID(ctx, *digicertCA); err != nil {
		svc.logger.ErrorContext(ctx, "Failed to validate DigiCert profile GUID", "err", err)
		return &automdm.BadRequestError{Message: fmt.Sprintf("%sCould not verify DigiCert profile ID: %s. Please correct and try again.", errPrefix, err.Error())}
	}
	return nil
}

func validateCAName(name string, errPrefix string) error {
	// This is used by NDES itself which doesn't have a name the user can set so we must reserve it. Names are unique without regard to case.
	if strings.EqualFold(name, "NDES") {
		return automdm.NewInvalidArgumentError("name", fmt.Sprintf("%sCA name cannot be NDES", errPrefix))
	}
	if len(name) == 0 {
		return automdm.NewInvalidArgumentError("name", fmt.Sprintf("%sCA name cannot be empty", errPrefix))
	}
	if len(name) > 255 {
		return automdm.NewInvalidArgumentError("name", fmt.Sprintf("%sCA name cannot be longer than 255 characters", errPrefix))
	}
	if !isAlphanumeric(name) {
		return automdm.NewInvalidArgumentError("name", fmt.Sprintf("%sInvalid characters in the \"name\" field. Only letters, numbers and underscores allowed.", errPrefix))
	}
	return nil
}

func validateDigicertCACN(cn string, errPrefix string) error {
	if len(strings.TrimSpace(cn)) == 0 {
		return automdm.NewInvalidArgumentError("certificate_common_name", fmt.Sprintf("%sCA Common Name (CN) cannot be empty", errPrefix))
	}
	return validateCAAutoMDMVariables("certificate_common_name", "CA Common Name (CN)", cn, automdm.AutoMDMVarsSupportedInDigiCert, errPrefix)
}

// validateCAAutoMDMVariables rejects an AutoMDM variable in the value of a CA field that is not one of the allowed ones.
func validateCAAutoMDMVariables(field, where, value string, allowed []automdm.AutoMDMVarName, errPrefix string) error {
	automdmVars := variables.Find(value)
	if err := automdm.FindRemovedAutoMDMVariable(automdmVars...); err != nil {
		return automdm.NewInvalidArgumentError(field, errPrefix+err.Error())
	}
	for _, automdmVar := range automdmVars {
		if !slices.Contains(allowed, automdm.AutoMDMVarName(automdmVar)) {
			return automdm.NewInvalidArgumentError(field, fmt.Sprintf("%sAUTOMDM_VAR_%s is not allowed in %s", errPrefix, automdmVar, where))
		}
	}
	return nil
}

// validateCARemovedAutoMDMVariables rejects an AutoMDM variable that AutoMDM 5 removed in the given fields. The batch apply runs it on
// unchanged CAs too: one saved before the upgrade that still uses the variable fails on delivery, so GitOps must not report it applied.
func validateCARemovedAutoMDMVariables(errPrefix string, fields map[string][]string) error {
	for _, field := range slices.Sorted(maps.Keys(fields)) {
		for _, value := range fields[field] {
			if err := automdm.FindRemovedAutoMDMVariable(variables.Find(value)...); err != nil {
				return automdm.NewInvalidArgumentError(field, errPrefix+err.Error())
			}
		}
	}
	return nil
}

var alphanumeric = regexp.MustCompile(`^\w+$`)

func isAlphanumeric(s string) bool {
	return alphanumeric.MatchString(s)
}

func validateDigicertSeatID(seatID string, errPrefix string) error {
	if len(strings.TrimSpace(seatID)) == 0 {
		return automdm.NewInvalidArgumentError("certificate_seat_id", fmt.Sprintf("%sCA Seat ID cannot be empty", errPrefix))
	}
	return validateCAAutoMDMVariables("certificate_seat_id", "DigiCert Seat ID", seatID, automdm.AutoMDMVarsSupportedInDigiCert, errPrefix)
}

func validateDigicertUserPrincipalNames(userPrincipalNames []string, errPrefix string) error {
	return validateCAUserPrincipalNames(userPrincipalNames, "DigiCert", automdm.AutoMDMVarsSupportedInDigiCert, 0, errPrefix)
}

// validateCAUserPrincipalNames allows at most one UPN, which has to have a value and only AutoMDM variables the CA supports. A maxLength of
// 0 is no limit.
func validateCAUserPrincipalNames(upns []string, vendor string, allowed []automdm.AutoMDMVarName, maxLength int, errPrefix string) error {
	if len(upns) == 0 {
		return nil
	}
	if len(upns) > 1 {
		return automdm.NewInvalidArgumentError("certificate_user_principal_names",
			fmt.Sprintf("%sCurrently, only one item can be added to certificate_user_principal_names.", errPrefix))
	}
	if len(strings.TrimSpace(upns[0])) == 0 {
		return automdm.NewInvalidArgumentError("certificate_user_principal_names",
			fmt.Sprintf("%s%s certificate_user_principal_name cannot be empty if specified", errPrefix, vendor))
	}
	if maxLength > 0 && utf8.RuneCountInString(upns[0]) > maxLength {
		return automdm.NewInvalidArgumentError("certificate_user_principal_names",
			fmt.Sprintf("%sThe user principal name cannot be longer than %d characters.", errPrefix, maxLength))
	}
	return validateCAAutoMDMVariables("certificate_user_principal_names", "CA User Principal Name", upns[0], allowed, errPrefix)
}

func (svc *Service) validateHydrant(ctx context.Context, hydrantCA *automdm.HydrantCA, errPrefix string) error {
	if err := validateCAName(hydrantCA.Name, errPrefix); err != nil {
		return err
	}
	if err := validateURL(hydrantCA.URL, "Hydrant", errPrefix); err != nil {
		return err
	}
	if hydrantCA.ClientID == "" {
		return automdm.NewInvalidArgumentError("client_id", fmt.Sprintf("%sInvalid Hydrant Client ID. Please correct and try again.", errPrefix))
	}
	if hydrantCA.ClientSecret == "" || hydrantCA.ClientSecret == automdm.MaskedPassword {
		return automdm.NewInvalidArgumentError("client_secret", fmt.Sprintf("%sInvalid Hydrant Client Secret. Please correct and try again.", errPrefix))
	}
	if err := svc.estService.ValidateESTURL(ctx, automdm.ESTProxyCA{
		ID:       hydrantCA.ID,
		Name:     hydrantCA.Name,
		URL:      hydrantCA.URL,
		Username: hydrantCA.ClientID,
		Password: hydrantCA.ClientSecret,
	}); err != nil {
		return automdm.NewInvalidArgumentError("url", fmt.Sprintf("%sInvalid Hydrant URL. Please correct and try again.", errPrefix))
	}
	return nil
}

func (svc *Service) validateEST(ctx context.Context, estProxyCA *automdm.ESTProxyCA, errPrefix string) error {
	if err := validateCAName(estProxyCA.Name, errPrefix); err != nil {
		return err
	}
	if err := validateURL(estProxyCA.URL, "EST", errPrefix); err != nil {
		return err
	}
	if estProxyCA.Username == "" {
		return automdm.NewInvalidArgumentError("username", fmt.Sprintf("%sInvalid EST Username. Please correct and try again.", errPrefix))
	}
	if estProxyCA.Password == "" || estProxyCA.Password == automdm.MaskedPassword {
		return automdm.NewInvalidArgumentError("password", fmt.Sprintf("%sInvalid EST Password. Please correct and try again.", errPrefix))
	}
	if err := svc.estService.ValidateESTURL(ctx, *estProxyCA); err != nil {
		return automdm.NewInvalidArgumentError("url", fmt.Sprintf("%sInvalid EST URL. Please correct and try again.", errPrefix))
	}
	return nil
}

func validateURL(caURL, displayType, errPrefix string) error {
	if u, err := url.ParseRequestURI(caURL); err != nil {
		return automdm.NewInvalidArgumentError("url", fmt.Sprintf("%sInvalid %s URL. Please correct and try again.", errPrefix, displayType))
	} else if u.Scheme != "https" && u.Scheme != "http" {
		return automdm.NewInvalidArgumentError("url", fmt.Sprintf("%s%s URL scheme must be https or http", errPrefix, displayType))
	}
	return nil
}

func (svc *Service) validateNDESSCEPProxy(ctx context.Context, ndesSCEP *automdm.NDESSCEPProxyCA, errPrefix string) error {
	if err := validateURL(ndesSCEP.URL, "NDES SCEP", errPrefix); err != nil {
		return err
	}
	if err := svc.scepConfigService.ValidateSCEPURL(ctx, ndesSCEP.URL); err != nil {
		svc.logger.ErrorContext(ctx, "Failed to validate NDES SCEP URL", "err", err)
		return &automdm.BadRequestError{Message: fmt.Sprintf("%sAutoMDM couldn't find the CA at the specified URL. Please correct and try again.", errPrefix)}
	}
	if err := svc.scepConfigService.ValidateNDESSCEPAdminURL(ctx, *ndesSCEP); err != nil {
		svc.logger.ErrorContext(ctx, "Failed to validate NDES SCEP admin URL", "err", err)
		switch {
		case errors.As(err, &scep.NDESPasswordCacheFullError{}):
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sThe NDES password cache is full. Please increase the number of cached passwords in NDES and try again.", errPrefix)}
		case errors.As(err, &scep.NDESInsufficientPermissionsError{}):
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sInsufficient permissions for NDES SCEP admin URL. Please correct and try again.", errPrefix)}
		default:
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sInvalid NDES SCEP admin URL or credentials. Please correct and try again.", errPrefix)}
		}
	}
	return nil
}

func (svc *Service) validateCustomSCEPProxy(ctx context.Context, customSCEP *automdm.CustomSCEPProxyCA, errPrefix string) error {
	if err := validateCAName(customSCEP.Name, errPrefix); err != nil {
		return err
	}
	if err := validateURL(customSCEP.URL, "SCEP", errPrefix); err != nil {
		return err
	}
	if customSCEP.Challenge == "" || customSCEP.Challenge == automdm.MaskedPassword {
		return automdm.NewInvalidArgumentError("challenge", fmt.Sprintf("%sCustom SCEP Proxy challenge cannot be empty", errPrefix))
	}
	if err := svc.scepConfigService.ValidateSCEPURL(ctx, customSCEP.URL); err != nil {
		svc.logger.ErrorContext(ctx, "Failed to validate custom SCEP URL", "err", err)
		return &automdm.BadRequestError{Message: fmt.Sprintf("%sAutoMDM couldn't find the CA at the specified URL. Please correct and try again.", errPrefix)}
	}
	return nil
}

func (svc *Service) validateSmallstepSCEPProxy(ctx context.Context, smallstepSCEP *automdm.SmallstepSCEPProxyCA, errPrefix string) error {
	if err := validateCAName(smallstepSCEP.Name, errPrefix); err != nil {
		return err
	}
	if err := validateURL(smallstepSCEP.URL, "Smallstep SCEP", errPrefix); err != nil {
		return err
	}
	if smallstepSCEP.Username == "" {
		return automdm.NewInvalidArgumentError("username", fmt.Sprintf("%sSmallstep username cannot be empty", errPrefix))
	}
	if smallstepSCEP.Password == "" || smallstepSCEP.Password == automdm.MaskedPassword {
		return automdm.NewInvalidArgumentError("password", fmt.Sprintf("%sSmallstep password cannot be empty", errPrefix))
	}
	if err := svc.scepConfigService.ValidateSCEPURL(ctx, smallstepSCEP.URL); err != nil {
		svc.logger.ErrorContext(ctx, "Failed to validate Smallstep SCEP URL", "err", err)
		return &automdm.BadRequestError{Message: fmt.Sprintf("%sAutoMDM couldn't find the CA at the specified URL. Please correct and try again.", errPrefix)}
	}
	if err := svc.scepConfigService.ValidateSmallstepChallengeURL(ctx, *smallstepSCEP); err != nil {
		svc.logger.ErrorContext(ctx, "Failed to validate Smallstep SCEP admin URL", "err", err)
		return &automdm.BadRequestError{Message: fmt.Sprintf("%sInvalid challenge URL or credentials. Please correct and try again.", errPrefix)}
	}
	return nil
}

type oauthIntrospectionResponse struct {
	Username *string `json:"username"`
	// Only active is required in the body by the spec
	Active bool `json:"active"`
}

func (svc *Service) DeleteCertificateAuthority(ctx context.Context, certificateAuthorityID uint) error {
	if err := svc.authz.Authorize(ctx, &automdm.CertificateAuthority{}, automdm.ActionWrite); err != nil {
		return err
	}

	ca, err := svc.ds.DeleteCertificateAuthority(ctx, certificateAuthorityID)
	if err != nil {
		return err
	}

	var activity automdm.ActivityDetails
	switch ca.Type {
	case string(automdm.CATypeCustomSCEPProxy):
		activity = automdm.ActivityDeletedCustomSCEPProxy{
			Name: ca.Name,
		}
	case string(automdm.CATypeDigiCert):
		activity = automdm.ActivityDeletedDigiCert{
			Name: ca.Name,
		}
	case string(automdm.CATypeNDESSCEPProxy):
		activity = automdm.ActivityDeletedNDESSCEPProxy{}
	case string(automdm.CATypeHydrant):
		activity = automdm.ActivityDeletedHydrant{
			Name: ca.Name,
		}
	case string(automdm.CATypeSmallstep):
		activity = automdm.ActivityDeletedSmallstep{
			Name: ca.Name,
		}
	case string(automdm.CATypeCustomESTProxy):
		activity = automdm.ActivityDeletedCustomESTProxy{
			Name: ca.Name,
		}
	case string(automdm.CATypeEJBCA):
		activity = automdm.ActivityDeletedEJBCA{
			Name: ca.Name,
		}
	case string(automdm.CATypeGoogleCAS):
		activity = automdm.ActivityDeletedGoogleCAS{
			Name: ca.Name,
		}
	}

	if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), activity); err != nil {
		return err
	}

	return nil
}

func (svc *Service) BatchApplyCertificateAuthorities(ctx context.Context, incoming automdm.GroupedCertificateAuthorities, opts automdm.BatchApplyCertificateAuthoritiesOpts) error {
	if err := svc.authz.Authorize(ctx, &automdm.CertificateAuthority{}, automdm.ActionWrite); err != nil {
		return err
	}

	if !opts.ViaGitOps {
		// Note: This check is here primarily for future reference to help make the usage intent
		// clear and to differentiate behavior from dual-use endpoints that support patch semantics (e.g., app config)
		return automdm.NewInvalidArgumentError("gitops", "certificate_authorities: batch apply is intended only for use with gitops")
	}

	// The private key is only needed to encrypt CA secrets, so don't require it when
	// no CAs are configured (the common case for GitOps users without CAs).
	if !incoming.IsEmpty() && len(svc.config.Server.PrivateKey) == 0 {
		return &automdm.BadRequestError{Message: "Server private key must be configured. Learn more: https://automdm.com/learn-more-about/automdm-server-private-key"}
	}

	ops, err := svc.getCertificateAuthoritiesBatchOperations(ctx, incoming)
	if err != nil {
		return err
	}

	if ops == nil {
		svc.logger.DebugContext(ctx, "batch apply certificate authorities: no certificate authority changes to apply")
		return nil
	}

	if opts.SkipDeletes {
		ops.Delete = deletesFreeingNames(ops)
		// A certificate template can only use a custom SCEP CA, so the templates that use a CA whose name now belongs to a CA of another type
		// can't be kept. The team configs would remove them, but only after this pass, so they are removed with the CA here.
		ops.DeleteCertificateTemplates = true
	}

	if opts.DryRun {
		svc.logger.DebugContext(ctx, "batch apply certificate authorities: no certificate authority changes to apply")
		return nil
	}

	templateTeamIDs, err := svc.ds.BatchApplyCertificateAuthorities(ctx, *ops)
	if err != nil {
		return err
	}

	if err := svc.recordActivitiesBatchApplyCAs(ctx, ops); err != nil {
		return err
	}

	// The team configs won't find these templates to remove, so the edit is recorded here, like it is when an automdm config removes one.
	for _, automdmID := range templateTeamIDs {
		activity := &automdm.ActivityTypeEditedAndroidCertificate{}
		if automdmID != 0 {
			team, err := svc.ds.TeamLite(ctx, automdmID)
			if err != nil {
				return ctxerr.Wrap(ctx, err, "getting team for edited android certificate activity")
			}
			activity.TeamID, activity.TeamName = &team.ID, &team.Name
		}
		if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), activity); err != nil {
			return ctxerr.Wrap(ctx, err, "create activity for edited android certificate")
		}
	}

	return nil
}

// deletesFreeingNames returns the deletes that have to run together with the adds and updates, because a CA can't share its name with
// another CA. That is a CA that gives up its name to a CA of another type. The other deletes are left out, so that they can run after the
// certificate templates that use the CAs are cleaned up.
func deletesFreeingNames(ops *automdm.CertificateAuthoritiesBatchOperations) []*automdm.CertificateAuthority {
	upserts := slices.Concat(ops.Add, ops.Update)
	return slices.DeleteFunc(slices.Clone(ops.Delete), func(deleted *automdm.CertificateAuthority) bool {
		return !slices.ContainsFunc(upserts, func(upserted *automdm.CertificateAuthority) bool {
			return deleted.Name != nil && upserted.Name != nil && caNameKey(*deleted.Name) == caNameKey(*upserted.Name) &&
				deleted.Type != upserted.Type
		})
	})
}

func (svc *Service) getCertificateAuthoritiesBatchOperations(ctx context.Context, incoming automdm.GroupedCertificateAuthorities) (*automdm.CertificateAuthoritiesBatchOperations, error) {
	batchOps := &automdm.CertificateAuthoritiesBatchOperations{
		Add:    make([]*automdm.CertificateAuthority, 0),
		Delete: make([]*automdm.CertificateAuthority, 0),
		Update: make([]*automdm.CertificateAuthority, 0),
	}

	existing, err := svc.ds.GetGroupedCertificateAuthorities(ctx, true)
	if err != nil {
		return nil, err
	}

	// Names are unique across all CA types, without regard to case. The NDES CA's name is fixed, so no other CA can have it. This is checked
	// for every CA, before the unchanged ones are skipped, so that one keeping a name from before these rules can't be applied either.
	allNames := make(map[string]struct{})
	checkAllNames := func(name, caType string) error {
		if name == "" {
			return automdm.NewInvalidArgumentError("name", fmt.Sprintf("certificate_authorities.%s: CA name cannot be empty.", caType))
		}
		if strings.EqualFold(name, "NDES") {
			return automdm.NewInvalidArgumentError("name", fmt.Sprintf("certificate_authorities.%s.name: CA name cannot be NDES", caType))
		}
		key := caNameKey(name)
		if _, seen := allNames[key]; seen {
			return automdm.NewInvalidArgumentError("name",
				fmt.Sprintf("certificate_authorities.%s.name: CertificateAuthority %q already exists", caType, name))
		}
		allNames[key] = struct{}{}
		return nil
	}

	// preprocess digicert
	for i := range incoming.DigiCert {
		ca := &incoming.DigiCert[i]
		ca.Preprocess()
		if err := checkAllNames(ca.Name, "digicert"); err != nil {
			return nil, err
		}
	}
	// preprocess ejbca, in place so the trimmed names are the ones that get stored
	for i := range incoming.EJBCA {
		ca := &incoming.EJBCA[i]
		ca.Preprocess()
		if err := checkAllNames(ca.Name, "ejbca"); err != nil {
			return nil, err
		}
	}
	// preprocess custom scep proxy
	for i := range incoming.CustomScepProxy {
		ca := &incoming.CustomScepProxy[i]
		ca.Preprocess()
		if err := checkAllNames(ca.Name, "custom_scep_proxy"); err != nil {
			return nil, err
		}
	}
	// preprocess hydrant
	for i := range incoming.Hydrant {
		ca := &incoming.Hydrant[i]
		ca.Preprocess()
		if err := checkAllNames(ca.Name, "hydrant"); err != nil {
			return nil, err
		}
	}
	// preprocess custom est proxy
	for i := range incoming.EST {
		ca := &incoming.EST[i]
		ca.Preprocess()
		if err := checkAllNames(ca.Name, "custom_est_proxy"); err != nil {
			return nil, err
		}
	}
	// preprocess smallstep
	for i := range incoming.Smallstep {
		ca := &incoming.Smallstep[i]
		ca.Preprocess()
		if err := checkAllNames(ca.Name, "smallstep"); err != nil {
			return nil, err
		}
	}
	// preprocess google cas, in place so the trimmed values are what gets stored
	for i := range incoming.GoogleCAS {
		ca := &incoming.GoogleCAS[i]
		ca.Preprocess()
		if err := checkAllNames(ca.Name, "google_cas"); err != nil {
			return nil, err
		}
	}
	// preprocess ndes
	if incoming.NDESSCEP != nil {
		incoming.NDESSCEP.Preprocess()
	}

	if err := svc.processNDESSCEP(ctx, batchOps, incoming.NDESSCEP, existing.NDESSCEP); err != nil {
		return nil, err
	}
	if err := svc.processDigiCertCAs(ctx, batchOps, incoming.DigiCert, existing.DigiCert); err != nil {
		return nil, err
	}
	if err := svc.processEJBCACAs(ctx, batchOps, incoming.EJBCA, existing.EJBCA); err != nil {
		return nil, err
	}
	if err := svc.processCustomSCEPProxyCAs(ctx, batchOps, incoming.CustomScepProxy, existing.CustomScepProxy); err != nil {
		return nil, err
	}
	if err := svc.processHydrantCAs(ctx, batchOps, incoming.Hydrant, existing.Hydrant); err != nil {
		return nil, err
	}
	if err := svc.processESTCAs(ctx, batchOps, incoming.EST, existing.EST); err != nil {
		return nil, err
	}
	if err := svc.processSmallstepCAs(ctx, batchOps, incoming.Smallstep, existing.Smallstep); err != nil {
		return nil, err
	}
	if err := svc.processGoogleCASCAs(ctx, batchOps, incoming.GoogleCAS, existing.GoogleCAS); err != nil {
		return nil, err
	}

	return batchOps, nil
}

func (svc *Service) processNDESSCEP(ctx context.Context, batchOps *automdm.CertificateAuthoritiesBatchOperations, incoming *automdm.NDESSCEPProxyCA, existing *automdm.NDESSCEPProxyCA) error {
	ndesName := "NDES"

	if existing == nil && incoming == nil {
		// do nothing
		svc.logger.DebugContext(ctx, "no existing or incoming NDES SCEP CA, skipping")
		return nil
	}

	if existing != nil && incoming != nil && incoming.URL == existing.URL && incoming.AdminURL == existing.AdminURL && incoming.Username == existing.Username && incoming.Password == existing.Password {
		// all fields are identical so we can skip further validation and processing
		svc.logger.DebugContext(ctx, "existing and incoming NDES SCEP CA are identical, skipping")
		return nil
	}

	if existing != nil && (incoming == nil || (incoming.URL == "" && incoming.AdminURL == "" && incoming.Username == "" && incoming.Password == "")) {
		// delete current
		svc.logger.DebugContext(ctx, "deleting existing NDES SCEP CA as incoming is empty")
		batchOps.Delete = append(batchOps.Delete, &automdm.CertificateAuthority{
			Type:     string(automdm.CATypeNDESSCEPProxy),
			Name:     &ndesName,
			AdminURL: &existing.AdminURL,
			Username: &existing.Username,
			Password: &existing.Password,
		})
		return nil
	}

	if incoming.Password == "" || incoming.Password == automdm.MaskedPassword {
		return automdm.NewInvalidArgumentError("password", "certificate_authorities.ndes_scep_proxy.password: NDES SCEP password cannot be empty.")
	}

	if err := svc.validateNDESSCEPProxy(ctx, incoming, "certificate_authorities.ndes_scep_proxy: "); err != nil {
		return err
	}

	// add if there is no existing
	if existing == nil || (existing.URL == "" && existing.AdminURL == "" && existing.Username == "" && existing.Password == "") {
		svc.logger.DebugContext(ctx, "adding new NDES SCEP CA as none exists")
		batchOps.Add = append(batchOps.Add, &automdm.CertificateAuthority{
			Type:     string(automdm.CATypeNDESSCEPProxy),
			Name:     &ndesName,
			URL:      &incoming.URL,
			AdminURL: &incoming.AdminURL,
			Username: &incoming.Username,
			Password: &incoming.Password,
		})
		return nil
	}

	// otherwise update with existing id
	svc.logger.DebugContext(ctx, "updating existing NDES SCEP CA")
	incoming.ID = existing.ID
	batchOps.Update = append(batchOps.Update, &automdm.CertificateAuthority{
		Type:     string(automdm.CATypeNDESSCEPProxy),
		Name:     &ndesName,
		URL:      &incoming.URL,
		AdminURL: &incoming.AdminURL,
		Username: &incoming.Username,
		Password: &incoming.Password,
	})

	return nil
}

// caNameKey is how batch planning compares CA names. Names are ASCII (see validateCAName) and the
// unique key on type and name ignores case, so a case-only rename is an update, not an add and a delete.
func caNameKey(name string) string {
	return strings.ToLower(name)
}

func (svc *Service) processDigiCertCAs(ctx context.Context, batchOps *automdm.CertificateAuthoritiesBatchOperations, incomingCAs []automdm.DigiCertCA, existingCAs []automdm.DigiCertCA) error {
	incomingByName := make(map[string]*automdm.DigiCertCA)
	for _, incoming := range incomingCAs {
		if incoming.Name == "" {
			return automdm.NewInvalidArgumentError("name", "certificate_authorities.digicert.name: Name cannot be empty.")
		}
		// Note: caller is responsible for ensuring incoming list has no duplicates
		incomingByName[caNameKey(incoming.Name)] = &incoming
	}

	existingByName := make(map[string]*automdm.DigiCertCA)
	for _, existing := range existingCAs {
		if _, ok := incomingByName[caNameKey(existing.Name)]; !ok {
			// if current CA isn't in the incoming list, we should delete it
			batchOps.Delete = append(batchOps.Delete, &automdm.CertificateAuthority{
				Type:                          string(automdm.CATypeDigiCert),
				Name:                          &existing.Name,
				URL:                           &existing.URL,
				APIToken:                      &existing.APIToken,
				ProfileID:                     &existing.ProfileID,
				CertificateCommonName:         &existing.CertificateCommonName,
				CertificateUserPrincipalNames: &existing.CertificateUserPrincipalNames,
				CertificateSeatID:             &existing.CertificateSeatID,
			})
		}
		// Note: datastore is responsible for ensuring no existing list has no duplicates
		existingByName[caNameKey(existing.Name)] = &existing
	}

	for name, incoming := range incomingByName {
		if err := validateCARemovedAutoMDMVariables("certificate_authorities.digicert: ", map[string][]string{
			"certificate_common_name":          {incoming.CertificateCommonName},
			"certificate_seat_id":              {incoming.CertificateSeatID},
			"certificate_user_principal_names": incoming.CertificateUserPrincipalNames,
		}); err != nil {
			return err
		}
		// check if incoming name matches existing
		existing, ok := existingByName[name]
		switch {
		case ok && incoming.Equals(existing):
			// found and identical so do nothing
			continue
		case ok:
			// found but not identical so update
			batchOps.Update = append(batchOps.Update, &automdm.CertificateAuthority{
				Type:                          string(automdm.CATypeDigiCert),
				Name:                          &incoming.Name,
				URL:                           &incoming.URL,
				APIToken:                      &incoming.APIToken,
				ProfileID:                     &incoming.ProfileID,
				CertificateCommonName:         &incoming.CertificateCommonName,
				CertificateUserPrincipalNames: &incoming.CertificateUserPrincipalNames,
				CertificateSeatID:             &incoming.CertificateSeatID,
			})
		default:
			// not found so add
			batchOps.Add = append(batchOps.Add, &automdm.CertificateAuthority{
				Type:                          string(automdm.CATypeDigiCert),
				Name:                          &incoming.Name,
				URL:                           &incoming.URL,
				APIToken:                      &incoming.APIToken,
				ProfileID:                     &incoming.ProfileID,
				CertificateCommonName:         &incoming.CertificateCommonName,
				CertificateUserPrincipalNames: &incoming.CertificateUserPrincipalNames,
				CertificateSeatID:             &incoming.CertificateSeatID,
			})
		}

		if err := svc.validateDigicert(ctx, incoming, "certificate_authorities.digicert: "); err != nil {
			return err
		}
	}

	return nil
}

func (svc *Service) processEJBCACAs(ctx context.Context, batchOps *automdm.CertificateAuthoritiesBatchOperations, incomingCAs []automdm.EJBCA, existingCAs []automdm.EJBCA) error {
	const errPrefix = "certificate_authorities.ejbca: "

	// Names are matched without regard to case, like the database does, so a rename that only changes the case is an update.
	incomingNames := make(map[string]struct{}, len(incomingCAs))
	for _, incoming := range incomingCAs {
		incomingNames[caNameKey(incoming.Name)] = struct{}{}
	}
	existingByName := make(map[string]*automdm.EJBCA, len(existingCAs))
	for _, existing := range existingCAs {
		if _, ok := incomingNames[caNameKey(existing.Name)]; !ok {
			batchOps.Delete = append(batchOps.Delete, &automdm.CertificateAuthority{Type: string(automdm.CATypeEJBCA), Name: &existing.Name})
		}
		existingByName[caNameKey(existing.Name)] = &existing
	}

	// Note: caller is responsible for ensuring incoming list has no duplicates
	for i := range incomingCAs {
		incoming := &incomingCAs[i]
		if err := validateCARemovedAutoMDMVariables(errPrefix, map[string][]string{
			"certificate_common_name":          {incoming.CertificateCommonName},
			"username_template":                {incoming.UsernameTemplate},
			"certificate_user_principal_names": incoming.CertificateUserPrincipalNames,
		}); err != nil {
			return err
		}
		existing, found := existingByName[caNameKey(incoming.Name)]
		if found {
			// An unchanged CA isn't validated, so a client certificate that expired after it was stored doesn't block the other changes.
			creds, err := parseEJBCAClientP12(ctx, incoming.ClientP12, incoming.Password, errPrefix)
			if err != nil {
				return err
			}
			candidate := *incoming
			candidate.MTLSCertPEM, candidate.MTLSKeyPEM = creds.CertPEM, creds.KeyPEM
			if candidate.Equals(existing) {
				continue
			}
		}

		if err := svc.validateEJBCA(ctx, incoming, errPrefix); err != nil {
			return err
		}
		if found {
			batchOps.Update = append(batchOps.Update, incoming.CertificateAuthority())
		} else {
			batchOps.Add = append(batchOps.Add, incoming.CertificateAuthority())
		}
	}

	return nil
}

func (svc *Service) processCustomSCEPProxyCAs(ctx context.Context, batchOps *automdm.CertificateAuthoritiesBatchOperations, incomingCAs []automdm.CustomSCEPProxyCA, existingCAs []automdm.CustomSCEPProxyCA) error {
	incomingByName := make(map[string]*automdm.CustomSCEPProxyCA)
	for _, incoming := range incomingCAs {
		// Note: caller is responsible for ensuring incoming list has no duplicates
		incomingByName[caNameKey(incoming.Name)] = &incoming
	}

	existingByName := make(map[string]*automdm.CustomSCEPProxyCA)
	for _, existing := range existingCAs {
		// if existing CA isn't in the incoming list, we should delete it
		if _, ok := incomingByName[caNameKey(existing.Name)]; !ok {
			batchOps.Delete = append(batchOps.Delete, &automdm.CertificateAuthority{
				Type:      string(automdm.CATypeCustomSCEPProxy),
				Name:      &existing.Name,
				URL:       &existing.URL,
				Challenge: &existing.Challenge,
			})
		}
		// Note: datastore is responsible for ensuring no existing list has no duplicates
		existingByName[caNameKey(existing.Name)] = &existing
	}

	for name, incoming := range incomingByName {
		existing, ok := existingByName[name]
		// An unchanged CA is left alone. A masked secret is not allowed and will be rejected later in validation.
		if ok && incoming.Equals(existing) {
			continue
		}
		if err := svc.validateCustomSCEPProxy(ctx, incoming, "certificate_authorities.custom_scep_proxy: "); err != nil {
			return err
		}
		// create the payload to be added or updated
		if ok {
			// update existing
			batchOps.Update = append(batchOps.Update, &automdm.CertificateAuthority{
				Type:      string(automdm.CATypeCustomSCEPProxy),
				Name:      &incoming.Name,
				URL:       &incoming.URL,
				Challenge: &incoming.Challenge,
			})
		} else {
			// add new
			batchOps.Add = append(batchOps.Add, &automdm.CertificateAuthority{
				Type:      string(automdm.CATypeCustomSCEPProxy),
				Name:      &incoming.Name,
				URL:       &incoming.URL,
				Challenge: &incoming.Challenge,
			})
		}
	}

	return nil
}

func (svc *Service) processHydrantCAs(ctx context.Context, batchOps *automdm.CertificateAuthoritiesBatchOperations, incomingCAs []automdm.HydrantCA, existingCAs []automdm.HydrantCA) error {
	incomingByName := make(map[string]*automdm.HydrantCA)
	for _, incoming := range incomingCAs {
		// Note: caller is responsible for ensuring incoming list has no duplicates
		incomingByName[caNameKey(incoming.Name)] = &incoming
	}

	existingByName := make(map[string]*automdm.HydrantCA)
	for _, existing := range existingCAs {
		// if existing CA isn't in the incoming list, we should delete it
		if _, ok := incomingByName[caNameKey(existing.Name)]; !ok {
			batchOps.Delete = append(batchOps.Delete, &automdm.CertificateAuthority{
				Type:         string(automdm.CATypeHydrant),
				Name:         &existing.Name,
				URL:          &existing.URL,
				ClientID:     &existing.ClientID,
				ClientSecret: &existing.ClientSecret,
			})
		}
		// Note: datastore is responsible for ensuring no existing list has no duplicates
		existingByName[caNameKey(existing.Name)] = &existing
	}

	for name, incoming := range incomingByName {
		existing, ok := existingByName[name]
		// An unchanged CA is left alone.
		if ok && incoming.Equals(existing) {
			continue
		}
		if err := svc.validateHydrant(ctx, incoming, "certificate_authorities.hydrant: "); err != nil {
			return err
		}

		// create the payload to be added or updated
		if ok {
			// update existing
			batchOps.Update = append(batchOps.Update, &automdm.CertificateAuthority{
				Type:         string(automdm.CATypeHydrant),
				Name:         &incoming.Name,
				URL:          &incoming.URL,
				ClientID:     &incoming.ClientID,
				ClientSecret: &incoming.ClientSecret,
			})
		} else {
			// add new
			batchOps.Add = append(batchOps.Add, &automdm.CertificateAuthority{
				Type:         string(automdm.CATypeHydrant),
				Name:         &incoming.Name,
				URL:          &incoming.URL,
				ClientID:     &incoming.ClientID,
				ClientSecret: &incoming.ClientSecret,
			})
		}
	}

	return nil
}

func (svc *Service) processESTCAs(ctx context.Context, batchOps *automdm.CertificateAuthoritiesBatchOperations, incomingCAs []automdm.ESTProxyCA, existingCAs []automdm.ESTProxyCA) error {
	incomingByName := make(map[string]*automdm.ESTProxyCA)
	for _, incoming := range incomingCAs {
		// Note: caller is responsible for ensuring incoming list has no duplicates
		incomingByName[caNameKey(incoming.Name)] = &incoming
	}

	existingByName := make(map[string]*automdm.ESTProxyCA)
	for _, existing := range existingCAs {
		// if existing CA isn't in the incoming list, we should delete it
		if _, ok := incomingByName[caNameKey(existing.Name)]; !ok {
			batchOps.Delete = append(batchOps.Delete, &automdm.CertificateAuthority{
				Type:     string(automdm.CATypeCustomESTProxy),
				Name:     &existing.Name,
				URL:      &existing.URL,
				Username: &existing.Username,
				Password: &existing.Password,
			})
		}
		// Note: datastore is responsible for ensuring no existing list has no duplicates
		existingByName[caNameKey(existing.Name)] = &existing
	}

	for name, incoming := range incomingByName {
		existing, ok := existingByName[name]
		// An unchanged CA is left alone.
		if ok && incoming.Equals(existing) {
			continue
		}
		if err := svc.validateEST(ctx, incoming, "certificate_authorities.custom_est_proxy: "); err != nil {
			return err
		}

		// create the payload to be added or updated
		if ok {
			// update existing
			batchOps.Update = append(batchOps.Update, &automdm.CertificateAuthority{
				Type:     string(automdm.CATypeCustomESTProxy),
				Name:     &incoming.Name,
				URL:      &incoming.URL,
				Username: &incoming.Username,
				Password: &incoming.Password,
			})
		} else {
			// add new
			batchOps.Add = append(batchOps.Add, &automdm.CertificateAuthority{
				Type:     string(automdm.CATypeCustomESTProxy),
				Name:     &incoming.Name,
				URL:      &incoming.URL,
				Username: &incoming.Username,
				Password: &incoming.Password,
			})
		}
	}

	return nil
}

func (svc *Service) processSmallstepCAs(ctx context.Context, batchOps *automdm.CertificateAuthoritiesBatchOperations, incomingCAs []automdm.SmallstepSCEPProxyCA, existingCAs []automdm.SmallstepSCEPProxyCA) error {
	incomingByName := make(map[string]*automdm.SmallstepSCEPProxyCA)
	for _, incoming := range incomingCAs {
		// Note: caller is responsible for ensuring incoming list has no duplicates
		incomingByName[caNameKey(incoming.Name)] = &incoming
	}

	existingByName := make(map[string]*automdm.SmallstepSCEPProxyCA)
	for _, existing := range existingCAs {
		// if existing CA isn't in the incoming list, we should delete it
		if _, ok := incomingByName[caNameKey(existing.Name)]; !ok {
			batchOps.Delete = append(batchOps.Delete, &automdm.CertificateAuthority{
				Type:         string(automdm.CATypeSmallstep),
				Name:         &existing.Name,
				URL:          &existing.URL,
				ChallengeURL: &existing.ChallengeURL,
				Username:     &existing.Username,
				Password:     &existing.Password,
			})
		}
		// Note: datastore is responsible for ensuring no existing list has no duplicates
		existingByName[caNameKey(existing.Name)] = &existing
	}

	for name, incoming := range incomingByName {
		existing, ok := existingByName[name]
		// An unchanged CA is left alone.
		if ok && incoming.Equals(existing) {
			continue
		}
		if err := svc.validateSmallstepSCEPProxy(ctx, incoming, "certificate_authorities.smallstep: "); err != nil {
			return err
		}

		// create the payload to be added or updated
		if ok {
			// update existing
			batchOps.Update = append(batchOps.Update, &automdm.CertificateAuthority{
				Type:         string(automdm.CATypeSmallstep),
				Name:         &incoming.Name,
				URL:          &incoming.URL,
				ChallengeURL: &incoming.ChallengeURL,
				Username:     &incoming.Username,
				Password:     &incoming.Password,
			})
		} else {
			// add new
			batchOps.Add = append(batchOps.Add, &automdm.CertificateAuthority{
				Type:         string(automdm.CATypeSmallstep),
				Name:         &incoming.Name,
				URL:          &incoming.URL,
				ChallengeURL: &incoming.ChallengeURL,
				Username:     &incoming.Username,
				Password:     &incoming.Password,
			})
		}
	}

	return nil
}

// recordActivitiesBatchApplyCAs records activities for batch operations on certificate authorities
// (i.e. added, edited, deleted).
func (svc *Service) recordActivitiesBatchApplyCAs(ctx context.Context, ops *automdm.CertificateAuthoritiesBatchOperations) error {
	if ops == nil {
		return nil
	}

	for _, ca := range ops.Add {
		switch ca.Type {
		case string(automdm.CATypeNDESSCEPProxy):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityAddedNDESSCEPProxy{}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for added NDES SCEP proxy")
			}
		case string(automdm.CATypeDigiCert):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityAddedDigiCert{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for added DigiCert")
			}
		case string(automdm.CATypeCustomSCEPProxy):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityAddedCustomSCEPProxy{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for added custom SCEP proxy")
			}
		case string(automdm.CATypeHydrant):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityAddedHydrant{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for added hydrant")
			}
		case string(automdm.CATypeCustomESTProxy):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityAddedCustomESTProxy{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for added custom est proxy")
			}
		case string(automdm.CATypeSmallstep):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityAddedSmallstep{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for added smallstep SCEP proxy")
			}
		case string(automdm.CATypeEJBCA):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityAddedEJBCA{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for added EJBCA")
			}
		case string(automdm.CATypeGoogleCAS):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityAddedGoogleCAS{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for added Google CAS")
			}
		}
	}
	for _, ca := range ops.Update {
		switch ca.Type {
		case string(automdm.CATypeNDESSCEPProxy):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityEditedNDESSCEPProxy{}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for edited NDES SCEP proxy")
			}
		case string(automdm.CATypeDigiCert):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityEditedDigiCert{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for edited DigiCert")
			}
		case string(automdm.CATypeCustomSCEPProxy):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityEditedCustomSCEPProxy{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for edited custom SCEP proxy")
			}
		case string(automdm.CATypeHydrant):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityEditedHydrant{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for edited hydrant")
			}
		case string(automdm.CATypeCustomESTProxy):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityEditedCustomESTProxy{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activityu for edited custom EST proxy")
			}
		case string(automdm.CATypeSmallstep):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityEditedSmallstep{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for edited smallstep SCEP proxy")
			}
		case string(automdm.CATypeEJBCA):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityEditedEJBCA{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for edited EJBCA")
			}
		case string(automdm.CATypeGoogleCAS):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityEditedGoogleCAS{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for edited Google CAS")
			}
		}
	}
	for _, ca := range ops.Delete {
		switch ca.Type {
		case string(automdm.CATypeNDESSCEPProxy):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityDeletedNDESSCEPProxy{}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for deleted NDES SCEP proxy")
			}
		case string(automdm.CATypeDigiCert):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityDeletedDigiCert{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for deleted DigiCert")
			}
		case string(automdm.CATypeCustomSCEPProxy):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityDeletedCustomSCEPProxy{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for deleted custom SCEP proxy")
			}
		case string(automdm.CATypeHydrant):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityDeletedHydrant{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for deleted hydrant")
			}
		case string(automdm.CATypeCustomESTProxy):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityDeletedCustomESTProxy{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for deleted custom EST proxy")
			}
		case string(automdm.CATypeSmallstep):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityDeletedSmallstep{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for deleted smallstep SCEP proxy")
			}
		case string(automdm.CATypeEJBCA):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityDeletedEJBCA{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for deleted EJBCA")
			}
		case string(automdm.CATypeGoogleCAS):
			if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), automdm.ActivityDeletedGoogleCAS{Name: *ca.Name}); err != nil {
				return ctxerr.Wrap(ctx, err, "create activity for deleted Google CAS")
			}
		}
	}

	return nil
}

func (svc *Service) UpdateCertificateAuthority(ctx context.Context, id uint, p automdm.CertificateAuthorityUpdatePayload) error {
	if err := svc.authz.Authorize(ctx, &automdm.CertificateAuthority{}, automdm.ActionWrite); err != nil {
		return err
	}

	errPrefix := "Couldn't edit certificate authority. "

	if err := p.ValidatePayload(svc.config.Server.PrivateKey, errPrefix); err != nil {
		return err
	}

	oldCA, err := svc.ds.GetCertificateAuthorityByID(ctx, id, true)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sCertificate authority with ID %d does not exist.", errPrefix, id)}
		}
		return err
	}

	caToUpdate := automdm.CertificateAuthority{}
	var activity automdm.ActivityDetails
	var caActivityName string

	switch {
	case p.DigiCertCAUpdatePayload != nil:
		if p.DigiCertCAUpdatePayload.IsEmpty() {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sDigiCert CA update payload is empty", errPrefix)}
		}

		if err := p.DigiCertCAUpdatePayload.ValidateRelatedFields(errPrefix, *oldCA.Name); err != nil {
			return err
		}
		p.DigiCertCAUpdatePayload.Preprocess()
		if err := svc.validateDigicertUpdate(ctx, p.DigiCertCAUpdatePayload, oldCA, errPrefix); err != nil {
			return err
		}
		caToUpdate.Type = string(automdm.CATypeDigiCert)
		caToUpdate.Name = p.DigiCertCAUpdatePayload.Name
		caToUpdate.URL = p.DigiCertCAUpdatePayload.URL
		caToUpdate.APIToken = p.DigiCertCAUpdatePayload.APIToken
		caToUpdate.ProfileID = p.DigiCertCAUpdatePayload.ProfileID
		caToUpdate.CertificateCommonName = p.DigiCertCAUpdatePayload.CertificateCommonName
		caToUpdate.CertificateUserPrincipalNames = p.DigiCertCAUpdatePayload.CertificateUserPrincipalNames
		caToUpdate.CertificateSeatID = p.DigiCertCAUpdatePayload.CertificateSeatID

		if caToUpdate.Name != nil {
			caActivityName = *caToUpdate.Name
		} else {
			caActivityName = *oldCA.Name
		}
		activity = automdm.ActivityEditedDigiCert{Name: caActivityName}
	case p.HydrantCAUpdatePayload != nil:
		if p.HydrantCAUpdatePayload.IsEmpty() {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sHydrant CA update payload is empty", errPrefix)}
		}

		if err := p.HydrantCAUpdatePayload.ValidateRelatedFields(errPrefix, *oldCA.Name); err != nil {
			return err
		}
		p.HydrantCAUpdatePayload.Preprocess()
		if err := svc.validateHydrantUpdate(ctx, p.HydrantCAUpdatePayload, oldCA, errPrefix); err != nil {
			return err
		}
		caToUpdate.Type = string(automdm.CATypeHydrant)
		caToUpdate.Name = p.HydrantCAUpdatePayload.Name
		caToUpdate.URL = p.HydrantCAUpdatePayload.URL
		caToUpdate.ClientID = p.HydrantCAUpdatePayload.ClientID
		caToUpdate.ClientSecret = p.HydrantCAUpdatePayload.ClientSecret
		if caToUpdate.Name != nil {
			caActivityName = *caToUpdate.Name
		} else {
			caActivityName = *oldCA.Name
		}
		activity = automdm.ActivityEditedHydrant{Name: caActivityName}
	case p.CustomESTCAUpdatePayload != nil:
		if p.CustomESTCAUpdatePayload.IsEmpty() {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sCustom EST CA update payload is empty", errPrefix)}
		}

		if err := p.CustomESTCAUpdatePayload.ValidateRelatedFields(errPrefix, *oldCA.Name); err != nil {
			return err
		}
		p.CustomESTCAUpdatePayload.Preprocess()
		if err := svc.validateCustomESTUpdate(ctx, p.CustomESTCAUpdatePayload, oldCA, errPrefix); err != nil {
			return err
		}
		caToUpdate.Type = string(automdm.CATypeCustomESTProxy)
		caToUpdate.Name = p.CustomESTCAUpdatePayload.Name
		caToUpdate.URL = p.CustomESTCAUpdatePayload.URL
		caToUpdate.Username = p.CustomESTCAUpdatePayload.Username
		caToUpdate.Password = p.CustomESTCAUpdatePayload.Password
		if caToUpdate.Name != nil {
			caActivityName = *caToUpdate.Name
		} else {
			caActivityName = *oldCA.Name
		}
		activity = automdm.ActivityEditedCustomESTProxy{Name: caActivityName}
	case p.NDESSCEPProxyCAUpdatePayload != nil:
		if p.NDESSCEPProxyCAUpdatePayload.IsEmpty() {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sNDES SCEP Proxy CA update payload is empty", errPrefix)}
		}

		if err := p.NDESSCEPProxyCAUpdatePayload.ValidateRelatedFields(errPrefix, *oldCA.Name); err != nil {
			return err
		}
		p.NDESSCEPProxyCAUpdatePayload.Preprocess()
		if err := svc.validateNDESSCEPProxyUpdate(ctx, p.NDESSCEPProxyCAUpdatePayload, oldCA, errPrefix); err != nil {
			return err
		}
		caToUpdate.Type = string(automdm.CATypeNDESSCEPProxy)
		caToUpdate.URL = p.NDESSCEPProxyCAUpdatePayload.URL
		caToUpdate.AdminURL = p.NDESSCEPProxyCAUpdatePayload.AdminURL
		caToUpdate.Username = p.NDESSCEPProxyCAUpdatePayload.Username
		caToUpdate.Password = p.NDESSCEPProxyCAUpdatePayload.Password
		if caToUpdate.Name != nil {
			caActivityName = *caToUpdate.Name
		} else {
			caActivityName = *oldCA.Name
		}
		activity = automdm.ActivityEditedNDESSCEPProxy{}
	case p.CustomSCEPProxyCAUpdatePayload != nil:
		if p.CustomSCEPProxyCAUpdatePayload.IsEmpty() {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sCustom SCEP Proxy CA update payload is empty", errPrefix)}
		}

		if err := p.CustomSCEPProxyCAUpdatePayload.ValidateRelatedFields(errPrefix, *oldCA.Name); err != nil {
			return err
		}
		p.CustomSCEPProxyCAUpdatePayload.Preprocess()
		if err := svc.validateCustomSCEPProxyUpdate(ctx, p.CustomSCEPProxyCAUpdatePayload, errPrefix); err != nil {
			return err
		}
		caToUpdate.Type = string(automdm.CATypeCustomSCEPProxy)
		caToUpdate.Name = p.CustomSCEPProxyCAUpdatePayload.Name
		caToUpdate.URL = p.CustomSCEPProxyCAUpdatePayload.URL
		caToUpdate.Challenge = p.CustomSCEPProxyCAUpdatePayload.Challenge
		if caToUpdate.Name != nil {
			caActivityName = *caToUpdate.Name
		} else {
			caActivityName = *oldCA.Name
		}
		activity = automdm.ActivityEditedCustomSCEPProxy{Name: caActivityName}

	case p.SmallstepSCEPProxyCAUpdatePayload != nil:
		if p.SmallstepSCEPProxyCAUpdatePayload.IsEmpty() {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sSmallstep SCEP Proxy CA update payload is empty", errPrefix)}
		}

		if err := p.SmallstepSCEPProxyCAUpdatePayload.ValidateRelatedFields(errPrefix, *oldCA.Name); err != nil {
			return err
		}
		p.SmallstepSCEPProxyCAUpdatePayload.Preprocess()
		if err := svc.validateSmallstepSCEPProxyUpdate(ctx, p.SmallstepSCEPProxyCAUpdatePayload, oldCA, errPrefix); err != nil {
			return err
		}

		caToUpdate.Type = string(automdm.CATypeSmallstep)
		caToUpdate.Name = p.SmallstepSCEPProxyCAUpdatePayload.Name
		caToUpdate.URL = p.SmallstepSCEPProxyCAUpdatePayload.URL
		caToUpdate.ChallengeURL = p.SmallstepSCEPProxyCAUpdatePayload.ChallengeURL
		caToUpdate.Username = p.SmallstepSCEPProxyCAUpdatePayload.Username
		caToUpdate.Password = p.SmallstepSCEPProxyCAUpdatePayload.Password
		if caToUpdate.Name != nil {
			caActivityName = *caToUpdate.Name
		} else {
			caActivityName = *oldCA.Name
		}
		activity = automdm.ActivityEditedSmallstep{Name: caActivityName}
	case p.EJBCAUpdatePayload != nil:
		if p.EJBCAUpdatePayload.IsEmpty() {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sEJBCA CA update payload is empty", errPrefix)}
		}
		if oldCA.Type != string(automdm.CATypeEJBCA) {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sThe certificate authority types must be the same.", errPrefix)}
		}
		update, err := svc.updateEJBCA(ctx, p.EJBCAUpdatePayload, oldCA, errPrefix)
		if err != nil {
			return err
		}
		caToUpdate = *update
		caActivityName = cmp.Or(ptr.ValOrZero(caToUpdate.Name), ptr.ValOrZero(oldCA.Name))
		activity = automdm.ActivityEditedEJBCA{Name: caActivityName}
	case p.GoogleCASCAUpdatePayload != nil:
		if oldCA.Type != string(automdm.CATypeGoogleCAS) {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sThe certificate authority types must be the same.", errPrefix)}
		}
		update, err := svc.updateGoogleCAS(ctx, p.GoogleCASCAUpdatePayload, oldCA, errPrefix)
		if err != nil {
			return err
		}
		if update == nil {
			// Nothing changes, so there's nothing to store or record.
			return nil
		}
		caToUpdate = *update
		caActivityName = cmp.Or(ptr.ValOrZero(caToUpdate.Name), ptr.ValOrZero(oldCA.Name))
		activity = automdm.ActivityEditedGoogleCAS{Name: caActivityName}
	}

	if oldCA.Type != caToUpdate.Type {
		return &automdm.BadRequestError{Message: fmt.Sprintf("%sThe certificate authority types must be the same.", errPrefix)}
	}
	// A CA may have a name from before NDES was reserved in every case, and the validators above only check a name that is sent.
	if oldCA.Type != string(automdm.CATypeNDESSCEPProxy) && caToUpdate.Name == nil && strings.EqualFold(ptr.ValOrZero(oldCA.Name), "NDES") {
		return automdm.NewInvalidArgumentError("name", errPrefix+"CA name cannot be NDES")
	}

	if err := svc.ds.UpdateCertificateAuthorityByID(ctx, id, &caToUpdate); err != nil {
		return err
	}

	if err := svc.NewActivity(ctx, authz.UserFromContext(ctx), activity); err != nil {
		return fmt.Errorf("recording activity for edited %s certificate authority %s: %w", caToUpdate.Type, caActivityName, err)
	}

	return nil
}

func (svc *Service) validateDigicertUpdate(ctx context.Context, digicert *automdm.DigiCertCAUpdatePayload, oldCA *automdm.CertificateAuthority, errPrefix string) error {
	if digicert.Name != nil {
		if err := validateCAName(*digicert.Name, errPrefix); err != nil {
			return err
		}
	}
	// The GET endpoint returns secrets masked, so the mask is rejected like the empty string, and before it reaches DigiCert below.
	if digicert.APIToken != nil && (*digicert.APIToken == "" || *digicert.APIToken == automdm.MaskedPassword) {
		return &automdm.BadRequestError{
			Message: fmt.Sprintf("%sInvalid DigiCert API token. Please correct and try again.", errPrefix),
		}
	}
	if digicert.URL != nil {
		if err := validateURL(*digicert.URL, "DigiCert", errPrefix); err != nil {
			return err
		}

		// We want to generate a DigiCertCA struct with all required fields to verify the new URL.
		// If URL or APIToken are not being updated we use the existing values from oldCA
		digicertCA := automdm.DigiCertCA{
			URL: *digicert.URL,
		}
		if digicert.ProfileID != nil {
			digicertCA.ProfileID = *digicert.ProfileID
		} else {
			digicertCA.ProfileID = *oldCA.ProfileID
		}
		if digicert.APIToken != nil {
			digicertCA.APIToken = *digicert.APIToken
		} else {
			digicertCA.APIToken = *oldCA.APIToken
		}
		if err := svc.digiCertService.VerifyProfileID(ctx, digicertCA); err != nil {
			svc.logger.ErrorContext(ctx, "Failed to validate DigiCert URL", "err", err)
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sCould not verify DigiCert URL: %s. Please correct and try again.", errPrefix, err.Error())}
		}
	}
	if digicert.ProfileID != nil {
		if *digicert.ProfileID == "" {
			return &automdm.BadRequestError{
				Message: fmt.Sprintf("%sInvalid profile GUID. Please correct and try again.", errPrefix),
			}
		}

		// We want to generate a DigiCertCA struct with all required fields to verify the profile ID.
		// If URL or APIToken are not being updated we use the existing values from oldCA
		digicertCA := automdm.DigiCertCA{
			ProfileID: *digicert.ProfileID,
		}
		if digicert.URL != nil {
			digicertCA.URL = *digicert.URL
		} else {
			digicertCA.URL = *oldCA.URL
		}
		if digicert.APIToken != nil {
			digicertCA.APIToken = *digicert.APIToken
		} else {
			digicertCA.APIToken = *oldCA.APIToken
		}
		if err := svc.digiCertService.VerifyProfileID(ctx, digicertCA); err != nil {
			svc.logger.ErrorContext(ctx, "Failed to validate DigiCert profile GUID", "err", err)
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sCould not verify DigiCert profile ID: %s. Please correct and try again.", errPrefix, err.Error())}
		}
	}
	if digicert.CertificateCommonName != nil {
		if err := validateDigicertCACN(*digicert.CertificateCommonName, errPrefix); err != nil {
			return err
		}
	}
	if digicert.CertificateUserPrincipalNames != nil {
		if err := validateDigicertUserPrincipalNames(*digicert.CertificateUserPrincipalNames, errPrefix); err != nil {
			return err
		}
	}
	if digicert.CertificateSeatID != nil {
		if err := validateDigicertSeatID(*digicert.CertificateSeatID, errPrefix); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) validateHydrantUpdate(ctx context.Context, hydrant *automdm.HydrantCAUpdatePayload, oldCA *automdm.CertificateAuthority, errPrefix string) error {
	if hydrant.Name != nil {
		if err := validateCAName(*hydrant.Name, errPrefix); err != nil {
			return err
		}
	}
	if hydrant.URL != nil {
		if err := validateURL(*hydrant.URL, "Hydrant", errPrefix); err != nil {
			return err
		}

		hydrantCAToVerify := automdm.ESTProxyCA{ // The hydrant service for verification only requires the URL.
			URL: *hydrant.URL,
		}
		if err := svc.estService.ValidateESTURL(ctx, hydrantCAToVerify); err != nil {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sInvalid Hydrant URL. Please correct and try again.", errPrefix)}
		}
	}
	if hydrant.ClientID != nil && *hydrant.ClientID == "" {
		return &automdm.BadRequestError{
			Message: fmt.Sprintf("%sInvalid Hydrant Client ID. Please correct and try again.", errPrefix),
		}
	}
	if hydrant.ClientSecret != nil && (*hydrant.ClientSecret == "" || *hydrant.ClientSecret == automdm.MaskedPassword) {
		return &automdm.BadRequestError{
			Message: fmt.Sprintf("%sInvalid Hydrant Client Secret. Please correct and try again.", errPrefix),
		}
	}

	return nil
}

func (svc *Service) validateCustomESTUpdate(ctx context.Context, estUpdate *automdm.CustomESTCAUpdatePayload, oldCA *automdm.CertificateAuthority, errPrefix string) error {
	if estUpdate.Name != nil {
		if err := validateCAName(*estUpdate.Name, errPrefix); err != nil {
			return err
		}
	}
	if estUpdate.URL != nil {
		if err := validateURL(*estUpdate.URL, "EST", errPrefix); err != nil {
			return err
		}

		hydrantCAToVerify := automdm.ESTProxyCA{ // The EST service for verification only requires the URL.
			URL: *estUpdate.URL,
		}
		if err := svc.estService.ValidateESTURL(ctx, hydrantCAToVerify); err != nil {
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sInvalid EST URL. Please correct and try again.", errPrefix)}
		}
	}
	if estUpdate.Username != nil && *estUpdate.Username == "" {
		return &automdm.BadRequestError{
			Message: fmt.Sprintf("%sInvalid EST Username. Please correct and try again.", errPrefix),
		}
	}
	if estUpdate.Password != nil && (*estUpdate.Password == "" || *estUpdate.Password == automdm.MaskedPassword) {
		return &automdm.BadRequestError{
			Message: fmt.Sprintf("%sInvalid EST Password. Please correct and try again.", errPrefix),
		}
	}

	return nil
}

func (svc *Service) validateNDESSCEPProxyUpdate(ctx context.Context, ndesSCEP *automdm.NDESSCEPProxyCAUpdatePayload, oldCA *automdm.CertificateAuthority, errPrefix string) error {
	// some methods in this fuction require the NDESSCEPProxyCA type so we convert the ndes update payload here

	if ndesSCEP.URL != nil {
		if err := validateURL(*ndesSCEP.URL, "NDES SCEP", errPrefix); err != nil {
			return err
		}
		if err := svc.scepConfigService.ValidateSCEPURL(ctx, *ndesSCEP.URL); err != nil {
			svc.logger.ErrorContext(ctx, "Failed to validate NDES SCEP URL", "err", err)
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sAutoMDM couldn't find the CA at the specified URL. Please correct and try again.", errPrefix)}
		}
	}
	if ndesSCEP.AdminURL != nil && *ndesSCEP.AdminURL == "" {
		return &automdm.BadRequestError{
			Message: fmt.Sprintf("%sInvalid NDES SCEP admin URL. Please correct and try again.", errPrefix),
		}
	}
	if ndesSCEP.Username != nil && *ndesSCEP.Username == "" {
		return &automdm.BadRequestError{
			Message: fmt.Sprintf("%sInvalid NDES SCEP username. Please correct and try again.", errPrefix),
		}
	}
	// The GET endpoint returns the password masked, so the mask is rejected along with the
	// empty string (as in the GitOps batch path): changing NDES credentials always requires
	// re-supplying the actual password.
	if ndesSCEP.Password != nil && (*ndesSCEP.Password == "" || *ndesSCEP.Password == automdm.MaskedPassword) {
		return &automdm.BadRequestError{
			Message: fmt.Sprintf("%sInvalid NDES SCEP password. Please correct and try again.", errPrefix),
		}
	}

	// The admin URL, username and password are used together to authenticate against NDES,
	// so a change to any of them means the whole set has to be re-validated against the server.
	if ndesSCEP.AdminURL != nil || ndesSCEP.Username != nil || ndesSCEP.Password != nil {
		// We want to generate a NDESSCEPProxyCA struct with all required fields to verify the admin URL.
		// Any field that is not being updated uses the existing value from oldCA. The checks above
		// reject blank updated values, so cmp.Or only ever falls back for a field left out of the update.
		NDESProxy := automdm.NDESSCEPProxyCA{
			URL:      cmp.Or(ptr.ValOrZero(ndesSCEP.URL), ptr.ValOrZero(oldCA.URL)),
			AdminURL: cmp.Or(ptr.ValOrZero(ndesSCEP.AdminURL), ptr.ValOrZero(oldCA.AdminURL)),
			Username: cmp.Or(ptr.ValOrZero(ndesSCEP.Username), ptr.ValOrZero(oldCA.Username)),
			Password: cmp.Or(ptr.ValOrZero(ndesSCEP.Password), ptr.ValOrZero(oldCA.Password)),
		}

		// If the merged set matches what's already stored there's nothing new to validate.
		// Skip the round-trip so a no-op update doesn't consume a slot in NDES's password
		// cache (each validation retrieves an enrollment challenge password).
		if NDESProxy.AdminURL == ptr.ValOrZero(oldCA.AdminURL) &&
			NDESProxy.Username == ptr.ValOrZero(oldCA.Username) &&
			NDESProxy.Password == ptr.ValOrZero(oldCA.Password) {
			return nil
		}

		if err := svc.scepConfigService.ValidateNDESSCEPAdminURL(ctx, NDESProxy); err != nil {
			svc.logger.ErrorContext(ctx, "Failed to validate NDES SCEP admin URL", "err", err)
			switch {
			case errors.As(err, &scep.NDESPasswordCacheFullError{}):
				return &automdm.BadRequestError{Message: fmt.Sprintf("%sThe NDES password cache is full. Please increase the number of cached passwords in NDES and try again.", errPrefix)}
			case errors.As(err, &scep.NDESInsufficientPermissionsError{}):
				return &automdm.BadRequestError{Message: fmt.Sprintf("%sInsufficient permissions for NDES SCEP admin URL. Please correct and try again.", errPrefix)}
			case errors.As(err, &scep.NDESInvalidError{}):
				return &automdm.BadRequestError{Message: fmt.Sprintf("%sInvalid NDES SCEP admin URL or credentials. Please correct and try again.", errPrefix)}
			default:
				// anything else means the admin URL couldn't be reached at all (timeout, DNS
				// failure, connection refused), not that the server rejected the credentials
				return &automdm.BadRequestError{Message: fmt.Sprintf("%sCouldn't connect to NDES SCEP admin URL. Please correct and try again.", errPrefix)}
			}
		}
	}
	return nil
}

func (svc *Service) validateCustomSCEPProxyUpdate(ctx context.Context, customSCEP *automdm.CustomSCEPProxyCAUpdatePayload, errPrefix string) error {
	if customSCEP.Name != nil {
		if err := validateCAName(*customSCEP.Name, errPrefix); err != nil {
			return err
		}
	}
	if customSCEP.URL != nil {
		if err := validateURL(*customSCEP.URL, "SCEP", errPrefix); err != nil {
			return err
		}
		if err := svc.scepConfigService.ValidateSCEPURL(ctx, *customSCEP.URL); err != nil {
			svc.logger.ErrorContext(ctx, "Failed to validate custom SCEP URL", "err", err)
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sAutoMDM couldn't find the CA at the specified URL. Please correct and try again.", errPrefix)}
		}
	}
	if customSCEP.Challenge != nil && (*customSCEP.Challenge == "" || *customSCEP.Challenge == automdm.MaskedPassword) {
		return &automdm.BadRequestError{
			Message: fmt.Sprintf("%sCustom SCEP Proxy challenge cannot be empty", errPrefix),
		}
	}

	return nil
}

func (svc *Service) validateSmallstepSCEPProxyUpdate(ctx context.Context, smallstep *automdm.SmallstepSCEPProxyCAUpdatePayload, oldCa *automdm.CertificateAuthority, errPrefix string) error {
	if smallstep.Name != nil {
		if err := validateCAName(*smallstep.Name, errPrefix); err != nil {
			return err
		}
	}
	if smallstep.URL != nil {
		if err := validateURL(*smallstep.URL, "SCEP", errPrefix); err != nil {
			return err
		}
		if err := svc.scepConfigService.ValidateSCEPURL(ctx, *smallstep.URL); err != nil {
			svc.logger.ErrorContext(ctx, "Failed to validate Smallstep SCEP URL", "err", err)
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sAutoMDM couldn't find the CA at the specified URL. Please correct and try again.", errPrefix)}
		}
	}
	// Call challenge URL to validate all fields are valid
	if smallstep.ChallengeURL != nil || smallstep.Username != nil || smallstep.Password != nil {

		smallstepSCEPProxy := automdm.SmallstepSCEPProxyCA{
			ChallengeURL: *oldCa.ChallengeURL,
			Username:     *oldCa.Username,
			Password:     *oldCa.Password,
		} // The object we are building to validate fields are valid.

		if smallstep.URL != nil {
			smallstepSCEPProxy.URL = *smallstep.URL
		} else {
			smallstepSCEPProxy.URL = *oldCa.URL
		}

		// Additional validation if url was updated
		if smallstep.ChallengeURL != nil {
			if err := validateURL(*smallstep.ChallengeURL, "Challenge", errPrefix); err != nil {
				return err
			}
			smallstepSCEPProxy.ChallengeURL = *smallstep.ChallengeURL
		}

		if smallstep.Username != nil {
			if *smallstep.Username == "" {
				return &automdm.BadRequestError{
					Message: fmt.Sprintf("%sSmallstep SCEP Proxy username cannot be empty", errPrefix),
				}
			}
			smallstepSCEPProxy.Username = *smallstep.Username
		}

		if smallstep.Password != nil {
			if *smallstep.Password == "" || *smallstep.Password == automdm.MaskedPassword {
				return &automdm.BadRequestError{
					Message: fmt.Sprintf("%sSmallstep SCEP Proxy password cannot be empty", errPrefix),
				}
			}
			smallstepSCEPProxy.Password = *smallstep.Password
		}

		if err := svc.scepConfigService.ValidateSmallstepChallengeURL(ctx, smallstepSCEPProxy); err != nil {
			svc.logger.ErrorContext(ctx, "Failed to validate Smallstep challenge URL", "err", err)
			return &automdm.BadRequestError{Message: fmt.Sprintf("%sInvalid challenge URL or credentials. Please correct and try again.", errPrefix)}
		}
	}

	return nil
}
