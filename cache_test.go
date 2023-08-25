package cache_test

import (
	"bytes"
	"runtime/debug"
	"testing"
	"time"

	"github.com/coocood/freecache"
	"github.com/v-grabko1999/cache"
	"github.com/v-grabko1999/cache/drivers"
)

func TestFreeCacheDriver(t *testing.T) {
	cacheSize := 100 * 1024 * 1024
	ch := freecache.NewCache(cacheSize)
	debug.SetGCPercent(20)

	testLogic(t, cache.NewCache(drivers.NewFreeCacheDriver(ch)))
}

var (
	Key   = []byte("test key")
	Value = []byte("test value")
)

func testLogic(t *testing.T, ch *cache.Cache) {
	testGetSet(t, ch)
	testGetAndDel(t, ch)
	testOnSet(t, ch)
	testClear(t, ch)
}

func testGetSet(t *testing.T, ch *cache.Cache) {
	err := ch.Set(Key, Value, 1)
	if err != nil {
		t.Fatal("cache set", err)
	}
	val, exist, err := ch.Get(Key)
	if err != nil {
		t.Fatal("cache get err:", err)
	}
	if !exist {
		t.Fatal("cache not found")
	}

	if !bytes.Equal(Value, val) {
		t.Fatal("cache invalid data", Value, val)
	}

	time.Sleep(2 * time.Second)
	_, exist, err = ch.Get(Key)
	if err != nil {
		t.Fatal("cache get err:", err)
	}
	if exist {
		t.Fatal("cache error garbare collector")
	}
	t.Log("ok")
}

func testGetAndDel(t *testing.T, ch *cache.Cache) {
	err := ch.Set(Key, Value, 5)
	if err != nil {
		t.Fatal("cache set", err)
	}
	val, exist, err := ch.GetAndDel(Key)
	if err != nil {
		t.Fatal("cache get err:", err)
	}
	if !exist {
		t.Fatal("cache not found")
	}

	if !bytes.Equal(Value, val) {
		t.Fatal("cache invalid data", Value, val)
	}

	_, exist, err = ch.Get(Key)
	if err != nil {
		t.Fatal("cache get err:", err)
	}
	if exist {
		t.Fatal("cache error del ")
	}
	t.Log("ok")
}

func testOnSet(t *testing.T, ch *cache.Cache) {
	vv, err := ch.OnSet(Key, func() (value []byte, err error) {
		return Value, nil
	}, 2)

	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(Value, vv) {
		t.Fatal("cache invalid data", Value, vv)
	}
}

func testClear(t *testing.T, ch *cache.Cache) {
	_, err := ch.OnSet(Key, func() (value []byte, err error) {
		return Value, nil
	}, 2)
	if err != nil {
		t.Fatal(err)
	}

	ch.Clear()
	_, exist, err := ch.Get(Key)
	if err != nil {
		t.Fatal("cache get err:", err)
	}
	if exist {
		t.Fatal("cache error clear")
	}
	t.Log("ok")
}
