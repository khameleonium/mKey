package evdev

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unsafe"
)

// DefaultUInputPath — стандартный путь к интерфейсу uinput.
const DefaultUInputPath = "/dev/uinput"

// BusVirtual — BUS_VIRTUAL из linux/input.h: тип шины для виртуальных устройств.
const BusVirtual = 0x06

// BusUSB — BUS_USB: тип шины, с которым игры (SDL, Steam) узнают геймпад по VID:PID.
const BusUSB = 0x03

// Setup — описание создаваемого виртуального устройства.
type Setup struct {
	// Name — имя устройства в системе (до 79 байт). У устройств mKey начинается с "mKey ".
	Name string
	// Phys — физический путь; у устройств mKey начинается с "mkey/" (так mKey узнаёт свои устройства).
	Phys string
	// ID — шина, производитель, модель, версия. Для имитации геймпадов важны VID:PID.
	ID ID
	// Keys — коды клавиш и кнопок (EV_KEY).
	Keys []uint16
	// Rels — относительные оси (EV_REL).
	Rels []uint16
	// Abs — абсолютные оси (EV_ABS) и их параметры.
	Abs map[uint16]AbsInfo
	// Msc — коды EV_MSC (например, MSC_SCAN).
	Msc []uint16
	// Props — свойства устройства (INPUT_PROP_DIRECT для тач-экрана и т.п.).
	Props []uint16
	// FF — виды вибрации (EV_FF: FF_RUMBLE…), которые устройство объявляет играм. Запросы вибрации
	// принимаются и подтверждаются, но никуда не передаются (FR-VD-5): так игры не ждут ответа и
	// не ломаются. FFEffectsMax — сколько эффектов можно загрузить разом (0 — 16).
	FF           []uint16
	FFEffectsMax uint32
}

// EvUinput — тип служебных событий uinput: запросы вибрации к создателю устройства.
const EvUinput = 0x0101

// Коды служебных событий uinput: загрузить и удалить эффект вибрации (value — номер запроса).
const (
	uiFFUpload = 1
	uiFFErase  = 2
)

// UInput — созданное виртуальное устройство.
type UInput struct {
	// file — открытый /dev/uinput, через который отправляются события.
	file *os.File
	// setup — описание, с которым устройство создано.
	setup Setup
	// sysname — имя устройства в sysfs, например "input42".
	sysname string
	// ffDone закрывается, когда обработчик запросов вибрации завершился (nil — вибрации нет).
	ffDone chan struct{}
}

// CreateUInput создаёт виртуальное устройство через интерфейс uinput по пути path
// (обычно DefaultUInputPath). Устройство существует, пока открыт UInput: Close
// или завершение процесса (в том числе аварийное) удаляют его из системы.
func CreateUInput(path string, s Setup) (*UInput, error) {
	// Проверяем описание до обращения к ядру.
	if s.Name == "" || len(s.Name) >= uinputMaxNameSize {
		return nil, fmt.Errorf("uinput: name must be 1..%d bytes", uinputMaxNameSize-1)
	}

	// Открываем uinput на запись (с вибрацией — и на чтение: запросы приходят событиями).
	mode := os.O_WRONLY
	if len(s.FF) > 0 {
		mode = os.O_RDWR
		if s.FFEffectsMax == 0 {
			s.FFEffectsMax = 16
		}
	}
	f, err := os.OpenFile(path, mode, 0)
	if err != nil {
		return nil, err
	}
	u := &UInput{file: f, setup: s}

	// Настраиваем и создаём устройство; при любой ошибке закрываем файл.
	if err := u.create(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("uinput: create %q: %w", s.Name, err)
	}

	// Вибрация: отвечаем на запросы, пока устройство живо.
	if len(s.FF) > 0 {
		u.ffDone = make(chan struct{})
		go u.serveFF()
	}
	return u, nil
}

// serveFF принимает запросы вибрации к устройству и подтверждает их (FR-VD-5): ядро ждёт ответа
// на загрузку и удаление эффекта, и без него игра зависла бы на время ожидания. Сами эффекты
// никуда не передаются. Завершается, когда устройство закрыто (чтение возвращает ошибку).
func (u *UInput) serveFF() {
	defer close(u.ffDone)
	buf := make([]byte, EventSize*16)
	for {
		n, err := u.file.Read(buf)
		if err != nil {
			return
		}
		for off := 0; off+EventSize <= n; off += EventSize {
			var e Event
			if e.UnmarshalBinary(buf[off:off+EventSize]) != nil || e.Type != EvUinput {
				continue
			}
			// Остальные события (EV_FF «включить эффект») просто пропускаются.
			switch e.Code {
			case uiFFUpload:
				u.answerFF(uiBeginFFUpload, uiEndFFUpload, sizeofFFUpload, uint32(e.Value))
			case uiFFErase:
				u.answerFF(uiBeginFFErase, uiEndFFErase, sizeofFFErase, uint32(e.Value))
			}
		}
	}
}

// answerFF подтверждает запрос вибрации id: «начать» (ядро заполняет структуру), retval = 0
// (успех), «закончить». Формат обеих структур начинается с request_id и retval.
func (u *UInput) answerFF(begin, end uintptr, size int, id uint32) {
	rc, err := u.file.SyscallConn()
	if err != nil {
		return
	}
	_ = rc.Control(func(fd uintptr) {
		buf := make([]byte, size)
		binary.LittleEndian.PutUint32(buf[0:4], id)
		if ioctlBuf(int(fd), begin, buf) != nil {
			return
		}
		binary.LittleEndian.PutUint32(buf[4:8], 0)
		_ = ioctlBuf(int(fd), end, buf)
	})
}

// create выполняет последовательность ioctl настройки и создания устройства.
func (u *UInput) create() error {
	rc, err := u.file.SyscallConn()
	if err != nil {
		return err
	}
	var cerr error
	err = rc.Control(func(ufd uintptr) {
		fd := int(ufd)
		s := u.setup

		// Объявляем типы событий и коды каждого типа.
		groups := []struct {
			typ   uint16
			req   uintptr
			codes []uint16
		}{
			{EvKey, uiSetKeyBit, s.Keys},
			{EvRel, uiSetRelBit, s.Rels},
			{EvAbs, uiSetAbsBit, sortedAbs(s.Abs)},
			{EvMsc, uiSetMscBit, s.Msc},
			{EvFf, uiSetFFBit, s.FF},
		}
		for _, g := range groups {
			if len(g.codes) == 0 {
				continue
			}
			if cerr = ioctlInt(fd, uiSetEvBit, uintptr(g.typ)); cerr != nil {
				cerr = fmt.Errorf("set %s: %w", TypeName(g.typ), cerr)
				return
			}
			for _, c := range g.codes {
				if cerr = ioctlInt(fd, g.req, uintptr(c)); cerr != nil {
					cerr = fmt.Errorf("set %s: %w", CodeName(g.typ, c), cerr)
					return
				}
			}
		}

		// Свойства устройства.
		for _, p := range s.Props {
			if cerr = ioctlInt(fd, uiSetPropBit, uintptr(p)); cerr != nil {
				cerr = fmt.Errorf("set %s: %w", PropName(p), cerr)
				return
			}
		}

		// Физический путь: ядро копирует строку по указателю, поэтому держим буфер живым до конца вызова.
		if s.Phys != "" {
			phys := append([]byte(s.Phys), 0)
			cerr = ioctlInt(fd, uiSetPhys, uintptr(unsafe.Pointer(&phys[0])))
			runtime.KeepAlive(phys)
			if cerr != nil {
				cerr = fmt.Errorf("set phys: %w", cerr)
				return
			}
		}

		// Параметры абсолютных осей (UI_ABS_SETUP).
		for _, code := range sortedAbs(s.Abs) {
			if cerr = ioctlBuf(fd, uiAbsSetup, absSetupBytes(code, s.Abs[code])); cerr != nil {
				cerr = fmt.Errorf("abs setup %s: %w", CodeName(EvAbs, code), cerr)
				return
			}
		}

		// Идентификатор и имя (UI_DEV_SETUP), затем создание устройства.
		if cerr = ioctlBuf(fd, uiDevSetup, devSetupBytes(s)); cerr != nil {
			cerr = fmt.Errorf("dev setup: %w", cerr)
			return
		}
		if cerr = ioctlInt(fd, uiDevCreate, 0); cerr != nil {
			cerr = fmt.Errorf("dev create: %w", cerr)
			return
		}

		// Имя в sysfs нужно, чтобы найти файл /dev/input/eventN созданного устройства.
		u.sysname, cerr = ioctlString(fd, uiGetSysname)
		if cerr != nil {
			cerr = fmt.Errorf("get sysname: %w", cerr)
		}
	})
	if err != nil {
		return err
	}
	return cerr
}

// Setup возвращает описание, с которым создано устройство.
func (u *UInput) Setup() Setup { return u.setup }

// Sysname возвращает имя устройства в sysfs, например "input42".
func (u *UInput) Sysname() string { return u.sysname }

// DevNode возвращает путь /dev/input/eventN созданного устройства (ищется через sysfs).
func (u *UInput) DevNode() (string, error) {
	// В каталоге устройства в sysfs есть подкаталог eventN.
	dir := filepath.Join("/sys/devices/virtual/input", u.sysname)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "event") {
			return filepath.Join("/dev/input", e.Name()), nil
		}
	}
	return "", errors.New("uinput: event node not found for " + u.sysname)
}

// Write отправляет события одним системным вызовом. Вызывающий сам добавляет SYN_REPORT
// в конце пакета (см. Sync); без него композитор не увидит изменения.
func (u *UInput) Write(events ...Event) error {
	// Кодируем все события в один буфер.
	buf := make([]byte, len(events)*EventSize)
	for i, e := range events {
		e.put(buf[i*EventSize:])
	}

	// Отправляем буфер целиком.
	_, err := u.file.Write(buf)
	return err
}

// Close уничтожает виртуальное устройство и закрывает uinput.
func (u *UInput) Close() error {
	// Сначала явно уничтожаем устройство, затем закрываем файл (закрытие уничтожило бы его и само).
	rc, err := u.file.SyscallConn()
	if err == nil {
		_ = rc.Control(func(fd uintptr) { _ = ioctlInt(int(fd), uiDevDestroy, 0) })
	}
	err = u.file.Close()

	// Обработчик вибрации завершается сам: чтение закрытого файла возвращает ошибку.
	if u.ffDone != nil {
		<-u.ffDone
	}
	return err
}

// Sync возвращает событие SYN_REPORT, завершающее пакет событий.
func Sync() Event {
	return Event{Type: EvSyn, Code: SynReport}
}

// ioctlBuf выполняет ioctl с указателем на байтовый буфер.
func ioctlBuf(fd int, req uintptr, buf []byte) error {
	err := ioctlPtr(fd, req, unsafe.Pointer(&buf[0]))
	runtime.KeepAlive(buf)
	return err
}

// devSetupBytes кодирует struct uinput_setup: input_id, name[80], ff_effects_max.
func devSetupBytes(s Setup) []byte {
	buf := make([]byte, sizeofUinputSetup)
	binary.LittleEndian.PutUint16(buf[0:2], s.ID.Bustype)
	binary.LittleEndian.PutUint16(buf[2:4], s.ID.Vendor)
	binary.LittleEndian.PutUint16(buf[4:6], s.ID.Product)
	binary.LittleEndian.PutUint16(buf[6:8], s.ID.Version)
	copy(buf[8:8+uinputMaxNameSize-1], s.Name)
	// ff_effects_max: сколько эффектов вибрации можно загрузить (0 — вибрации нет).
	binary.LittleEndian.PutUint32(buf[88:92], s.FFEffectsMax)
	return buf
}

// absSetupBytes кодирует struct uinput_abs_setup: code, выравнивание, input_absinfo.
func absSetupBytes(code uint16, a AbsInfo) []byte {
	buf := make([]byte, sizeofUinputAbsSetup)
	binary.LittleEndian.PutUint16(buf[0:2], code)
	for i, v := range []int32{a.Value, a.Minimum, a.Maximum, a.Fuzz, a.Flat, a.Resolution} {
		binary.LittleEndian.PutUint32(buf[4+i*4:8+i*4], uint32(v))
	}
	return buf
}

// sortedAbs возвращает коды абсолютных осей в порядке возрастания (для стабильной настройки).
func sortedAbs(abs map[uint16]AbsInfo) []uint16 {
	codes := make([]uint16, 0, len(abs))
	for c := range abs {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	return codes
}
