package cinnamon

import (
	"context"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/khameleonium/mKey/internal/contracts"
)

// Адрес интерфейса Cinnamon и имя источника.
const (
	service = "org.Cinnamon"
	path    = "/org/Cinnamon"
	iface   = "org.Cinnamon"
	source  = "cinnamon"
)

// Ожидание переключения: Cinnamon переключает раскладку асинхронно, поэтому после запроса
// mKey проверяет текущую раскладку каждые pollEvery, но не дольше switchWait.
const (
	switchWait = 500 * time.Millisecond
	pollEvery  = 10 * time.Millisecond
)

// InputSource — источник ввода, как его отдаёт GetInputSources: тип ("xkb" или "ibus"), id,
// номер, названия, раскладка XKB, вариант и признак «текущий».
type InputSource struct {
	Type        string
	ID          string
	Index       int32
	DisplayName string
	ShortName   string
	FlagName    string
	XkbID       string
	XkbLayout   string
	Variant     string
	Preferences string
	DupeID      int32
	Current     bool
}

// client — вызовы D-Bus, нужные адаптеру (в тестах подменяются).
type client interface {
	// sources возвращает источники ввода.
	sources(ctx context.Context) ([]InputSource, error)
	// activate делает текущим источник с номером index.
	activate(ctx context.Context, index int32) error
}

// Layouts — источник раскладок Cinnamon (contracts.LayoutProvider).
type Layouts struct {
	c client
	// wait и poll — ожидание переключения (в тестах короче).
	wait, poll time.Duration
}

// New создаёт адаптер поверх подключения к сессионной шине D-Bus.
func New(conn *dbus.Conn) *Layouts {
	return &Layouts{c: dbusClient{obj: conn.Object(service, path)}, wait: switchWait, poll: pollEvery}
}

// xkbSources возвращает раскладки XKB (источники ibus — методы ввода, не раскладки — пропускаются).
func (l *Layouts) xkbSources(ctx context.Context) ([]InputSource, error) {
	all, err := l.c.sources(ctx)
	if err != nil {
		return nil, fmt.Errorf("cinnamon: get input sources: %w", err)
	}
	var out []InputSource
	for _, s := range all {
		if s.Type == "xkb" && s.XkbLayout != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("cinnamon: no keyboard layouts")
	}
	return out, nil
}

// Layouts возвращает раскладки (без повторов, по порядку Cinnamon) и текущую.
func (l *Layouts) Layouts(ctx context.Context) (contracts.LayoutInfo, error) {
	srcs, err := l.xkbSources(ctx)
	if err != nil {
		return contracts.LayoutInfo{}, err
	}

	// Собираем список; текущая — отмеченная Cinnamon (нет такой — текущий источник не раскладка).
	info := contracts.LayoutInfo{CanSwitch: true, Source: source}
	seen := map[string]bool{}
	for _, s := range srcs {
		if !seen[s.XkbLayout] {
			seen[s.XkbLayout] = true
			info.Available = append(info.Available, s.XkbLayout)
		}
		if s.Current {
			info.Current = s.XkbLayout
		}
	}
	if info.Current == "" {
		return contracts.LayoutInfo{}, fmt.Errorf("cinnamon: current input source is not a keyboard layout")
	}
	return info, nil
}

// Switch переключает раскладку на первую с именем name и ждёт, пока Cinnamon её применит.
func (l *Layouts) Switch(ctx context.Context, name string) error {
	// Находим источник ввода с этой раскладкой.
	srcs, err := l.xkbSources(ctx)
	if err != nil {
		return err
	}
	target := InputSource{Index: -1}
	for _, s := range srcs {
		if s.XkbLayout == name {
			target = s
			break
		}
	}
	if target.Index < 0 {
		return fmt.Errorf("cinnamon: layout %q is not configured", name)
	}
	if target.Current {
		return nil
	}

	// Просим Cinnamon переключить и ждём, пока раскладка станет текущей.
	if err := l.c.activate(ctx, target.Index); err != nil {
		return fmt.Errorf("cinnamon: activate layout: %w", err)
	}
	deadline := time.Now().Add(l.wait)
	for {
		if info, err := l.Layouts(ctx); err == nil && info.Current == name {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("cinnamon: layout %q was not applied", name)
		}
		t := time.NewTimer(l.poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// dbusClient — настоящие вызовы D-Bus.
type dbusClient struct {
	obj dbus.BusObject
}

// sources вызывает GetInputSources.
func (d dbusClient) sources(ctx context.Context) ([]InputSource, error) {
	var out []InputSource
	err := d.obj.CallWithContext(ctx, iface+".GetInputSources", 0).Store(&out)
	return out, err
}

// activate вызывает ActivateInputSourceIndex.
func (d dbusClient) activate(ctx context.Context, index int32) error {
	return d.obj.CallWithContext(ctx, iface+".ActivateInputSourceIndex", 0, index).Err
}

// Проверка на этапе компиляции, что Layouts реализует контракт.
var _ contracts.LayoutProvider = (*Layouts)(nil)
