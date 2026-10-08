package x11

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Минимальный клиент протокола X11 (X Window System Protocol, X11R7) и расширения XKEYBOARD
// (The X Keyboard Extension: Protocol Specification) — ровно столько, сколько нужно раскладкам:
// установка соединения с авторизацией MIT-MAGIC-COOKIE-1, InternAtom, GetProperty, QueryExtension,
// XkbUseExtension, XkbGetState и XkbLatchLockState. Порядок байтов — little-endian ('l').

// ioTimeout — предел одного обмена с X-сервером: локальный сервер отвечает за миллисекунды,
// а зависший не должен останавливать набор текста.
const ioTimeout = 2 * time.Second

// Коды запросов ядра X11 и XKB.
const (
	opInternAtom     = 16
	opGetProperty    = 20
	opQueryExtension = 98
	xkbUseExtension  = 0
	xkbGetState      = 4
	xkbLatchLockSt   = 5
	// xkbUseCoreKbd — «основная клавиатура» (XkbUseCoreKbd) в запросах XKB.
	xkbUseCoreKbd = 0x100
)

// conn — соединение с X-сервером.
type conn struct {
	c    net.Conn
	r    *bufio.Reader
	root uint32
	// xkb — основной код запросов расширения XKEYBOARD (0 — ещё не узнан).
	xkb byte
}

// dial подключается к дисплею display (":0", ":0.0", "unix:0", "host:10.0") с авторизацией из
// XAUTHORITY или ~/.Xauthority.
func dial(display string) (*conn, error) {
	// Разбор дисплея: хост (пусто или unix — локальный сокет) и номер.
	host, num, err := parseDisplay(display)
	if err != nil {
		return nil, err
	}

	// Сокет: локальный файл, затем абстрактный (Linux), иначе TCP 6000+N.
	var c net.Conn
	if host == "" || host == "unix" {
		path := "/tmp/.X11-unix/X" + num
		if c, err = net.DialTimeout("unix", path, ioTimeout); err != nil {
			c, err = net.DialTimeout("unix", "@"+path, ioTimeout)
		}
	} else {
		n, _ := strconv.Atoi(num)
		c, err = net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(6000+n)), ioTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("x11: connect %s: %w", display, err)
	}

	// Установка соединения.
	x := &conn{c: c, r: bufio.NewReader(c)}
	name, data := authCookie(host, num)
	if err := x.setup(name, data); err != nil {
		_ = c.Close()
		return nil, err
	}
	return x, nil
}

// parseDisplay разбирает значение DISPLAY на хост и номер дисплея (номер экрана отбрасывается).
func parseDisplay(display string) (host, num string, err error) {
	i := strings.LastIndexByte(display, ':')
	if i < 0 {
		return "", "", fmt.Errorf("x11: bad DISPLAY %q", display)
	}
	host, num = display[:i], display[i+1:]
	if j := strings.IndexByte(num, '.'); j >= 0 {
		num = num[:j]
	}
	if _, err := strconv.Atoi(num); err != nil {
		return "", "", fmt.Errorf("x11: bad DISPLAY %q", display)
	}
	return host, num, nil
}

// authCookie ищет в файле авторизации X (XAUTHORITY, иначе ~/.Xauthority) запись
// MIT-MAGIC-COOKIE-1 для этого компьютера и дисплея; нет файла или записи — пустые (сервер без
// авторизации или с доступом по пользователю, как у многих локальных сессий).
func authCookie(host, num string) (string, []byte) {
	path := os.Getenv("XAUTHORITY")
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".Xauthority")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	hostname, _ := os.Hostname()
	if host != "" && host != "unix" {
		hostname = host
	}

	// Записи: семья (2), адрес, номер дисплея, имя способа, данные — строки с длиной (2, big-endian).
	r := bytes.NewReader(raw)
	str := func() ([]byte, bool) {
		var n uint16
		if binary.Read(r, binary.BigEndian, &n) != nil {
			return nil, false
		}
		b := make([]byte, n)
		_, err := io.ReadFull(r, b)
		return b, err == nil
	}
	for {
		var family uint16
		if binary.Read(r, binary.BigEndian, &family) != nil {
			return "", nil
		}
		addr, ok1 := str()
		number, ok2 := str()
		name, ok3 := str()
		data, ok4 := str()
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return "", nil
		}
		// 256 — FamilyLocal (адрес — имя компьютера), 65535 — FamilyWild (любой).
		local := family == 256 && string(addr) == hostname
		if (local || family == 65535) && (len(number) == 0 || string(number) == num) && string(name) == "MIT-MAGIC-COOKIE-1" {
			return string(name), data
		}
	}
}

// pad4 — длина, выровненная вверх до кратной 4.
func pad4(n int) int { return (n + 3) &^ 3 }

// setup выполняет установку соединения и запоминает корневое окно первого экрана.
func (x *conn) setup(authName string, authData []byte) error {
	// Запрос: порядок байтов, версия 11.0, имя и данные авторизации.
	req := make([]byte, 12+pad4(len(authName))+pad4(len(authData)))
	req[0] = 'l'
	binary.LittleEndian.PutUint16(req[2:], 11)
	binary.LittleEndian.PutUint16(req[6:], uint16(len(authName)))
	binary.LittleEndian.PutUint16(req[8:], uint16(len(authData)))
	copy(req[12:], authName)
	copy(req[12+pad4(len(authName)):], authData)
	if err := x.write(req); err != nil {
		return err
	}

	// Ответ: 8 байт заголовка и данные длиной length*4.
	head := make([]byte, 8)
	if err := x.read(head); err != nil {
		return err
	}
	body := make([]byte, int(binary.LittleEndian.Uint16(head[6:]))*4)
	if err := x.read(body); err != nil {
		return err
	}
	if head[0] != 1 {
		reason := ""
		if head[0] == 0 && int(head[1]) <= len(body) {
			reason = string(body[:head[1]])
		}
		return fmt.Errorf("x11: connection refused (%d): %s", head[0], strings.TrimSpace(reason))
	}

	// Корневое окно первого экрана: после 32 байт сведений, имени производителя и форматов.
	if len(body) < 32 {
		return errors.New("x11: short setup reply")
	}
	vendorLen := int(binary.LittleEndian.Uint16(body[16:]))
	formats := int(body[21])
	off := 32 + pad4(vendorLen) + formats*8
	if len(body) < off+4 {
		return errors.New("x11: short setup reply")
	}
	x.root = binary.LittleEndian.Uint32(body[off:])
	return nil
}

// write отправляет байты с пределом времени.
func (x *conn) write(b []byte) error {
	_ = x.c.SetDeadline(time.Now().Add(ioTimeout))
	_, err := x.c.Write(b)
	if err != nil {
		return fmt.Errorf("x11: write: %w", err)
	}
	return nil
}

// read читает ровно len(b) байт с пределом времени.
func (x *conn) read(b []byte) error {
	_ = x.c.SetDeadline(time.Now().Add(ioTimeout))
	if _, err := io.ReadFull(x.r, b); err != nil {
		return fmt.Errorf("x11: read: %w", err)
	}
	return nil
}

// request отправляет запрос (код, байт данных, тело без заголовка — длина кратна 4) и читает
// ответ: 32 байта и продолжение длиной length*4. Ошибка сервера — ошибка Go с её кодом.
func (x *conn) request(major, data byte, body []byte) ([]byte, error) {
	if err := x.send(major, data, body); err != nil {
		return nil, err
	}
	for {
		reply := make([]byte, 32)
		if err := x.read(reply); err != nil {
			return nil, err
		}
		switch reply[0] {
		case 0:
			return nil, fmt.Errorf("x11: request %d/%d: error %d", major, data, reply[1])
		case 1:
			rest := make([]byte, int(binary.LittleEndian.Uint32(reply[4:]))*4)
			if err := x.read(rest); err != nil {
				return nil, err
			}
			return append(reply, rest...), nil
		}
		// События (их mKey не запрашивал, но сервер может прислать) — пропускаем.
	}
}

// send отправляет запрос без чтения ответа (для запросов, у которых ответа нет).
func (x *conn) send(major, data byte, body []byte) error {
	req := make([]byte, 4+len(body))
	req[0], req[1] = major, data
	binary.LittleEndian.PutUint16(req[2:], uint16(len(req)/4))
	copy(req[4:], body)
	return x.write(req)
}

// close закрывает соединение.
func (x *conn) close() { _ = x.c.Close() }

// atom возвращает атом с именем name (0 — такого нет).
func (x *conn) atom(name string) (uint32, error) {
	body := make([]byte, 4+pad4(len(name)))
	binary.LittleEndian.PutUint16(body, uint16(len(name)))
	copy(body[4:], name)
	reply, err := x.request(opInternAtom, 1, body) // only-if-exists
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(reply[8:]), nil
}

// rootProperty возвращает значение свойства name корневого окна (nil — свойства нет).
func (x *conn) rootProperty(name string) ([]byte, error) {
	a, err := x.atom(name)
	if err != nil || a == 0 {
		return nil, err
	}
	body := make([]byte, 20)
	binary.LittleEndian.PutUint32(body[0:], x.root)
	binary.LittleEndian.PutUint32(body[4:], a)
	binary.LittleEndian.PutUint32(body[8:], 0)     // любой тип
	binary.LittleEndian.PutUint32(body[12:], 0)    // с начала
	binary.LittleEndian.PutUint32(body[16:], 1024) // до 4 КиБ
	reply, err := x.request(opGetProperty, 0, body)
	if err != nil {
		return nil, err
	}
	format := int(reply[1])
	n := int(binary.LittleEndian.Uint32(reply[16:])) * max(format/8, 1)
	if 32+n > len(reply) {
		return nil, errors.New("x11: short property reply")
	}
	return reply[32 : 32+n], nil
}

// useXKB находит расширение XKEYBOARD и включает его (XkbUseExtension 1.0).
func (x *conn) useXKB() error {
	if x.xkb != 0 {
		return nil
	}
	const name = "XKEYBOARD"
	body := make([]byte, 4+pad4(len(name)))
	binary.LittleEndian.PutUint16(body, uint16(len(name)))
	copy(body[4:], name)
	reply, err := x.request(opQueryExtension, 0, body)
	if err != nil {
		return err
	}
	if reply[8] == 0 {
		return errors.New("x11: no XKEYBOARD extension")
	}
	major := reply[9]
	use := []byte{1, 0, 0, 0} // wantedMajor=1, wantedMinor=0
	reply, err = x.request(major, xkbUseExtension, use)
	if err != nil {
		return err
	}
	if reply[1] == 0 {
		return errors.New("x11: XKEYBOARD 1.0 not supported")
	}
	x.xkb = major
	return nil
}

// group возвращает текущую группу XKB (номер раскладки с 0) основной клавиатуры.
func (x *conn) group() (int, error) {
	if err := x.useXKB(); err != nil {
		return 0, err
	}
	body := make([]byte, 4)
	binary.LittleEndian.PutUint16(body, xkbUseCoreKbd)
	reply, err := x.request(x.xkb, xkbGetState, body)
	if err != nil {
		return 0, err
	}
	return int(reply[12]), nil
}

// lockGroup делает группу XKB g текущей (как переключение раскладки сочетанием клавиш) и ждёт,
// пока сервер её применит (повторный запрос состояния — ответ приходит после обработки).
func (x *conn) lockGroup(g int) error {
	if err := x.useXKB(); err != nil {
		return err
	}
	// xkbLatchLockStateReq: deviceSpec, affectModLocks, modLocks, lockGroup, groupLock,
	// affectModLatches, modLatches, pad, latchGroup, groupLatch.
	body := make([]byte, 12)
	binary.LittleEndian.PutUint16(body[0:], xkbUseCoreKbd)
	body[4] = 1 // lockGroup
	body[5] = byte(g)
	if err := x.send(x.xkb, xkbLatchLockSt, body); err != nil {
		return err
	}
	got, err := x.group()
	if err != nil {
		return err
	}
	if got != g {
		return fmt.Errorf("x11: layout group is %d after switching to %d", got, g)
	}
	return nil
}
