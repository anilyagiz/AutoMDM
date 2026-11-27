package constant

import "time"

const (
	// DefaultDirMode is the default file mode to apply to created directories.
	DefaultDirMode = 0o755
	// DefaultFileMode is the default file mode to apply to created files.
	DefaultFileMode = 0o600
	// DefaultWorldReadableFileMode is the default file mode to apply to files
	// that can be read by other processes.
	DefaultWorldReadableFileMode = 0o644
	// DefaultSystemdUnitMode is the required file mode to systemd unit files.
	DefaultSystemdUnitMode = DefaultWorldReadableFileMode
	// DesktopAppExecName is the name of AutoMDM's Desktop executable.
	//
	// We use automdm-desktop as name to properly identify the process when listing
	// running processes/tasks.
	DesktopAppExecName = "automdm-desktop"
	// DesktopTokenFileName is the filename on disk (in the orbit base dir) where we store the AutoMDM Desktop auth token
	DesktopTokenFileName = "identifier"
	// OrbitNodeKeyFileName is the filename on disk where we write the orbit node key to
	OrbitNodeKeyFileName = "secret-orbit-node-key.txt"
	// HardwareUUIDFileName is the filename on disk where we store the hardware UUID for migration detection
	HardwareUUIDFileName = "hardware-uuid.txt"
	// OrbitEnrollMaxRetries is the max number of retries when doing an enroll request.
	// We set it to 6 to allow the retry backoff to take effect.
	OrbitEnrollMaxRetries = 6
	// OrbitEnrollBackoffMultiplier is the multiplier to use for backing off between enroll retries.
	OrbitEnrollBackoffMultiplier = 2
	// OrbitEnrollRetrySleep is the duration to sleep between enroll retries.
	OrbitEnrollRetrySleep = 10 * time.Second
	// OsqueryPidfile is the file containing the PID of the running osqueryd process
	OsqueryPidfile = "osquery.pid"
	// OsqueryEnrollSecretFileName is the filename on disk where we write
	// the orbit enroll secret.
	OsqueryEnrollSecretFileName = "secret.txt"
	// SystemServiceName is the name of Orbit system service
	// The service name is used by the OS service management framework
	SystemServiceName = "AutoMDM osquery"
	// AgentDisplayName is the name end users see for the agent: the Windows Task Manager and Installed apps names.
	AgentDisplayName = "AutoMDM"
	// AgentLinuxProcessName is the agent's process name on Linux, lowercase like other Linux process names.
	AgentLinuxProcessName = "automdm"
	// AgentPublisher is the Manufacturer of automdmd's MSI and the company name of its Windows executables. Installed apps
	// shows it as the publisher, which tells automdmd's "AutoMDM" entry apart from other apps with that name.
	AgentPublisher = "Fleet Device Management (automdm.com)"
	// AutoMDMTLSClientCertificateFileName is the name of the TLS client certificate file
	// used when connecting to the AutoMDM server.
	AutoMDMTLSClientCertificateFileName = "automdm_client.crt"
	// AutoMDMTLSClientKeyFileName is the name of the TLS client private key file
	// used when connecting to the AutoMDM server.
	AutoMDMTLSClientKeyFileName = "automdm_client.key"
	// UpdateTLSClientCertificateFileName is the name of the TLS client certificate file
	// used when connecting to the update server.
	UpdateTLSClientCertificateFileName = "update_client.crt"
	// UpdateTLSClientKeyFileName is the name of the TLS client private key file
	// used when connecting to the update server.
	UpdateTLSClientKeyFileName = "update_client.key"
	// SilenceEnrollLogErrorEnvVer is an environment variable name for disabling enroll log errors
	SilenceEnrollLogErrorEnvVar = "AUTOMDMD_SILENCE_ENROLL_ERROR"
	// ServerOverridesFileName is the name of the file in the root directory
	// that specifies the override configuration fetched from the server.
	ServerOverridesFileName = "server-overrides.json"
	// MigrationFileName is the name of the file used by automdmd to determine if the host is
	// partially through an MDM migration.
	MigrationFileName = "mdm_migration.txt"
	// MDMMigrationTypeManual indicates that the MDM migration is for a manually enrolled host.
	MDMMigrationTypeManual = "manual"
	// MDMMigrationTypeADE indicates that the MDM migration is for an ADE enrolled host.
	MDMMigrationTypeADE = "ade"
	// MDMMigrationTypePreSonoma indicates that the MDM migration is for a host on a macOS version < 14.
	MDMMigrationTypePreSonoma = "pre-sonoma"
	// MDMMigrationOfflineWatcherInterval is the interval at which the offline watcher checks for
	// the presence of the migration file.
	MDMMigrationOfflineWatcherInterval = 3 * time.Minute
	SonomaMajorVersion                 = 14

	// OrbitTUFTargetName is the target name of the orbit component of automdmd in TUF.
	OrbitTUFTargetName = "orbit"
	// OsqueryTUFTargetName is the target name of the osquery component of automdmd in TUF.
	OsqueryTUFTargetName = "osqueryd"
	// DesktopTUFTargetName is the target name of the AutoMDM Desktop component of automdmd in TUF.
	DesktopTUFTargetName = "desktop"
	// AutoMDMURLFileName is the file where AutoMDM URL is stored after being read from Apple config profile.
	AutoMDMURLFileName = "automdm_url.txt"

	// SetupExperienceComplete is a file created when Linux (and soon Windows) completes setup experience
	SetupExperienceFilename = "setup_experience.json"

	AutoMDMHTTPSignatureCertificateFileName = "host_identity.crt"
	// AutoMDMHTTPSignatureTPMKeyFileName is the filename for the TPM key used for HTTP signature authentication
	AutoMDMHTTPSignatureTPMKeyFileName = "host_identity_tpm.pem"
	// AutoMDMHTTPSignatureTPMKeyBackupFileName is the filename for the backup of the TPM key during renewal
	AutoMDMHTTPSignatureTPMKeyBackupFileName = "host_identity_tpm.old.pem"

	// UnusedFlagKeyword is used by the MSI builder and installer to populate parameters, which cannot be empty.
	UnusedFlagKeyword = "dummy"
)
