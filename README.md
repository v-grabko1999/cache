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

## Chunk — іменовані групи ключів з версіонуванням (optimistic commit)

`Chunk` — це іменований контейнер `map[string][]byte`, який зберігається в базовому кеші як **один payload** (gob) і працює в режимі “RAM-снапшот → commit”.

Ціль: зручно робити багато дрібних операцій (`Set/Del/Get`) у памʼяті, а в кеш записувати агреговано через `SaveChanges()`.

---

### Ключі, які використовує Chunk

Для кожного `name` існують **два ключі**:

1) Payload (дані + версія у структурі):
- `cache_package_chank_<name>`

2) Окремий ключ версії (8 байт, `uint64`, little-endian):
- `cache_package_chank_<name>_version`

---

### Внутрішній формат payload

Payload серіалізується через `encoding/gob` як:

- `Version uint64` — версія чанку в payload
- `Data map[string][]byte` — дані

Ключі зберігаються як `string(key)` (перетворення з `[]byte`).

---

### Як працює loadToMemory()

При створенні чанку (`Cache.Chunk(...)`) виконується `loadToMemory()`:

1) Читає `_version` ключ (швидко, 8 байт).
2) Читає payload ключ і декодує `ChunkRaw`.
3) Перевіряє самоконсистентність:
   - якщо `_version` існує, то `payload.Version` **має дорівнювати** `_version`.
   - якщо `_version` **не існує**, він ініціалізується значенням `payload.Version`.
4) Робить **глибоку копію** даних у RAM (`cloneChunkRaw`) і зберігає:
   - `memoryData` — робочий снапшот
   - `baseVersion` — версія, з якою завантажились
   - `changes=false`

---

### SaveChanges(): “commit” з подвійною перевіркою версії

`SaveChanges()` записує зміни з RAM назад у кеш як оптимістичну транзакцію:

1) Якщо `changes == false` — нічого не робить.
2) **Швидка перевірка**: читає тільки `_version` ключ.
   - якщо `_version` відсутній — відновлює його з payload.
   - якщо `_version != baseVersion` — повертає `ErrChunkConflict` і **не читає payload зайвий раз**.
3) **Фінальна перевірка**: читає payload і перевіряє `payload.Version == baseVersion`.
   - якщо ні — повертає `ErrChunkConflict` (payload).
4) Формує `toSave`:
   - глибока копія RAM-снапшота
   - `toSave.Version = baseVersion + 1`
5) Порядок запису:
   - спочатку payload (`cache_package_chank_<name>`)
   - потім `_version` (`cache_package_chank_<name>_version`)
6) Після успіху:
   - `baseVersion = toSave.Version`
   - `memoryData = toSave`
   - `changes = false`

> `ErrChunkConflict` означає: чанк був змінений іншим writer-ом між завантаженням і комітом. Правильна реакція — retry: перезавантажити чанк і застосувати зміни знову.

---

### Операції з Chunk

Усі операції працюють по RAM-снапшоту та позначають `changes=true` (для Set/Del/Clear/GetAndDel):

- `Get(key)` — повертає **копію** `[]byte`
- `Set(key, val)` — записує **копію** `[]byte`
- `Del(key)` — видаляє ключ
- `GetAndDel(key)` — повертає копію і видаляє ключ
- `Clear()` — очищає всі ключі
- `OnSet(key, fn)` — якщо ключа немає, викликає `fn()` і робить `Set`

`Chunk` потокобезпечний: всі методи захищені `sync.Mutex`.

---

### Рекомендований шаблон використання

`SaveChanges()` потрібно викликати явно (наприклад, раз на N секунд або в кінці транзакції логіки).

Приклад з retry на конфлікт:

    chunk, err := ch.Chunk("stats", 60)
    if err != nil { panic(err) }

    // Робота з RAM-снапшотом
    chunk.Set([]byte("rpc_ok"), []byte("1"))

    // Commit з retry
    for i := 0; i < 3; i++ {
        err = chunk.SaveChanges()
        if err == nil {
            break
        }
        if errors.Is(err, cache.ErrChunkConflict) {
            // Перезавантажуємо чанк (найпростіше: створити новий екземпляр)
            chunk, _ = ch.Chunk("stats", 60)
            chunk.Set([]byte("rpc_ok"), []byte("1")) // застосувати зміни знову
            continue
        }
        panic(err)
    }

---

### Нюанси та обмеження

- Версійний ключ `_version` зменшує зайві читання великого payload: у випадку конфлікту `SaveChanges()` “відсікається” на швидкій перевірці.
- Запис payload і `_version` відбувається двома `Set()` без загального CAS на рівні драйвера. Для міжпроцесного сценарію це дає високий рівень практичної надійності, але не є строго атомарною транзакцією.
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

