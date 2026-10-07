package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/clock"
	"github.com/khameleonium/mKey/internal/lib/dsl"
)

// fakeScreen — виртуальный сенсорный экран: записывает касания («down x y», «move x y», «up»).
type fakeScreen struct {
	*fakeDevice
	smu sync.Mutex
	got []string
}

func (s *fakeScreen) add(e string) {
	s.smu.Lock()
	defer s.smu.Unlock()
	s.got = append(s.got, e)
}

func (s *fakeScreen) TouchDown(_ context.Context, x, y float64) error {
	s.add(fmt.Sprintf("down %.2f %.2f", x, y))
	return nil
}

func (s *fakeScreen) TouchMove(_ context.Context, x, y float64) error {
	s.add(fmt.Sprintf("move %.2f %.2f", x, y))
	return nil
}

func (s *fakeScreen) TouchUp(context.Context) error { s.add("up"); return nil }

func (s *fakeScreen) log() []string {
	s.smu.Lock()
	defer s.smu.Unlock()
	return append([]string(nil), s.got...)
}

// fakeTouchVdevs — виртуальные устройства: сенсорные экраны по именам.
type fakeTouchVdevs struct {
	contracts.VirtualDeviceManager
	screens map[string]*fakeScreen
}

func (f fakeTouchVdevs) List() []contracts.VirtualDeviceInfo {
	var out []contracts.VirtualDeviceInfo
	for n := range f.screens {
		out = append(out, contracts.VirtualDeviceInfo{Name: n, Template: contracts.TemplateTouchscreen})
	}
	return out
}

func (f fakeTouchVdevs) Device(name string) (contracts.VirtualDevice, error) {
	if s, ok := f.screens[name]; ok {
		return s, nil
	}
	return nil, contracts.ErrUnknownVirtual
}

func (f fakeTouchVdevs) Resolve(string, string) (uint16, bool, error) {
	return 0, false, contracts.ErrUnknownVirtual
}

// fakeScreenInfo — экран 1001×501 (пиксель 500 — ровно середина).
type fakeScreenInfo struct{ known bool }

func (f fakeScreenInfo) ScreenSize(context.Context) (int, int, error) {
	if !f.known {
		return 0, 0, contracts.ErrUnsupported
	}
	return 1001, 501, nil
}

// TestTouch проверяет касания из макросов: проценты, пиксели, свайп по шагам, выбор экрана и
// понятные ошибки.
func TestTouch(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(clock.NewFake(time.Unix(0, 0)), nil)
	scr := &fakeScreen{fakeDevice: newFakeDevice("mKey screen")}
	m.vdevs = fakeTouchVdevs{screens: map[string]*fakeScreen{"screen": scr}}
	m.screen = fakeScreenInfo{known: true}

	// Касание в процентах и в пикселях, свайп 30 мс — 3 шага.
	if err := m.Run(context.Background(), "{Touch 50% 80%}{Touch 500 250}{Swipe 0% 0% 30% 60% 30}"); err != nil {
		t.Fatal(err)
	}
	want := "down 0.50 0.80|up|down 0.50 0.50|up|down 0.00 0.00|move 0.10 0.20|move 0.20 0.40|move 0.30 0.60|up"
	if got := strings.Join(scr.log(), "|"); got != want {
		t.Fatalf("touches:\n%s\nwant:\n%s", got, want)
	}

	// Пиксели без размера экрана, два экрана без имени, не тот экран — понятные ошибки.
	m.screen = fakeScreenInfo{}
	m.vdevs = fakeTouchVdevs{screens: map[string]*fakeScreen{"a": scr, "b": scr}}
	for src, code := range map[string]string{
		"{Touch a 10 10}":   dsl.ErrScreenUnknown,
		"{Touch 10% 10%}":   dsl.ErrManyTouchscreens,
		"{Touch zzz 1% 1%}": dsl.ErrUnknownDevice,
	} {
		var de *dsl.Error
		if err := m.Run(context.Background(), src); !errors.As(err, &de) || de.Code != code {
			t.Errorf("%s: %v, want %s", src, err, code)
		}
	}
}

// TestTouchCancel проверяет, что остановка во время долгого касания отрывает палец.
func TestTouchCancel(t *testing.T) {
	t.Parallel()
	m, _ := newTestModule(clock.Real{}, nil)
	scr := &fakeScreen{fakeDevice: newFakeDevice("mKey screen")}
	m.vdevs = fakeTouchVdevs{screens: map[string]*fakeScreen{"screen": scr}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := m.Run(ctx, "{Touch 10% 10% 5000}"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(scr.log(), "|"); got != "down 0.10 0.10|up" {
		t.Fatalf("touches = %s", got)
	}
}
