package bus

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mkey/internal/contracts"
)

// DefaultBuffer — размер буфера канала подписчика по умолчанию.
const DefaultBuffer = 256

// Bus — реализация contracts.Bus.
type Bus struct {
	// mu защищает список подписчиков.
	mu sync.RWMutex
	// subs — активные подписчики.
	subs map[*subscriber]struct{}
	// buffer — размер канала для новых подписчиков.
	buffer int
	// now — источник времени (подменяется в тестах).
	now func() time.Time
	// dropped — сколько событий отброшено из-за медленных подписчиков.
	dropped atomic.Uint64
}

// subscriber — одна подписка на шину.
type subscriber struct {
	// pattern — шаблон темы, на которую подписан получатель.
	pattern string
	// ch — канал доставки событий.
	ch chan contracts.Event
}

// New создаёт шину с буфером подписчика buffer (если buffer <= 0, берётся DefaultBuffer).
func New(buffer int) *Bus {
	if buffer <= 0 {
		buffer = DefaultBuffer
	}
	return &Bus{
		subs:   make(map[*subscriber]struct{}),
		buffer: buffer,
		now:    time.Now,
	}
}

// Publish отправляет событие всем подписчикам, чей шаблон совпадает с темой.
func (b *Bus) Publish(topic string, payload any) {
	// Собираем событие один раз для всех получателей.
	ev := contracts.Event{Topic: topic, Time: b.now(), Payload: payload}

	// Рассылаем событие без блокировки: переполненный канал означает потерю события для этого подписчика.
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		if !Match(s.pattern, topic) {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			b.dropped.Add(1)
		}
	}
}

// Subscribe подписывает на темы по шаблону и возвращает канал событий и функцию отписки.
// Функция отписки закрывает канал; вызывать её повторно безопасно.
func (b *Bus) Subscribe(pattern string) (<-chan contracts.Event, func()) {
	// Регистрируем нового подписчика.
	s := &subscriber{pattern: pattern, ch: make(chan contracts.Event, b.buffer)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()

	// Готовим идемпотентную отписку: удалить из списка и закрыть канал ровно один раз.
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, s)
			b.mu.Unlock()
			close(s.ch)
		})
	}
	return s.ch, cancel
}

// Dropped возвращает число событий, отброшенных из-за переполненных подписчиков.
func (b *Bus) Dropped() uint64 {
	return b.dropped.Load()
}

// Match сообщает, подходит ли тема topic под шаблон pattern.
// Поддерживаются "*" (всё), "prefix.*" (все темы, начинающиеся с "prefix.") и точное совпадение.
func Match(pattern, topic string) bool {
	switch {
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, ".*"):
		return strings.HasPrefix(topic, strings.TrimSuffix(pattern, "*"))
	default:
		return pattern == topic
	}
}

// Проверка на этапе компиляции, что Bus реализует контракт.
var _ contracts.Bus = (*Bus)(nil)
