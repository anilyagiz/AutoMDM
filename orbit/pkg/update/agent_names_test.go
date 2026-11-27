package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anilyagiz/automdm/v4/orbit/pkg/constant"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

func TestAgentNamesReceiver(t *testing.T) {
	capabilities := automdm.CapabilityMap{}
	var calls int
	applyErr := errors.New("apply failed")
	stateFile := filepath.Join(t.TempDir(), agentNamesStateFile)
	r := &AgentNamesReceiver{
		serverCapabilities: func() automdm.CapabilityMap { return capabilities },
		apply: func() error {
			calls++
			return applyErr
		},
		stateFile: stateFile,
	}

	// A server older than AutoMDM 5 doesn't advertise the capability: nothing changes.
	require.NoError(t, r.Run(&automdm.OrbitConfig{}))
	require.Zero(t, calls)
	require.NoFileExists(t, stateFile)

	// A failure doesn't fail the config fetch and is retried on the next one. The next start applies the names too.
	capabilities[automdm.CapabilityAutoMDMAgentNames] = struct{}{}
	require.NoError(t, r.Run(&automdm.OrbitConfig{}))
	require.NoError(t, r.Run(&automdm.OrbitConfig{}))
	require.Equal(t, 2, calls)
	require.FileExists(t, stateFile)

	// Once applied, it isn't applied again during this orbit run.
	applyErr = nil
	require.NoError(t, r.Run(&automdm.OrbitConfig{}))
	require.NoError(t, r.Run(&automdm.OrbitConfig{}))
	require.Equal(t, 3, calls)

	// A server without the capability stops the next start from applying the names.
	delete(capabilities, automdm.CapabilityAutoMDMAgentNames)
	require.NoError(t, r.Run(&automdm.OrbitConfig{}))
	require.NoFileExists(t, stateFile)
	require.Equal(t, 3, calls)
}

func TestApplyRememberedAgentNames(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), agentNamesStateFile)
	var calls int
	apply := func() error {
		calls++
		return errors.New("apply failed") // logged only: the receiver tries again
	}

	applyRememberedAgentNames(stateFile, apply)
	require.Zero(t, calls)

	require.NoError(t, os.WriteFile(stateFile, nil, constant.DefaultFileMode))
	applyRememberedAgentNames(stateFile, apply)
	require.Equal(t, 1, calls)
}
