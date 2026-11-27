package update

import (
	"errors"
	"sync/atomic"

	"github.com/anilyagiz/automdm/v4/orbit/pkg/useraction"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/rs/zerolog/log"
)

const maxRetries = 2

type DiskEncryptionRunner struct {
	isRunning           atomic.Bool
	capabilitiesFetcher func() automdm.CapabilityMap
	triggerOrbitRestart func(reason string)
}

func ApplyDiskEncryptionRunnerMiddleware(
	capabilitiesFetcher func() automdm.CapabilityMap,
	triggerOrbitRestart func(reason string),
) automdm.OrbitConfigReceiver {
	return &DiskEncryptionRunner{
		capabilitiesFetcher: capabilitiesFetcher,
		triggerOrbitRestart: triggerOrbitRestart,
	}
}

func (d *DiskEncryptionRunner) Run(cfg *automdm.OrbitConfig) error {
	log.Debug().Msgf("running disk encryption fetcher middleware, notification: %v, isIdle: %v", cfg.Notifications.RotateDiskEncryptionKey, d.isRunning.Load())

	if d.capabilitiesFetcher == nil {
		return errors.New("disk encryption runner needs a capabilitites fetcher configured")
	}

	if d.triggerOrbitRestart == nil {
		return errors.New("disk encryption runner needs a function to trigger orbit restarts configured")
	}

	if d.capabilitiesFetcher().Has(automdm.CapabilityEscrowBuddy) {
		d.triggerOrbitRestart("server has Escrow Buddy capability but old disk encryption fetcher was running")
		return nil
	}

	if cfg.Notifications.RotateDiskEncryptionKey && !d.isRunning.Swap(true) {
		go func() {
			defer d.isRunning.Store(false)
			if err := useraction.RotateDiskEncryptionKey(maxRetries); err != nil {
				log.Error().Err(err).Msg("rotating encryption key")
			}
		}()
	}

	return nil
}
