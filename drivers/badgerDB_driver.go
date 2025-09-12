package drivers

import (
	"errors"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"
	"github.com/v-grabko1999/cache"
)

func NewBadgerDBDriver(dir string) (cache.CacheDriver, error) {
	db, err := badger.Open(badger.DefaultOptions(dir).WithCompression(options.None))
	if err != nil {
		return nil, err
	}

	err = db.RunValueLogGC(0.5)
	if err != nil {
		return nil, err
	}

	return &BadgerDBDriver{
		db: db,
	}, nil
}

type BadgerDBDriver struct {
	db *badger.DB
}

func (rt *BadgerDBDriver) Get(key []byte) (val []byte, exist bool, err error) {
	err = rt.db.View(func(txn *badger.Txn) error {
		item, e := txn.Get(key)
		if e != nil {
			if errors.Is(e, badger.ErrKeyNotFound) {
				return nil // не помилка: просто немає ключа
			}
			return e
		}
		exist = true
		return item.Value(func(v []byte) error {
			val = append(val[:0], v...) // копія значення
			return nil
		})
	})
	return
}

func (rt *BadgerDBDriver) Set(key, value []byte, expiriesSecond int) error {
	return rt.db.Update(func(txn *badger.Txn) error {
		e := badger.NewEntry(key, value)
		if expiriesSecond > 0 {
			e = e.WithTTL(time.Duration(expiriesSecond) * time.Second)
		}
		return txn.SetEntry(e)
	})
}

func (rt *BadgerDBDriver) Del(key []byte) error {
	return rt.db.Update(func(txn *badger.Txn) error {
		err := txn.Delete(key)
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil
		}
		return err
	})
}

func (rt *BadgerDBDriver) Clear() error {
	return rt.db.DropAll()
}

func (rt *BadgerDBDriver) Close() error {
	return rt.db.Close()
}
