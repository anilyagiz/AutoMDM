// Package conditional_access_google holds the Google conditional access clients: the
// automdm.com relay and the Cloud Identity API.
package conditional_access_google

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// Relay registers this server with the relay on automdm.com. Customer IDs include the
// leading "C". serverURL is this server's current URL, which automdm.com calls back.
type Relay interface {
	// Register replaces the secret when called again from the same server.
	Register(ctx context.Context, serverURL, customerID string) (*Registration, error)
	// Deregister returns an automdm.IsNotFound error when there's no matching registration.
	Deregister(ctx context.Context, serverURL, customerID, secret string) error
	// SignInURL is automdm.com's sign-in page for an iOS or iPadOS host, which carries
	// the host's single-use registration token back to this server.
	SignInURL(customerID, hostUUID, token string) (string, error)
}

// CloudIdentity calls the Cloud Identity API for one customer as the delegated admin.
type CloudIdentity interface {
	// CheckDelegation lists one device under the customer. It proves read access only.
	CheckDelegation(ctx context.Context) error
	// SetClientState writes the set fields of state on AutoMDM's client state of a
	// DeviceUser. It returns an automdm.IsNotFound error when Google has no such DeviceUser.
	SetClientState(ctx context.Context, deviceUserName string, state automdm.GoogleConditionalAccessClientState) error
	// ListDeviceUserNames lists the DeviceUser names of a device, by its device ID. It
	// returns an IsStale error when Google has no such device, which is an automdm.IsNotFound
	// error too, or when the device left the customer's Workspace.
	ListDeviceUserNames(ctx context.Context, deviceID string) ([]string, error)
	// FindDeviceIDs returns the device IDs of the customer's records whose deviceId field
	// is androidDeviceID, the Android Management API device ID. Google doesn't keep the
	// field unique, so a re-enrolled device can have more than one record.
	FindDeviceIDs(ctx context.Context, androidDeviceID string) ([]string, error)
	// DeviceExists reports whether Google still has the device, by its device ID. A
	// device that left the customer's Workspace is a 403, returned as an IsStale error
	// for the caller to decide on.
	DeviceExists(ctx context.Context, deviceID string) (bool, error)
}

// CloudIdentityFactory builds a CloudIdentity without calling Google. Build one per
// request or cron run.
type CloudIdentityFactory func(ctx context.Context, creds Credentials) (CloudIdentity, error)

// Credentials identify the customer and authenticate the Cloud Identity calls.
type Credentials struct {
	ServiceAccountKey []byte
	// AdminEmail is the Workspace admin the service account impersonates.
	AdminEmail string
	// CustomerID includes the leading "C".
	CustomerID string
}
