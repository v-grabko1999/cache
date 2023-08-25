package cache

func NewCache(dr CacheDriver) *Cache {
	return &Cache{dr}
}

type Cache struct {
	dr CacheDriver
}

func (ch *Cache) Get(key []byte) (val []byte, exist bool, err error) {
	return ch.dr.Get(key)
}

func (ch *Cache) GetAndDel(key []byte) (val []byte, exist bool, err error) {
	val, exist, err = ch.Get(key)
	if err != nil {
		return
	}

	if exist {
		ch.Del(key)
	}
	return
}

func (ch *Cache) Set(key, val []byte, expiries int64) error {
	return ch.dr.Set(key, val, expiries)
}

type OnSet func() (value []byte, err error)

func (ch *Cache) OnSet(key []byte, fn OnSet, expiries int64) (val []byte, err error) {
	val, exist, err := ch.Get(key)
	if err != nil {
		return
	}
	if !exist {
		val, err = fn()
		if err != nil {
			return
		}
		err = ch.Set(key, val, expiries)
	}
	return
}

func (ch *Cache) Del(key []byte) error {
	return ch.dr.Del(key)
}

func (ch *Cache) Clear() error {
	return ch.dr.Clear()
}
