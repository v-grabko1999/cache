package cache

type CacheDriver interface {
	Get(key []byte) (val []byte, exist bool, err error)
	Set(key, val []byte, expiries int64) error
	Del(key []byte) error

	//очищает весь кеш
	Clear() error
}
