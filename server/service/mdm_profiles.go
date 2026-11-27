package service

import (
	"fmt"
	"strings"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/mobileconfig"
	"github.com/anilyagiz/automdm/v4/server/variables"
)

// pkcs12CAPrefixes are the AutoMDM variable prefixes of a certificate authority that delivers a PKCS#12 in a profile.
type pkcs12CAPrefixes interface {
	prefixes() (data, password automdm.AutoMDMVarName)
}

type digiCertPrefixes struct{}

func (digiCertPrefixes) prefixes() (automdm.AutoMDMVarName, automdm.AutoMDMVarName) {
	return automdm.AutoMDMVarDigiCertDataPrefix, automdm.AutoMDMVarDigiCertPasswordPrefix
}

type ejbcaPrefixes struct{}

func (ejbcaPrefixes) prefixes() (automdm.AutoMDMVarName, automdm.AutoMDMVarName) {
	return automdm.AutoMDMVarEJBCADataPrefix, automdm.AutoMDMVarEJBCAPasswordPrefix
}

// Certificate Authority structs for MDM profile checking
// to ensure all variables needed for a given CA is present within a single profile.
type PKCS12VarsFound[P pkcs12CAPrefixes] struct {
	dataCA     map[string]struct{}
	passwordCA map[string]struct{}
}

// DigiCertVarsFound and EJBCAVarsFound track the variables of the CAs that deliver a PKCS#12.
type (
	DigiCertVarsFound = PKCS12VarsFound[digiCertPrefixes]
	EJBCAVarsFound    = PKCS12VarsFound[ejbcaPrefixes]
)

// Ok makes sure that both DATA and PASSWORD variables are present for each CA in the profile.
func (d *PKCS12VarsFound[P]) Ok() bool {
	if d == nil {
		return true
	}
	if len(d.dataCA) != len(d.passwordCA) {
		return false
	}
	for ca := range d.dataCA {
		if _, ok := d.passwordCA[ca]; !ok {
			return false
		}
	}
	return true
}

func (d *PKCS12VarsFound[P]) Found() bool {
	return d != nil
}

func (d *PKCS12VarsFound[P]) CAs() []string {
	if d == nil {
		return nil
	}
	keys := make([]string, 0, len(d.dataCA))
	for key := range d.dataCA {
		keys = append(keys, key)
	}
	return keys
}

func (d *PKCS12VarsFound[P]) ErrorMessage() string {
	if d == nil {
		return ""
	}
	var p P
	dataVar, passwordVar := p.prefixes()
	for ca := range d.passwordCA {
		if _, ok := d.dataCA[ca]; !ok {
			return fmt.Sprintf("Missing $AUTOMDM_VAR_%s%s in the profile", dataVar, ca)
		}
	}
	for ca := range d.dataCA {
		if _, ok := d.passwordCA[ca]; !ok {
			return fmt.Sprintf("Missing $AUTOMDM_VAR_%s%s in the profile", passwordVar, ca)
		}
	}
	return fmt.Sprintf("CA name mismatch between $AUTOMDM_VAR_%s<ca_name> and $AUTOMDM_VAR_%s<ca_name> in the profile.", dataVar, passwordVar)
}

func (d *PKCS12VarsFound[P]) SetData(value string) (*PKCS12VarsFound[P], bool) {
	if d == nil {
		d = &PKCS12VarsFound[P]{}
	}
	if d.dataCA == nil {
		d.dataCA = make(map[string]struct{})
	}
	_, alreadyPresent := d.dataCA[value]
	d.dataCA[value] = struct{}{}
	return d, !alreadyPresent
}

func (d *PKCS12VarsFound[P]) SetPassword(value string) (*PKCS12VarsFound[P], bool) {
	if d == nil {
		d = &PKCS12VarsFound[P]{}
	}
	if d.passwordCA == nil {
		d.passwordCA = make(map[string]struct{})
	}
	_, alreadyPresent := d.passwordCA[value]
	d.passwordCA[value] = struct{}{}
	return d, !alreadyPresent
}

type NDESVarsFound struct {
	urlFound       bool
	challengeFound bool
	renewalIdFound bool
}

// Ok makes sure that Challenge, URL, and renewal ID are present.
func (n *NDESVarsFound) Ok() bool {
	if n == nil {
		return true
	}
	return n.urlFound && n.challengeFound && n.renewalIdFound
}

func (n *NDESVarsFound) Found() bool {
	return n != nil
}

func (n *NDESVarsFound) RenewalOnly() bool {
	return n != nil && !n.urlFound && !n.challengeFound && n.renewalIdFound
}

func (n *NDESVarsFound) ErrorMessage() string {
	if n.renewalIdFound && !n.urlFound && !n.challengeFound {
		return automdm.SCEPRenewalIDWithoutURLChallengeErrMsg
	}
	return automdm.NDESSCEPVariablesMissingErrMsg
}

func (n *NDESVarsFound) SetURL() (*NDESVarsFound, bool) {
	if n == nil {
		n = &NDESVarsFound{}
	}
	alreadyPresent := n.urlFound
	n.urlFound = true
	return n, !alreadyPresent
}

func (n *NDESVarsFound) SetChallenge() (*NDESVarsFound, bool) {
	if n == nil {
		n = &NDESVarsFound{}
	}
	alreadyPresent := n.challengeFound
	n.challengeFound = true
	return n, !alreadyPresent
}

func (n *NDESVarsFound) SetRenewalID() (*NDESVarsFound, bool) {
	if n == nil {
		n = &NDESVarsFound{}
	}
	alreadyPresent := n.renewalIdFound
	n.renewalIdFound = true
	return n, !alreadyPresent
}

// ConditionalAccessVarsFound tracks the variables that point a SCEP profile at AutoMDM's conditional access SCEP server.
// The challenge variable is optional: profiles written before it existed carry a global enroll secret instead.
type ConditionalAccessVarsFound struct {
	urlFound       bool
	challengeFound bool
	renewalIdFound bool
}

// Ok makes sure that URL and renewal ID are present. A challenge variable without the URL variable fails too, so a
// one-time challenge is never sent to another SCEP server.
func (c *ConditionalAccessVarsFound) Ok() bool {
	if c == nil {
		return true
	}
	return c.urlFound && c.renewalIdFound
}

func (c *ConditionalAccessVarsFound) Found() bool {
	return c != nil
}

func (c *ConditionalAccessVarsFound) RenewalOnly() bool {
	return c != nil && !c.urlFound && !c.challengeFound && c.renewalIdFound
}

func (c *ConditionalAccessVarsFound) ChallengeFound() bool {
	return c != nil && c.challengeFound
}

func (c *ConditionalAccessVarsFound) ErrorMessage() string {
	return automdm.ConditionalAccessSCEPVariablesMissingErrMsg
}

func (c *ConditionalAccessVarsFound) SetURL() (*ConditionalAccessVarsFound, bool) {
	if c == nil {
		c = &ConditionalAccessVarsFound{}
	}
	alreadyPresent := c.urlFound
	c.urlFound = true
	return c, !alreadyPresent
}

func (c *ConditionalAccessVarsFound) SetChallenge() (*ConditionalAccessVarsFound, bool) {
	if c == nil {
		c = &ConditionalAccessVarsFound{}
	}
	alreadyPresent := c.challengeFound
	c.challengeFound = true
	return c, !alreadyPresent
}

func (c *ConditionalAccessVarsFound) SetRenewalID() (*ConditionalAccessVarsFound, bool) {
	if c == nil {
		c = &ConditionalAccessVarsFound{}
	}
	alreadyPresent := c.renewalIdFound
	c.renewalIdFound = true
	return c, !alreadyPresent
}

type CustomSCEPVarsFound struct {
	urlCA          map[string]struct{}
	challengeCA    map[string]struct{}
	renewalIdFound bool
}

// Ok makes sure that Challenge is present only if URL is also present in SCEP profile.
// This allows the Admin to override the SCEP challenge in the profile.
func (cs *CustomSCEPVarsFound) Ok() bool {
	if cs == nil {
		return true
	}
	if len(cs.challengeCA) != len(cs.urlCA) {
		return false
	}
	if len(cs.challengeCA) == 0 {
		return false
	}
	for ca := range cs.challengeCA {
		if _, ok := cs.urlCA[ca]; !ok {
			return false
		}
	}

	return cs.renewalIdFound
}

func (cs *CustomSCEPVarsFound) Found() bool {
	return cs != nil
}

func (cs *CustomSCEPVarsFound) RenewalOnly() bool {
	return cs != nil && len(cs.urlCA) == 0 && len(cs.challengeCA) == 0 && cs.renewalIdFound
}

func (cs *CustomSCEPVarsFound) CAs() []string {
	if cs == nil {
		return nil
	}
	keys := make([]string, 0, len(cs.urlCA))
	for key := range cs.urlCA {
		keys = append(keys, key)
	}
	return keys
}

func (cs *CustomSCEPVarsFound) ErrorMessage() string {
	if cs.renewalIdFound && len(cs.challengeCA) == 0 && len(cs.urlCA) == 0 {
		return automdm.SCEPRenewalIDWithoutURLChallengeErrMsg
	}

	if !cs.renewalIdFound || len(cs.challengeCA) == 0 || len(cs.urlCA) == 0 {
		return fmt.Sprintf("SCEP profile for custom SCEP certificate authority requires: $AUTOMDM_VAR_%s<CA_NAME>, $AUTOMDM_VAR_%s<CA_NAME>, and $AUTOMDM_VAR_%s variables.", automdm.AutoMDMVarCustomSCEPChallengePrefix, automdm.AutoMDMVarCustomSCEPProxyURLPrefix, automdm.AutoMDMVarCertificateRenewalID)
	}

	for ca := range cs.challengeCA {
		if _, ok := cs.urlCA[ca]; !ok {
			return fmt.Sprintf("Missing $AUTOMDM_VAR_%s%s in the profile", automdm.AutoMDMVarCustomSCEPProxyURLPrefix, ca)
		}
	}
	for ca := range cs.urlCA {
		if _, ok := cs.challengeCA[ca]; !ok {
			return fmt.Sprintf("Missing $AUTOMDM_VAR_%s%s in the profile", automdm.AutoMDMVarCustomSCEPChallengePrefix, ca)
		}
	}

	return fmt.Sprintf("CA name mismatch between $AUTOMDM_VAR_%s<ca_name> and $AUTOMDM_VAR_%s<ca_name> in the profile.",
		automdm.AutoMDMVarCustomSCEPProxyURLPrefix, automdm.AutoMDMVarCustomSCEPChallengePrefix)
}

func (cs *CustomSCEPVarsFound) SetURL(value string) (*CustomSCEPVarsFound, bool) {
	if cs == nil {
		cs = &CustomSCEPVarsFound{}
	}
	if cs.urlCA == nil {
		cs.urlCA = make(map[string]struct{})
	}
	_, alreadyPresent := cs.urlCA[value]
	cs.urlCA[value] = struct{}{}
	return cs, !alreadyPresent
}

func (cs *CustomSCEPVarsFound) SetChallenge(value string) (*CustomSCEPVarsFound, bool) {
	if cs == nil {
		cs = &CustomSCEPVarsFound{}
	}
	if cs.challengeCA == nil {
		cs.challengeCA = make(map[string]struct{})
	}
	_, alreadyPresent := cs.challengeCA[value]
	cs.challengeCA[value] = struct{}{}
	return cs, !alreadyPresent
}

func (cs *CustomSCEPVarsFound) SetRenewalID() (*CustomSCEPVarsFound, bool) {
	if cs == nil {
		cs = &CustomSCEPVarsFound{}
	}
	alreadyPresent := cs.renewalIdFound
	cs.renewalIdFound = true
	return cs, !alreadyPresent
}

type SmallstepVarsFound struct {
	urlCA          map[string]struct{}
	challengeCA    map[string]struct{}
	renewalIdFound bool
}

// Ok makes sure that Challenge is present only if URL is also present in SCEP profile.
// This allows the Admin to override the SCEP challenge in the profile.
func (cs *SmallstepVarsFound) Ok() bool {
	if cs == nil {
		return true
	}
	// There must be a 1:1 mapping between URL and Challenge CAs
	if len(cs.challengeCA) != len(cs.urlCA) {
		return false
	}
	if len(cs.challengeCA) == 0 {
		return false
	}
	for ca := range cs.challengeCA {
		if _, ok := cs.urlCA[ca]; !ok {
			// Unable to find matching URL CA for Challenge CA
			return false
		}
	}
	return cs.renewalIdFound
}

func (cs *SmallstepVarsFound) Found() bool {
	return cs != nil
}

func (cs *SmallstepVarsFound) RenewalOnly() bool {
	return cs != nil && len(cs.urlCA) == 0 && len(cs.challengeCA) == 0 && cs.renewalIdFound
}

func (cs *SmallstepVarsFound) CAs() []string {
	if cs == nil {
		return nil
	}
	keys := make([]string, 0, len(cs.urlCA))
	for key := range cs.urlCA {
		keys = append(keys, key)
	}
	return keys
}

func (cs *SmallstepVarsFound) ErrorMessage() string {
	if cs.renewalIdFound && len(cs.challengeCA) == 0 && len(cs.urlCA) == 0 {
		return automdm.SCEPRenewalIDWithoutURLChallengeErrMsg
	}
	if !cs.renewalIdFound || len(cs.challengeCA) == 0 || len(cs.urlCA) == 0 {
		return fmt.Sprintf("SCEP profile for Smallstep certificate authority requires: $AUTOMDM_VAR_%s<CA_NAME>, $AUTOMDM_VAR_%s<CA_NAME>, and $AUTOMDM_VAR_%s variables.", automdm.AutoMDMVarSmallstepSCEPChallengePrefix, automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix, automdm.AutoMDMVarCertificateRenewalID)
	}
	for ca := range cs.challengeCA {
		if _, ok := cs.urlCA[ca]; !ok {
			return fmt.Sprintf("Missing $AUTOMDM_VAR_%s%s in the profile", automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix, ca)
		}
	}
	for ca := range cs.urlCA {
		if _, ok := cs.challengeCA[ca]; !ok {
			return fmt.Sprintf("Missing $AUTOMDM_VAR_%s%s in the profile", automdm.AutoMDMVarSmallstepSCEPChallengePrefix, ca)
		}
	}
	return fmt.Sprintf("CA name mismatch between $AUTOMDM_VAR_%s<ca_name> and $AUTOMDM_VAR_%s<ca_name> in the profile.",
		automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix, automdm.AutoMDMVarSmallstepSCEPChallengePrefix)
}

func (cs *SmallstepVarsFound) SetURL(value string) (*SmallstepVarsFound, bool) {
	if cs == nil {
		cs = &SmallstepVarsFound{}
	}
	if cs.urlCA == nil {
		cs.urlCA = make(map[string]struct{})
	}
	_, alreadyPresent := cs.urlCA[value]
	cs.urlCA[value] = struct{}{}
	return cs, !alreadyPresent
}

func (cs *SmallstepVarsFound) SetChallenge(value string) (*SmallstepVarsFound, bool) {
	if cs == nil {
		cs = &SmallstepVarsFound{}
	}
	if cs.challengeCA == nil {
		cs.challengeCA = make(map[string]struct{})
	}
	_, alreadyPresent := cs.challengeCA[value]
	cs.challengeCA[value] = struct{}{}
	return cs, !alreadyPresent
}

func (cs *SmallstepVarsFound) SetRenewalID() (*SmallstepVarsFound, bool) {
	if cs == nil {
		cs = &SmallstepVarsFound{}
	}
	alreadyPresent := cs.renewalIdFound
	cs.renewalIdFound = true
	return cs, !alreadyPresent
}

var (
	renewalIDNotInSubjectMsg = "Variable " + automdm.AutoMDMVarCertificateRenewalID.WithPrefix() +
		" must be in the SCEP certificate's organizational unit (OU). In some use cases, it can go in the common name (CN) instead."
	renewalIDDuplicateMsg = "Variable " + automdm.AutoMDMVarCertificateRenewalID.WithPrefix() +
		" can only be used once in a profile. Put it in the SCEP certificate's organizational unit (OU). " +
		"In some use cases, it can go in the common name (CN) instead."
)

// caVarValidators holds platform-specific checks run after a CA's variables are
// found and complete. A nil field skips the check for that CA.
type caVarValidators struct {
	digiCert          func(contents string, digiCertVars *DigiCertVarsFound) error
	customSCEP        func(contents string, customSCEPVars *CustomSCEPVarsFound) error
	ndes              func(contents string, ndesVars *NDESVarsFound) error
	smallstep         func(contents string, smallstepVars *SmallstepVarsFound) error
	conditionalAccess func(contents string, conditionalAccessVars *ConditionalAccessVarsFound) error
}

// validateProfileCertificateAuthorityVariables checks that all AutoMDM variables
// used in the given profile contents correspond to existing Certificate Authorities,
// and that is mapped to a set of CA vars, that can later be used for validation.
func validateProfileCertificateAuthorityVariables(profileContents string, lic *automdm.LicenseInfo, groupedCAs *automdm.GroupedCertificateAuthorities,
	validators caVarValidators,
) error {
	automdmVars := variables.FindKeepDuplicates(profileContents)
	if len(automdmVars) == 0 {
		return nil
	}

	// Check for premium license if the profile contains AutoMDM variables
	if lic == nil || !lic.IsPremium() {
		return automdm.ErrMissingLicense
	}

	var (
		digiCertVars          *DigiCertVarsFound
		ejbcaVars             *EJBCAVarsFound
		ndesVars              *NDESVarsFound
		smallstepVars         *SmallstepVarsFound
		customSCEPVars        *CustomSCEPVarsFound
		conditionalAccessVars *ConditionalAccessVarsFound
	)
	for _, k := range automdmVars {
		caFound := false
		ok := true
		switch {
		case strings.HasPrefix(k, string(automdm.AutoMDMVarDigiCertDataPrefix)):
			caName := strings.TrimPrefix(k, string(automdm.AutoMDMVarDigiCertDataPrefix))
			for _, ca := range groupedCAs.DigiCert {
				if ca.Name == caName {
					caFound = true
					digiCertVars, ok = digiCertVars.SetData(caName)
					break
				}
			}
			if !caFound {
				ok = false
			}
		case strings.HasPrefix(k, string(automdm.AutoMDMVarDigiCertPasswordPrefix)):
			caName := strings.TrimPrefix(k, string(automdm.AutoMDMVarDigiCertPasswordPrefix))
			for _, ca := range groupedCAs.DigiCert {
				if ca.Name == caName {
					caFound = true
					digiCertVars, ok = digiCertVars.SetPassword(caName)
					break
				}
			}
			if !caFound {
				ok = false
			}
		case strings.HasPrefix(k, string(automdm.AutoMDMVarEJBCADataPrefix)):
			caName := strings.TrimPrefix(k, string(automdm.AutoMDMVarEJBCADataPrefix))
			for _, ca := range groupedCAs.EJBCA {
				if ca.Name == caName {
					caFound = true
					ejbcaVars, ok = ejbcaVars.SetData(caName)
					break
				}
			}
			if !caFound {
				ok = false
			}
		case strings.HasPrefix(k, string(automdm.AutoMDMVarEJBCAPasswordPrefix)):
			caName := strings.TrimPrefix(k, string(automdm.AutoMDMVarEJBCAPasswordPrefix))
			for _, ca := range groupedCAs.EJBCA {
				if ca.Name == caName {
					caFound = true
					ejbcaVars, ok = ejbcaVars.SetPassword(caName)
					break
				}
			}
			if !caFound {
				ok = false
			}
		case strings.HasPrefix(k, string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix)):
			caName := strings.TrimPrefix(k, string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix))
			for _, ca := range groupedCAs.CustomScepProxy {
				if ca.Name == caName {
					caFound = true
					customSCEPVars, ok = customSCEPVars.SetURL(caName)
					break
				}
			}
			if !caFound {
				ok = false
			}
		case strings.HasPrefix(k, string(automdm.AutoMDMVarCustomSCEPChallengePrefix)):
			caName := strings.TrimPrefix(k, string(automdm.AutoMDMVarCustomSCEPChallengePrefix))
			for _, ca := range groupedCAs.CustomScepProxy {
				if ca.Name == caName {
					caFound = true
					customSCEPVars, ok = customSCEPVars.SetChallenge(caName)
					break
				}
			}
			if !caFound {
				ok = false
			}
		case strings.HasPrefix(k, string(automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix)):
			caName := strings.TrimPrefix(k, string(automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix))
			for _, ca := range groupedCAs.Smallstep {
				if ca.Name == caName {
					caFound = true
					smallstepVars, ok = smallstepVars.SetURL(caName)
					break
				}
			}
			if !caFound {
				ok = false
			}
		case strings.HasPrefix(k, string(automdm.AutoMDMVarSmallstepSCEPChallengePrefix)):
			caName := strings.TrimPrefix(k, string(automdm.AutoMDMVarSmallstepSCEPChallengePrefix))
			for _, ca := range groupedCAs.Smallstep {
				if ca.Name == caName {
					caFound = true
					smallstepVars, ok = smallstepVars.SetChallenge(caName)
					break
				}
			}
			if !caFound {
				ok = false
			}
		case k == string(automdm.AutoMDMVarNDESSCEPProxyURL):
			caFound = true
			ndesVars, ok = ndesVars.SetURL()
		case k == string(automdm.AutoMDMVarNDESSCEPChallenge):
			caFound = true
			ndesVars, ok = ndesVars.SetChallenge()
		case k == string(automdm.AutoMDMVarConditionalAccessSCEPURL):
			caFound = true
			conditionalAccessVars, ok = conditionalAccessVars.SetURL()
		case k == string(automdm.AutoMDMVarConditionalAccessSCEPChallenge):
			caFound = true
			conditionalAccessVars, ok = conditionalAccessVars.SetChallenge()
		case k == string(automdm.AutoMDMVarSCEPRenewalID), k == string(automdm.AutoMDMVarCertificateRenewalID):
			caFound = true
			// Custom SCEP, NDES, Smallstep, and conditional access all share the
			// renewal-ID AutoMDM variable. The legacy SCEP_RENEWAL_ID and the
			// preferred CERTIFICATE_RENEWAL_ID names are interchangeable here.

			customSCEPVars, ok = customSCEPVars.SetRenewalID()
			if ok {
				ndesVars, ok = ndesVars.SetRenewalID()
				if ok {
					smallstepVars, ok = smallstepVars.SetRenewalID()
					if ok {
						conditionalAccessVars, ok = conditionalAccessVars.SetRenewalID()
					}
				}
			}
		}

		if !ok {
			if !caFound {
				return &automdm.BadRequestError{Message: fmt.Sprintf("AutoMDM variable $AUTOMDM_VAR_%s does not exist.", k)}
			}

			if k == string(automdm.AutoMDMVarSCEPRenewalID) || k == string(automdm.AutoMDMVarCertificateRenewalID) {
				// Special message for renewal ID — surface the preferred name
				// in the error regardless of which form the user authored.
				return &automdm.BadRequestError{Message: renewalIDDuplicateMsg}
			}

			return &automdm.BadRequestError{Message: fmt.Sprintf("AutoMDM variable $AUTOMDM_VAR_%s is already present in configuration profile.", k)}
		}
	}

	if digiCertVars.Found() {
		if !digiCertVars.Ok() {
			return &automdm.BadRequestError{Message: digiCertVars.ErrorMessage()}
		}
		if validators.digiCert != nil {
			err := validators.digiCert(profileContents, digiCertVars)
			if err != nil {
				return err
			}
		}
	}

	if ejbcaVars.Found() {
		if !ejbcaVars.Ok() {
			return &automdm.BadRequestError{Message: ejbcaVars.ErrorMessage()}
		}
		// Only supported for Apple.
		if err := additionalEJBCAValidation(profileContents, ejbcaVars); err != nil {
			return err
		}
	}

	// Since custom SCEP, NDES, Smallstep, and conditional access share the renewal ID AutoMDM variable, we need to figure out
	// which one to validate.
	if customSCEPVars.Found() || ndesVars.Found() || smallstepVars.Found() || conditionalAccessVars.Found() {
		if ndesVars.RenewalOnly() {
			ndesVars = nil
		}
		if conditionalAccessVars.RenewalOnly() {
			conditionalAccessVars = nil
		}
		if customSCEPVars.RenewalOnly() {
			customSCEPVars = nil
		}
		if smallstepVars.RenewalOnly() {
			smallstepVars = nil
		}
		// ACME and non-proxied SCEP profiles legitimately have only the
		// renewal-ID variable; bypass the "needs URL/Challenge" error when
		// such a payload is present. Windows profiles parse as no-payload
		// and remain subject to the check.
		hasRenewableCertPayload := false
		mc := mobileconfig.Mobileconfig(profileContents)
		for _, pt := range []string{mobileconfig.ACMEPayloadType, mobileconfig.SCEPPayloadType} {
			if found, err := mc.HasPayloadType(pt); err == nil && found {
				hasRenewableCertPayload = true
				break
			}
		}
		if ndesVars == nil && smallstepVars == nil && customSCEPVars == nil && conditionalAccessVars == nil && !hasRenewableCertPayload {
			return &automdm.BadRequestError{Message: automdm.SCEPRenewalIDWithoutURLChallengeErrMsg}
		}
	}

	if customSCEPVars.Found() {
		if !customSCEPVars.Ok() {
			return &automdm.BadRequestError{Message: customSCEPVars.ErrorMessage()}
		}
		if validators.customSCEP != nil {
			err := validators.customSCEP(profileContents, customSCEPVars)
			if err != nil {
				return err
			}
		}
	}
	if ndesVars.Found() {
		if !ndesVars.Ok() {
			return &automdm.BadRequestError{Message: ndesVars.ErrorMessage()}
		}
		if validators.ndes != nil {
			err := validators.ndes(profileContents, ndesVars)
			if err != nil {
				return err
			}
		}
	}
	if smallstepVars.Found() {
		if !smallstepVars.Ok() {
			return &automdm.BadRequestError{Message: smallstepVars.ErrorMessage()}
		}
		if validators.smallstep != nil {
			err := validators.smallstep(profileContents, smallstepVars)
			if err != nil {
				return err
			}
		}
	}
	if conditionalAccessVars.Found() {
		if !conditionalAccessVars.Ok() {
			return &automdm.BadRequestError{Message: conditionalAccessVars.ErrorMessage()}
		}
		if validators.conditionalAccess != nil {
			err := validators.conditionalAccess(profileContents, conditionalAccessVars)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
