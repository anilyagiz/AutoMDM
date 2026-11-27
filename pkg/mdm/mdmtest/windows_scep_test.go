package mdmtest

import (
	"encoding/xml"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractSCEPCommandsAtomicLayout anchors the most-common parser shape: an Atomic-wrapped
// SCEP CSP with all required fields.
func TestExtractSCEPCommandsAtomicLayout(t *testing.T) {
	atomic := buildAtomicSCEPCmd(t, "/Device", "uniq-1", "https://scep.example.com/scep", "challenge-abc",
		"CN=device-1,OU=automdm-1")
	cmds := map[string]automdm.ProtoCmdOperation{
		atomic.CmdID.Value: {Verb: automdm.CmdAtomic, Cmd: atomic},
	}

	c := &TestWindowsMDMClient{}
	got, incomplete := c.ExtractSCEPCommands(cmds)
	require.Empty(t, incomplete)
	require.Len(t, got, 1)
	assert.Equal(t, "uniq-1", got[0].UniqueID)
	assert.Equal(t, "https://scep.example.com/scep", got[0].ServerURL)
	assert.Equal(t, "challenge-abc", got[0].Challenge)
	assert.Equal(t, "CN=device-1,OU=automdm-1", got[0].SubjectName)
	assert.Equal(t, "7+urn:device:automdm:uuid:uniq-1", got[0].SubjectAlternativeNames)
	assert.Equal(t, atomic.CmdID.Value, got[0].AtomicCmdID)
	assert.NotEmpty(t, got[0].EnrollCmdID)
}

// TestExtractSCEPCommandsSurfacesIncomplete guards the contract that AppendSCEPInstallResponses'
// hasWork return value depends on: malformed SCEP CSPs land in the incomplete slice instead of
// being silently dropped, so callers fail loudly. Without this, a future "drop incomplete" change
// would only surface as a confusing downstream failure.
func TestExtractSCEPCommandsSurfacesIncomplete(t *testing.T) {
	// Only Challenge is set: missing ServerURL, SubjectName, and the Enroll Exec.
	loc := "./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/incomplete/Install/Challenge"
	add := automdm.SyncMLCmd{
		XMLName: xml.Name{Local: automdm.CmdAdd},
		CmdID:   automdm.CmdID{Value: uuid.NewString()},
		Items:   []automdm.CmdItem{{Target: &loc, Data: &automdm.RawXmlData{Content: "ch"}}},
	}
	cmds := map[string]automdm.ProtoCmdOperation{
		add.CmdID.Value: {Verb: automdm.CmdAdd, Cmd: add},
	}
	complete, incomplete := (&TestWindowsMDMClient{}).ExtractSCEPCommands(cmds)
	assert.Empty(t, complete)
	require.Len(t, incomplete, 1)
	assert.Equal(t, "incomplete", incomplete[0].UniqueID)

	missing := missingSCEPFields(incomplete[0])
	assert.Contains(t, missing, "ServerURL")
	assert.Contains(t, missing, "SubjectName")
	assert.Contains(t, missing, "/Install/Enroll Exec")
}

// buildAtomicSCEPCmd constructs an Atomic command containing the typical SCEP CSP nodes AutoMDM
// sends, plus an Exec on /Install/Enroll. locPrefix should be either "/Device" or "/User".
func buildAtomicSCEPCmd(t *testing.T, locPrefix, uniqueID, serverURL, challenge, subject string) automdm.SyncMLCmd {
	t.Helper()
	mkAdd := func(field, data string) automdm.SyncMLCmd {
		loc := "." + locPrefix + "/Vendor/MSFT/ClientCertificateInstall/SCEP/" + uniqueID + "/Install/" + field
		return automdm.SyncMLCmd{
			XMLName: xml.Name{Local: automdm.CmdAdd},
			CmdID:   automdm.CmdID{Value: uuid.NewString()},
			Items:   []automdm.CmdItem{{Target: &loc, Data: &automdm.RawXmlData{Content: data}}},
		}
	}
	enrollLoc := "." + locPrefix + "/Vendor/MSFT/ClientCertificateInstall/SCEP/" + uniqueID + "/Install/Enroll"
	return automdm.SyncMLCmd{
		XMLName: xml.Name{Local: automdm.CmdAtomic},
		CmdID:   automdm.CmdID{Value: uuid.NewString()},
		AddCommands: []automdm.SyncMLCmd{
			mkAdd("ServerURL", serverURL),
			mkAdd("Challenge", challenge),
			mkAdd("SubjectName", subject),
			mkAdd("SubjectAlternativeNames", "7+urn:device:automdm:uuid:"+uniqueID),
			mkAdd("KeyLength", "2048"),
			mkAdd("HashAlgorithm", "SHA-256"),
		},
		ExecCommands: []automdm.SyncMLCmd{{
			XMLName: xml.Name{Local: automdm.CmdExec},
			CmdID:   automdm.CmdID{Value: uuid.NewString()},
			Items:   []automdm.CmdItem{{Target: &enrollLoc}},
		}},
	}
}

// Windows CertEnroll accepts quoted RDN values, and AutoMDM quotes values it substitutes into SubjectName.
func TestParseSCEPSubjectQuotedValues(t *testing.T) {
	n, err := parseSCEPSubject(`CN=AutoMDM conditional access for Okta,OU="automdm-abc"`)
	require.NoError(t, err)
	assert.Equal(t, "AutoMDM conditional access for Okta", n.CommonName)
	assert.Equal(t, []string{"automdm-abc"}, n.OrganizationalUnit)

	n, err = parseSCEPSubject(`CN="say ""hi"""`)
	require.NoError(t, err)
	assert.Equal(t, `say "hi"`, n.CommonName)
}

func TestParseSCEPSubjectAltNameURIs(t *testing.T) {
	uris, err := parseSCEPSubjectAltNameURIs("2+user@example.com;7+urn:device:automdm:uuid:ABC;3+host.example.com")
	require.NoError(t, err)
	require.Len(t, uris, 1)
	assert.Equal(t, "urn:device:automdm:uuid:ABC", uris[0].String())

	uris, err = parseSCEPSubjectAltNameURIs("")
	require.NoError(t, err)
	assert.Empty(t, uris)

	_, err = parseSCEPSubjectAltNameURIs("no-name-type")
	require.Error(t, err)
}
