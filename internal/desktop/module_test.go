package desktop

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/registry"
)

// fakeSource — источник раскладок для тестов: работает в сессиях типа kind; err — его сбой.
type fakeSource struct {
	id, kind string
	info     contracts.LayoutInfo
	err      error
	switched *string
}

func (f fakeSource) Meta() contracts.ExtensionMeta { return contracts.ExtensionMeta{ID: f.id} }
func (f fakeSource) Supports(s contracts.SessionInfo) bool {
	return s.Type == f.kind
}
func (f fakeSource) Layouts(context.Context) (contracts.LayoutInfo, error) { return f.info, f.err }
func (f fakeSource) Switch(_ context.Context, name string) error {
	*f.switched = name
	return nil
}

// TestLayoutSources проверяет выбор источника раскладок: первый подходящий к сессии и работающий
// (сбойный пропускается, сбой пишется в журнал один раз); источник другого модуля или плагина для
// незнакомой сессии; ни один не видит раскладку — раскладки из настроек с пометкой Blind.
func TestLayoutSources(t *testing.T) {
	t.Parallel()
	logs := &strings.Builder{}
	m := &Module{log: slog.New(slog.NewTextHandler(logs, nil)), ext: registry.NewExtensions(), warned: map[string]bool{}}
	m.fallback = configLayouts{cfg: Config{Layouts: []string{"us", "ru"}, Layout: "ru"}}
	switched := ""
	broken := fakeSource{id: "broken", kind: "x11", err: errors.New("no server"), switched: &switched}
	x := fakeSource{id: "x11", kind: "x11", info: contracts.LayoutInfo{Current: "us", Available: []string{"us", "ru"}, CanSwitch: true, Source: "x11"}, switched: &switched}
	other := fakeSource{id: "plugin", kind: "mystery", info: contracts.LayoutInfo{Current: "de", Available: []string{"de"}, Source: "plugin"}, switched: &switched}
	for _, src := range []contracts.LayoutSource{broken, x, other} {
		if err := m.ext.Register(contracts.PointLayoutSource, src); err != nil {
			t.Fatal(err)
		}
	}

	// Сессия X11: сбойный источник пропущен, работает следующий; переключение — через него.
	m.session = contracts.SessionInfo{Type: "x11"}
	for range 2 {
		if info, err := m.Layouts(context.Background()); err != nil || info.Source != "x11" || info.Blind {
			t.Fatalf("x11 = %+v, %v", info, err)
		}
	}
	if n := strings.Count(logs.String(), "no server"); n != 1 {
		t.Fatalf("source failure logged %d times", n)
	}
	if err := m.Switch(context.Background(), "ru"); err != nil || switched != "ru" {
		t.Fatalf("switch = %v, %q", err, switched)
	}

	// Незнакомая сессия: источник, добавленный модулем или плагином.
	m.session = contracts.SessionInfo{Type: "mystery"}
	if info, _ := m.Layouts(context.Background()); info.Source != "plugin" {
		t.Fatalf("plugin source = %+v", info)
	}

	// Ни один источник не подходит: раскладки из настроек, Blind; переключать нечем.
	m.session = contracts.SessionInfo{Type: "wayland"}
	info, err := m.Layouts(context.Background())
	if err != nil || info.Current != "ru" || info.Source != "config" || !info.Blind || info.CanSwitch {
		t.Fatalf("fallback = %+v, %v", info, err)
	}
	if err := m.Switch(context.Background(), "us"); !errors.Is(err, contracts.ErrUnsupported) {
		t.Fatalf("Switch = %v", err)
	}

	// Пустые настройки: одна раскладка us.
	if info, _ := (configLayouts{}).Layouts(context.Background()); info.Current != "us" || len(info.Available) != 1 {
		t.Fatalf("defaults = %+v", info)
	}
}

// TestScreenSize проверяет размер экрана из настроек: задан, не задан, записан неверно.
func TestScreenSize(t *testing.T) {
	t.Parallel()
	m := &Module{cfg: Config{Screen: "1920x1080"}}
	if w, h, err := m.ScreenSize(context.Background()); err != nil || w != 1920 || h != 1080 {
		t.Fatalf("size = %d %d %v", w, h, err)
	}
	m.cfg.Screen = ""
	if _, _, err := m.ScreenSize(context.Background()); !errors.Is(err, contracts.ErrUnsupported) {
		t.Fatalf("unknown: %v", err)
	}
	if _, _, err := parseScreen("1920*1080"); err == nil {
		t.Fatal("bad format accepted")
	}
}

// TestOffline проверяет режим без живой сессии: встроенных источников нет (раскладки — из
// настроек, «вслепую»), к D-Bus модуль не подключается.
func TestOffline(t *testing.T) {
	t.Parallel()
	m := NewOffline()
	if n := len(m.builtinSources()); n != 0 || len(New().builtinSources()) != 4 {
		t.Fatalf("builtin sources in offline mode: %d", n)
	}
	if _, err := m.connect(); err == nil {
		t.Fatal("offline module connects to D-Bus")
	}
	m.log, m.ext, m.warned = slog.New(slog.DiscardHandler), registry.NewExtensions(), map[string]bool{}
	m.fallback = configLayouts{cfg: m.cfg}
	m.session = contracts.SessionInfo{Type: "x11", Display: ":0"}
	if info, err := m.Layouts(context.Background()); err != nil || !info.Blind || info.Source != "config" {
		t.Fatalf("Layouts = %+v, %v", info, err)
	}
}
