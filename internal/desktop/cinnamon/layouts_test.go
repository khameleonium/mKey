package cinnamon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClient — Cinnamon с заданными источниками ввода; переключение применяется не сразу,
// а со второго опроса (как асинхронное переключение в Cinnamon).
type fakeClient struct {
	mu      sync.Mutex
	srcs    []InputSource
	pending int32
	polls   int
	err     error
}

// sources возвращает копию источников, применяя отложенное переключение.
func (f *fakeClient) sources(context.Context) ([]InputSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.pending >= 0 {
		if f.polls++; f.polls > 1 {
			for i := range f.srcs {
				f.srcs[i].Current = f.srcs[i].Index == f.pending
			}
			f.pending = -1
		}
	}
	return append([]InputSource(nil), f.srcs...), nil
}

// activate запоминает переключение.
func (f *fakeClient) activate(_ context.Context, index int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending, f.polls = index, 0
	return nil
}

// TestLayouts проверяет чтение раскладок (ibus пропускается, повтор — один раз), переключение
// с ожиданием, неизвестную раскладку и ошибку D-Bus.
func TestLayouts(t *testing.T) {
	t.Parallel()
	f := &fakeClient{pending: -1, srcs: []InputSource{
		{Type: "xkb", Index: 0, XkbLayout: "us"},
		{Type: "ibus", Index: 1, XkbLayout: "", ID: "anthy"},
		{Type: "xkb", Index: 2, XkbLayout: "ru", Current: true},
		{Type: "xkb", Index: 3, XkbLayout: "ru", Variant: "phonetic"},
	}}
	l := &Layouts{c: f, wait: time.Second, poll: time.Millisecond}
	ctx := context.Background()

	info, err := l.Layouts(ctx)
	if err != nil || info.Current != "ru" || strings.Join(info.Available, ",") != "us,ru" || !info.CanSwitch || info.Source != "cinnamon" {
		t.Fatalf("Layouts = %+v, %v", info, err)
	}
	if err := l.Switch(ctx, "us"); err != nil {
		t.Fatalf("Switch = %v", err)
	}
	if info, _ := l.Layouts(ctx); info.Current != "us" {
		t.Fatalf("after switch = %+v", info)
	}
	if err := l.Switch(ctx, "us"); err != nil {
		t.Fatalf("switch to current = %v", err)
	}
	if err := l.Switch(ctx, "de"); err == nil {
		t.Fatal("unknown layout switched")
	}

	// Ошибка D-Bus — понятная ошибка.
	f.err = errors.New("no such service")
	if _, err := l.Layouts(ctx); err == nil || !strings.Contains(err.Error(), "no such service") {
		t.Fatalf("dbus error = %v", err)
	}
}

// TestSwitchNotApplied: Cinnamon не применил переключение — ошибка по истечении ожидания.
func TestSwitchNotApplied(t *testing.T) {
	t.Parallel()
	f := &stuckClient{srcs: []InputSource{{Type: "xkb", Index: 0, XkbLayout: "us", Current: true}, {Type: "xkb", Index: 1, XkbLayout: "ru"}}}
	l := &Layouts{c: f, wait: 20 * time.Millisecond, poll: time.Millisecond}
	if err := l.Switch(context.Background(), "ru"); err == nil || !strings.Contains(err.Error(), "not applied") {
		t.Fatalf("Switch = %v", err)
	}
}

// stuckClient — Cinnamon, который принимает переключение, но не применяет его.
type stuckClient struct{ srcs []InputSource }

// sources возвращает неизменные источники.
func (s *stuckClient) sources(context.Context) ([]InputSource, error) { return s.srcs, nil }

// activate принимает запрос и ничего не делает.
func (s *stuckClient) activate(context.Context, int32) error { return nil }
