package kde

import (
	"context"
	"errors"
	"testing"
)

// fakeClient — KWin с заданными раскладками.
type fakeClient struct {
	names []LayoutName
	idx   uint32
}

func (f *fakeClient) list(context.Context) ([]LayoutName, error) { return f.names, nil }
func (f *fakeClient) current(context.Context) (uint32, error)    { return f.idx, nil }
func (f *fakeClient) set(_ context.Context, i uint32) (bool, error) {
	f.idx = i
	return true, nil
}

// TestLayouts проверяет чтение и переключение раскладок (данные как на машине владельца).
func TestLayouts(t *testing.T) {
	t.Parallel()
	c := &fakeClient{names: []LayoutName{{"ru", "", "Russian"}, {"us", "", "English (US)"}}}
	l := &Layouts{c: c}

	// Текущая — русская, доступны обе, переключение поддерживается.
	info, err := l.Layouts(context.Background())
	if err != nil || info.Current != "ru" || len(info.Available) != 2 || info.Available[1] != "us" || !info.CanSwitch {
		t.Fatalf("Layouts = %+v, %v", info, err)
	}

	// Переключение на us меняет индекс; неизвестная раскладка — ошибка.
	if err := l.Switch(context.Background(), "us"); err != nil || c.idx != 1 {
		t.Fatalf("Switch = %v, idx %d", err, c.idx)
	}
	if err := l.Switch(context.Background(), "de"); err == nil {
		t.Fatal("unknown layout must fail")
	}

	// Неверный индекс от KWin — ошибка, а не паника.
	c.idx = 5
	if _, err := l.Layouts(context.Background()); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("out of range index: %v", err)
	}
}
