// Package async batches host updates in memory so that the hot path of host
// check-ins doesn't write to mysql on every request.
package async

import (
	"github.com/anilyagiz/automdm/v4/server/automdm"
)

type Task struct {
	datastore   automdm.Datastore
	seenHostSet seenHostSet
	otelEnabled bool
}

func NewTask(ds automdm.Datastore, otelEnabled bool) *Task {
	return &Task{
		datastore:   ds,
		otelEnabled: otelEnabled,
	}
}
