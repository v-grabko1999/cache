package cache

import (
	"bytes"
	"encoding/gob"
)

func NewCache(dr CacheDriver) *Cache {
	return &Cache{dr}
}

type Cache struct {
	dr CacheDriver
}

func (ch *Cache) Get(key []byte) (val []byte, exist bool, err error) {
	return ch.dr.Get(key)
}

func (ch *Cache) GetAndDel(key []byte) (val []byte, exist bool, err error) {
	val, exist, err = ch.Get(key)
	if err != nil {
		return
	}

	if exist {
		ch.Del(key)
	}
	return
}

func (ch *Cache) Set(key, val []byte, expiriesSecond int) error {
	return ch.dr.Set(key, val, expiriesSecond)
}

type OnSet func() (value []byte, err error)

func (ch *Cache) OnSet(key []byte, fn OnSet, expiriesSecond int) (val []byte, err error) {
	val, exist, err := ch.Get(key)
	if err != nil {
		return
	}
	if !exist {
		val, err = fn()
		if err != nil {
			return
		}
		err = ch.Set(key, val, expiriesSecond)
	}
	return
}

func (ch *Cache) Del(key []byte) error {
	return ch.dr.Del(key)
}

func (ch *Cache) Clear() error {
	return ch.dr.Clear()
}

// Функция создания нового Chunk
func (ch *Cache) Chunk(name string, expiriesSecond int) (*Chunk, error) {
	_, err := ch.OnSet(getChunkKey(name), func() ([]byte, error) {
		var buffer bytes.Buffer
		encoder := gob.NewEncoder(&buffer)
		err := encoder.Encode(&ChunkRaw{})
		return buffer.Bytes(), err
	}, expiriesSecond)

	chunk := &Chunk{
		ch:             ch,
		name:           name,
		expiriesSecond: expiriesSecond,
	}

	// Автоматическая загрузка данных в память в момент создания чанка
	if err := chunk.loadToMemory(); err != nil {
		return nil, err
	}

	return chunk, err
}
func (ch *Cache) DeleteChunk(name string) error {
	return ch.Del(getChunkKey(name))
}

func (ch *Cache) Close() error {
	return ch.dr.Close()
}
