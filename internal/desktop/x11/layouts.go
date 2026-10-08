package x11

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
)

// Layouts — источник раскладок X11 (contracts.LayoutSource): раскладки — свойство
// _XKB_RULES_NAMES корневого окна («evdev\0pc105\0us,ru\0,\0grp:alt_shift_toggle»), текущая —
// группа XKB основной клавиатуры, переключение — XkbLatchLockState (как сочетанием клавиш;
// окружение рабочего стола видит смену и обновляет индикатор).
type Layouts struct {
	// display — значение DISPLAY ("" — из окружения процесса); dial — подключение (в тестах подменяется).
	display string
	dial    func(display string) (xconn, error)
	// settle — через сколько после переключения проверить его ещё раз: некоторые окружения
	// возвращают свою раскладку, если её сменила другая программа (в тестах — 0).
	settle time.Duration
}

// xconn — то, что нужно раскладкам от соединения с X-сервером (в тестах — фейк).
type xconn interface {
	rootProperty(name string) ([]byte, error)
	group() (int, error)
	lockGroup(g int) error
	close()
}

// New создаёт источник для дисплея display ("" — DISPLAY процесса).
func New(display string) *Layouts {
	return &Layouts{display: display, dial: func(d string) (xconn, error) { return dial(d) }, settle: settleDelay}
}

// Meta возвращает метаданные источника.
func (l *Layouts) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: "x11", NameKey: "layout_source.x11", Provider: "desktop"}
}

// Supports — только сессия X11 с известным дисплеем: в Wayland у XWayland своя, не настоящая
// раскладка, а без DISPLAY подключаться не к чему.
func (l *Layouts) Supports(s contracts.SessionInfo) bool {
	return s.Type == "x11" && l.displayName(s.Display) != ""
}

// displayName — дисплей источника: заданный, из сведений о сессии или из окружения.
func (l *Layouts) displayName(session string) string {
	for _, d := range []string{l.display, session, os.Getenv("DISPLAY")} {
		if d != "" {
			return d
		}
	}
	return ""
}

// open подключается к X-серверу.
func (l *Layouts) open() (xconn, error) {
	d := l.displayName("")
	if d == "" {
		return nil, errors.New("x11: DISPLAY is not set")
	}
	return l.dial(d)
}

// groups возвращает раскладки по номерам групп XKB ("us", "ru"…; варианты отбрасываются).
func groups(x xconn) ([]string, error) {
	raw, err := x.rootProperty("_XKB_RULES_NAMES")
	if err != nil {
		return nil, err
	}
	// Поля через NUL: правила, модель, раскладки, варианты, параметры.
	fields := strings.Split(string(raw), "\x00")
	if len(fields) < 3 || strings.TrimSpace(fields[2]) == "" {
		return nil, errors.New("x11: no layouts in _XKB_RULES_NAMES")
	}
	var out []string
	for _, l := range strings.Split(fields[2], ",") {
		out = append(out, strings.TrimSpace(l))
	}
	return out, nil
}

// Layouts возвращает раскладки: список без повторов по порядку групп, текущая — по группе XKB.
func (l *Layouts) Layouts(context.Context) (contracts.LayoutInfo, error) {
	x, err := l.open()
	if err != nil {
		return contracts.LayoutInfo{}, err
	}
	defer x.close()
	names, err := groups(x)
	if err != nil {
		return contracts.LayoutInfo{}, err
	}
	g, err := x.group()
	if err != nil {
		return contracts.LayoutInfo{}, err
	}
	if g < 0 || g >= len(names) {
		return contracts.LayoutInfo{}, fmt.Errorf("x11: layout group %d of %d", g, len(names))
	}
	var available []string
	for _, n := range names {
		if !slices.Contains(available, n) {
			available = append(available, n)
		}
	}
	return contracts.LayoutInfo{Current: names[g], Available: available, CanSwitch: true, Source: "x11"}, nil
}

// settleDelay — пауза перед повторной проверкой переключения: окружение, которое откатывает чужую
// смену группы XKB, делает это за десятки миллисекунд (Cinnamon 6.6 — меньше 50 мс).
const settleDelay = 80 * time.Millisecond

// Switch переключает раскладку на name (первая группа с такой раскладкой) и проверяет, что
// окружение рабочего стола не вернуло прежнюю.
func (l *Layouts) Switch(ctx context.Context, name string) error {
	x, err := l.open()
	if err != nil {
		return err
	}
	defer x.close()
	names, err := groups(x)
	if err != nil {
		return err
	}
	g := slices.Index(names, name)
	if g < 0 {
		return fmt.Errorf("x11: layout %q is not enabled (%s)", name, strings.Join(names, ", "))
	}
	if err := x.lockGroup(g); err != nil {
		return err
	}

	// Ждём и проверяем ещё раз: откат окружением — ошибка, иначе текст напечатается не той раскладкой.
	if l.settle > 0 {
		t := time.NewTimer(l.settle)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	got, err := x.group()
	if err != nil {
		return err
	}
	if got != g {
		return fmt.Errorf("x11: the desktop reverted the layout switch to %q", name)
	}
	return nil
}

var _ contracts.LayoutSource = (*Layouts)(nil)
