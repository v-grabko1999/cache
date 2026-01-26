package cache_test

import (
	"bytes"
	"errors"
	"runtime/debug"
	"testing"
	"time"

	"github.com/coocood/freecache"

	"github.com/v-grabko1999/cache"
	"github.com/v-grabko1999/cache/drivers"
)

func TestFreeCacheDriverChunk(t *testing.T) {
	cacheSize := 100 * 1024 * 1024
	fc := freecache.NewCache(cacheSize)
	debug.SetGCPercent(20)

	testLogicChunk(t, cache.NewCache(drivers.NewFreeCacheDriver(fc)))
}

func testLogicChunk(t *testing.T, ch *cache.Cache) {
	testGetSetChunk(t, ch)
	testGetAndDelRawChunk(t, ch)
	testOnSetChunk(t, ch)
	testClearChunk(t, ch)
	testCopySemanticsChunk(t, ch)
	testChunkConflictCAS(t, ch)
	testChunkTTLExpires(t, ch)
}

// ------------------------------------------------------------
// tests
// ------------------------------------------------------------

type testObj struct {
	A int
	B string
}

func testGetSetChunk(t *testing.T, c *cache.Cache) {
	const chunkName = "chunk_get_set"

	ch, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk(): %v", err)
	}

	key := []byte("k1")
	want := testObj{A: 123, B: "hello"}

	if err := ch.Set(key, want); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if err := ch.SaveChanges(); err != nil {
		t.Fatalf("SaveChanges(): %v", err)
	}

	// Відкриваємо заново (перевіряємо, що дані реально збережені в драйвері)
	ch2, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk() re-open: %v", err)
	}

	var got testObj
	exist, err := ch2.Get(key, &got)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if !exist {
		t.Fatalf("Get(): expected exist=true")
	}
	if got != want {
		t.Fatalf("Get(): want=%+v got=%+v", want, got)
	}
}

func testGetAndDelRawChunk(t *testing.T, c *cache.Cache) {
	const chunkName = "chunk_get_and_del_raw"

	ch, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk(): %v", err)
	}

	key := []byte("k_del")
	val := []byte("value")

	ch.SetRaw(key, val)

	v, exist, err := ch.GetAndDelRaw(key)
	if err != nil {
		t.Fatalf("GetAndDelRaw(): %v", err)
	}
	if !exist {
		t.Fatalf("GetAndDelRaw(): expected exist=true")
	}
	if !bytes.Equal(v, val) {
		t.Fatalf("GetAndDelRaw(): want=%q got=%q", val, v)
	}

	// після delete ключа не має бути
	_, exist = ch.GetRaw(key)
	if exist {
		t.Fatalf("GetRaw(): expected exist=false after GetAndDelRaw")
	}

	// комітимо видалення і перевіряємо на повторному відкритті
	if err := ch.SaveChanges(); err != nil {
		t.Fatalf("SaveChanges(): %v", err)
	}

	ch2, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk() re-open: %v", err)
	}
	_, exist = ch2.GetRaw(key)
	if exist {
		t.Fatalf("GetRaw(): expected exist=false after SaveChanges()")
	}
}

func testOnSetChunk(t *testing.T, c *cache.Cache) {
	const chunkName = "chunk_onset"

	ch, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk(): %v", err)
	}

	key := []byte("k_onset")

	calls := 0
	var got testObj

	err = ch.OnSet(key, &got, func() (any, error) {
		calls++
		return testObj{A: 7, B: "created"}, nil
	})
	if err != nil {
		t.Fatalf("OnSet(): %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnSet(): expected calls=1, got %d", calls)
	}
	if got != (testObj{A: 7, B: "created"}) {
		t.Fatalf("OnSet(): unexpected got=%+v", got)
	}

	// Другий виклик має взяти з RAM і не викликати fn
	var got2 testObj
	err = ch.OnSet(key, &got2, func() (any, error) {
		calls++
		return testObj{A: 999, B: "must-not-run"}, nil
	})
	if err != nil {
		t.Fatalf("OnSet() second: %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnSet(): expected calls still=1, got %d", calls)
	}
	if got2 != (testObj{A: 7, B: "created"}) {
		t.Fatalf("OnSet(): second unexpected got=%+v", got2)
	}

	// Перевіримо, що після SaveChanges значення збережене
	if err := ch.SaveChanges(); err != nil {
		t.Fatalf("SaveChanges(): %v", err)
	}

	ch2, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk() re-open: %v", err)
	}

	var got3 testObj
	exist, err := ch2.Get(key, &got3)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if !exist {
		t.Fatalf("Get(): expected exist=true after re-open")
	}
	if got3 != (testObj{A: 7, B: "created"}) {
		t.Fatalf("Get(): after re-open unexpected got=%+v", got3)
	}
}

func testClearChunk(t *testing.T, c *cache.Cache) {
	const chunkName = "chunk_clear"

	ch, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk(): %v", err)
	}

	ch.SetRaw([]byte("a"), []byte("1"))
	ch.SetRaw([]byte("b"), []byte("2"))

	ch.Clear()

	_, exist := ch.GetRaw([]byte("a"))
	if exist {
		t.Fatalf("Clear(): expected key a removed in RAM")
	}
	_, exist = ch.GetRaw([]byte("b"))
	if exist {
		t.Fatalf("Clear(): expected key b removed in RAM")
	}

	if err := ch.SaveChanges(); err != nil {
		t.Fatalf("SaveChanges(): %v", err)
	}

	ch2, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk() re-open: %v", err)
	}
	_, exist = ch2.GetRaw([]byte("a"))
	if exist {
		t.Fatalf("Clear(): expected key a removed after re-open")
	}
	_, exist = ch2.GetRaw([]byte("b"))
	if exist {
		t.Fatalf("Clear(): expected key b removed after re-open")
	}
}

func testCopySemanticsChunk(t *testing.T, c *cache.Cache) {
	const chunkName = "chunk_copy_semantics"

	ch, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk(): %v", err)
	}

	// SetRaw має копіювати вхідний slice
	orig := []byte("hello")
	ch.SetRaw([]byte("k"), orig)
	orig[0] = 'H'

	got1, exist := ch.GetRaw([]byte("k"))
	if !exist {
		t.Fatalf("GetRaw(): expected exist=true")
	}
	if string(got1) != "hello" {
		t.Fatalf("SetRaw copy: want=%q got=%q", "hello", got1)
	}

	// GetRaw має повертати копію (мутуємо результат і перевіряємо, що в чанку не змінилось)
	got1[0] = 'X'
	got2, exist := ch.GetRaw([]byte("k"))
	if !exist {
		t.Fatalf("GetRaw(): expected exist=true")
	}
	if string(got2) != "hello" {
		t.Fatalf("GetRaw copy: want=%q got=%q", "hello", got2)
	}
}

func testChunkConflictCAS(t *testing.T, c *cache.Cache) {
	const chunkName = "chunk_conflict"

	// Два незалежні снапшоти одного чанку
	a, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk() A: %v", err)
	}
	b, err := c.Chunk(chunkName, 60)
	if err != nil {
		t.Fatalf("Chunk() B: %v", err)
	}

	// A записує і комітить
	if err := a.Set([]byte("k"), testObj{A: 1, B: "A"}); err != nil {
		t.Fatalf("A.Set(): %v", err)
	}
	if err := a.SaveChanges(); err != nil {
		t.Fatalf("A.SaveChanges(): %v", err)
	}

	// B (зі старим baseVersion) намагається комітнути — має отримати конфлікт
	if err := b.Set([]byte("k"), testObj{A: 2, B: "B"}); err != nil {
		t.Fatalf("B.Set(): %v", err)
	}
	err = b.SaveChanges()
	if err == nil {
		t.Fatalf("B.SaveChanges(): expected conflict error")
	}
	if !errors.Is(err, cache.ErrChunkConflict) {
		t.Fatalf("B.SaveChanges(): expected ErrChunkConflict, got: %v", err)
	}
}

func testChunkTTLExpires(t *testing.T, c *cache.Cache) {
	const chunkName = "chunk_ttl"

	// TTL = 1 секунда
	ch, err := c.Chunk(chunkName, 1)
	if err != nil {
		t.Fatalf("Chunk(): %v", err)
	}

	if err := ch.Set([]byte("k"), testObj{A: 10, B: "ttl"}); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if err := ch.SaveChanges(); err != nil {
		t.Fatalf("SaveChanges(): %v", err)
	}

	time.Sleep(2 * time.Second)

	// Після TTL обидва ключі (payload+versionKey) можуть зникнути.
	// Chunk() має піднятися як “порожній” (або перевстановити versionKey).
	ch2, err := c.Chunk(chunkName, 1)
	if err != nil {
		t.Fatalf("Chunk() after ttl: %v", err)
	}

	var got testObj
	exist, err := ch2.Get([]byte("k"), &got)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if exist {
		t.Fatalf("expected value to expire, got=%+v", got)
	}
}
