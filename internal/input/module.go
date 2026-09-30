package input

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"mkey/internal/contracts"
	"mkey/internal/lib/clock"
	ev "mkey/internal/lib/evdev"
)

// ModuleID — идентификатор модуля.
const ModuleID = "input"

// DefaultDir — каталог файлов устройств evdev.
const DefaultDir = "/dev/input"

// readBatch — сколько событий читается за один системный вызов.
const readBatch = 64

// Config — настройки модуля из секции modules.input.
type Config struct {
	// Dir — каталог файлов устройств (по умолчанию /dev/input).
	Dir string `json:"dir"`
	// RetryAttempts — сколько раз пытаться открыть новое устройство при отказе в доступе
	// (udev выставляет права на новый файл с задержкой).
	RetryAttempts int `json:"retry_attempts"`
	// RetryDelayMS — пауза между попытками в миллисекундах.
	RetryDelayMS int `json:"retry_delay_ms"`
}

// deviceReader — открытое устройство: настоящее (*evdev.Device) или фейк в тестах.
type deviceReader interface {
	// Info возвращает сведения об устройстве.
	Info() ev.Info
	// ReadEvents блокирующе читает события в буфер.
	ReadEvents(buf []ev.Event) ([]ev.Event, error)
	// Close закрывает устройство и прерывает ожидающий ReadEvents.
	Close() error
}

// Module — модуль чтения физических устройств, реализует contracts.InputSource.
type Module struct {
	// open открывает устройство по пути.
	open func(path string) (deviceReader, error)
	// clk — часы для пауз между повторными попытками.
	clk clock.Clock
	// log — логгер модуля; bus — шина для событий подключения.
	log *slog.Logger
	bus contracts.Bus
	// cfg — настройки.
	cfg Config

	// ctx отменяется при остановке; wg ждёт завершения всех горутин модуля.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// watcher следит за появлением и удалением файлов в dir.
	watcher *fsnotify.Watcher

	// mu защищает devices, denied, own и subs.
	mu sync.RWMutex
	// devices — открытые устройства по пути.
	devices map[string]*openDevice
	// denied — пути устройств, к которым нет доступа.
	denied map[string]bool
	// own — пути собственных виртуальных устройств mKey (их не читаем).
	own map[string]bool
	// subs — подписчики на события.
	subs map[*subscriber]struct{}
	// dropped — число событий, отброшенных из-за медленных подписчиков.
	dropped atomic.Uint64
}

// openDevice — открытое устройство и его описание для подписчиков.
type openDevice struct {
	reader deviceReader
	desc   contracts.InputDevice
}

// subscriber — подписка на события устройств.
type subscriber struct {
	ch chan contracts.InputEvent
}

// New создаёт модуль, работающий с настоящим каталогом /dev/input.
func New() *Module {
	return newModule(DefaultDir, func(path string) (deviceReader, error) { return ev.Open(path) }, clock.Real{})
}

// newModule создаёт модуль с заданными каталогом, функцией открытия и часами (для тестов).
func newModule(dir string, open func(string) (deviceReader, error), clk clock.Clock) *Module {
	return &Module{
		open:    open,
		clk:     clk,
		cfg:     Config{Dir: dir, RetryAttempts: 10, RetryDelayMS: 100},
		devices: map[string]*openDevice{},
		denied:  map[string]bool{},
		own:     map[string]bool{},
		subs:    map[*subscriber]struct{}{},
	}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки и публикует сервис источника событий.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Запоминаем зависимости и читаем настройки поверх значений по умолчанию.
	m.log = host.Logger()
	m.bus = host.Bus()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}

	// Публикуем сервис.
	return contracts.ProvideService[contracts.InputSource](host.Services(), m)
}

// Start открывает все имеющиеся устройства и начинает следить за подключениями.
func (m *Module) Start(context.Context) error {
	// Контекст модуля живёт до Stop, а не до конца вызова Start.
	m.ctx, m.cancel = context.WithCancel(context.Background())

	// Подписываемся на изменения каталога до сканирования, чтобы не пропустить устройство,
	// подключённое между сканированием и подпиской. Без inotify модуль работает, но без hotplug.
	w, err := fsnotify.NewWatcher()
	if err == nil {
		err = w.Add(m.cfg.Dir)
	}
	if err != nil {
		m.log.Warn("device hotplug disabled", "dir", m.cfg.Dir, "err", err)
		if w != nil {
			_ = w.Close()
		}
	} else {
		m.watcher = w
		m.wg.Add(1)
		go m.watch()
	}

	// Открываем устройства, которые уже есть.
	paths, err := filepath.Glob(filepath.Join(m.cfg.Dir, "event*"))
	if err != nil {
		return fmt.Errorf("%s: list devices: %w", ModuleID, err)
	}
	for _, p := range paths {
		m.tryOpen(p)
	}
	m.publishStatus()
	return nil
}

// Stop прекращает слежение, закрывает устройства и закрывает каналы подписчиков.
func (m *Module) Stop(context.Context) error {
	// Модуль мог не стартовать (ошибка на Init) — тогда останавливать нечего.
	if m.cancel == nil {
		return nil
	}

	// Отменяем контекст и закрываем watcher: горутины повторных попыток и слежения завершатся.
	m.cancel()
	if m.watcher != nil {
		_ = m.watcher.Close()
	}

	// Закрываем устройства: это прерывает ожидающие ReadEvents в горутинах чтения.
	m.mu.Lock()
	for _, d := range m.devices {
		_ = d.reader.Close()
	}
	m.mu.Unlock()
	m.wg.Wait()

	// Закрываем каналы подписчиков.
	m.mu.Lock()
	defer m.mu.Unlock()
	for s := range m.subs {
		close(s.ch)
	}
	clear(m.subs)
	clear(m.devices)
	return nil
}

// Devices возвращает открытые устройства, отсортированные по пути.
func (m *Module) Devices() []contracts.InputDevice {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]contracts.InputDevice, 0, len(m.devices))
	for _, d := range m.devices {
		out = append(out, d.desc)
	}
	slices.SortFunc(out, func(a, b contracts.InputDevice) int { return strings.Compare(a.Info.Path, b.Info.Path) })
	return out
}

// Status возвращает число открытых устройств и пути недоступных.
func (m *Module) Status() contracts.InputStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := contracts.InputStatus{Open: len(m.devices)}
	for p := range m.denied {
		st.Denied = append(st.Denied, p)
	}
	slices.Sort(st.Denied)
	return st
}

// Subscribe подписывает на события всех устройств. Функция отписки закрывает канал.
func (m *Module) Subscribe(buffer int) (<-chan contracts.InputEvent, func()) {
	// Регистрируем подписчика.
	if buffer <= 0 {
		buffer = 1
	}
	s := &subscriber{ch: make(chan contracts.InputEvent, buffer)}
	m.mu.Lock()
	m.subs[s] = struct{}{}
	m.mu.Unlock()

	// Отписка: удалить и закрыть канал ровно один раз (если его ещё не закрыл Stop).
	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if _, ok := m.subs[s]; ok {
				delete(m.subs, s)
				close(s.ch)
			}
		})
	}
}

// Dropped возвращает число событий, отброшенных из-за медленных подписчиков.
func (m *Module) Dropped() uint64 { return m.dropped.Load() }

// watch обрабатывает события inotify каталога устройств до остановки модуля.
func (m *Module) watch() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			m.log.Warn("device watcher error", "err", err)
		case e, ok := <-m.watcher.Events:
			if !ok {
				return
			}
			m.handleFSEvent(e)
		}
	}
}

// handleFSEvent реагирует на появление, удаление и смену прав файла устройства.
func (m *Module) handleFSEvent(e fsnotify.Event) {
	// Интересуют только файлы eventN.
	if !strings.HasPrefix(filepath.Base(e.Name), "event") {
		return
	}

	switch {
	// Новый файл: права выставляются udev с задержкой, поэтому открываем с повторами в фоне.
	case e.Has(fsnotify.Create):
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.openWithRetry(e.Name)
		}()

	// Сменились права (например, пользователь только что выдал доступ): пробуем открыть недоступное.
	case e.Has(fsnotify.Chmod):
		m.mu.RLock()
		wasDenied := m.denied[e.Name]
		m.mu.RUnlock()
		if wasDenied {
			m.tryOpen(e.Name)
			m.publishStatus()
		}

	// Файл удалён: забываем про недоступность и «свои» устройства (само закрытие делает горутина чтения).
	case e.Has(fsnotify.Remove):
		m.mu.Lock()
		delete(m.denied, e.Name)
		delete(m.own, e.Name)
		m.mu.Unlock()
		m.publishStatus()
	}
}

// openWithRetry пытается открыть новое устройство, повторяя попытки при отказе в доступе.
func (m *Module) openWithRetry(path string) {
	delay := time.Duration(m.cfg.RetryDelayMS) * time.Millisecond
	for attempt := 0; attempt <= m.cfg.RetryAttempts; attempt++ {
		// Пауза перед повтором; остановка модуля прерывает ожидание.
		if attempt > 0 {
			if err := m.clk.Sleep(m.ctx, delay); err != nil {
				return
			}
		}

		// Пробуем открыть; повторяем только при отказе в доступе.
		if !m.tryOpen(path) {
			break
		}
	}
	m.publishStatus()
}

// tryOpen открывает устройство path, если оно ещё не открыто.
// Возвращает true, если стоит повторить попытку позже (отказ в доступе).
func (m *Module) tryOpen(path string) (retry bool) {
	// Пропускаем уже открытые и собственные устройства.
	m.mu.RLock()
	_, opened := m.devices[path]
	isOwn := m.own[path]
	m.mu.RUnlock()
	if opened || isOwn {
		return false
	}

	// Открываем устройство; отказ в доступе запоминаем для диагностики.
	r, err := m.open(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			m.mu.Lock()
			m.denied[path] = true
			m.mu.Unlock()
			return true
		}
		m.log.Debug("skip device", "path", path, "err", err)
		return false
	}

	// Собственные виртуальные устройства mKey не читаем никогда (защита от петель).
	info := r.Info()
	if strings.HasPrefix(info.Name, contracts.VirtualNamePrefix) || strings.HasPrefix(info.Phys, contracts.VirtualPhysPrefix) {
		_ = r.Close()
		m.mu.Lock()
		m.own[path] = true
		delete(m.denied, path)
		m.mu.Unlock()
		return false
	}

	// Регистрируем устройство и запускаем горутину чтения.
	d := &openDevice{reader: r, desc: contracts.InputDevice{Info: info, Kinds: ev.Classify(info.Caps)}}
	m.mu.Lock()
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		_ = r.Close()
		return false
	}
	m.devices[path] = d
	delete(m.denied, path)
	m.wg.Add(1)
	m.mu.Unlock()
	go m.read(path, d)

	// Сообщаем остальным модулям о новом устройстве.
	m.log.Info("device opened", "path", path, "name", info.Name, "id", info.ID.String(), "kinds", d.desc.Kinds)
	m.bus.Publish(contracts.TopicInputDeviceAdded, d.desc)
	return false
}

// read читает события устройства и раздаёт их подписчикам, пока устройство не отключат
// или модуль не остановят.
func (m *Module) read(path string, d *openDevice) {
	defer m.wg.Done()
	buf := make([]ev.Event, readBatch)
	for {
		// Блокирующее чтение; Close из Stop или отключение устройства прерывают его ошибкой.
		events, err := d.reader.ReadEvents(buf)
		if err != nil {
			m.detach(path, d, err)
			return
		}

		// Раздаём события подписчикам без блокировки.
		m.mu.RLock()
		for _, e := range events {
			ie := contracts.InputEvent{Device: path, Event: e}
			for s := range m.subs {
				select {
				case s.ch <- ie:
				default:
					m.dropped.Add(1)
				}
			}
		}
		m.mu.RUnlock()
	}
}

// detach убирает устройство после ошибки чтения. При остановке модуля ничего не публикует.
func (m *Module) detach(path string, d *openDevice, err error) {
	// При остановке модуля устройства закрывает Stop — это штатная ситуация.
	if m.ctx.Err() != nil {
		return
	}

	// Устройство отключено (ENODEV) или сломалось: закрываем и сообщаем.
	m.mu.Lock()
	if cur, ok := m.devices[path]; ok && cur == d {
		delete(m.devices, path)
	}
	m.mu.Unlock()
	_ = d.reader.Close()
	m.log.Info("device removed", "path", path, "name", d.desc.Info.Name, "reason", err)
	m.bus.Publish(contracts.TopicInputDeviceRemoved, d.desc)
	m.publishStatus()
}

// publishStatus сообщает на шину текущую доступность устройств.
func (m *Module) publishStatus() {
	if m.bus != nil {
		m.bus.Publish(contracts.TopicInputAccessChanged, m.Status())
	}
}

// Проверки на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module      = (*Module)(nil)
	_ contracts.InputSource = (*Module)(nil)
)
