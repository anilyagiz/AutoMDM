package service

import (
	"fmt"
	"log/slog"

	"github.com/WatchBeam/clock"
	"github.com/anilyagiz/automdm/v4/ee/server/service/scep"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	apple_mdm "github.com/anilyagiz/automdm/v4/server/mdm/apple"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanodep/storage"
	"github.com/anilyagiz/automdm/v4/server/microsoft/msgraph"
	"github.com/anilyagiz/automdm/v4/server/sso"
)

// Service wraps a free Service and implements additional premium functionality on top of it.
type Service struct {
	automdm.Service

	// pssoState is the lazily-initialized cache of the PSSO signing key.
	// Constructed on first use of a PSSO method.
	pssoState pssoServiceState

	// pssoNonceStore backs the single-use nonces issued and consumed by the
	// PSSO nonce/token flows. Redis-backed in production; may be nil in
	// deployments/tests that never exercise PSSO.
	pssoNonceStore automdm.PSSONonceStore

	ds                     automdm.Datastore
	logger                 *slog.Logger
	config                 config.AutoMDMConfig
	clock                  clock.Clock
	authz                  *authz.Authorizer
	depStorage             storage.AllDEPStorage
	mdmAppleCommander      automdm.MDMAppleCommandIssuer
	ssoSessionStore        sso.SessionStore
	depService             *apple_mdm.DEPService
	softwareInstallStore   automdm.SoftwareInstallerStore
	bootstrapPackageStore  automdm.MDMBootstrapPackageStore
	softwareTitleIconStore automdm.SoftwareTitleIconStore
	stagedUploadStore      automdm.StagedUploadStore
	distributedLock        automdm.Lock
	keyValueStore          automdm.KeyValueStore
	installAttemptCounter  automdm.SoftwareInstallAttemptCounter
	scepConfigService      automdm.SCEPConfigService
	scepEnrollmentClient   automdm.SCEPEnrollmentClient
	digiCertService        automdm.DigiCertService
	ejbcaService           automdm.EJBCAService
	androidModule          android.Service
	estService             automdm.ESTService
	googleCASService       automdm.GoogleCASService
	msGraphClientFactory   msgraph.ClientFactory
}

func NewService(
	svc automdm.Service,
	ds automdm.Datastore,
	logger *slog.Logger,
	config config.AutoMDMConfig,
	mailService automdm.MailService,
	c clock.Clock,
	depStorage storage.AllDEPStorage,
	mdmAppleCommander automdm.MDMAppleCommandIssuer,
	sso sso.SessionStore,
	softwareInstallStore automdm.SoftwareInstallerStore,
	bootstrapPackageStore automdm.MDMBootstrapPackageStore,
	softwareTitleIconStore automdm.SoftwareTitleIconStore,
	stagedUploadStore automdm.StagedUploadStore,
	distributedLock automdm.Lock,
	keyValueStore automdm.KeyValueStore,
	installAttemptCounter automdm.SoftwareInstallAttemptCounter,
	scepConfigService automdm.SCEPConfigService,
	digiCertService automdm.DigiCertService,
	ejbcaService automdm.EJBCAService,
	androidService android.Service,
	estService automdm.ESTService,
	googleCASService automdm.GoogleCASService,
	pssoNonceStore automdm.PSSONonceStore,
	msGraphClientFactory msgraph.ClientFactory,
) (*Service, error) {
	authorizer, err := authz.NewAuthorizer()
	if err != nil {
		return nil, fmt.Errorf("new authorizer: %w", err)
	}

	// Default to the real Graph client.
	if msGraphClientFactory == nil {
		msGraphClientFactory = msgraph.NewClient
	}

	eeservice := &Service{
		Service:                svc,
		ds:                     ds,
		logger:                 logger,
		config:                 config,
		clock:                  c,
		authz:                  authorizer,
		depStorage:             depStorage,
		mdmAppleCommander:      mdmAppleCommander,
		ssoSessionStore:        sso,
		depService:             apple_mdm.NewDEPService(ds, depStorage, logger),
		softwareInstallStore:   softwareInstallStore,
		bootstrapPackageStore:  bootstrapPackageStore,
		softwareTitleIconStore: softwareTitleIconStore,
		stagedUploadStore:      stagedUploadStore,
		distributedLock:        distributedLock,
		keyValueStore:          keyValueStore,
		installAttemptCounter:  installAttemptCounter,
		scepConfigService:      scepConfigService,
		scepEnrollmentClient:   scep.NewEnrollmentClient(logger),
		digiCertService:        digiCertService,
		ejbcaService:           ejbcaService,
		androidModule:          androidService,
		estService:             estService,
		googleCASService:       googleCASService,
		pssoNonceStore:         pssoNonceStore,
		msGraphClientFactory:   msGraphClientFactory,
	}

	// Override methods that can't be easily overriden via
	// embedding.
	svc.SetEnterpriseOverrides(automdm.EnterpriseOverrides{
		HostFeatures:                      eeservice.HostFeatures,
		TeamByIDOrName:                    eeservice.teamByIDOrName,
		UpdateTeamMDMDiskEncryption:       eeservice.updateTeamMDMDiskEncryption,
		UpdateTeamMDMHostNameTemplate:     eeservice.updateTeamMDMHostNameTemplate,
		ApplyHostNameTemplateChange:       eeservice.applyHostNameTemplateChange,
		MDMAppleReconcileFileVaultProfile: eeservice.MDMAppleReconcileFileVaultProfile,
		DeleteMDMAppleSetupAssistant:      eeservice.DeleteMDMAppleSetupAssistant,
		MDMAppleSyncDEPProfiles:           eeservice.mdmAppleSyncDEPProfiles,
		DeleteMDMAppleBootstrapPackage:    eeservice.DeleteMDMAppleBootstrapPackage,
		MDMWindowsEnableOSUpdates:         eeservice.mdmWindowsEnableOSUpdates,
		MDMWindowsDisableOSUpdates:        eeservice.mdmWindowsDisableOSUpdates,
		MDMAppleEditedAppleOSUpdates:      eeservice.mdmAppleEditedAppleOSUpdates,
		SetupExperienceNextStep:           eeservice.SetupExperienceNextStep,
		CheckCanInstallVPPApps:            eeservice.CheckCanInstallVPPApps,
		InstallVPPAppPostValidation:       eeservice.InstallVPPAppPostValidation,
		VerifyAppleAccountProvisioning:    eeservice.verifyAppleAccountProvisioning,
	})

	return eeservice, nil
}
