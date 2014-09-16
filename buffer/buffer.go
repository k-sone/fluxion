package buffer

import (
	"container/list"
	"fmt"
	"sync"
	"time"
)

type StringItem string

func (s StringItem) Size() int64 {
	return int64(len(s))
}

type BytesItem []byte

func (s BytesItem) Size() int64 {
	return int64(len(s))
}

type Sizer interface {
	Size() int64
}

type Chunk interface {
	Push(Sizer)
}

type Handler interface {
	Write([]Sizer) (int, error)
}

type ChunkHandler func(c Chunk) error

type MemoryChunk struct {
	Size  int64
	Items []Sizer
}

func (m *MemoryChunk) Push(s Sizer) {
	m.Size += s.Size()
	m.Items = append(m.Items, s)
}

type Memory struct {
	chunks        *list.List
	maxChunkSize  int64
	maxQueueSize  int64
	flushInterval time.Duration
	handler       Handler
	eventCh       chan bool
	closed        bool
	m             sync.Mutex
}

func NewMemory(opts *Options, h Handler) *Memory {
	m := &Memory{
		chunks:        list.New(),
		maxChunkSize:  int64(opts.MaxChunkSize),
		maxQueueSize:  int64(opts.MaxQueueSize),
		flushInterval: time.Duration(opts.FlushInterval),
		handler:       h,
		eventCh:       make(chan bool),
	}
	go m.pop()
	return m
}

func (m *Memory) Push(s Sizer) error {
	n := s.Size()
	if n > m.maxChunkSize {
		return fmt.Errorf("Too large item: %d, max: %d", n, m.maxChunkSize)
	}
	m.m.Lock()
	defer m.m.Unlock()

	e := m.chunks.Front()
	if e == nil || e.Value.(*MemoryChunk).Size+n > m.maxChunkSize {
		if e != nil {
			m.eventCh <- true
		}

		e = m.chunks.PushFront(&MemoryChunk{})
		if int64(m.chunks.Len()) > m.maxQueueSize {
			m.chunks.Remove(m.chunks.Back())
		}
	}

	e.Value.(*MemoryChunk).Push(s)
	return nil
}

func (m *Memory) Close() {
	m.closed = true
	close(m.eventCh)
	m.m.Lock()
	defer m.m.Unlock()
	for e := m.chunks.Front(); e != nil; e = e.Next() {
		m.handler.Write(e.Value.(*MemoryChunk).Items)
	}
	m.chunks.Init()
}

func (m *Memory) pop() {
	var tick <-chan time.Time
	tick = time.Tick(m.flushInterval)
	for {
		select {
		case <-m.eventCh:
			if m.closed {
				return
			}
		case <-tick:
		}

		m.m.Lock()
		e := m.chunks.Back()
		if e == nil {
			m.m.Unlock()
			continue
		}

		chunk := e.Value.(*MemoryChunk)
		n, err := m.handler.Write(chunk.Items)
		if err != nil {
			copy(chunk.Items, chunk.Items[n:])
		} else {
			m.chunks.Remove(e)
		}
		m.m.Unlock()
	}
}