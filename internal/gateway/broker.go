package gateway

import (
	"sync"

	"github.com/yernsun/network-service-composer/internal/model"
)

type Broker struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[string]map[uint64]chan model.StateChange
}

func NewBroker() *Broker {
	return &Broker{subscribers: make(map[string]map[uint64]chan model.StateChange)}
}

func (b *Broker) Publish(change model.StateChange) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, channel := range b.subscribers[change.PoolID] {
		select {
		case channel <- change:
		default:
		}
	}
}

func (b *Broker) Subscribe(poolID string) (<-chan model.StateChange, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	channel := make(chan model.StateChange, 1)
	if b.subscribers[poolID] == nil {
		b.subscribers[poolID] = make(map[uint64]chan model.StateChange)
	}
	b.subscribers[poolID][id] = channel
	return channel, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subscribers := b.subscribers[poolID]; subscribers != nil {
			delete(subscribers, id)
			if len(subscribers) == 0 {
				delete(b.subscribers, poolID)
			}
		}
	}
}
