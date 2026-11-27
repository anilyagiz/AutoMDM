package carvestore

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

type key int

const carveStoreKey key = 0

func NewContext(ctx context.Context, svc automdm.CarveBySessionIder) context.Context {
	return context.WithValue(ctx, carveStoreKey, svc)
}

func FromContext(ctx context.Context) automdm.CarveBySessionIder {
	svc, ok := ctx.Value(carveStoreKey).(automdm.CarveBySessionIder)
	if !ok {
		return nil
	}
	return svc
}
