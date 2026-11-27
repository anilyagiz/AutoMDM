package mobileconfig

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"

	"github.com/anilyagiz/automdm/v4/server/mdm"
	"github.com/anilyagiz/automdm/v4/server/variables"
	"github.com/micromdm/plist"
)

const (
	// AutoMDMFileVaultPayloadIdentifier is the value for the PayloadIdentifier
	// used by AutoMDM to configure FileVault and FileVault Escrow.
	AutoMDMFileVaultPayloadIdentifier   = "com.fleetdm.fleet.mdm.filevault"
	AutoMDMFileVaultPayloadType         = "com.apple.MCX.FileVault2"
	AutoMDMCustomSettingsPayloadType    = "com.apple.MCX"
	AutoMDMRecoveryKeyEscrowPayloadType = "com.apple.security.FDERecoveryKeyEscrow"

	// ACMEPayloadType is the Apple-defined PayloadType for ACME certificate
	// payloads (com.apple.security.acme).
	ACMEPayloadType = "com.apple.security.acme"
	// SCEPPayloadType is the Apple-defined PayloadType for SCEP certificate
	// payloads (com.apple.security.scep).
	SCEPPayloadType                        = "com.apple.security.scep"
	DiskEncryptionProfileRestrictionErrMsg = "Couldn't add. The configuration profile can't include FileVault settings."

	// AutoMDMdConfigPayloadIdentifier is the value for the PayloadIdentifier used
	// by automdmd to read configuration values from the system.
	AutoMDMdConfigPayloadIdentifier = "com.fleetdm.fleetd.config"

	// AutoMDMCARootConfigPayloadIdentifier TODO
	AutoMDMCARootConfigPayloadIdentifier = "com.fleetdm.caroot"

	// AutoMDMEnrollmentPayloadIdentifier is the value for the PayloadIdentifier used
	// by AutoMDM to enroll a device with the MDM server.
	AutoMDMEnrollmentPayloadIdentifier = "com.fleetdm.fleet.mdm.apple.mdm"

	// AutoMDMEnrollReferenceKey is the key used by AutoMDM of the URL query parameter representing a unique
	// identifier for an MDM enrollment. The unique value of the query parameter is appended to the
	// AutoMDM server URL when an MDM enrollment profile is generated for download by a device.
	//
	// TODO: We have some inconsistencies where we use enroll_reference sometimes and
	// enrollment_reference other times. It really should be the same everywhere, but
	// it seems to be working now because the values are matching where they need to match.
	// We should clean this up at some point and update hardcoded values in the codebase.
	AutoMDMEnrollReferenceKey = "enroll_reference"
)

// AutoMDMPayloadIdentifiers returns a map of PayloadIdentifier strings
// that are handled and delivered by AutoMDM.
//
// TODO(roperzh): at some point we should also include
// apple_mdm.FletPayloadIdentifier here too, but that requires moving a lot of
// files around due to import cycles.
func AutoMDMPayloadIdentifiers() map[string]struct{} {
	return map[string]struct{}{
		AutoMDMFileVaultPayloadIdentifier:    {},
		AutoMDMdConfigPayloadIdentifier:      {},
		AutoMDMCARootConfigPayloadIdentifier: {},
	}
}

// AutoMDMPayloadTypes returns a map of PayloadType strings
// that are fully or partially handled and delivered by AutoMDM.
func AutoMDMPayloadTypes() map[string]struct{} {
	return map[string]struct{}{
		AutoMDMRecoveryKeyEscrowPayloadType:      {},
		AutoMDMFileVaultPayloadType:              {},
		AutoMDMCustomSettingsPayloadType:         {},
		"com.apple.security.FDERecoveryRedirect": {}, // no longer supported in macOS 10.13 and later
	}
}

// Mobileconfig is the byte slice corresponding to an XML property list (i.e. plist) representation
// of an Apple MDM configuration profile in AutoMDM.
//
// Configuration profiles are used to configure Apple devices. See also
// https://developer.apple.com/documentation/devicemanagement/configuring_multiple_devices_using_profiles.
type Mobileconfig []byte

type Parsed struct {
	PayloadIdentifier  string
	PayloadDisplayName string
	PayloadType        string
	PayloadScope       string
}

func (mc Mobileconfig) IsSignedProfile() bool {
	trimmed := bytes.TrimSpace(mc)
	if bytes.HasPrefix(trimmed, []byte("${AUTOMDM_")) || bytes.HasPrefix(trimmed, []byte("$AUTOMDM_")) {
		return false // Not a signed profile since it only contains secret variable.
	}
	return !bytes.HasPrefix(bytes.TrimSpace(mc), []byte("<?xml"))
}

// ParseConfigProfile attempts to parse the Mobileconfig byte slice as an AutoMDM MDMAppleConfigProfile.
//
// The byte slice must be XML or PKCS7 parseable. AutoMDM also requires that it contains both
// a PayloadIdentifier and a PayloadDisplayName and that it has PayloadType set to "Configuration".
//
// Adapted from https://github.com/micromdm/micromdm/blob/main/platform/profile/profile.go
func (mc Mobileconfig) ParseConfigProfile() (*Parsed, error) {
	mcBytes := mc
	// Remove AutoMDM variables expected in <data> section.
	mcBytes = variables.ProfileDataVariableRegex.ReplaceAll(mcBytes, []byte(""))
	if mc.IsSignedProfile() {
		return nil, errors.New("signed profiles are not supported")
	}
	var p Parsed
	if err := plist.Unmarshal(mcBytes, &p); err != nil {
		if strings.Contains(err.Error(), "illegal base64 data") || strings.Contains(err.Error(), "invalid character entity") || strings.Contains(err.Error(), "expected attribute name in element") {
			return nil, errors.New("The configuration profile contains special characters (&, <, >, ', \") that must be XML-escaped. Please escape them (e.g. & → &amp;, < → &lt;) and try again.")
		}
		return nil, err
	}
	if p.PayloadType != "Configuration" {
		return nil, fmt.Errorf("invalid PayloadType: %s", p.PayloadType)
	}
	if p.PayloadIdentifier == "" {
		return nil, errors.New("empty PayloadIdentifier in profile")
	}
	if p.PayloadDisplayName == "" {
		return nil, errors.New("empty PayloadDisplayName in profile")
	}
	// PayloadScope is optional and according to
	// Apple(https://developer.apple.com/business/documentation/Configuration-Profile-Reference.pdf
	// p6) defaults to "User". We've always sent them to the Device channel but now we're saying
	// "User" means use the user channel. For backwards compatibility we are maintaining existing
	// behavior of defaulting to device channel below but we should consider whether this is correct.
	if p.PayloadScope == "" {
		p.PayloadScope = "System"
	}
	if p.PayloadScope != "System" && p.PayloadScope != "User" {
		return nil, fmt.Errorf("invalid PayloadScope: %s", p.PayloadScope)
	}

	return &p, nil
}

type payloadSummary struct {
	Type       string
	Identifier string
	Name       string
}

// payloadSummary attempts to parse the PayloadContent list of the Mobileconfig's TopLevel object.
// It returns the PayloadType for each PayloadContentItem.
//
// See also https://developer.apple.com/documentation/devicemanagement/toplevel
func (mc Mobileconfig) payloadSummary() ([]payloadSummary, error) {
	mcBytes := mc
	// Remove AutoMDM variables expected in <data> section.
	mcBytes = variables.ProfileDataVariableRegex.ReplaceAll(mcBytes, []byte(""))
	if mc.IsSignedProfile() {
		return nil, errors.New("signed profiles are not supported")
	}

	// unmarshal the values we need from the top-level object
	var tlo struct {
		IsEncrypted    bool
		PayloadContent []map[string]interface{}
		PayloadType    string
	}
	err := plist.Unmarshal(mcBytes, &tlo)
	if err != nil {
		if strings.Contains(err.Error(), "illegal base64 data") || strings.Contains(err.Error(), "invalid character entity") || strings.Contains(err.Error(), "expected attribute name in element") {
			return nil, errors.New("The configuration profile contains special characters (&, <, >, ', \") that must be XML-escaped. Please escape them (e.g. & → &amp;, < → &lt;) and try again.")
		}
		return nil, err
	}
	// confirm that the top-level payload type matches the expected value
	if tlo.PayloadType != "Configuration" {
		return nil, &ErrInvalidPayloadType{tlo.PayloadType}
	}

	if len(tlo.PayloadContent) < 1 {
		if tlo.IsEncrypted {
			return nil, ErrEncryptedPayloadContent
		}
		return nil, ErrEmptyPayloadContent
	}

	// extract the payload types of each payload content item from the array of
	// payload dictionaries
	var result []payloadSummary
	for _, payloadDict := range tlo.PayloadContent {
		summary := payloadSummary{}

		pt, ok := payloadDict["PayloadType"]
		if ok {
			if s, ok := pt.(string); ok {
				summary.Type = s
			}
		}

		pi, ok := payloadDict["PayloadIdentifier"]
		if ok {
			if s, ok := pi.(string); ok {
				summary.Identifier = s
			}
		}

		pdn, ok := payloadDict["PayloadDisplayName"]
		if ok {
			if s, ok := pdn.(string); ok {
				summary.Name = s
			}
		}

		if summary.Type != "" || summary.Identifier != "" || summary.Name != "" {
			result = append(result, summary)
		}

	}

	return result, nil
}

// HasPayloadType reports whether the profile contains at least one payload
// content item with the given PayloadType. Returns an error if the profile
// cannot be parsed.
func (mc Mobileconfig) HasPayloadType(payloadType string) (bool, error) {
	summaries, err := mc.payloadSummary()
	if err != nil {
		return false, err
	}
	for _, s := range summaries {
		if s.Type == payloadType {
			return true, nil
		}
	}
	return false, nil
}

func (mc *Mobileconfig) ScreenPayloads(allowCustomFileVault bool) error {
	pct, err := mc.payloadSummary()
	if err != nil {
		// don't error if there's nothing for us to screen.
		if !errors.Is(err, ErrEmptyPayloadContent) && !errors.Is(err, ErrEncryptedPayloadContent) {
			return err
		}
	}

	automdmNames := mdm.AutoMDMReservedProfileNames()
	automdmIdentifiers := AutoMDMPayloadIdentifiers()
	automdmTypes := AutoMDMPayloadTypes()
	screenedTypes := []string{}
	screenedIdentifiers := []string{}
	screenedNames := []string{}
	for _, t := range pct {
		if _, ok := automdmTypes[t.Type]; ok {
			screenedTypes = append(screenedTypes, t.Type)
		}
		if _, ok := automdmIdentifiers[t.Identifier]; ok {
			screenedIdentifiers = append(screenedIdentifiers, t.Identifier)
		}
		if _, ok := automdmNames[t.Name]; ok {
			screenedNames = append(screenedNames, t.Name)
		}
	}

	if len(screenedTypes) > 0 {
		var unsupportedTypes []string
		for _, t := range screenedTypes {
			switch t {
			case AutoMDMFileVaultPayloadType, AutoMDMRecoveryKeyEscrowPayloadType:
				if !allowCustomFileVault {
					return errors.New(DiskEncryptionProfileRestrictionErrMsg)
				}
			case AutoMDMCustomSettingsPayloadType:
				contains, err := ContainsFDEFileVaultOptionsPayload(*mc)
				if err != nil {
					return fmt.Errorf("checking for FDEVileVaultOptions payload: %w", err)
				}
				if contains && !allowCustomFileVault {
					return errors.New(DiskEncryptionProfileRestrictionErrMsg)
				}
			default:
				unsupportedTypes = append(unsupportedTypes, t)
			}
		}
		if len(unsupportedTypes) > 0 {
			return fmt.Errorf("unsupported PayloadType(s): %s", strings.Join(screenedTypes, ", "))
		}
	}

	if len(screenedIdentifiers) > 0 {
		return fmt.Errorf("unsupported PayloadIdentifier(s): %s", strings.Join(screenedIdentifiers, ", "))
	}

	if len(screenedNames) > 0 {
		return fmt.Errorf("unsupported PayloadDisplayName(s): %s", strings.Join(screenedNames, ", "))
	}

	return nil
}

type ErrInvalidPayloadType struct {
	payloadType string
}

func (e ErrInvalidPayloadType) Error() string {
	return fmt.Sprintf("invalid PayloadType: %s", e.payloadType)
}

var (
	ErrEmptyPayloadContent     = errors.New("empty PayloadContent")
	ErrEncryptedPayloadContent = errors.New("encrypted PayloadContent")
)

// XMLEscapeString returns the escaped XML equivalent of the plain text data s.
func XMLEscapeString(s string) (string, error) {
	// No fast path: xml.EscapeText also replaces runes outside the XML character
	// range (e.g. control bytes), which a ContainsAny check would let through raw.
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return "", err
	}

	return b.String(), nil
}
