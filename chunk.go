package cache

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/vmihailenco/msgpack/v5"
)

var (
	// ErrChunkConflict означає, що чанк був змінений іншим writer-ом між loadToMemory() і SaveChanges().
	// Це “оптимістична транзакція”: треба повторити операцію (перезавантажити чанк і застосувати зміни знову).
	ErrChunkConflict = errors.New("конфлікт версії чанку: дані були змінені паралельно")
)

// Chunk — це “снапшотний” KV-буфер поверх Cache, який працює у дві фази:
//  1. loadToMemory() завантажує ChunkRaw у RAM та фіксує baseVersion.
//  2. Get/Set/Del працюють з RAM-снапшотом, а SaveChanges() комітить зміни назад у кеш.
//
// Конкурентність:
// всі публічні методи Chunk потокобезпечні (захищені mu).
//
// Узгодженість:
// Chunk використовує optimistic CAS на основі версії (baseVersion) і окремого ключа версії,
// щоб детектити паралельні модифікації іншими writer-ами.
//
// Серіалізація:
// payload ChunkRaw кодується msgpack, а значення в Data зберігаються як []byte (частіше це msgpack-пакети).
type Chunk struct {
	ch             *Cache
	name           string
	expiriesSecond int

	// memoryData — робочий снапшот у RAM (з ним працює Get/Set/Del).
	memoryData ChunkRaw

	// baseVersion — версія, з якою ми завантажили чанк у памʼять (для CAS у SaveChanges).
	baseVersion uint64

	mu      sync.Mutex
	changes bool
}

// ChunkRaw — серіалізований стан чанку.
// Version — монотонно зростаюча версія payload.
// Data — key/value, де value зберігається як []byte.
type ChunkRaw struct {
	Version uint64
	Data    map[string][]byte
}

// getChunkKey створює ключ для збереження payload чанку в кеші.
func getChunkKey(name string) []byte {
	return []byte("cache_package_chank_" + name)
}

// getChunkVersionKey створює ключ для окремого збереження версії payload (8 байт LE).
// Використовується для швидкої CAS-перевірки без читання всього payload.
func getChunkVersionKey(name string) []byte {
	return []byte("cache_package_chank_" + name + "_version")
}

// loadVersionKey читає versionKey з кешу.
// Повертає (ver, exist, err). Якщо ключ існує, його довжина має бути рівно 8 байт.
func (ch *Chunk) loadVersionKey() (uint64, bool, error) {
	b, exist, err := ch.ch.Get(getChunkVersionKey(ch.name))
	if err != nil {
		return 0, false, err
	}
	if !exist {
		return 0, false, nil
	}
	if len(b) != 8 {
		return 0, true, fmt.Errorf("invalid chunk version bytes len=%d", len(b))
	}
	return binary.LittleEndian.Uint64(b), true, nil
}

// saveVersionKey записує versionKey у кеш (8 байт LE) з TTL чанку.
func (ch *Chunk) saveVersionKey(ver uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], ver)
	return ch.ch.Set(getChunkVersionKey(ch.name), buf[:], ch.expiriesSecond)
}

// loadToMemory завантажує payload чанку з кешу у RAM та ініціалізує baseVersion.
//
// Самоконсистентність:
// якщо versionKey існує, його значення має збігатися з ChunkRaw.Version.
//
// Ініціалізація:
// якщо versionKey відсутній, він створюється зі значенням payload.Version.
func (ch *Chunk) loadToMemory() error {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	verKey, verKeyExist, err := ch.loadVersionKey()
	if err != nil {
		return err
	}

	chunkData, err := ch.getOrCreateChunkRaw()
	if err != nil {
		return err
	}

	// Якщо версія-ключ існує — вона має збігатись з версією у payload.
	if verKeyExist && chunkData.Version != verKey {
		return ErrChunkConflict
	}

	// Якщо verKey не існує — ініціалізуємо його з payload (або 0).
	if !verKeyExist {
		if err := ch.saveVersionKey(chunkData.Version); err != nil {
			return err
		}
	}

	ch.memoryData = cloneChunkRaw(chunkData)
	ch.baseVersion = chunkData.Version
	ch.changes = false
	return nil
}

// Set кодує val у msgpack та зберігає результат у RAM-снапшоті.
// Значення в RAM зберігається як копія []byte (див. SetRaw).
func (ch *Chunk) Set(key []byte, val any) error {
	b, err := msgpack.Marshal(val)
	if err != nil {
		return err
	}
	ch.SetRaw(key, b)
	return nil
}

// Get читає []byte з RAM-снапшота та декодує msgpack у dst (dst має бути вказівником).
// Повертає exist=false, якщо ключ відсутній.
func (ch *Chunk) Get(key []byte, dst any) (exist bool, err error) {
	raw, exist := ch.GetRaw(key)
	if !exist {
		return false, nil
	}
	if err := msgpack.Unmarshal(raw, dst); err != nil {
		return true, err
	}
	return true, nil
}

// OnSetCh — фабрика значення для OnSet.
// Викликається лише якщо ключ відсутній у RAM-снапшоті.
type OnSetCh func() (value any, err error)

// OnSet заповнює dst даними з ключа, а якщо ключ відсутній — генерує значення через fn,
// кодує його у msgpack, зберігає у RAM через SetRaw і декодує у dst.
//
// На відміну від OnSetRaw, цей метод працює з типізованими значеннями (any <-> msgpack).
// Метод не викликає SaveChanges(): коміт залишається відповідальністю викликача.
func (ch *Chunk) OnSet(key []byte, dst any, fn OnSetCh) error {
	// 1) швидкий шлях: спробувати прочитати з RAM
	if ok, err := ch.Get(key, dst); err != nil {
		return err
	} else if ok {
		return nil
	}

	// 2) якщо немає — генеруємо значення
	v, err := fn()
	if err != nil {
		return err
	}

	// 3) кодуємо та кладемо в RAM
	b, err := msgpack.Marshal(v)
	if err != nil {
		return err
	}

	ch.SetRaw(key, b)

	// 4) декодуємо назад у dst (щоб dst гарантовано заповнився даними саме з кодека)
	if err := msgpack.Unmarshal(b, dst); err != nil {
		return err
	}

	return nil
}

// GetRaw повертає значення з RAM-снапшота по ключу.
// Повертає копію []byte, щоб викликач не міг мутувати внутрішній стан чанку.
func (ch *Chunk) GetRaw(key []byte) (val []byte, exist bool) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.memoryData.Data == nil {
		return nil, false
	}

	v, ok := ch.memoryData.Data[string(key)]
	if !ok {
		return nil, false
	}

	valCopy := make([]byte, len(v))
	copy(valCopy, v)
	return valCopy, true
}

// GetAndDelRaw повертає значення з RAM-снапшота та видаляє ключ.
// Повертає копію []byte і встановлює changes=true.
func (ch *Chunk) GetAndDelRaw(key []byte) (val []byte, exist bool, err error) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.memoryData.Data == nil {
		return nil, false, nil
	}

	v, ok := ch.memoryData.Data[string(key)]
	if !ok {
		return nil, false, nil
	}

	valCopy := make([]byte, len(v))
	copy(valCopy, v)

	delete(ch.memoryData.Data, string(key))
	ch.changes = true

	return valCopy, true, nil
}

// SetRaw записує значення у RAM-снапшот по ключу.
// Значення копіюється, щоб викликач не міг змінити байти “постфактум” через aliasing.
func (ch *Chunk) SetRaw(key, val []byte) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.memoryData.Data == nil {
		ch.memoryData.Data = make(map[string][]byte)
	}

	valCopy := make([]byte, len(val))
	copy(valCopy, val)
	ch.memoryData.Data[string(key)] = valCopy
	ch.changes = true
}

// Del видаляє ключ з RAM-снапшота і встановлює changes=true.
func (ch *Chunk) Del(key []byte) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.memoryData.Data == nil {
		return
	}

	delete(ch.memoryData.Data, string(key))
	ch.changes = true
}

// Clear очищає RAM-снапшот (видаляє всі ключі) і встановлює changes=true.
func (ch *Chunk) Clear() {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	ch.memoryData.Data = make(map[string][]byte)
	ch.changes = true
}

// OnSetRaw виконує fn і записує результат у RAM (SetRaw), якщо ключ відсутній.
// Повертає []byte (копію) так само, як GetRaw.
//
// Увага: цей метод працює з “сирими” байтами. Якщо ви використовуєте типізовані значення,
// використовуйте OnSet + Set/Get (msgpack).
func (ch *Chunk) OnSetRaw(key []byte, fn OnSet) (val []byte, err error) {
	val, exist := ch.GetRaw(key)
	if exist {
		return val, nil
	}

	val, err = fn()
	if err != nil {
		return nil, err
	}

	ch.SetRaw(key, val)
	return val, nil
}

// getOrCreateChunkRaw читає payload чанку з кешу або повертає порожній ChunkRaw.
// Payload кодується msgpack. Повернутий ChunkRaw завжди має не-nil Data.
func (ch *Chunk) getOrCreateChunkRaw() (ChunkRaw, error) {
	rawData, exist, err := ch.ch.Get(getChunkKey(ch.name))
	if err != nil {
		return ChunkRaw{}, err
	}

	var chunkData ChunkRaw
	if exist {
		dec := msgpack.NewDecoder(bytes.NewReader(rawData))
		if err := dec.Decode(&chunkData); err != nil {
			return ChunkRaw{}, err
		}
	}

	if chunkData.Data == nil {
		chunkData.Data = make(map[string][]byte)
	}

	return chunkData, nil
}

// saveChunkRaw серіалізує ChunkRaw (msgpack) і записує в кеш з TTL чанку.
func (ch *Chunk) saveChunkRaw(chunkData ChunkRaw) error {
	var buffer bytes.Buffer
	enc := msgpack.NewEncoder(&buffer)
	if err := enc.Encode(chunkData); err != nil {
		return err
	}
	return ch.ch.Set(getChunkKey(ch.name), buffer.Bytes(), ch.expiriesSecond)
}

// cloneChunkRaw робить глибоку копію ChunkRaw (map + []byte).
// Використовується при завантаженні у RAM, щоб відокремити RAM-снапшот від декодованого payload.
func cloneChunkRaw(src ChunkRaw) ChunkRaw {
	dst := ChunkRaw{
		Version: src.Version,
		Data:    make(map[string][]byte, len(src.Data)),
	}

	for k, v := range src.Data {
		b := make([]byte, len(v))
		copy(b, v)
		dst.Data[k] = b
	}

	return dst
}

// cloneChunkMapShallow копіює лише map, без копіювання []byte.
//
// Це оптимізація для SaveChanges(): ми створюємо новий payload (нову map),
// але значення []byte не дублюємо, щоб зменшити алокації.
//
// Безпечно за умови, що:
//  1. SetRaw завжди копіює вхідні байти,
//  2. GetRaw повертає копію,
//  3. ніхто не мутує внутрішні []byte напряму.
func cloneChunkMapShallow(src map[string][]byte) map[string][]byte {
	if src == nil {
		return make(map[string][]byte)
	}
	dst := make(map[string][]byte, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// SaveChanges фіксує зміни RAM-снапшота у кеш з optimistic CAS.
//
// Алгоритм:
//  1. Якщо змін не було — повертає nil.
//  2. Швидка перевірка: читає тільки versionKey і робить fast-fail при розбіжності з baseVersion.
//  3. Фінальна перевірка: читає payload і перевіряє самоконсистентність (payload.Version vs versionKey)
//     та збіг з baseVersion.
//  4. Формує next payload з версією baseVersion+1.
//     Для продуктивності копіює лише map (shallow copy), без дублювання []byte.
//  5. Записує payload, потім versionKey.
//  6. Оновлює локальний стан (baseVersion/memoryData.Version) і скидає changes.
//
// Повертає ErrChunkConflict, якщо чанк паралельно змінив інший writer.
func (ch *Chunk) SaveChanges() error {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if !ch.changes {
		return nil
	}

	// 1) швидка перевірка: читаємо тільки versionKey
	verKey, verKeyExist, err := ch.loadVersionKey()
	if err != nil {
		return err
	}

	// FAST FAIL: якщо версія-ключ існує і вже не збігається з baseVersion — конфлікт.
	if verKeyExist && verKey != ch.baseVersion {
		return ErrChunkConflict
	}

	// 2/3) payload для самоконсистентності (друга лінія оборони)
	current, err := ch.getOrCreateChunkRaw()
	if err != nil {
		return err
	}

	// 3) самоконсистентність: payload.Version має збігатися з versionKey (коли ключ існує)
	if verKeyExist && current.Version != verKey {
		return ErrChunkConflict
	}

	// 4) якщо versionKey не існував — ініціалізуємо його з payload.Version
	if !verKeyExist {
		if err := ch.saveVersionKey(current.Version); err != nil {
			return err
		}
		verKey = current.Version

		// після ініціалізації: якщо payload.Version не той, з яким ми працювали — конфлікт
		if verKey != ch.baseVersion {
			return ErrChunkConflict
		}
	}

	// 5) фінальна CAS перевірка по payload
	if current.Version != ch.baseVersion {
		return ErrChunkConflict
	}

	// 6) готуємо новий payload
	newVer := ch.baseVersion + 1
	next := ChunkRaw{
		Version: newVer,
		Data:    cloneChunkMapShallow(ch.memoryData.Data),
	}

	// 7) запис payload -> потім versionKey
	if err := ch.saveChunkRaw(next); err != nil {
		return err
	}
	if err := ch.saveVersionKey(newVer); err != nil {
		return err
	}

	// 8) оновлюємо локальний стан
	ch.memoryData.Version = newVer
	ch.baseVersion = newVer
	ch.changes = false
	return nil
}
