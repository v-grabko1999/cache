package drivers

import (
	"errors"
	"time"

	"github.com/dgraph-io/badger/v4"
)

type BadgerDBDriver struct {
	db *badger.DB
}

func NewBadgerDBDriver(dir string) (*BadgerDBDriver, error) {
	opts := badger.DefaultOptions(dir)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}

	drv := &BadgerDBDriver{db: db}

	// Одноразовий GC на старті — ігноруємо “no cleanup”.
	if err := runVlogGC(db, 0.5); err != nil {
		_ = db.Close()
		return nil, err
	}

	// Періодичний GC у фоні.
	go gcLoop(db, 0.5, 15*time.Minute)

	return drv, nil
}

func gcLoop(db *badger.DB, discard float64, period time.Duration) {
	t := time.NewTicker(period)
	defer t.Stop()
	for range t.C {
		_ = runVlogGC(db, discard) // м’яко ігноруємо “no cleanup”
	}
}
func runVlogGC(db *badger.DB, discard float64) error {
	for {
		err := db.RunValueLogGC(discard)
		switch {
		case errors.Is(err, badger.ErrNoRewrite):
			return nil // нічого не зібрано — це OK
		case errors.Is(err, badger.ErrRejected):
			return nil // GC відхилено — теж не критично
		case err != nil:
			return err // інші помилки — повертаємо
		default:
			// Щось зібрали — пробуємо ще раз, поки не отримаємо ErrNoRewrite.
			continue
		}
	}
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
