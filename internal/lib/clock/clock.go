// Package clock — абстракция времени mKey (AGENTS.md §4.3).
//
// Весь код с паузами и таймаутами получает Clock снаружи, а не вызывает time.Sleep
// напрямую: в тестах подставляются фейковые часы (Fake), и тесты не ждут реального времени.
// Каждая пауза прерывается отменой контекста — это нужно для экстренной остановки макросов.
package clock

import (
	"context"
	"sync"
	"time"
)

// Clock — источник времени и прерываемых пауз.
type Clock interface {
	// Now возвращает текущее время.
	Now() time.Time
	// Sleep ждёт d или отмены ctx; при отмене возвращает ctx.Err().
	Sleep(ctx context.Context, d time.Duration) error
}

// Real — часы на основе системного времени.
type Real struct{}

// Now возвращает текущее системное время.
func (Real) Now() time.Time { return time.Now() }

// Sleep ждёт d или отмены ctx.
func (Real) Sleep(ctx context.Context, d time.Duration) error {
	// Нулевая или отрицательная пауза — только проверка отмены.
	if d <= 0 {
		return ctx.Err()
	}

	// Ждём таймер или отмену контекста, что наступит раньше.
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Fake — фейковые часы для тестов: Sleep не ждёт, а мгновенно сдвигает время вперёд.
type Fake struct {
	// mu защищает текущее время и журнал пауз.
	mu sync.Mutex
	// now — текущее фейковое время.
	now time.Time
	// sleeps — журнал запрошенных пауз (для проверок в тестах).
	sleeps []time.Duration
}

// NewFake создаёт фейковые часы с начальным временем start.
func NewFake(start time.Time) *Fake {
	return &Fake{now: start}
}

// Now возвращает текущее фейковое время.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Sleep записывает паузу в журнал и сдвигает время на d без реального ожидания.
func (f *Fake) Sleep(ctx context.Context, d time.Duration) error {
	// Отменённый контекст прерывает паузу, как у настоящих часов.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Сдвигаем время и запоминаем паузу.
	f.mu.Lock()
	defer f.mu.Unlock()
	if d > 0 {
		f.now = f.now.Add(d)
	}
	f.sleeps = append(f.sleeps, d)
	return nil
}

// Advance сдвигает фейковое время на d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Sleeps возвращает копию журнала пауз.
func (f *Fake) Sleeps() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.sleeps...)
}
