package mock

import (
	"github.com/anilyagiz/automdm/v4/server/automdm"
	kvmock "github.com/anilyagiz/automdm/v4/server/mock/redis"
	akvmock "github.com/anilyagiz/automdm/v4/server/mock/redis_advanced"
	svcmock "github.com/anilyagiz/automdm/v4/server/mock/service"
)

//go:generate go run ./mockimpl/impl.go -o service/service_mock.go "s *Service" "automdm.Service"
//go:generate go run ./mockimpl/impl.go -o redis/key_value_store.go "kv *KeyValueStore" "automdm.KeyValueStore"
// We need to use a new folder to avoid multiple of the same functions
//go:generate go run ./mockimpl/impl.go -o redis_advanced/advanced_key_value_store.go "akv *AdvancedKeyValueStore" "automdm.AdvancedKeyValueStore"

var _ automdm.Service = new(svcmock.Service)

type KVStore struct {
	kvmock.KeyValueStore
}

type AdvancedKVStore struct {
	akvmock.AdvancedKeyValueStore
}
