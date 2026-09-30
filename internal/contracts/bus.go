package contracts

import "time"

// Event — сообщение внутренней шины.
type Event struct {
	// Topic — тема события в виде "модуль.событие", например "input.device_added".
	Topic string
	// Time — момент публикации.
	Time time.Time
	// Payload — данные события; тип определяется темой и описывается рядом с её константой.
	Payload any
}

// Bus — внутренняя шина событий между модулями.
type Bus interface {
	// Publish отправляет событие всем подписчикам темы. Никогда не блокируется:
	// если подписчик не успевает читать, событие для него отбрасывается.
	Publish(topic string, payload any)
	// Subscribe подписывает на тему. Шаблон "prefix.*" означает все темы с этим префиксом,
	// "*" — все темы. Возвращает канал событий и функцию отписки.
	Subscribe(pattern string) (<-chan Event, func())
}
