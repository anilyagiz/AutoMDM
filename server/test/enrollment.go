package test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func SetOrbitEnrollment(t *testing.T, h *automdm.Host, ds automdm.Datastore) string {
	orbitKey := uuid.New().String()
	_, err := ds.EnrollOrbit(context.Background(),
		automdm.WithEnrollOrbitHostInfo(automdm.OrbitHostInfo{
			HardwareUUID:   *h.OsqueryHostID,
			HardwareSerial: h.HardwareSerial,
		}),
		automdm.WithEnrollOrbitNodeKey(orbitKey),
		automdm.WithEnrollOrbitTeamID(h.TeamID),
	)
	require.NoError(t, err)
	err = ds.SetOrUpdateHostOrbitInfo(
		context.Background(), h.ID, "1.22.0", sql.NullString{String: "42", Valid: true}, sql.NullBool{Bool: true, Valid: true},
	)
	require.NoError(t, err)
	return orbitKey
}
