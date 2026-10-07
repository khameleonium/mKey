//go:build integration

// Package integration — интеграционные тесты mKey с настоящими /dev/uinput и /dev/input (T1.7).
//
// Запуск: make test-integration (нужны права на /dev/uinput и /dev/input/event*: mkey doctor --fix).
// Без прав тесты пропускаются с пояснением.
//
// Безопасность живой сессии: тесты создают только виртуальные «джойстики» с кнопками
// BTN_TRIGGER_HAPPY*. Композиторы (libinput) такие устройства не обрабатывают, поэтому
// тестовые нажатия не попадают ни в одно окно. Клавиатура и мышь в тестах не создаются.
package integration

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khameleonium/mKey/internal/bus"
	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
	"github.com/khameleonium/mKey/internal/input"
	ev "github.com/khameleonium/mKey/internal/lib/evdev"
	"github.com/khameleonium/mKey/internal/registry"
)

// helperEnv — переменная окружения, по которой тестовый бинарник работает как вспомогательный процесс.
const helperEnv = "MKEY_INTEGRATION_HELPER"

// TestMain запускает вспомогательный процесс вместо тестов, если задана helperEnv.
func TestMain(m *testing.M) {
	if name := os.Getenv(helperEnv); name != "" {
		runHelper(name)
		return
	}
	os.Exit(m.Run())
}

// runHelper создаёт виртуальное устройство, печатает его имя в sysfs и ждёт, пока процесс не убьют.
func runHelper(name string) {
	u, err := ev.CreateUInput(ev.DefaultUInputPath, joystickSetup(name))
	if err != nil {
		fmt.Println("ERR", err)
		os.Exit(1)
	}
	fmt.Println("SYSNAME", u.Sysname())
	select {}
}

// joystickSetup описывает безопасное тестовое устройство: только кнопки BTN_TRIGGER_HAPPY1..4.
func joystickSetup(name string) ev.Setup {
	return ev.Setup{
		Name: name,
		Phys: "mkey-test/" + name,
		ID:   ev.ID{Bustype: ev.BusVirtual, Vendor: 0x6d6b, Product: 0xfff0, Version: 1},
		Keys: []uint16{ev.BtnTriggerHappy1, ev.BtnTriggerHappy2, ev.BtnTriggerHappy3, ev.BtnTriggerHappy4},
	}
}

// requireUinput пропускает тест, если нет прав на /dev/uinput.
func requireUinput(t *testing.T) {
	t.Helper()
	f, err := os.OpenFile(ev.DefaultUInputPath, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no access to %s (%v): run `mkey doctor --fix` first", ev.DefaultUInputPath, err)
	}
	_ = f.Close()
}

// uniqueName возвращает уникальное имя тестового устройства.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s %d", prefix, time.Now().UnixNano()%1_000_000)
}

// waitFor ждёт выполнения условия cond не дольше timeout.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// TestUInputRoundTrip создаёт виртуальное устройство, открывает его как обычное evdev-устройство
// и проверяет, что отправленные события читаются обратно и сведения совпадают.
func TestUInputRoundTrip(t *testing.T) {
	requireUinput(t)

	// Создаём тестовый джойстик.
	name := uniqueName("mKey Test Joystick")
	u, err := ev.CreateUInput(ev.DefaultUInputPath, joystickSetup(name))
	if err != nil {
		t.Fatalf("CreateUInput: %v", err)
	}
	defer func() { _ = u.Close() }()

	// Находим и открываем его файл /dev/input/eventN (udev выставляет права с задержкой).
	var node string
	if !waitFor(3*time.Second, func() bool { node, err = u.DevNode(); return err == nil }) {
		t.Fatalf("DevNode: %v", err)
	}
	var d *ev.Device
	if !waitFor(3*time.Second, func() bool { d, err = ev.Open(node); return err == nil }) {
		t.Skipf("cannot open %s (%v): no read access to input devices, run `mkey doctor --fix`", node, err)
	}
	defer func() { _ = d.Close() }()

	// Сведения совпадают с описанием.
	info := d.Info()
	if info.Name != name || info.ID.Product != 0xfff0 || !info.Caps.Has(ev.EvKey, ev.BtnTriggerHappy3) || info.Caps.Has(ev.EvKey, ev.KeyA) {
		t.Fatalf("info = %+v", info)
	}

	// Отправляем нажатие и отпускание кнопки и читаем их обратно.
	if err := u.Write(ev.Event{Type: ev.EvKey, Code: ev.BtnTriggerHappy3, Value: 1}, ev.Sync(),
		ev.Event{Type: ev.EvKey, Code: ev.BtnTriggerHappy3, Value: 0}, ev.Sync()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var got []ev.Event
	buf := make([]ev.Event, 16)
	for len(got) < 4 {
		events, err := d.ReadEvents(buf)
		if err != nil {
			t.Fatalf("ReadEvents: %v", err)
		}
		got = append(got, events...)
	}
	if got[0].Code != ev.BtnTriggerHappy3 || got[0].Value != 1 || !got[1].IsSync() || got[2].Value != 0 {
		t.Fatalf("events = %v", got)
	}
}

// TestDeviceRemovedOnKill проверяет, что при аварийном завершении процесса (kill -9)
// ядро само удаляет его виртуальные устройства (SEC-2).
func TestDeviceRemovedOnKill(t *testing.T) {
	requireUinput(t)

	// Запускаем вспомогательный процесс, который создаёт устройство и ждёт.
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), helperEnv+"="+uniqueName("mKey Test Kill"))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	// Читаем имя устройства в sysfs.
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "SYSNAME ") {
		t.Fatalf("helper output %q, err %v", line, err)
	}
	sysDir := filepath.Join("/sys/devices/virtual/input", strings.TrimSpace(strings.TrimPrefix(line, "SYSNAME ")))
	if _, err := os.Stat(sysDir); err != nil {
		t.Fatalf("device not created: %v", err)
	}

	// Убиваем процесс без шанса на очистку — устройство должно исчезнуть.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if !waitFor(3*time.Second, func() bool { _, err := os.Stat(sysDir); return errors.Is(err, os.ErrNotExist) }) {
		t.Fatalf("device %s still exists after kill -9", sysDir)
	}
}

// TestInputModuleHotplug проверяет модуль input на настоящем /dev/input: подключение
// стороннего устройства видно и его события приходят, а устройства mKey игнорируются.
func TestInputModuleHotplug(t *testing.T) {
	requireUinput(t)

	// Запускаем модуль input в менеджере.
	cat, err := i18n.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	b := bus.New(0)
	mod := input.New()
	mgr, err := registry.NewManager(registry.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Translator: i18n.New(cat, "en"),
		Bus:        b,
	}, []registry.Entry{{Module: mod, Core: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mgr.Stop(context.Background()) }()
	if mod.Status().Open == 0 {
		t.Skip("no readable input devices: run `mkey doctor --fix`")
	}
	added, unsub := b.Subscribe(contracts.TopicInputDeviceAdded)
	defer unsub()
	events, cancel := mod.Subscribe(64)
	defer cancel()

	// Подключаем «чужое» устройство (без префикса mKey) — модуль должен его увидеть.
	foreign := uniqueName("Integration Joystick")
	u, err := ev.CreateUInput(ev.DefaultUInputPath, joystickSetup(foreign))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = u.Close() }()
	if !waitForAdded(added, foreign, 5*time.Second) {
		t.Fatalf("device %q was not reported as added", foreign)
	}

	// Его событие приходит подписчику.
	time.Sleep(200 * time.Millisecond)
	if err := u.Write(ev.Event{Type: ev.EvKey, Code: ev.BtnTriggerHappy2, Value: 1}, ev.Sync(),
		ev.Event{Type: ev.EvKey, Code: ev.BtnTriggerHappy2, Value: 0}, ev.Sync()); err != nil {
		t.Fatal(err)
	}
	if !waitForEvent(events, ev.BtnTriggerHappy2, 3*time.Second) {
		t.Fatal("event from the new device was not delivered")
	}

	// Устройство с префиксом "mKey " модуль не открывает.
	own := uniqueName("mKey Integration Own")
	u2, err := ev.CreateUInput(ev.DefaultUInputPath, joystickSetup(own))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = u2.Close() }()
	if waitForAdded(added, own, 2*time.Second) {
		t.Fatalf("own device %q must be ignored", own)
	}
}

// waitForAdded ждёт на шине событие подключения устройства с именем name.
func waitForAdded(ch <-chan contracts.Event, name string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			if d, ok := e.Payload.(contracts.InputDevice); ok && d.Info.Name == name {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// waitForEvent ждёт событие кнопки code от любого устройства.
func waitForEvent(ch <-chan contracts.InputEvent, code uint16, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			if e.Event.Type == ev.EvKey && e.Event.Code == code {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// TestForceFeedback проверяет вибрацию виртуального устройства (FR-VD-5): игра загружает эффект
// (EVIOCSFF) и удаляет его — mKey подтверждает запросы, поэтому оба вызова быстро успешны (без
// ответа ядро ждало бы до таймаута, и игра зависала бы). Устройство — тестовый «джойстик».
func TestForceFeedback(t *testing.T) {
	s := joystickSetup("mKey FF test")
	s.FF = []uint16{0x50} // FF_RUMBLE
	u, err := ev.CreateUInput(ev.DefaultUInputPath, s)
	if err != nil {
		t.Skipf("uinput unavailable: %v", err)
	}
	node, err := u.DevNode()
	if err != nil {
		_ = u.Close()
		t.Fatal(err)
	}

	// Устройство видно с поддержкой вибрации; загрузка и удаление эффекта — быстро и без ошибок.
	time.Sleep(200 * time.Millisecond) // udev выдаёт права на новый eventN не сразу
	d, err := ev.Open(node)
	if err != nil {
		_ = u.Close()
		t.Skipf("cannot open %s: %v", node, err)
	}
	if !d.Info().Caps.Has(ev.EvFf, 0x50) {
		t.Errorf("FF_RUMBLE not announced: %v", d.Info().Caps.Codes[ev.EvFf])
	}
	start := time.Now()
	id, err := d.UploadRumble(0x8000, 0x4000, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := d.EraseEffect(id); err != nil {
		t.Fatalf("erase: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("FF requests took %v (not answered?)", took)
	}
	t.Logf("effect %d uploaded and erased in %v", id, time.Since(start))

	// Закрытие устройства завершает обработчик вибрации (Close не зависает).
	_ = d.Close()
	done := make(chan error, 1)
	go func() { done <- u.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close hangs: FF handler did not stop")
	}
}
