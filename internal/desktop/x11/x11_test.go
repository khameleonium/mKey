package x11

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
)

// fakeServer — имитация X-сервера по протоколу X11: установка соединения (проверяет cookie),
// InternAtom, GetProperty (_XKB_RULES_NAMES), QueryExtension (XKEYBOARD), XkbUseExtension,
// XkbGetState и XkbLatchLockState. group — текущая группа XKB.
type fakeServer struct {
	rules  string
	group  byte
	cookie []byte
}

// serve обслуживает одно соединение до его закрытия.
func (s *fakeServer) serve(t *testing.T, c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	le := binary.LittleEndian

	// Установка соединения: проверяем порядок байтов и cookie.
	head := make([]byte, 12)
	if _, err := io.ReadFull(r, head); err != nil {
		return
	}
	nameLen, dataLen := int(le.Uint16(head[6:])), int(le.Uint16(head[8:]))
	auth := make([]byte, pad4(nameLen)+pad4(dataLen))
	if _, err := io.ReadFull(r, auth); err != nil {
		return
	}
	data := auth[pad4(nameLen) : pad4(nameLen)+dataLen]
	if head[0] != 'l' || (s.cookie != nil && string(data) != string(s.cookie)) {
		reason := "No protocol specified"
		resp := make([]byte, 8+pad4(len(reason)))
		resp[1] = byte(len(reason))
		le.PutUint16(resp[6:], uint16(pad4(len(reason))/4))
		copy(resp[8:], reason)
		_, _ = c.Write(resp)
		return
	}
	vendor := "fake"
	body := make([]byte, 32+pad4(len(vendor))+8+40) // сведения, производитель, 1 формат, экран
	le.PutUint16(body[16:], uint16(len(vendor)))
	body[20], body[21] = 1, 1 // экранов, форматов
	copy(body[32:], vendor)
	le.PutUint32(body[32+pad4(len(vendor))+8:], 0x1e5) // корневое окно
	resp := make([]byte, 8)
	resp[0] = 1
	le.PutUint16(resp[2:], 11)
	le.PutUint16(resp[6:], uint16(len(body)/4))
	_, _ = c.Write(append(resp, body...))

	// Запросы.
	const xkbMajor = 135
	reply := func(extra []byte, fill func(b []byte)) {
		b := make([]byte, 32+len(extra))
		b[0] = 1
		le.PutUint32(b[4:], uint32(len(extra)/4))
		fill(b)
		copy(b[32:], extra)
		_, _ = c.Write(b)
	}
	for {
		h := make([]byte, 4)
		if _, err := io.ReadFull(r, h); err != nil {
			return
		}
		req := make([]byte, int(le.Uint16(h[2:]))*4-4)
		if _, err := io.ReadFull(r, req); err != nil {
			return
		}
		switch {
		case h[0] == opInternAtom:
			name := string(req[4 : 4+le.Uint16(req)])
			reply(nil, func(b []byte) {
				if name == "_XKB_RULES_NAMES" {
					le.PutUint32(b[8:], 300)
				}
			})
		case h[0] == opGetProperty:
			if le.Uint32(req[0:]) != 0x1e5 || le.Uint32(req[4:]) != 300 {
				t.Errorf("GetProperty window/atom = %x/%d", le.Uint32(req[0:]), le.Uint32(req[4:]))
			}
			v := make([]byte, pad4(len(s.rules)))
			copy(v, s.rules)
			reply(v, func(b []byte) { b[1] = 8; le.PutUint32(b[16:], uint32(len(s.rules))) })
		case h[0] == opQueryExtension:
			reply(nil, func(b []byte) { b[8], b[9] = 1, xkbMajor })
		case h[0] == xkbMajor && h[1] == xkbUseExtension:
			reply(nil, func(b []byte) { b[1] = 1 })
		case h[0] == xkbMajor && h[1] == xkbGetState:
			reply(nil, func(b []byte) { b[12] = s.group })
		case h[0] == xkbMajor && h[1] == xkbLatchLockSt:
			if req[4] == 1 {
				s.group = req[5]
			}
		default:
			t.Errorf("unexpected request %d/%d", h[0], h[1])
			return
		}
	}
}

// pipeSource — источник раскладок, подключающийся к fakeServer через net.Pipe.
func pipeSource(t *testing.T, s *fakeServer, cookie []byte) *Layouts {
	l := New(":0")
	l.dial = func(string) (xconn, error) {
		client, server := net.Pipe()
		go s.serve(t, server)
		x := &conn{c: client, r: bufio.NewReader(client)}
		if err := x.setup("MIT-MAGIC-COOKIE-1", cookie); err != nil {
			_ = client.Close()
			return nil, err
		}
		return x, nil
	}
	return l
}

// TestLayouts проверяет источник X11 с имитацией X-сервера: раскладки по группам (повтор группы
// не повторяется в списке), текущая — по группе XKB, переключение, отказ при чужом cookie,
// работа только в сессии X11.
func TestLayouts(t *testing.T) {
	t.Parallel()
	s := &fakeServer{rules: "evdev\x00pc105\x00us,ru,ru\x00,,\x00grp:alt_shift_toggle\x00", group: 1, cookie: []byte("secret")}
	l := pipeSource(t, s, []byte("secret"))
	ctx := context.Background()

	info, err := l.Layouts(ctx)
	if err != nil || info.Current != "ru" || strings.Join(info.Available, ",") != "us,ru" || !info.CanSwitch || info.Source != "x11" || info.Blind {
		t.Fatalf("Layouts = %+v, %v", info, err)
	}
	if err := l.Switch(ctx, "us"); err != nil || s.group != 0 {
		t.Fatalf("Switch = %v, group %d", err, s.group)
	}
	if err := l.Switch(ctx, "de"); err == nil {
		t.Fatal("unknown layout switched")
	}

	// Чужой cookie — понятная ошибка установки соединения.
	if _, err := pipeSource(t, s, []byte("wrong")).Layouts(ctx); err == nil || !strings.Contains(err.Error(), "No protocol specified") {
		t.Fatalf("bad cookie = %v", err)
	}

	// Только X11 с дисплеем.
	if !l.Supports(contracts.SessionInfo{Type: "x11", Display: ":0"}) || l.Supports(contracts.SessionInfo{Type: "wayland", Display: ":0"}) {
		t.Fatal("Supports")
	}
}

// TestDisplayAndCookie проверяет разбор DISPLAY и поиск cookie в файле авторизации.
func TestDisplayAndCookie(t *testing.T) {
	// Не t.Parallel: меняет XAUTHORITY.
	for in, want := range map[string]string{":0": "|0", ":1.0": "|1", "unix:2": "unix|2", "host:10.0": "host|10"} {
		h, n, err := parseDisplay(in)
		if err != nil || h+"|"+n != want {
			t.Errorf("parseDisplay(%q) = %q %q %v", in, h, n, err)
		}
	}
	if _, _, err := parseDisplay("bad"); err == nil {
		t.Error("bad DISPLAY accepted")
	}

	// Файл авторизации: запись другого дисплея, затем нужная.
	hostname, _ := os.Hostname()
	var raw []byte
	str := func(s string) {
		raw = binary.BigEndian.AppendUint16(raw, uint16(len(s)))
		raw = append(raw, s...)
	}
	for _, e := range []struct{ num, cookie string }{{"5", "other"}, {"0", "right"}} {
		raw = binary.BigEndian.AppendUint16(raw, 256)
		str(hostname)
		str(e.num)
		str("MIT-MAGIC-COOKIE-1")
		str(e.cookie)
	}
	path := filepath.Join(t.TempDir(), "Xauthority")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", path)
	if name, data := authCookie("", "0"); name != "MIT-MAGIC-COOKIE-1" || string(data) != "right" {
		t.Fatalf("cookie = %q %q", name, data)
	}
	if name, _ := authCookie("", "9"); name != "" {
		t.Fatalf("cookie for unknown display = %q", name)
	}
}
