package clock

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRealSleepCancel проверяет, что пауза реальных часов прерывается отменой контекста.
func TestRealSleepCancel(t *testing.T) {
	t.Parallel()

	// Отменённый контекст: Sleep на час должен вернуться сразу с ошибкой отмены.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Real{}).Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep = %v, want context.Canceled", err)
	}
}

// TestFake проверяет сдвиг времени и журнал пауз фейковых часов.
func TestFake(t *testing.T) {
	t.Parallel()

	// Две паузы сдвигают время на сумму и попадают в журнал.
	start := time.Unix(0, 0)
	f := NewFake(start)
	_ = f.Sleep(context.Background(), 100*time.Millisecond)
	_ = f.Sleep(context.Background(), 50*time.Millisecond)
	if got := f.Now().Sub(start); got != 150*time.Millisecond {
		t.Fatalf("elapsed = %v", got)
	}
	if s := f.Sleeps(); len(s) != 2 || s[0] != 100*time.Millisecond {
		t.Fatalf("Sleeps = %v", s)
	}
}
