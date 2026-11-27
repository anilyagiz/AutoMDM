package service

import (
	"context"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mock"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/stretchr/testify/assert"
)

func TestLinuxHostDiskEncryptionStatus(t *testing.T) {
	ds := new(mock.Store)
	svc, ctx := newTestService(t, ds, nil, nil)

	actionRequired := automdm.DiskEncryptionActionRequired
	verified := automdm.DiskEncryptionVerified
	failed := automdm.DiskEncryptionFailed

	testcases := []struct {
		name              string
		host              automdm.Host
		keyExists         bool
		clientErrorExists bool
		status            automdm.HostMDMDiskEncryption
		notFound          bool
	}{
		{
			name:              "no key",
			host:              automdm.Host{ID: 1, Platform: "ubuntu"},
			keyExists:         false,
			clientErrorExists: false,
			status: automdm.HostMDMDiskEncryption{
				Status: &actionRequired,
			},
		},
		{
			name:              "key exists",
			host:              automdm.Host{ID: 1, Platform: "ubuntu"},
			keyExists:         true,
			clientErrorExists: false,
			status: automdm.HostMDMDiskEncryption{
				Status: &verified,
			},
		},
		{
			name:              "key exists && client error",
			host:              automdm.Host{ID: 1, Platform: "ubuntu"},
			keyExists:         true,
			clientErrorExists: true,
			status: automdm.HostMDMDiskEncryption{
				Status: &failed,
				Detail: "client error",
			},
		},
		{
			name:              "no key && client error",
			host:              automdm.Host{ID: 1, Platform: "ubuntu"},
			keyExists:         false,
			clientErrorExists: true,
			status: automdm.HostMDMDiskEncryption{
				Status: &failed,
				Detail: "client error",
			},
		},
		{
			name:              "key not found",
			host:              automdm.Host{ID: 1, Platform: "ubuntu"},
			keyExists:         false,
			clientErrorExists: false,
			status: automdm.HostMDMDiskEncryption{
				Status: &actionRequired,
			},
			notFound: true,
		},
		{
			name:   "unsupported platform",
			host:   automdm.Host{ID: 1, Platform: "amzn"},
			status: automdm.HostMDMDiskEncryption{},
		},
	}

	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			ds.GetHostDiskEncryptionKeyFunc = func(ctx context.Context, hostID uint) (*automdm.HostDiskEncryptionKey, error) {
				var encrypted string
				if tt.keyExists {
					encrypted = "encrypted"
				}

				var clientError string
				if tt.clientErrorExists {
					clientError = "client error"
				}

				var nfe notFoundError
				if tt.notFound {
					return nil, &nfe
				}

				return &automdm.HostDiskEncryptionKey{
					HostID:          hostID,
					Base64Encrypted: encrypted,
					Decryptable:     ptr.Bool(true),
					UpdatedAt:       time.Now(),
					ClientError:     clientError,
				}, nil
			}
			ds.GetHostArchivedDiskEncryptionKeyFunc = func(ctx context.Context, host *automdm.Host, _ bool) (*automdm.HostArchivedDiskEncryptionKey, error) {
				return &automdm.HostArchivedDiskEncryptionKey{}, nil
			}

			status, err := svc.LinuxHostDiskEncryptionStatus(ctx, tt.host)
			assert.Nil(t, err)

			assert.Equal(t, tt.status, status)
		})
	}
}
