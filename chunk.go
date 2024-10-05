package cache

import (
	"bytes"
	"encoding/gob"
	"sync"
)

type Chunk struct {
	ch             *Cache
	name           string
	expiriesSecond int
	memoryData     ChunkRaw
	mu             sync.Mutex
	changes        bool
}

type ChunkRaw map[string][]byte

// Функция для создания ключа в кеше
func getChunkKey(name string) []byte {
	return []byte("cache_package_chank_" + name)
}

// Загрузка данных в оперативную память (часть инициализации Chunk)
func (ch *Chunk) loadToMemory() error {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	chunkData, err := ch.getOrCreateChunkRaw()
	if err != nil {
		return err
	}

	// Загружаем данные в оперативную память
	ch.memoryData = chunkData
	ch.changes = false
	return nil
}

// Окончательная запись изменений в кеш (выполняется в `defer`)
func (ch *Chunk) SaveChanges() error {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if !ch.changes {
		return nil
	}

	return ch.saveChunkRaw(ch.memoryData)
}

// Получение значения по ключу из оперативной памяти
func (ch *Chunk) Get(key []byte) (val []byte, exist bool) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	val, exist = ch.memoryData[string(key)]
	if exist {
		// Создаем копию данных перед возвратом
		valCopy := make([]byte, len(val))
		copy(valCopy, val)
		return valCopy, true
	}
	return nil, false
}

// Получение значения по ключу и удаление его из оперативной памяти
func (ch *Chunk) GetAndDel(key []byte) (val []byte, exist bool, err error) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	val, exist = ch.memoryData[string(key)]
	if exist {
		// Создаем копию данных перед удалением
		valCopy := make([]byte, len(val))
		copy(valCopy, val)
		delete(ch.memoryData, string(key))
		ch.changes = true
		return valCopy, true, nil
	}
	return nil, false, nil
}

// Установка значения по ключу в оперативной памяти
func (ch *Chunk) Set(key, val []byte) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	// Создаем копию данных перед сохранением
	valCopy := make([]byte, len(val))
	copy(valCopy, val)
	ch.memoryData[string(key)] = valCopy
	ch.changes = true
}

// Удаление значения по ключу в оперативной памяти
func (ch *Chunk) Del(key []byte) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	delete(ch.memoryData, string(key))
	ch.changes = true
}

// Очистка всех данных в оперативной памяти
func (ch *Chunk) Clear() {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	ch.memoryData = ChunkRaw{}
	ch.changes = true
}

// Метод OnSet выполняет действие, если ключ не существует в Chunk
func (ch *Chunk) OnSet(key []byte, fn OnSet) (val []byte, err error) {
	val, exist := ch.Get(key)
	if !exist {
		val, err = fn()
		if err != nil {
			return nil, err
		}
		ch.Set(key, val)
	}
	return val, err
}

// Вспомогательная функция для получения или создания нового ChunkRaw
func (ch *Chunk) getOrCreateChunkRaw() (ChunkRaw, error) {
	rawData, exist, err := ch.ch.Get(getChunkKey(ch.name))
	if err != nil {
		return nil, err
	}

	var chunkData ChunkRaw
	if exist {
		decoder := gob.NewDecoder(bytes.NewReader(rawData))
		err = decoder.Decode(&chunkData)
		if err != nil {
			return nil, err
		}
	} else {
		chunkData = ChunkRaw{}
	}

	return chunkData, nil
}

// Вспомогательная функция для сохранения ChunkRaw в кеш
func (ch *Chunk) saveChunkRaw(chunkData ChunkRaw) error {
	var buffer bytes.Buffer
	encoder := gob.NewEncoder(&buffer)
	err := encoder.Encode(chunkData)
	if err != nil {
		return err
	}

	return ch.ch.Set(getChunkKey(ch.name), buffer.Bytes(), ch.expiriesSecond)
}
