package microsoft_mdm

import "github.com/anilyagiz/automdm/v4/server/automdm"

// BitLockerPINMinLength and BitLockerPINMaxLength bound a startup PIN. They match what AutoMDM's PIN policies set on the
// host (see SystemDriveRequiresStartupAuthSpec.ConfigurePINPolicies), so this validation and Windows' own check agree.
const (
	BitLockerPINMinLength = 6
	BitLockerPINMaxLength = 20
)

const (
	BitLockerPINLengthMessage       = "BitLocker PIN must be between 6 and 20 characters"
	BitLockerPINInvalidCharsMessage = "BitLocker PIN can only contain letters, numbers, spaces, and symbols from a US English keyboard"
)

// ValidateBitLockerPIN checks a PIN against what Windows accepts with enhanced PINs enabled. It returns an
// InvalidArgumentError naming the "pin" field.
func ValidateBitLockerPIN(pin string) error {
	// Count runes, not bytes, so a multi-byte character cannot pass as a long-enough PIN.
	length := 0
	printableASCII := true
	for _, r := range pin {
		length++
		if r < ' ' || r > '~' {
			printableASCII = false
		}
	}

	switch {
	case !printableASCII:
		// The pre-boot PIN screen always uses the US English keyboard layout, so anything else could be set in Windows
		// but never typed at startup.
		return automdm.NewInvalidArgumentError("pin", BitLockerPINInvalidCharsMessage)
	case length < BitLockerPINMinLength || length > BitLockerPINMaxLength:
		return automdm.NewInvalidArgumentError("pin", BitLockerPINLengthMessage)
	}
	return nil
}
