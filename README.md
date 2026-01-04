# cache

`cache` — легкий Go-пакет для роботи з кешем через абстрактний backend-драйвер.

Пакет надає уніфікований API для:
- Get / Set / Del / Clear / Close
- GetAndDel — отримати значення та одразу видалити
- OnSet — lazy-ініціалізація (обчислення лише якщо ключ відсутній)
- Chunk — іменовані чанки (map у памʼяті з відкладеним збереженням)

У комплекті є готові драйвери:
- FreeCache — in-memory кеш
- BadgerDB — persistent кеш на диску

---

## Встановлення

    go get github.com/v-grabko1999/cache

---

## Архітектура

### CacheDriver

Пакет не привʼязаний до конкретного сховища. Будь-який бекенд реалізує інтерфейс:

    type CacheDriver interface {
        Get(key []byte) (val []byte, exist bool, err error)
        Set(key, val []byte, expiriesSecond int) error
        Del(key []byte) error
        Clear() error
        Close() error
    }

---

### Cache

`Cache` — тонка обгортка над драйвером:

- Get — читання з кешу
- Set — запис у кеш з TTL (у секундах)
- Del / Clear / Close — делегуються драйверу
- GetAndDel — Get + Del (помилка Del не повертається)
- OnSet — викликає функцію лише якщо ключ відсутній
- Chunk(name, ttl) — створює іменований чанк

---

## Швидкий старт (FreeCache)

    fc := freecache.NewCache(100 * 1024 * 1024) // 100 MB
    ch := cache.NewCache(drivers.NewFreeCacheDriver(fc))
    defer ch.Close()

    _ = ch.Set([]byte("key"), []byte("value"), 10)

    val, exist, err := ch.Get([]byte("key"))

---

## Базові операції

### Set / Get + TTL

    _ = ch.Set([]byte("test key"), []byte("test value"), 1)

    val, exist, _ := ch.Get([]byte("test key"))
    // exist == true

    time.Sleep(2 * time.Second)

    _, exist, _ = ch.Get([]byte("test key"))
    // exist == false (TTL спрацював)

---

### GetAndDel

Отримати значення та одразу видалити ключ.

    _ = ch.Set([]byte("test key"), []byte("test value"), 5)

    val, exist, _ := ch.GetAndDel([]byte("test key"))
    // exist == true

    _, exist, _ = ch.Get([]byte("test key"))
    // exist == false

Нюанс: помилка з Del не повертається (best-effort delete).

---

### OnSet (lazy-ініціалізація)

    val, err := ch.OnSet([]byte("config"), func() ([]byte, error) {
        return []byte("computed value"), nil
    }, 30)

Функція виконується лише якщо ключ відсутній.

---

### Clear

    _ = ch.Clear()

Очищає весь кеш у драйвері.

---

## Chunk — іменовані групи ключів

### Що таке Chunk

Chunk — це:
- map[string][]byte в оперативній памʼяті
- збережений у кеші як один ключ
- серіалізується через encoding/gob
- записується назад лише після SaveChanges()

Ключ у кеші:

    cache_package_chank_<name>

---

### Створення та життєвий цикл

    chunk, err := ch.Chunk("users", 60)
    if err != nil {
        panic(err)
    }
    defer chunk.SaveChanges()

ВАЖЛИВО: без SaveChanges() зміни не збережуться.

---

### Операції з Chunk

    chunk.Set([]byte("42"), []byte("John"))
    chunk.Set([]byte("43"), []byte("Alice"))

    val, ok := chunk.Get([]byte("42"))

    val, ok, _ = chunk.GetAndDel([]byte("43"))

    chunk.Del([]byte("42"))
    chunk.Clear()

Гарантії:
- Chunk потокобезпечний (mutex)
- дані копіюються при Get / Set
- ключі зберігаються як string(key)

---

### Chunk.OnSet

    val, err := chunk.OnSet([]byte("settings"), func() ([]byte, error) {
        return []byte("defaults"), nil
    })

---

### Видалення чанка

    _ = ch.DeleteChunk("users")

---

## BadgerDB (persistent кеш)

    drv, err := drivers.NewBadgerDBDriver("./cache-data")
    if err != nil {
        panic(err)
    }

    ch := cache.NewCache(drv)
    defer ch.Close()

    _ = ch.Set([]byte("key"), []byte("value"), 30)

Особливості:
- TTL застосовується лише якщо expiriesSecond > 0
- Clear() виконує повне DropAll()
- запускається фоновий GC ValueLog
- GC-горутин не має явного stop при Close()

---

## Рекомендовані патерни

### Кешування важких операцій

    data, _ := ch.OnSet([]byte("heavy"), func() ([]byte, error) {
        return expensiveCall(), nil
    }, 60)

---

### Chunk як “маленька база”

    chunk, _ := ch.Chunk("session-index", 300)
    defer chunk.SaveChanges()

    chunk.Set([]byte("sid:1"), []byte("user:42"))
    chunk.Set([]byte("sid:2"), []byte("user:99"))

---

