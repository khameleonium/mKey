package devmap

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	ev "mkey/internal/lib/evdev"
)

// Профили устройств (FR-DEV-7, FR-PLG-8, ADR-0027): готовые имена кнопок и осей для известной
// модели устройства. Профиль — небольшой YAML-файл, которым можно поделиться:
//
//	version: 1
//	title: "Sega USB Joystick"          # для людей
//	match: { vid: "0079", pid: "0011" } # модель; name — точное название (необязательно)
//	device_name: Sega                   # имя устройства (необязательно)
//	buttons: { BTN_TRIGGER: A, BTN_THUMB: B }   # код ядра → имя кнопки
//	axes: { ABS_THROTTLE: Gas }                 # код ядра → имя оси
//
// Кнопки указываются кодами ядра, а не номерами 001…: так профиль не зависит от того, какие
// кнопки у модели без стандартного имени. Имена — по тем же правилам, что и при переименовании.

// ProfileVersion — версия формата профиля.
const ProfileVersion = 1

// Profile — профиль устройства.
type Profile struct {
	Version int `yaml:"version"`
	// Title — название профиля для людей ("Sega USB Joystick").
	Title string `yaml:"title,omitempty"`
	// Match — модель: vid и pid обязательны, name — точное название устройства (необязательно).
	Match ProfileMatch `yaml:"match"`
	// DeviceName — имя устройства для макросов ("Sega"); пусто — не задавать.
	DeviceName string `yaml:"device_name,omitempty"`
	// Buttons и Axes — имена по кодам ядра ("BTN_TRIGGER" → "A", "ABS_THROTTLE" → "Gas").
	Buttons map[string]string `yaml:"buttons,omitempty"`
	Axes    map[string]string `yaml:"axes,omitempty"`
}

// ProfileMatch — модель устройства в профиле.
type ProfileMatch struct {
	Vid  string `yaml:"vid"`
	Pid  string `yaml:"pid"`
	Name string `yaml:"name,omitempty"`
}

// ErrProfile — файл не является профилем устройства или в нём ошибка.
var ErrProfile = errors.New("not a valid mKey device profile")

// ParseProfile читает и проверяет профиль: версия, vid и pid (4 шестнадцатеричные цифры),
// известные коды ядра, имена по правилам (без повторов).
func ParseProfile(data []byte) (*Profile, error) {
	p := &Profile{}
	if err := yaml.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProfile, err)
	}

	// Версия и модель.
	if p.Version == 0 {
		p.Version = ProfileVersion
	}
	if p.Version > ProfileVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrProfile, p.Version)
	}
	p.Match.Vid, p.Match.Pid = strings.ToLower(p.Match.Vid), strings.ToLower(p.Match.Pid)
	if !isHex4(p.Match.Vid) || !isHex4(p.Match.Pid) {
		return nil, fmt.Errorf("%w: match.vid and match.pid must be 4 hex digits (e.g. \"0079\")", ErrProfile)
	}

	// Имя устройства и имена кнопок и осей.
	if p.DeviceName != "" {
		if err := checkName(p.DeviceName); err != nil {
			return nil, fmt.Errorf("%w: device_name: %w", ErrProfile, err)
		}
	}
	seen := map[string]string{}
	for _, g := range []struct {
		typ   []uint16
		names map[string]string
	}{{[]uint16{ev.EvKey}, p.Buttons}, {[]uint16{ev.EvAbs, ev.EvRel}, p.Axes}} {
		for code, name := range g.names {
			if !slices.ContainsFunc(g.typ, func(t uint16) bool { _, ok := ev.ParseCode(t, code); return ok }) {
				return nil, fmt.Errorf("%w: unknown code %q", ErrProfile, code)
			}
			if err := checkName(name); err != nil {
				return nil, fmt.Errorf("%w: %s: %w", ErrProfile, code, err)
			}
			if other, dup := seen[strings.ToLower(name)]; dup {
				return nil, fmt.Errorf("%w: name %q is used twice (%s, %s)", ErrProfile, name, other, code)
			}
			seen[strings.ToLower(name)] = code
		}
	}
	return p, nil
}

// isHex4 сообщает, что строка — ровно 4 шестнадцатеричные цифры.
func isHex4(s string) bool {
	return len(s) == 4 && strings.Trim(s, "0123456789abcdef") == ""
}

// Matches сообщает, подходит ли профиль устройству с приметами m.
func (p *Profile) Matches(m Match) bool {
	return p.Match.Vid == m.Vid && p.Match.Pid == m.Pid && (p.Match.Name == "" || p.Match.Name == m.Name)
}

// Apply подставляет имена профиля туда, где у устройства d их ещё нет: имя устройства (если оно
// свободно в файле f) и имена кнопок и осей, которые есть в записи. Имена, данные человеком,
// не меняются; имя, которое нельзя дать (занято, совпадает с клавишей), пропускается.
// Возвращает true, если что-то изменилось.
func (p *Profile) Apply(f *File, d *Device) bool {
	// Имя устройства.
	changed := d.Name == "" && p.DeviceName != "" && f.SetDeviceName(d.AutoID, p.DeviceName) == nil

	// Имена кнопок и осей: по коду ядра — номер в записи.
	for _, g := range []struct {
		m     map[string]Control
		names map[string]string
	}{{d.Buttons, p.Buttons}, {d.Axes, p.Axes}} {
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

// ExportProfile составляет профиль из имён устройства d (для «Сохранить профиль», FR-DEV-5):
// модель из примет, имя устройства и имена кнопок и осей, данные человеком.
func ExportProfile(d *Device, title string) *Profile {
	p := &Profile{
		Version: ProfileVersion, Title: title,
		Match:      ProfileMatch{Vid: d.Match.Vid, Pid: d.Match.Pid, Name: d.Match.Name},
		DeviceName: d.Name, Buttons: map[string]string{}, Axes: map[string]string{},
	}
	for _, c := range d.Buttons {
		if c.Name != "" {
			p.Buttons[c.Code] = c.Name
		}
	}
	for _, c := range d.Axes {
		if c.Name != "" {
			p.Axes[c.Code] = c.Name
		}
	}
	return p
}

// profileHeader — пояснение в начале сохранённого профиля.
const profileHeader = `# Профиль устройства mKey: имена кнопок и осей для этой модели (по кодам ядра).
# Положите файл в папку профилей (mkey paths), и такое же устройство получит эти имена само.
`

// MarshalProfile записывает профиль в YAML с пояснением в начале.
func MarshalProfile(p *Profile) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(profileHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
