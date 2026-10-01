// Package devmap — авто-ID устройств и кнопок, у которых нет стандартного имени mKey
// (FR-DEV-2, FR-DEV-6), и файл devices.yaml, где они хранятся.
//
// Устройство, у которого есть кнопки без имени, получает имя UnKey (затем UnKey2, UnKey3…),
// его кнопки без имени — номера 001, 002… по возрастанию кодов ядра, оси без имени — Axis01…
// (абсолютные) и Rel01… (относительные). Кнопки с обычным именем ({A}, {Mouse0}) номеров не
// получают. Какие устройства получают имя, задаёт режим (Mode): по умолчанию — все, кроме
// служебных (кнопки питания и сна, видео), можно — все или только необычные устройства.
//
// Имя присваивается один раз и хранится в devices.yaml вместе с приметами устройства (Match):
// после переподключения и перезагрузки устройство узнаётся и остаётся тем же UnKey2.
// Номера кнопок записаны в файле и дальше не меняются; новые кнопки получают следующие номера.
//
// Пакет — чистая библиотека: файл читает и пишет вызывающий (модуль inspector).
package devmap

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"

	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
)

// FileName — имя файла с авто-ID в папке настроек.
const FileName = "devices.yaml"

// Version — версия формата devices.yaml.
const Version = 1

// AutoPrefix — начало авто-ID устройства: UnKey, UnKey2, UnKey3 и так далее.
const AutoPrefix = "UnKey"

// Mode — каким устройствам давать авто-ID (настройка modules.inspector.auto_ids).
type Mode string

const (
	// ModeSmart — всем устройствам с кнопками или осями без имени, кроме служебных
	// (кнопки питания и сна, видео, радио; приёмники только с осью ABS_MISC): по умолчанию.
	ModeSmart Mode = "smart"
	// ModeAll — всем устройствам с кнопками или осями без имени, включая служебные.
	ModeAll Mode = "all"
	// ModeUnusual — только необычным устройствам: не клавиатуре, не мыши, не геймпаду и не
	// сенсорным (служебные тоже не получают).
	ModeUnusual Mode = "unusual"
)

// ErrMode — неизвестный режим авто-ID.
var ErrMode = errors.New("unknown auto-id mode")

// ParseMode разбирает режим; пустая строка — ModeSmart.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.TrimSpace(s)); m {
	case "":
		return ModeSmart, nil
	case ModeSmart, ModeAll, ModeUnusual:
		return m, nil
	}
	return "", fmt.Errorf("%w %q (smart, all, unusual)", ErrMode, s)
}

// File — содержимое devices.yaml.
type File struct {
	Version int `yaml:"version"`
	// Devices — указатели: ссылки на устройства (из Add, Find, Lookup) не устаревают при добавлении новых.
	Devices []*Device `yaml:"devices"`
}

// Device — устройство с авто-ID.
type Device struct {
	// AutoID — авто-ID (UnKey, UnKey2…); остаётся рабочим именем навсегда.
	AutoID string `yaml:"auto_id"`
	// Name — имя, заданное человеком (переименование — T7.3); пусто — только авто-ID.
	Name string `yaml:"name,omitempty"`
	// Match — приметы, по которым устройство узнаётся после переподключения.
	Match Match `yaml:"match"`
	// Buttons — кнопки без стандартного имени: номер ("001") → код и имя.
	Buttons map[string]Control `yaml:"buttons,omitempty"`
	// Axes — оси без стандартного имени: "Axis01" (абсолютные), "Rel01" (относительные) → код и имя.
	Axes map[string]Control `yaml:"axes,omitempty"`
	// Applied — проекты, имена кнопок из которых уже подставлены (ADR-0027): повторно они не
	// применяются, поэтому убранное человеком имя не возвращается.
	Applied []string `yaml:"applied,omitempty"`
}

// Match — приметы устройства (FR-DEV-6): модель и название; серийный номер, если есть;
// порт — чтобы различать одинаковые устройства без серийного номера.
type Match struct {
	Vid     string `yaml:"vid"`
	Pid     string `yaml:"pid"`
	Version string `yaml:"version,omitempty"`
	Name    string `yaml:"name"`
	Uniq    string `yaml:"uniq,omitempty"`
	ByPath  string `yaml:"by_path,omitempty"`
}

// Control — кнопка или ось: код ядра ("KEY_CALC") и имя, заданное человеком.
type Control struct {
	Code string `yaml:"code"`
	Name string `yaml:"name,omitempty"`
}

// fileHeader — пояснение в начале devices.yaml.
const fileHeader = `# Устройства mKey: автоматические имена (UnKey, UnKey2…) для устройств, у которых есть кнопки
# без стандартного имени, и номера их кнопок (001, 002…) и осей (Axis01, Rel01…).
# В макросах: {UnKey001} — кнопка 001 устройства UnKey, {UnKey2.001} — устройства UnKey2.
# Файл ведёт mKey; имена, которые здесь есть, работают всегда (и после переименования).
`

// Виды ошибок в имени (NameError.Code): по ним API пишет понятное сообщение.
const (
	// NameChars — имя не по правилам: буквы, цифры и «_», начинается с буквы.
	NameChars = "chars"
	// NameKey — имя совпадает со стандартным именем клавиши ({Enter} — это клавиша, а не устройство).
	NameKey = "key"
	// NameAuto — имя похоже на авто-ID (UnKey5): его легко спутать с другим устройством.
	NameAuto = "auto"
	// NameTaken — имя уже занято другим устройством или другой кнопкой этого устройства (Other).
	NameTaken = "taken"
	// NameUnknown — нет такого устройства или такой кнопки (Name).
	NameUnknown = "unknown"
	// NameAmbiguous — под описание подходит несколько устройств (Other — их список).
	NameAmbiguous = "ambiguous"
)

// NameError — имя нельзя дать: Code — вид ошибки, Name — имя, Other — кем занято.
type NameError struct {
	Code  string
	Name  string
	Other string
}

// Error описывает ошибку по-английски (для журнала).
func (e *NameError) Error() string {
	msg := "name " + strconv.Quote(e.Name) + ": " + e.Code
	if e.Other != "" {
		msg += " (" + e.Other + ")"
	}
	return msg
}

// autoLikeRe — имена, похожие на авто-ID: UnKey, UnKey2… (без учёта регистра).
var autoLikeRe = regexp.MustCompile(`(?i)^unkey[0-9]*$`)

// checkName проверяет имя по правилам (FR-DEV-3): буквы (любого алфавита), цифры и «_»,
// начинается с буквы.
func checkName(name string) error {
	for i, r := range name {
		ok := unicode.IsLetter(r) || r == '_' || (i > 0 && unicode.IsDigit(r))
		if !ok || (i == 0 && r == '_') {
			return &NameError{Code: NameChars, Name: name}
		}
	}
	if name == "" {
		return &NameError{Code: NameChars, Name: name}
	}
	return nil
}

// SetDeviceName даёт устройству autoID имя name ("" — убрать имя; авто-ID работает всегда).
// Имя не должно совпадать со стандартной клавишей, походить на авто-ID и быть занятым другим
// устройством (без учёта регистра). Ошибка — *NameError.
func (f *File) SetDeviceName(autoID, name string) error {
	d := f.Lookup(autoID)
	if d == nil {
		return &NameError{Code: NameUnknown, Name: autoID}
	}
	if name == "" {
		d.Name = ""
		return nil
	}

	// Правила имени.
	if err := checkName(name); err != nil {
		return err
	}
	if _, ok := keys.Lookup(name); ok {
		return &NameError{Code: NameKey, Name: name}
	}
	if autoLikeRe.MatchString(name) {
		return &NameError{Code: NameAuto, Name: name}
	}

	// Не занято другим устройством (ни именем, ни авто-ID).
	if other := f.Lookup(name); other != nil && other != d {
		return &NameError{Code: NameTaken, Name: name, Other: other.AutoID}
	}
	d.Name = name
	return nil
}

// SetButtonName даёт кнопке или оси устройства имя name ("" — убрать имя; номер работает всегда).
// control — номер ("001", "Axis01") или текущее имя. Имя уникально в пределах устройства.
// Ошибка — *NameError.
func (d *Device) SetButtonName(control, name string) error {
	// Кнопка или ось — по номеру или имени.
	m, key := d.findControl(control)
	if m == nil {
		return &NameError{Code: NameUnknown, Name: control}
	}
	c := m[key]
	if name == "" {
		c.Name = ""
		m[key] = c
		return nil
	}

	// Правила имени и уникальность в устройстве.
	if err := checkName(name); err != nil {
		return err
	}
	for _, mm := range []map[string]Control{d.Buttons, d.Axes} {
		for k, other := range mm {
			if k != key && strings.EqualFold(other.Name, name) {
				return &NameError{Code: NameTaken, Name: name, Other: k}
			}
		}
	}
	c.Name = name
	m[key] = c
	return nil
}

// findControl ищет кнопку или ось по номеру или имени (без учёта регистра): карта и ключ в ней.
func (d *Device) findControl(control string) (map[string]Control, string) {
	for _, m := range []map[string]Control{d.Buttons, d.Axes} {
		for k, c := range m {
			if strings.EqualFold(k, control) || (c.Name != "" && strings.EqualFold(c.Name, control)) {
				return m, k
			}
		}
	}
	return nil, ""
}

// Display — имя устройства для показа и макросов: имя человека, иначе авто-ID.
func (d *Device) Display() string {
	if d.Name != "" {
		return d.Name
	}
	return d.AutoID
}

// ControlDisplay — имя кнопки или оси key для показа: имя человека, иначе номер.
func (d *Device) ControlDisplay(key string) string {
	for _, m := range []map[string]Control{d.Buttons, d.Axes} {
		if c, ok := m[key]; ok && c.Name != "" {
			return c.Name
		}
	}
	return key
}

// Ref — имя кнопки или оси для макросов (без фигурных скобок): у первого устройства (UnKey)
// кнопка пишется слитно — UnKey001; у остальных и у осей — через точку: UnKey2.001, UnKey.Axis01.
func Ref(device, control string) string {
	if strings.EqualFold(device, AutoPrefix) && len(control) == 3 && strings.Trim(control, "0123456789") == "" {
		return device + control
	}
	return device + "." + control
}

// joinedRe — слитная запись кнопки авто-ID-устройства: «UnKey» (без учёта регистра), необязательный
// номер устройства и ровно три цифры номера кнопки в конце: UnKey001, UnKey2001, UnKey12001.
var joinedRe = regexp.MustCompile(`(?i)^(unkey[0-9]*)([0-9]{3})$`)

// SplitJoined разбирает слитную запись кнопки устройства (FR-DEV-2): "UnKey001" → ("UnKey", "001"),
// "UnKey2001" → ("UnKey2", "001") — последние три цифры всегда номер кнопки. Имя устройства
// приводится к виду «UnKey…». false — это не слитная запись.
func SplitJoined(name string) (device, button string, ok bool) {
	m := joinedRe.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	return AutoPrefix + m[1][len(AutoPrefix):], m[2], true
}

// Label возвращает номер кнопки ("001") или оси ("Axis01", "Rel01") по коду; "" — номера нет.
func (d *Device) Label(typ, code uint16) string {
	name := ev.CodeName(typ, code)
	m, prefix := d.Buttons, ""
	switch typ {
	case ev.EvKey:
	case ev.EvAbs:
		m, prefix = d.Axes, "Axis"
	case ev.EvRel:
		m, prefix = d.Axes, "Rel"
	default:
		return ""
	}
	for k, c := range m {
		if c.Code == name && strings.HasPrefix(k, prefix) {
			return k
		}
	}
	return ""
}

// Load читает devices.yaml; файла нет — пустой список.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{Version: Version}, nil
	}
	if err != nil {
		return nil, err
	}
	f := &File{}
	if err := yaml.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if f.Version == 0 {
		f.Version = Version
	}
	if f.Version > Version {
		return nil, fmt.Errorf("%s: unsupported version %d", filepath.Base(path), f.Version)
	}
	return f, nil
}

// Save записывает devices.yaml атомарно (во временный файл и переименованием), с отступом
// в два пробела, как остальные файлы mKey.
func Save(path string, f *File) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	data := buf.Bytes()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(fileHeader), data...), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// MatchOf составляет приметы устройства по его сведениям и постоянным именам.
func MatchOf(info ev.Info, links ev.Links) Match {
	return Match{
		Vid: fmt.Sprintf("%04x", info.ID.Vendor), Pid: fmt.Sprintf("%04x", info.ID.Product),
		Version: fmt.Sprintf("%04x", info.ID.Version), Name: info.Name, Uniq: info.Uniq, ByPath: links.ByPath,
	}
}

// Find ищет в файле устройство с приметами m (FR-DEV-6). busy — авто-ID устройств, уже
// узнанных среди подключённых: два одинаковых устройства не получат одну запись.
// Порядок: серийный номер (если есть); иначе модель и название — при нескольких таких
// записях выбирается та, что с тем же портом, а если порт сменился — единственная свободная.
func (f *File) Find(m Match, busy map[string]bool) *Device {
	// Кандидаты: та же модель, версия и название, запись не занята другим устройством.
	var cands []*Device
	for _, d := range f.Devices {
		dm := d.Match
		if dm.Vid == m.Vid && dm.Pid == m.Pid && dm.Name == m.Name && (dm.Version == "" || dm.Version == m.Version) && !busy[d.AutoID] {
			cands = append(cands, d)
		}
	}

	// Серийный номер решает сразу.
	if m.Uniq != "" {
		for _, d := range cands {
			if d.Match.Uniq == m.Uniq {
				return d
			}
		}
		return nil
	}

	// Без серийного номера: тот же порт, иначе — единственная свободная запись без серийного номера.
	var noUniq []*Device
	for _, d := range cands {
		if d.Match.Uniq != "" {
			continue
		}
		if m.ByPath != "" && d.Match.ByPath == m.ByPath {
			return d
		}
		noUniq = append(noUniq, d)
	}
	if len(noUniq) == 1 {
		return noUniq[0]
	}
	return nil
}

// Lookup ищет устройство по авто-ID или имени (без учёта регистра).
func (f *File) Lookup(name string) *Device {
	for _, d := range f.Devices {
		if strings.EqualFold(d.AutoID, name) || (d.Name != "" && strings.EqualFold(d.Name, name)) {
			return d
		}
	}
	return nil
}

// Add добавляет новое устройство с очередным авто-ID и номерами его кнопок и осей.
func (f *File) Add(info ev.Info, kinds []ev.Kind, links ev.Links) *Device {
	d := &Device{AutoID: f.nextAutoID(), Match: MatchOf(info, links)}
	f.Devices = append(f.Devices, d)
	d.Update(info, kinds)
	return d
}

// nextAutoID возвращает первый свободный авто-ID: UnKey, затем UnKey2, UnKey3 и так далее.
func (f *File) nextAutoID() string {
	for n := 1; ; n++ {
		id := AutoPrefix
		if n > 1 {
			id += strconv.Itoa(n)
		}
		if f.Lookup(id) == nil {
			return id
		}
	}
}

// Update дописывает номера кнопкам и осям устройства, у которых их ещё нет (новые возможности
// после обновления прошивки); уже выданные номера не меняются. Возвращает true, если что-то
// добавилось.
func (d *Device) Update(info ev.Info, kinds []ev.Kind) bool {
	keyCodes, absCodes, relCodes := Unnamed(info.Caps, kinds)
	changed := false
	if d.Buttons == nil {
		d.Buttons = map[string]Control{}
	}
	if d.Axes == nil {
		d.Axes = map[string]Control{}
	}
	changed = number(d.Buttons, ev.EvKey, keyCodes, "%03d") || changed
	changed = number(d.Axes, ev.EvAbs, absCodes, "Axis%02d") || changed
	changed = number(d.Axes, ev.EvRel, relCodes, "Rel%02d") || changed
	return changed
}

// number выдаёт номера по образцу format (с 1) кодам типа typ, у которых номера ещё нет:
// следующий номер — после наибольшего уже выданного по этому образцу.
func number(m map[string]Control, typ uint16, codes []uint16, format string) bool {
	// Уже выданные коды и наибольший номер этого образца.
	have := map[string]bool{}
	last := 0
	for k, c := range m {
		var n int
		if _, err := fmt.Sscanf(k, format, &n); err == nil && fmt.Sprintf(format, n) == k {
			have[c.Code] = true
			last = max(last, n)
		}
	}

	// Новые коды — по возрастанию, следующими номерами.
	changed := false
	for _, code := range codes {
		name := ev.CodeName(typ, code)
		if have[name] {
			continue
		}
		last++
		m[fmt.Sprintf(format, last)] = Control{Code: name}
		changed = true
	}
	return changed
}

// Qualifies сообщает, нужно ли давать устройству авто-ID в режиме mode: у него есть кнопки
// или оси без имени, и оно подходит под режим.
func Qualifies(mode Mode, caps ev.Capabilities, kinds []ev.Kind) bool {
	keyCodes, absCodes, relCodes := Unnamed(caps, kinds)
	if len(keyCodes)+len(absCodes)+len(relCodes) == 0 {
		return false
	}
	if mode == ModeAll {
		return true
	}

	// Служебное устройство: все кнопки без имени — системные, а из осей без имени есть только
	// ABS_MISC (её показывают многие беспроводные приёмники, хотя ничего по ней не шлют).
	service := len(relCodes) == 0
	for _, c := range keyCodes {
		service = service && systemKeys[c]
	}
	for _, c := range absCodes {
		service = service && c == ev.AbsMisc
	}
	if service {
		return false
	}

	// «Необычные»: не клавиатура, не мышь, не геймпад и не сенсорное устройство.
	if mode == ModeUnusual {
		for _, k := range kinds {
			if usualKinds[k] {
				return false
			}
		}
	}
	return true
}

// Unnamed возвращает коды без стандартного имени mKey: кнопки (без признаков касания и
// инструмента — это не кнопки), абсолютные оси (кроме сенсорных устройств: их оси — касания)
// и относительные (кроме движения и колёс). Коды — по возрастанию.
func Unnamed(caps ev.Capabilities, kinds []ev.Kind) (keyCodes, absCodes, relCodes []uint16) {
	// Кнопки и клавиши.
	for _, c := range caps.Codes[ev.EvKey] {
		if _, ok := keys.NameOf(c); !ok && !toolKeys[c] {
			keyCodes = append(keyCodes, c)
		}
	}

	// Абсолютные оси — только у несенсорных устройств.
	touch := slices.ContainsFunc(kinds, func(k ev.Kind) bool {
		return k == ev.KindTouchpad || k == ev.KindTouchscreen || k == ev.KindTablet
	})
	if !touch {
		for _, c := range caps.Codes[ev.EvAbs] {
			if _, ok := keys.AxisNameOf(c); !ok {
				absCodes = append(absCodes, c)
			}
		}
	}

	// Относительные оси, кроме движения и колёс.
	for _, c := range caps.Codes[ev.EvRel] {
		if !standardRel[c] {
			relCodes = append(relCodes, c)
		}
	}
	slices.Sort(keyCodes)
	slices.Sort(absCodes)
	slices.Sort(relCodes)
	return keyCodes, absCodes, relCodes
}

// toolKeys — признаки касания и инструмента (BTN_TOOL_*, BTN_TOUCH): их шлют тачпады и
// планшеты, нажать их как кнопку нельзя — номеров они не получают.
var toolKeys = map[uint16]bool{
	ev.BtnToolPen: true, ev.BtnToolRubber: true, ev.BtnToolBrush: true, ev.BtnToolPencil: true,
	ev.BtnToolAirbrush: true, ev.BtnToolFinger: true, ev.BtnToolMouse: true, ev.BtnToolLens: true,
	ev.BtnToolQuinttap: true, ev.BtnTouch: true, ev.BtnToolDoubletap: true, ev.BtnToolTripletap: true,
	ev.BtnToolQuadtap: true,
}

// standardRel — движение мыши и колёса: они пишутся в макросах словами (move, wheel).
var standardRel = map[uint16]bool{
	ev.RelX: true, ev.RelY: true, ev.RelWheel: true, ev.RelHwheel: true, ev.RelWheelHiRes: true, ev.RelHwheelHiRes: true,
}

// systemKeys — служебные клавиши: питание, сон, яркость и видео, радио. Устройство, у которого
// без имени только они (кнопка питания, «Video Bus»), в режиме smart авто-ID не получает.
var systemKeys = map[uint16]bool{
	// Питание и сон.
	ev.KeyPower: true, ev.KeyPower2: true, ev.KeySleep: true, ev.KeyWakeup: true, ev.KeySuspend: true,
	// Яркость и видеовыход.
	ev.KeyBrightnessdown: true, ev.KeyBrightnessup: true, ev.KeyBrightnessCycle: true, ev.KeyBrightnessAuto: true,
	ev.KeyBrightnessToggle: true, ev.KeyBrightnessMin: true, ev.KeyBrightnessMax: true, ev.KeyBrightnessMenu: true,
	ev.KeySwitchvideomode: true, ev.KeyVideoNext: true, ev.KeyVideoPrev: true, ev.KeyDisplayOff: true,
	// Радио, батарея и «неизвестная» клавиша, которую шлют некоторые прошивки.
	ev.KeyWlan: true, ev.KeyWwan: true, ev.KeyBluetooth: true, ev.KeyRfkill: true, ev.KeyBattery: true, ev.KeyUnknown: true,
}

// usualKinds — обычные устройства (для режима ModeUnusual).
var usualKinds = map[ev.Kind]bool{
	ev.KindKeyboard: true, ev.KindMouse: true, ev.KindGamepad: true,
	ev.KindTouchpad: true, ev.KindTouchscreen: true, ev.KindTablet: true,
}
