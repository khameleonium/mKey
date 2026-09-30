package bus

import (
	"testing"
)

// TestMatch проверяет сопоставление тем с шаблонами подписки.
func TestMatch(t *testing.T) {
	t.Parallel()

	// Таблица: шаблон, тема, ожидаемый результат.
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"*", "input.device_added", true},
		{"input.*", "input.device_added", true},
		{"input.*", "inputx.device_added", false},
		{"input.*", "input", false},
		{"input.device_added", "input.device_added", true},
		{"input.device_added", "input.device_removed", false},
	}

	// Прогоняем все случаи.
	for _, c := range cases {
		if got := Match(c.pattern, c.topic); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

// TestPublishSubscribe проверяет доставку события подписчику и отписку.
func TestPublishSubscribe(t *testing.T) {
	t.Parallel()

	// Подписываемся на все события модуля input.
	b := New(4)
	ch, cancel := b.Subscribe("input.*")

	// Публикуем подходящее и неподходящее события.
	b.Publish("input.device_added", 42)
	b.Publish("engine.started", nil)

	// Должно прийти только подходящее событие с правильными данными.
	ev := <-ch
	if ev.Topic != "input.device_added" || ev.Payload != 42 {
		t.Fatalf("unexpected event: %+v", ev)
	}
	select {
	case ev := <-ch:
		t.Fatalf("unexpected extra event: %+v", ev)
	default:
	}

	// После отписки канал закрыт, повторная отписка не паникует.
	cancel()
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel must be closed after cancel")
	}
}

// TestPublishNeverBlocks проверяет, что медленный подписчик не блокирует публикацию.
func TestPublishNeverBlocks(t *testing.T) {
	t.Parallel()

	// Подписчик с буфером 1, который ничего не читает.
	b := New(1)
	_, cancel := b.Subscribe("*")
	defer cancel()

	// Публикуем три события: первое ляжет в буфер, два отбросятся.
	for range 3 {
		b.Publish("x.y", nil)
	}
	if got := b.Dropped(); got != 2 {
		t.Fatalf("Dropped() = %d, want 2", got)
	}
}
