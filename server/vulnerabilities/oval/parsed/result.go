package oval_parsed

import "github.com/anilyagiz/automdm/v4/server/automdm"

type Result interface {
	// Eval evaluates the current OVAL definition against an OS version and a list of installed software, returns all software
	// vulnerabilities found.
	Eval(automdm.OSVersion, []automdm.Software) ([]automdm.SoftwareVulnerability, error)

	// EvalKernel evaluates the current OVAL definition against a list of installed kernel-image software,
	// returns all kernel-image vulnerabilities found.  Currently only used for Ubuntu.
	EvalKernel([]automdm.Software) ([]automdm.SoftwareVulnerability, error)
}
