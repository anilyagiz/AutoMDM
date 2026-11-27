//go:build !darwin

package profiles

import "github.com/anilyagiz/automdm/v4/server/automdm"

func GetAutoMDMdConfig() (*automdm.MDMAppleAutoMDMdConfig, error) {
	return nil, ErrNotImplemented
}

func IsEnrolledInMDM() (bool, string, error) {
	return false, "", ErrNotImplemented
}

func CheckAssignedEnrollmentProfile(expectedURL string) error {
	return ErrNotImplemented
}

func GetCustomEnrollmentProfileEndUserEmail() (string, error) {
	return "", ErrNotImplemented
}
