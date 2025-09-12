package cache

type CacheDriver interface {
	Get(key []byte) (val []byte, exist bool, err error)
	Set(key, val []byte, expiriesSecond int) error
	Del(key []byte) error

	//очищает весь кеш
	Clear() error

	//завершает запись всех значений и закрывает хранилище
	Close() error
}
