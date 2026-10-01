package evdev

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// EventSize — размер struct input_event на 64-битных архитектурах:
// struct timeval (два int64) + type (u16) + code (u16) + value (s32).
const EventSize = 24

// Значения поля Value для событий EV_KEY.
const (
	// ValueUp — клавиша отпущена.
	ValueUp = 0
	// ValueDown — клавиша нажата.
	ValueDown = 1
	// ValueRepeat — автоповтор удерживаемой клавиши.
	ValueRepeat = 2
)

// ErrShortEvent возвращается при разборе буфера короче EventSize.
var ErrShortEvent = errors.New("evdev: short event buffer")

// Event — одно событие ввода (struct input_event).
type Event struct {
	// Time — метка времени ядра (CLOCK_REALTIME по умолчанию). При отправке в uinput игнорируется.
	Time time.Time
	// Type — тип события (EvKey, EvRel, EvAbs, EvSyn…).
	Type uint16
	// Code — код внутри типа (KeyA, RelX, AbsX, SynReport…).
	Code uint16
	// Value — значение: для клавиш 0/1/2, для осей — координата или смещение.
	Value int32
}

// String возвращает читаемое описание события, например "EV_KEY KEY_A 1".
func (e Event) String() string {
	return fmt.Sprintf("%s %s %d", TypeName(e.Type), CodeName(e.Type, e.Code), e.Value)
}

// IsSync сообщает, является ли событие SYN_REPORT — концом пакета событий.
func (e Event) IsSync() bool {
	return e.Type == EvSyn && e.Code == SynReport
}

// MarshalBinary кодирует событие в формат ядра (24 байта, little-endian).
func (e Event) MarshalBinary() ([]byte, error) {
	buf := make([]byte, EventSize)
	e.put(buf)
	return buf, nil
}

// put записывает событие в buf (длина не меньше EventSize).
func (e Event) put(buf []byte) {
	// Время: секунды и микросекунды. Нулевое время ядро uinput заменяет своим.
	var sec, usec int64
	if !e.Time.IsZero() {
		sec = e.Time.Unix()
		usec = int64(e.Time.Nanosecond() / 1000)
	}
	binary.LittleEndian.PutUint64(buf[0:8], uint64(sec))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(usec))

	// Тип, код и значение.
	binary.LittleEndian.PutUint16(buf[16:18], e.Type)
	binary.LittleEndian.PutUint16(buf[18:20], e.Code)
	binary.LittleEndian.PutUint32(buf[20:24], uint32(e.Value))
}

// UnmarshalBinary разбирает событие из формата ядра.
func (e *Event) UnmarshalBinary(buf []byte) error {
	// Проверяем длину буфера.
	if len(buf) < EventSize {
		return ErrShortEvent
	}

	// Разбираем поля в том же порядке, что и в struct input_event.
	sec := int64(binary.LittleEndian.Uint64(buf[0:8]))
	usec := int64(binary.LittleEndian.Uint64(buf[8:16]))
	e.Time = time.Unix(sec, usec*1000)
	e.Type = binary.LittleEndian.Uint16(buf[16:18])
	e.Code = binary.LittleEndian.Uint16(buf[18:20])
	e.Value = int32(binary.LittleEndian.Uint32(buf[20:24]))
	return nil
}

// typeGroups сопоставляет тип события с группой в таблице имён кодов.
var typeGroups = map[uint16]string{
	EvSyn: "SYN", EvKey: "KEY", EvRel: "REL", EvAbs: "ABS", EvMsc: "MSC",
	EvSw: "SW", EvLed: "LED", EvSnd: "SND", EvRep: "REP", EvFf: "FF",
}

// TypeName возвращает имя типа события ядра, например "EV_KEY", или "EV_0x1f" для неизвестного.
func TypeName(t uint16) string {
	if n, ok := codeNames["EV"][t]; ok {
		return n
	}
	return fmt.Sprintf("EV_0x%x", t)
}

// CodeName возвращает имя кода ядра, например "KEY_A" или "BTN_TRIGGER_HAPPY3",
// либо шестнадцатеричный код, если имя неизвестно.
func CodeName(t, code uint16) string {
	if n, ok := codeNames[typeGroups[t]][code]; ok {
		return n
	}
	if n, ok := ffNames[code]; ok && t == EvFf {
		return n
	}
	return fmt.Sprintf("0x%x", code)
}

// ffNames — виды отдачи (force feedback) из linux/input.h: их нет в input-event-codes.h,
// по которому генерируется таблица codeNames.
var ffNames = map[uint16]string{
	0x50: "FF_RUMBLE", 0x51: "FF_PERIODIC", 0x52: "FF_CONSTANT", 0x53: "FF_SPRING", 0x54: "FF_FRICTION",
	0x55: "FF_DAMPER", 0x56: "FF_INERTIA", 0x57: "FF_RAMP", 0x58: "FF_SQUARE", 0x59: "FF_TRIANGLE",
	0x5a: "FF_SINE", 0x5b: "FF_SAW_UP", 0x5c: "FF_SAW_DOWN", 0x5d: "FF_CUSTOM", 0x60: "FF_GAIN", 0x61: "FF_AUTOCENTER",
}

// busNames — шины подключения устройств (BUS_* из linux/input.h), коротко и понятно.
var busNames = map[uint16]string{
	0x01: "PCI", 0x03: "USB", 0x05: "Bluetooth", 0x06: "virtual", 0x10: "ISA", 0x11: "i8042",
	0x18: "I2C", 0x19: "host", 0x1c: "SPI",
}

// BusName возвращает название шины подключения ("USB", "Bluetooth", "i8042" — встроенная
// клавиатура ноутбука) или шестнадцатеричный код, если шина неизвестна.
func BusName(b uint16) string {
	if n, ok := busNames[b]; ok {
		return n
	}
	return fmt.Sprintf("0x%02x", b)
}

// PropName возвращает имя свойства устройства INPUT_PROP_*.
func PropName(p uint16) string {
	if n, ok := codeNames["INPUT_PROP"][p]; ok {
		return n
	}
	return fmt.Sprintf("INPUT_PROP_0x%x", p)
}
