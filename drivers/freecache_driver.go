package drivers

import (
	"errors"

	"github.com/coocood/freecache"
	"github.com/v-grabko1999/cache"
)

func NewFreeCacheDriver(ch *freecache.Cache) cache.CacheDriver {
	return &FreeCacheDriver{ch}
}

type FreeCacheDriver struct {
	ch *freecache.Cache
}

var (
	ErrInvalidData = errors.New("invalid storage data")
)

func (rt *FreeCacheDriver) Get(key []byte) (val []byte, exist bool, err error) {
	val, err = rt.ch.Get(key)
	if err != nil {
		if errors.Is(err, freecache.ErrNotFound) {
			err = nil
			exist = false
		}
		return
	}
	exist = true
	return
}

func (rt *FreeCacheDriver) Set(key []byte, val []byte, expiriesSecond int) error {
	rt.ch.Set(key, val, expiriesSecond)
	return nil
}

func (rt *FreeCacheDriver) Del(key []byte) error {
	rt.ch.Del(key)
	return nil
}

func (rt *FreeCacheDriver) Clear() error {
	rt.ch.Clear()
	return nil
}
