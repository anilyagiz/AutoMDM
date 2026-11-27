//go:build !windows

package toast

import "errors"

// Show is only implemented on Windows.
func Show(Notification) error {
	return errors.ErrUnsupported
}

// Remove is only implemented on Windows.
func Remove(string, string) error {
	return errors.ErrUnsupported
}

// RegisterAutoMDMDesktopAppID is only implemented on Windows.
func RegisterAutoMDMDesktopAppID(string, []byte) error {
	return errors.ErrUnsupported
}
