package drivers

import (
	"errors"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/v-grabko1999/cache"
)

func NewRistrettoDriver(ch *ristretto.Cache) cache.CacheDriver {
	return &RistrettoDriver{ch}
}

type RistrettoDriver struct {
	ch *ristretto.Cache
}

var (
	ErrInvalidData = errors.New("invalid storage data")
)

func (rt *RistrettoDriver) Get(key []byte) (val []byte, exist bool, err error) {
	v, exist := rt.ch.Get(key)
	val, ok := v.([]byte)
	if !ok {
		err = ErrInvalidData
		return
	}
	return
}

func (rt *RistrettoDriver) Set(key, val []byte, expiries int64) error {
	rt.ch.SetWithTTL(key, val, 1, time.Duration(expiries))
	return nil
}

func (rt *RistrettoDriver) Del(key []byte) error {
	rt.ch.Del(key)
	return nil
}

func (rt *RistrettoDriver) Clear() error {
	rt.ch.Clear()
	return nil
}
