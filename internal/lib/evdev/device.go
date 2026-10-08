package evdev

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ID — идентификатор устройства (struct input_id).
type ID struct {
	// Bustype — тип шины (BUS_USB = 0x03, BUS_BLUETOOTH = 0x05, BUS_VIRTUAL = 0x06…).
	Bustype uint16 `json:"bustype"`
	// Vendor — идентификатор производителя (VID).
	Vendor uint16 `json:"vendor"`
	// Product — идентификатор модели (PID).
	Product uint16 `json:"product"`
	// Version — версия устройства.
	Version uint16 `json:"version"`
}

// String возвращает идентификатор в виде "vid:pid", например "046d:c52b".
func (id ID) String() string {
	return fmt.Sprintf("%04x:%04x", id.Vendor, id.Product)
}

// AbsInfo — параметры абсолютной оси (struct input_absinfo).
type AbsInfo struct {
	// Value — текущее значение.
	Value int32 `json:"value"`
	// Minimum — минимальное значение.
	Minimum int32 `json:"min"`
	// Maximum — максимальное значение.
	Maximum int32 `json:"max"`
	// Fuzz — порог фильтрации шума.
	Fuzz int32 `json:"fuzz"`
	// Flat — мёртвая зона около центра.
	Flat int32 `json:"flat"`
	// Resolution — разрешение (единиц на мм или на радиан).
	Resolution int32 `json:"resolution"`
}

// Capabilities — возможности устройства: какие типы событий и коды оно умеет порождать.
type Capabilities struct {
	// Codes — поддерживаемые коды по типам событий (EvKey → [KeyA, KeyB…]).
	// Тип без кодов (например, EvSyn) присутствует с пустым списком.
	Codes map[uint16][]uint16 `json:"codes"`
	// Abs — параметры абсолютных осей.
	Abs map[uint16]AbsInfo `json:"abs,omitempty"`
	// Props — свойства устройства (INPUT_PROP_DIRECT, INPUT_PROP_POINTER…).
	Props []uint16 `json:"props,omitempty"`
}

// Has сообщает, поддерживает ли устройство код code события типа typ.
func (c Capabilities) Has(typ, code uint16) bool {
	return slices.Contains(c.Codes[typ], code)
}

// HasType сообщает, поддерживает ли устройство события типа typ.
func (c Capabilities) HasType(typ uint16) bool {
	_, ok := c.Codes[typ]
	return ok
}

// HasProp сообщает, есть ли у устройства свойство prop.
func (c Capabilities) HasProp(prop uint16) bool {
	return slices.Contains(c.Props, prop)
}

// Info — сведения об устройстве, прочитанные при открытии.
type Info struct {
	// Path — путь к файлу устройства, например "/dev/input/event3".
	Path string `json:"path"`
	// Name — имя устройства, которое сообщает драйвер.
	Name string `json:"name"`
	// Phys — физический путь (порт USB и т.п.); может быть пустым.
	Phys string `json:"phys,omitempty"`
	// Uniq — уникальный идентификатор (серийный номер); часто пустой.
	Uniq string `json:"uniq,omitempty"`
	// ID — шина, производитель, модель, версия.
	ID ID `json:"id"`
	// DriverVersion — версия протокола evdev драйвера.
	DriverVersion int32 `json:"driver_version"`
	// Caps — возможности устройства.
	Caps Capabilities `json:"caps"`
}

// ffMax — FF_MAX из linux/input.h: коды force feedback описаны не в input-event-codes.h,
// поэтому генератор их не видит.
const ffMax = 0x7f

// maxCodes — число кодов в каждом типе событий (X_MAX + 1) для чтения масок EVIOCGBIT.
//
// EV_REP (автоповтор клавиатуры) здесь намеренно нет: ядро не поддерживает EVIOCGBIT для
// этого типа и отвечает EINVAL (параметры повтора читаются другим ioctl — EVIOCGREP).
// Из-за такого запроса раньше не открывались все настоящие клавиатуры.
var maxCodes = map[uint16]int{
	EvKey: KeyMax + 1, EvRel: RelMax + 1, EvAbs: AbsMax + 1, EvMsc: MscMax + 1,
	EvSw: SwMax + 1, EvLed: LedMax + 1, EvSnd: SndMax + 1, EvFf: ffMax + 1,
}

// Device — открытое физическое устройство ввода.
type Device struct {
	// file — открытый файл устройства; чтение идёт через поллер Go, поэтому Close прерывает Read.
	file *os.File
	// info — сведения, прочитанные при открытии.
	info Info
	// raw — байты событий для ReadEvents, один буфер на устройство: чтение идёт на каждое
	// событие (у мыши — тысячи раз в секунду), а новый буфер на каждое чтение нагружал бы
	// сборщик мусора.
	raw []byte
}

// Open открывает устройство по пути (например, "/dev/input/event3") и читает его сведения.
// Ошибка os.ErrPermission означает, что у пользователя нет доступа к устройствам.
func Open(path string) (*Device, error) {
	// Открываем только на чтение: записывать в физические устройства mKey не нужно.
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	d := &Device{file: f}

	// Читаем сведения об устройстве; при ошибке закрываем файл.
	info, err := d.readInfo(path)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("evdev: read info %s: %w", path, err)
	}
	d.info = info
	return d, nil
}

// Info возвращает сведения об устройстве.
func (d *Device) Info() Info { return d.info }

// Path возвращает путь к файлу устройства.
func (d *Device) Path() string { return d.info.Path }

// Close закрывает устройство (и снимает grab, если он был: ядро делает это при закрытии).
func (d *Device) Close() error { return d.file.Close() }

// ReadEvents блокирующе читает события в буфер buf и возвращает прочитанные.
// Возвращает ошибку os.ErrClosed после Close и syscall.ENODEV, если устройство отключили.
// Читает одна горутина на устройство: одновременные вызовы для одного устройства не допускаются.
func (d *Device) ReadEvents(buf []Event) ([]Event, error) {
	// Читаем столько целых событий, сколько поместится в buf (буфер байтов — от прошлых чтений).
	if size := len(buf) * EventSize; cap(d.raw) < size {
		d.raw = make([]byte, size)
	}
	raw := d.raw[:len(buf)*EventSize]
	n, err := d.file.Read(raw)
	if err != nil {
		return nil, err
	}

	// Разбираем прочитанные байты; ядро всегда отдаёт целое число событий.
	count := n / EventSize
	for i := range count {
		if err := buf[i].UnmarshalBinary(raw[i*EventSize:]); err != nil {
			return nil, err
		}
	}
	return buf[:count], nil
}

// Grab захватывает устройство эксклюзивно (EVIOCGRAB): его события получает только mKey.
// Обязательно используйте вместе с экстренной остановкой (SEC-1) — иначе пользователь
// может остаться без клавиатуры.
func (d *Device) Grab() error {
	return d.control(func(fd int) error { return ioctlInt(fd, eviocgrab, 1) })
}

// Ungrab снимает эксклюзивный захват.
func (d *Device) Ungrab() error {
	return d.control(func(fd int) error { return ioctlInt(fd, eviocgrab, 0) })
}

// PressedKeys возвращает коды клавиш и кнопок, зажатых на устройстве прямо сейчас (EVIOCGKEY).
func (d *Device) PressedKeys() ([]uint16, error) {
	var codes []uint16
	err := d.control(func(fd int) error {
		var err error
		codes, err = ioctlBits(fd, eviocgkey, KeyMax+1)
		return err
	})
	return codes, err
}

// control выполняет f с числовым дескриптором файла, не переводя файл в блокирующий режим
// (в отличие от os.File.Fd, который отключил бы прерывание Read через Close).
func (d *Device) control(f func(fd int) error) error {
	rc, err := d.file.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = f(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

// readInfo читает имя, идентификаторы и возможности устройства через ioctl.
func (d *Device) readInfo(path string) (Info, error) {
	info := Info{Path: path}
	err := d.control(func(fd int) error {
		// Строковые поля: имя обязательно, phys и uniq могут отсутствовать.
		var err error
		if info.Name, err = ioctlString(fd, eviocgname); err != nil {
			return fmt.Errorf("name: %w", err)
		}
		if info.Phys, err = ioctlString(fd, eviocgphys); err != nil {
			return fmt.Errorf("phys: %w", err)
		}
		if info.Uniq, err = ioctlString(fd, eviocguniq); err != nil {
			return fmt.Errorf("uniq: %w", err)
		}

		// Идентификатор устройства и версия протокола драйвера.
		var id [4]uint16
		if err := ioctlPtr(fd, eviocgid, unsafe.Pointer(&id)); err != nil {
			return fmt.Errorf("id: %w", err)
		}
		info.ID = ID{Bustype: id[0], Vendor: id[1], Product: id[2], Version: id[3]}
		if err := ioctlPtr(fd, eviocgversion, unsafe.Pointer(&info.DriverVersion)); err != nil {
			return fmt.Errorf("version: %w", err)
		}

		// Возможности устройства.
		info.Caps, err = readCaps(fd)
		return err
	})
	return info, err
}

// readCaps читает поддерживаемые типы событий, коды, параметры осей и свойства устройства.
func readCaps(fd int) (Capabilities, error) {
	caps := Capabilities{Codes: map[uint16][]uint16{}, Abs: map[uint16]AbsInfo{}}

	// Какие типы событий поддерживает устройство (EVIOCGBIT с ev = 0).
	types, err := ioctlBits(fd, func(size uintptr) uintptr { return eviocgbit(0, size) }, EvMax+1)
	if err != nil {
		return caps, fmt.Errorf("event types: %w", err)
	}

	// Для каждого типа — список кодов.
	for _, t := range types {
		caps.Codes[t] = nil
		limit, ok := maxCodes[t]
		if !ok {
			continue
		}
		codes, err := ioctlBits(fd, func(size uintptr) uintptr { return eviocgbit(uintptr(t), size) }, limit)
		if err != nil {
			return caps, fmt.Errorf("codes of %s: %w", TypeName(t), err)
		}
		caps.Codes[t] = codes
	}

	// Параметры каждой абсолютной оси.
	for _, code := range caps.Codes[EvAbs] {
		var raw [6]int32
		if err := ioctlPtr(fd, eviocgabs(uintptr(code)), unsafe.Pointer(&raw)); err != nil {
			return caps, fmt.Errorf("abs %s: %w", CodeName(EvAbs, code), err)
		}
		caps.Abs[code] = AbsInfo{Value: raw[0], Minimum: raw[1], Maximum: raw[2], Fuzz: raw[3], Flat: raw[4], Resolution: raw[5]}
	}

	// Свойства устройства; старые ядра могут не поддерживать EVIOCGPROP — это не ошибка.
	props, err := ioctlBits(fd, eviocgprop, InputPropMax+1)
	if err != nil && !errors.Is(err, unix.EINVAL) {
		return caps, fmt.Errorf("props: %w", err)
	}
	caps.Props = props
	return caps, nil
}

// UploadRumble загружает в устройство эффект вибрации FF_RUMBLE (как это делают игры через
// EVIOCSFF): strong и weak — сила тяжёлого и лёгкого моторов (0…65535), length — длительность.
// Возвращает номер эффекта для EraseEffect. Для виртуального устройства запрос получает его
// создатель (UInput с вибрацией подтверждает его, FR-VD-5).
func (d *Device) UploadRumble(strong, weak uint16, length time.Duration) (int16, error) {
	// struct ff_effect: type, id (-1 — новый), direction, trigger, replay{length, delay}, rumble.
	buf := make([]byte, sizeofFFEffect)
	binary.LittleEndian.PutUint16(buf[0:2], 0x50) // FF_RUMBLE
	binary.LittleEndian.PutUint16(buf[2:4], 0xffff)
	binary.LittleEndian.PutUint16(buf[10:12], uint16(length.Milliseconds()))
	binary.LittleEndian.PutUint16(buf[16:18], strong)
	binary.LittleEndian.PutUint16(buf[18:20], weak)

	// Загрузка: ядро записывает номер эффекта в поле id.
	rc, err := d.file.SyscallConn()
	if err != nil {
		return 0, err
	}
	var ierr error
	if err := rc.Control(func(fd uintptr) { ierr = ioctlBuf(int(fd), eviocsff, buf) }); err != nil {
		return 0, err
	}
	if ierr != nil {
		return 0, fmt.Errorf("evdev: upload rumble: %w", ierr)
	}
	return int16(binary.LittleEndian.Uint16(buf[2:4])), nil
}

// EraseEffect удаляет загруженный эффект вибрации (EVIOCRMFF).
func (d *Device) EraseEffect(id int16) error {
	rc, err := d.file.SyscallConn()
	if err != nil {
		return err
	}
	var ierr error
	if err := rc.Control(func(fd uintptr) { ierr = ioctlInt(int(fd), eviocrmff, uintptr(id)) }); err != nil {
		return err
	}
	if ierr != nil {
		return fmt.Errorf("evdev: erase effect: %w", ierr)
	}
	return nil
}
