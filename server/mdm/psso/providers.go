// Package psso is the bounded context for AutoMDM's Apple Platform SSO IdP
// implementation. It declares the minimal external collaborators (such as
// the Redis pool) used by the PSSO subpackages so that those subpackages
// can avoid importing the full server/automdm types.
package psso

import (
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/psso/internal/redis_nonces_store"
	redigo "github.com/gomodule/redigo/redis"
)

// RedisPool is the minimal Redis pool interface needed by the PSSO bounded
// context. automdm.RedisPool satisfies this implicitly via Go's structural
// typing.
type RedisPool interface {
	Get() redigo.Conn
}

// NewRedisNonceStore returns an automdm.PSSONonceStore backed by Redis. This is
// the public constructor that callers outside the PSSO bounded context (e.g.
// cmd/automdm) use to wire up nonce storage without depending on the internal
// implementation package directly.
func NewRedisNonceStore(pool RedisPool) automdm.PSSONonceStore {
	return redis_nonces_store.New(pool)
}
