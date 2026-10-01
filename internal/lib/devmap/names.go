package devmap

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	ev "mkey/internal/lib/evdev"
)

// Имена кнопок модели устройства в проекте (ADR-0027): раздел devices проекта. Когда проект
// сохраняют в файл, mKey дописывает туда имена кнопок, которые встречаются в событиях проекта;
// у того, кто загрузит проект, такое же устройство при подключении получит эти имена — и события
// с {Геймпад.Старт} сразу заработают.
//
//	devices:
//	  - match: { vid: "0079", pid: "0011", name: "USB Gamepad" }  # модель (name — необязательно)
//	    name: Геймпад                                            # имя устройства (необязательно)
//	    buttons: { BTN_TRIGGER: Старт, BTN_THUMB: Огонь }        # код ядра → имя кнопки
//	    axes: { ABS_THROTTLE: Газ }                              # код ядра → имя оси
//
// Кнопки — кодами ядра, а не номерами 001…: номер зависит от того, у каких кнопок модели нет
// стандартного имени, а код — нет. Имена — по правилам переименования.

// Names — имена кнопок и осей одной модели устройства.
type Names struct {
	// Match — модель: vid и pid обязательны, name — точное название устройства (необязательно).
	Match NamesMatch `yaml:"match" json:"match"`
	// Name — имя устройства для макросов ("Геймпад"); пусто — не задавать.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Buttons и Axes — имена по кодам ядра ("BTN_TRIGGER" → "Старт", "ABS_THROTTLE" → "Газ").
	Buttons map[string]string `yaml:"buttons,omitempty" json:"buttons,omitempty"`
	Axes    map[string]string `yaml:"axes,omitempty" json:"axes,omitempty"`
}

// NamesMatch — модель устройства.
type NamesMatch struct {
	Vid  string `yaml:"vid" json:"vid"`
	Pid  string `yaml:"pid" json:"pid"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// ErrNames — ошибка в именах кнопок устройства.
var ErrNames = errors.New("invalid device names")

// Validate проверяет имена: vid и pid (4 шестнадцатеричные цифры), известные коды ядра, имена по
// правилам и без повторов.
func (n Names) Validate() error {
	// Модель и имя устройства.
	if !isHex4(strings.ToLower(n.Match.Vid)) || !isHex4(strings.ToLower(n.Match.Pid)) {
		return fmt.Errorf("%w: match.vid and match.pid must be 4 hex digits (e.g. \"0079\")", ErrNames)
	}
	if n.Name != "" {
		if err := checkName(n.Name); err != nil {
			return fmt.Errorf("%w: name: %w", ErrNames, err)
		}
	}

	// Кнопки и оси: известные коды, имена по правилам, без повторов.
	seen := map[string]string{}
	for _, g := range []struct {
		typ   []uint16
		names map[string]string
	}{{[]uint16{ev.EvKey}, n.Buttons}, {[]uint16{ev.EvAbs, ev.EvRel}, n.Axes}} {
		for code, name := range g.names {
			if !slices.ContainsFunc(g.typ, func(t uint16) bool { _, ok := ev.ParseCode(t, code); return ok }) {
				return fmt.Errorf("%w: unknown code %q", ErrNames, code)
			}
			if err := checkName(name); err != nil {
				return fmt.Errorf("%w: %s: %w", ErrNames, code, err)
			}
			if other, dup := seen[strings.ToLower(name)]; dup {
				return fmt.Errorf("%w: name %q is used twice (%s, %s)", ErrNames, name, other, code)
			}
			seen[strings.ToLower(name)] = code
		}
	}
	return nil
}

// isHex4 сообщает, что строка — ровно 4 шестнадцатеричные цифры.
func isHex4(s string) bool {
	return len(s) == 4 && strings.Trim(s, "0123456789abcdef") == ""
}

// Matches сообщает, подходят ли имена устройству с приметами m.
func (n Names) Matches(m Match) bool {
	return strings.EqualFold(n.Match.Vid, m.Vid) && strings.EqualFold(n.Match.Pid, m.Pid) &&
		(n.Match.Name == "" || n.Match.Name == m.Name)
}

// Apply подставляет имена туда, где у устройства d их ещё нет: имя устройства (если оно свободно
// в файле f) и имена кнопок и осей, которые есть в записи. Имена, данные человеком, не меняются;
// имя, которое нельзя дать (занято, совпадает с клавишей), пропускается. true — что-то изменилось.
func (n Names) Apply(f *File, d *Device) bool {
	// Имя устройства.
	changed := d.Name == "" && n.Name != "" && f.SetDeviceName(d.AutoID, n.Name) == nil

	// Имена кнопок и осей: по коду ядра — номер в записи.
	for _, g := range []struct {
		m     map[string]Control
		names map[string]string
	}{{d.Buttons, n.Buttons}, {d.Axes, n.Axes}} {
		for num, c := range g.m {
			name, ok := g.names[c.Code]
			if !ok || c.Name != "" {
				continue
			}
			if d.SetButtonName(num, name) == nil {
				changed = true
			}
		}
	}
	return changed
}

// NamesOf составляет имена модели из записи устройства d (для сохранения проекта в файл): модель
// из примет, имя устройства и имена кнопок и осей. false — у устройства нет ни одного имени.
func NamesOf(d *Device) (Names, bool) {
	n := Names{
		Match: NamesMatch{Vid: d.Match.Vid, Pid: d.Match.Pid, Name: d.Match.Name},
		Name:  d.Name, Buttons: map[string]string{}, Axes: map[string]string{},
	}
	for _, c := range d.Buttons {
		if c.Name != "" {
			n.Buttons[c.Code] = c.Name
		}
	}
	for _, c := range d.Axes {
		if c.Name != "" {
			n.Axes[c.Code] = c.Name
		}
	}
	return n, n.Name != "" || len(n.Buttons)+len(n.Axes) > 0
}
