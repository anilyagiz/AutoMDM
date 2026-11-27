package microsoft_mdm

import (
	"fmt"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/google/uuid"
)

const remoteWipeCmdTmpl = `
		<Exec>
			<CmdID>%s</CmdID>
			<Item>
				<Target>
					<LocURI>%s</LocURI>
				</Target>
				<Meta>
					<Format xmlns="syncml:metinf">chr</Format>
					<Type>text/plain</Type>
				</Meta>
				<Data></Data>
			</Item>
		</Exec>`

// RemoteWipeCmd builds an Exec of the given RemoteWipe CSP node, such as automdm.WindowsRemoteWipeProtected.
// https://learn.microsoft.com/en-us/windows/client-management/mdm/remotewipe-csp
func RemoteWipeCmd(node string) *automdm.MDMWindowsCommand {
	cmdUUID := uuid.NewString()
	locURI := automdm.WindowsRemoteWipeLocURI(node)
	return &automdm.MDMWindowsCommand{
		CommandUUID:  cmdUUID,
		RawCommand:   fmt.Appendf(nil, remoteWipeCmdTmpl, cmdUUID, locURI),
		TargetLocURI: locURI,
	}
}
