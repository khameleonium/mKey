package evdev

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Кодирование номеров ioctl, как макросы _IOC/_IO/_IOR/_IOW в asm-generic/ioctl.h.
// Эта схема одинакова для amd64 и arm64 — единственных поддерживаемых архитектур.
const (
	iocNone  = 0
	iocWrite = 1
	iocRead  = 2

	iocNrShift   = 0
	iocTypeShift = 8
	iocSizeShift = 16
	iocDirShift  = 30
)

// ioc собирает номер ioctl из направления, типа, номера и размера аргумента.
func ioc(dir, typ, nr, size uintptr) uintptr {
	return dir<<iocDirShift | typ<<iocTypeShift | nr<<iocNrShift | size<<iocSizeShift
}

// Размеры структур ядра на 64-битных архитектурах.
const (
	// sizeofInputID — struct input_id: четыре __u16.
	sizeofInputID = 8
	// sizeofAbsInfo — struct input_absinfo: шесть __s32.
	sizeofAbsInfo = 24
	// sizeofUinputSetup — struct uinput_setup: input_id (8) + name[80] + ff_effects_max (4).
	sizeofUinputSetup = 92
	// sizeofUinputAbsSetup — struct uinput_abs_setup: code (2) + выравнивание (2) + absinfo (24).
	sizeofUinputAbsSetup = 28
	// uinputMaxNameSize — UINPUT_MAX_NAME_SIZE: длина имени виртуального устройства с нулём.
	uinputMaxNameSize = 80
	// sizeofFFEffect, sizeofFFUpload, sizeofFFErase — struct ff_effect, uinput_ff_upload, uinput_ff_erase.
	sizeofFFEffect = 48
	sizeofFFUpload = 104
	sizeofFFErase  = 12
)

// Номера ioctl evdev (linux/input.h).
var (
	eviocgversion = ioc(iocRead, 'E', 0x01, 4)
	eviocgid      = ioc(iocRead, 'E', 0x02, sizeofInputID)
	eviocgrab     = ioc(iocWrite, 'E', 0x90, 4)
)

// eviocgname — EVIOCGNAME(len): имя устройства.
func eviocgname(size uintptr) uintptr { return ioc(iocRead, 'E', 0x06, size) }

// eviocgphys — EVIOCGPHYS(len): физический путь устройства.
func eviocgphys(size uintptr) uintptr { return ioc(iocRead, 'E', 0x07, size) }

// eviocguniq — EVIOCGUNIQ(len): уникальный идентификатор (серийный номер), если есть.
func eviocguniq(size uintptr) uintptr { return ioc(iocRead, 'E', 0x08, size) }

// eviocgprop — EVIOCGPROP(len): битовая маска свойств INPUT_PROP_*.
func eviocgprop(size uintptr) uintptr { return ioc(iocRead, 'E', 0x09, size) }

// eviocgkey — EVIOCGKEY(len): текущее состояние (нажата/отпущена) всех клавиш.
func eviocgkey(size uintptr) uintptr { return ioc(iocRead, 'E', 0x18, size) }

// eviocgbit — EVIOCGBIT(ev, len): маска поддерживаемых кодов типа ev (или типов событий при ev = 0).
func eviocgbit(ev, size uintptr) uintptr { return ioc(iocRead, 'E', 0x20+ev, size) }

// eviocgabs — EVIOCGABS(abs): параметры абсолютной оси.
func eviocgabs(abs uintptr) uintptr { return ioc(iocRead, 'E', 0x40+abs, sizeofAbsInfo) }

// Номера ioctl uinput (linux/uinput.h).
var (
	uiDevCreate  = ioc(iocNone, 'U', 1, 0)
	uiDevDestroy = ioc(iocNone, 'U', 2, 0)
	uiDevSetup   = ioc(iocWrite, 'U', 3, sizeofUinputSetup)
	uiAbsSetup   = ioc(iocWrite, 'U', 4, sizeofUinputAbsSetup)
	uiSetEvBit   = ioc(iocWrite, 'U', 100, 4)
	uiSetKeyBit  = ioc(iocWrite, 'U', 101, 4)
	uiSetRelBit  = ioc(iocWrite, 'U', 102, 4)
	uiSetAbsBit  = ioc(iocWrite, 'U', 103, 4)
	uiSetMscBit  = ioc(iocWrite, 'U', 104, 4)
	uiSetPhys    = ioc(iocWrite, 'U', 108, unsafe.Sizeof(uintptr(0)))
	uiSetPropBit = ioc(iocWrite, 'U', 110, 4)
	uiSetFFBit   = ioc(iocWrite, 'U', 107, 4)

	// Ответы на запросы вибрации (force feedback) к виртуальному устройству: загрузка и удаление
	// эффекта (struct uinput_ff_upload — 104 байта, struct uinput_ff_erase — 12; размеры сверены
	// с linux/uinput.h компилятором C на x86_64 и arm64).
	uiBeginFFUpload = ioc(iocRead|iocWrite, 'U', 200, sizeofFFUpload)
	uiEndFFUpload   = ioc(iocWrite, 'U', 201, sizeofFFUpload)
	uiBeginFFErase  = ioc(iocRead|iocWrite, 'U', 202, sizeofFFErase)
	uiEndFFErase    = ioc(iocWrite, 'U', 203, sizeofFFErase)

	// Загрузка и удаление эффекта со стороны программы, читающей устройство (EVIOCSFF, EVIOCRMFF):
	// так игры просят вибрацию (struct ff_effect — 48 байт).
	eviocsff  = ioc(iocWrite, 'E', 0x80, sizeofFFEffect)
	eviocrmff = ioc(iocWrite, 'E', 0x81, 4)
)

// uiGetSysname — UI_GET_SYSNAME(len): имя созданного устройства в sysfs (например, "input42").
func uiGetSysname(size uintptr) uintptr { return ioc(iocRead, 'U', 44, size) }

// ioctlPtr выполняет ioctl с указателем на буфер buf.
func ioctlPtr(fd int, req uintptr, buf unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(buf))
	if errno != 0 {
		return errno
	}
	return nil
}

// ioctlInt выполняет ioctl с целочисленным аргументом, передаваемым по значению.
func ioctlInt(fd int, req uintptr, arg uintptr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, arg)
	if errno != 0 {
		return errno
	}
	return nil
}

// ioctlString читает строку фиксированной максимальной длины (имя, phys, uniq) через ioctl.
// Возвращает пустую строку, если ядро сообщает, что значения нет (ENOENT).
func ioctlString(fd int, reqFor func(uintptr) uintptr) (string, error) {
	// Буфер с запасом: ядро обрезает строку по размеру буфера.
	buf := make([]byte, 256)
	if err := ioctlPtr(fd, reqFor(uintptr(len(buf))), unsafe.Pointer(&buf[0])); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return "", nil
		}
		return "", err
	}

	// Обрезаем по первому нулевому байту.
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i]), nil
		}
	}
	return string(buf), nil
}

// ioctlBits читает битовую маску длиной bits бит через ioctl и возвращает номера установленных битов.
func ioctlBits(fd int, req func(uintptr) uintptr, bits int) ([]uint16, error) {
	// Буфер на bits бит, округлённый вверх до байта.
	buf := make([]byte, (bits+7)/8)
	if err := ioctlPtr(fd, req(uintptr(len(buf))), unsafe.Pointer(&buf[0])); err != nil {
		return nil, fmt.Errorf("ioctl bits: %w", err)
	}
	return bitsToCodes(buf), nil
}

// bitsToCodes превращает битовую маску (порядок бит ядра: little-endian по байтам) в список номеров битов.
func bitsToCodes(buf []byte) []uint16 {
	var codes []uint16
	for i, b := range buf {
		for bit := range 8 {
			if b&(1<<bit) != 0 {
				codes = append(codes, uint16(i*8+bit))
			}
		}
	}
	return codes
}
