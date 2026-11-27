// Package notificationsacl is the only place that imports both notifications
// and team types, which is what lets the notifications bounded context stay
// independent of the rest of AutoMDM.
package notificationsacl

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/notifications"
)

type AutoMDMServiceAdapter struct {
	ds automdm.Datastore
}

func NewAutoMDMServiceAdapter(ds automdm.Datastore) *AutoMDMServiceAdapter {
	return &AutoMDMServiceAdapter{ds: ds}
}

var _ notifications.ScriptQueueProvider = (*AutoMDMServiceAdapter)(nil)

func (a *AutoMDMServiceAdapter) QueueScriptForHosts(ctx context.Context, hostIDs []uint, contents string) (map[uint]string, error) {
	return a.ds.BatchNewInternalHostScriptExecutionRequests(ctx, hostIDs, contents)
}
