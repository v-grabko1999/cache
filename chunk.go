package cache

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrChunkConflict означає, що чанк був змінений іншим writer-ом між loadToMemory() і SaveChanges().
	// Це “оптимістична транзакція”: треба повторити операцію (перезавантажити чанк і застосувати зміни знову).
	ErrChunkConflict = errors.New("конфлікт версії чанку: дані були змінені паралельно")
)

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

type ChunkRaw struct {
	Version uint64
	Data    map[string][]byte
}

// getChunkKey — створює ключ для збереження чанку в кеші.
func getChunkKey(name string) []byte {
	return []byte("cache_package_chank_" + name)
}

// versionKey — окремий ключ версії (8 байт).
func getChunkVersionKey(name string) []byte {
	return []byte("cache_package_chank_" + name + "_version")
}

func (ch *Chunk) loadVersionKey() (uint64, bool, error) {
	b, exist, err := ch.ch.Get(getChunkVersionKey(ch.name))
	if err != nil {
		return 0, false, err
	}
	if !exist {
		return 0, false, nil
	}
	if len(b) != 8 {
		// якщо хочеш — повертай помилку, це ознака зіпсованих даних
		return 0, true, fmt.Errorf("invalid chunk version bytes len=%d", len(b))
	}
	return binary.LittleEndian.Uint64(b), true, nil
}

func (ch *Chunk) saveVersionKey(ver uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], ver)
	return ch.ch.Set(getChunkVersionKey(ch.name), buf[:], ch.expiriesSecond)
}

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

	// Самоконсистентність:
	// якщо версія-ключ існує — вона має збігатись з версією всередині payload.
	if verKeyExist && chunkData.Version != verKey {
		return fmt.Errorf("chunk version mismatch: key=%d payload=%d", verKey, chunkData.Version)
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

func (ch *Chunk) SaveChanges() error {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if !ch.changes {
		return nil
	}

	// 1) Швидка перевірка: читаємо тільки ключ версії
	verKey, verKeyExist, err := ch.loadVersionKey()
	if err != nil {
		return err
	}

	// Якщо версійного ключа нема — це нетипово, але можна відновити з payload
	if !verKeyExist {
		// відновлюємо консистентно: читаємо payload і виставляємо verKey
		current, e := ch.getOrCreateChunkRaw()
		if e != nil {
			return e
		}
		if err := ch.saveVersionKey(current.Version); err != nil {
			return err
		}
		verKey = current.Version
	}

	if verKey != ch.baseVersion {
		return fmt.Errorf("%w (очікувалось=%d, актуально=%d)", ErrChunkConflict, ch.baseVersion, verKey)
	}

	// 2) Фінальна перевірка: читаємо payload і звіряємо Version у ньому
	current, err := ch.getOrCreateChunkRaw()
	if err != nil {
		return err
	}
	if current.Version != ch.baseVersion {
		return fmt.Errorf("%w (payload) (очікувалось=%d, актуально=%d)", ErrChunkConflict, ch.baseVersion, current.Version)
	}

	// 3) Готуємо дані до запису
	toSave := cloneChunkRaw(ch.memoryData)
	toSave.Version = ch.baseVersion + 1

	// 4) Пишемо payload, потім версійний ключ
	if err := ch.saveChunkRaw(toSave); err != nil {
		return err
	}
	if err := ch.saveVersionKey(toSave.Version); err != nil {
		return err
	}

	ch.baseVersion = toSave.Version
	ch.memoryData = toSave
	ch.changes = false
	return nil
}

// Get повертає значення по ключу з RAM-снапшота.
// Повертає копію []byte.
func (ch *Chunk) Get(key []byte) (val []byte, exist bool) {
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

// GetAndDel повертає значення і видаляє його з RAM-снапшота.
func (ch *Chunk) GetAndDel(key []byte) (val []byte, exist bool, err error) {
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

// Set записує значення в RAM-снапшот (з копією []byte).
func (ch *Chunk) Set(key, val []byte) {
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

// Del видаляє значення з RAM-снапшота.
func (ch *Chunk) Del(key []byte) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.memoryData.Data == nil {
		return
	}

	delete(ch.memoryData.Data, string(key))
	ch.changes = true
}

// Clear очищає RAM-снапшот (всі ключі).
func (ch *Chunk) Clear() {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	ch.memoryData.Data = make(map[string][]byte)
	ch.changes = true
}

// OnSet виконує fn і Set, якщо ключ відсутній.
func (ch *Chunk) OnSet(key []byte, fn OnSet) (val []byte, err error) {
	val, exist := ch.Get(key)
	if exist {
		return val, nil
	}

	val, err = fn()
	if err != nil {
		return nil, err
	}

	ch.Set(key, val)
	return val, nil
}

// getOrCreateChunkRaw читає чанк з кешу або створює порожній.
// Повертає структуру з ініціалізованою Data.
func (ch *Chunk) getOrCreateChunkRaw() (ChunkRaw, error) {
	rawData, exist, err := ch.ch.Get(getChunkKey(ch.name))
	if err != nil {
		return ChunkRaw{}, err
	}

	var chunkData ChunkRaw
	if exist {
		decoder := gob.NewDecoder(bytes.NewReader(rawData))
		if err := decoder.Decode(&chunkData); err != nil {
			return ChunkRaw{}, err
		}
	}

	// Гарантуємо, що Data не nil.
	if chunkData.Data == nil {
		chunkData.Data = make(map[string][]byte)
	}

	return chunkData, nil
}

// saveChunkRaw серіалізує ChunkRaw і записує в кеш.
func (ch *Chunk) saveChunkRaw(chunkData ChunkRaw) error {
	var buffer bytes.Buffer
	encoder := gob.NewEncoder(&buffer)

	if err := encoder.Encode(chunkData); err != nil {
		return err
	}

	return ch.ch.Set(getChunkKey(ch.name), buffer.Bytes(), ch.expiriesSecond)
}

// cloneChunkRaw робить глибоку копію ChunkRaw (map + []byte).
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
