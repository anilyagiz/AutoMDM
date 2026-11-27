package service

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
)

func (svc *Service) LinuxHostDiskEncryptionStatus(ctx context.Context, host automdm.Host) (automdm.HostMDMDiskEncryption, error) {
	if !host.IsLUKSSupported() {
		return automdm.HostMDMDiskEncryption{}, nil
	}

	actionRequired := automdm.DiskEncryptionActionRequired
	verified := automdm.DiskEncryptionVerified
	failed := automdm.DiskEncryptionFailed

	key, err := svc.ds.GetHostDiskEncryptionKey(ctx, host.ID)
	if err != nil {
		if automdm.IsNotFound(err) {
			return automdm.HostMDMDiskEncryption{
				Status: &actionRequired,
			}, nil
		}
		return automdm.HostMDMDiskEncryption{}, err
	}

	if key.ClientError != "" {
		return automdm.HostMDMDiskEncryption{
			Status: &failed,
			Detail: key.ClientError,
		}, nil
	}

	if key.Base64Encrypted == "" {
		return automdm.HostMDMDiskEncryption{
			Status: &actionRequired,
		}, nil
	}

	return automdm.HostMDMDiskEncryption{
		Status: &verified,
	}, nil
}

func (svc *Service) GetMDMLinuxProfilesSummary(ctx context.Context, teamId *uint) (summary automdm.MDMProfilesSummary, err error) {
	if err = svc.authz.Authorize(ctx, automdm.MDMConfigProfileAuthz{TeamID: teamId}, automdm.ActionRead); err != nil {
		return summary, ctxerr.Wrap(ctx, err)
	}

	// Linux doesn't have configuration profiles, so if we aren't enforcing disk encryption we have nothing to report
	diskEncryptionConfig, err := svc.ds.GetConfigEnableDiskEncryption(ctx, teamId)
	if err != nil {
		return summary, ctxerr.Wrap(ctx, err)
	} else if !diskEncryptionConfig.LinuxEscrowEnabled {
		return summary, nil
	}

	counts, err := svc.ds.GetLinuxDiskEncryptionSummary(ctx, teamId)
	if err != nil {
		return summary, ctxerr.Wrap(ctx, err)
	}

	return automdm.MDMProfilesSummary{
		Verified: counts.Verified,
		Pending:  counts.ActionRequired,
		Failed:   counts.Failed,
	}, nil
}
