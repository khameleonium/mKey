package recorder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/clock"
	"mkey/internal/lib/paths"
)

// ModuleID — идентификатор модуля: имя секции в config.yaml и префикс i18n-ключей.
const ModuleID = "recorder"

// Config — настройки модуля из секции modules.recorder в config.yaml.
type Config struct {
	// Dir — каталог записей; пусто — ~/.local/share/mkey/recordings.
	Dir string `json:"dir"`
	// Hotkey — сочетание «начать/закончить запись» из любой программы; пусто — выключено.
	Hotkey string `json:"hotkey"`
	// Kinds — какие устройства записывать по умолчанию (keyboard, mouse, touchpad, gamepad…).
	Kinds []string `json:"kinds"`
	// CoalesceMS — окно склейки перемещений мыши при воспроизведении, мс (0 — не склеивать).
	CoalesceMS int `json:"coalesce_ms"`
}

// Module — реализация contracts.Module для модуля «recorder».
type Module struct {
	// log — логгер; cfg — настройки; bus — шина (заполняются в Init).
	log *slog.Logger
	cfg Config
	bus contracts.Bus
	// clk — часы (подменяются в тестах); now — текущее время для меток записи.
	clk clock.Clock
	// input и devs — ввод и вывод (любой может быть nil, если модуль отключён).
	input contracts.InputSource
	devs  contracts.VirtualDevices
	// projects и events — хранилище проектов и движок (для превращения записи в блоки; любой может быть nil);
	// tr — переводчик названий созданного проекта.
	projects contracts.Projects
	events   contracts.Events
	tr       contracts.Translator

	// mu защищает sess, plays и chord.
	mu sync.Mutex
	// sess — идущая запись (nil — запись не идёт).
	sess *session
	// hotkey — отслеживание сочетания «начать/закончить запись».
	hotkey *chord
	// plays — отмена идущих воспроизведений.
	plays map[*int]context.CancelFunc

	// Фоновое чтение событий и подписки на шину.
	cancel context.CancelFunc
	wg     sync.WaitGroup
	unsub  []func()
}

// New создаёт модуль. Зависимости модуль получает в Init, а не в конструкторе.
func New() *Module {
	return &Module{
		clk:   clock.Real{},
		cfg:   Config{Hotkey: "^{Ctrl}^{Alt}{R}", Kinds: []string{"keyboard", "mouse"}, CoalesceMS: 4},
		plays: map[*int]context.CancelFunc{},
	}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки, находит ввод и вывод, публикует сервисы и регистрирует действие play.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Логгер, шина и секция конфига.
	m.log = host.Logger()
	m.bus = host.Bus()
	m.tr = host.I18n()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	if m.cfg.Dir == "" {
		m.cfg.Dir = filepath.Join(paths.Data(os.Getenv), "recordings")
	}

	// Сочетание записи: неверное — ошибка настройки.
	if m.cfg.Hotkey != "" {
		c, err := parseChord(m.cfg.Hotkey)
		if err != nil {
			return fmt.Errorf("%s: hotkey: %w", ModuleID, err)
		}
		m.hotkey = c
	}

	// Ввод и вывод других модулей.
	s := host.Services()
	m.input, _ = contracts.LookupService[contracts.InputSource](s)
	m.devs, _ = contracts.LookupService[contracts.VirtualDevices](s)
	m.projects, _ = contracts.LookupService[contracts.Projects](s)
	m.events, _ = contracts.LookupService[contracts.Events](s)

	// Сервисы и действие play.
	if err := contracts.ProvideService[contracts.Recorder](s, m); err != nil {
		return err
	}
	if err := contracts.ProvideService[contracts.Player](s, m); err != nil {
		return err
	}
	if err := host.Extensions().Register(contracts.PointAction, centerAction{m: m}); err != nil {
		return err
	}
	return host.Extensions().Register(contracts.PointAction, playAction{m: m})
}

// Start начинает читать события ввода (для записи и сочетания) и следить за экстренной остановкой.
func (m *Module) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	// Экстренная остановка прерывает воспроизведения (клавиши отпустит модуль output).
	emer, u := m.bus.Subscribe(contracts.TopicEmergency)
	m.unsub = append(m.unsub, u)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-emer:
				if !ok {
					return
				}
				m.StopPlayback()
				m.stopOnEmergency()
			}
		}
	}()

	// События ввода: запись и сочетание «начать/закончить». Большой буфер — чтобы не терять
	// события мыши с частотой 1000 Гц, пока пишется файл.
	if m.input != nil {
		events, unsub := m.input.Subscribe(8192)
		m.unsub = append(m.unsub, unsub)
		m.wg.Add(1)
		go m.readLoop(ctx, events)
	}
	return nil
}

// Stop прерывает воспроизведения, сохраняет идущую запись и останавливает фоновую работу.
func (m *Module) Stop(context.Context) error {
	m.StopPlayback()
	if _, err := m.StopRecording(""); err != nil && !errors.Is(err, contracts.ErrNotRecording) {
		m.log.Warn("recording not saved on shutdown", "err", err)
	}
	if m.cancel != nil {
		m.cancel()
	}
	for _, u := range m.unsub {
		u()
	}
	m.wg.Wait()
	return nil
}

// readLoop передаёт события ввода идущей записи и отслеживает сочетание записи.
func (m *Module) readLoop(ctx context.Context, events <-chan contracts.InputEvent) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-events:
			if !ok {
				return
			}
			m.handle(e)
		}
	}
}

// handle обрабатывает одно событие: пишет его в запись и проверяет сочетание записи.
func (m *Module) handle(e contracts.InputEvent) {
	// У событий без метки времени ядра (редко, например у тестовых) — текущее время.
	if e.Event.Time.IsZero() {
		e.Event.Time = m.now()
	}

	m.mu.Lock()
	sess, hotkey := m.sess, m.hotkey
	var toggled bool
	if hotkey != nil {
		idx := 0
		if sess != nil {
			idx = sess.total()
		}
		toggled = hotkey.feed(e.Event, idx, m.clk.Now())
	}

	// Событие — в запись (включая нажатия сочетания: они вырежутся при остановке).
	if sess != nil {
		sess.add(e, m.devices)
	}
	m.mu.Unlock()

	// Сочетание нажато: запись идёт — останавливаем (с вырезанием), нет — начинаем.
	if !toggled {
		return
	}
	if sess != nil {
		if _, err := m.stop(sess, cutAt(hotkey, m.now())); err != nil {
			m.log.Warn("recording not saved", "err", err)
		}
		return
	}
	if _, err := m.StartRecording(contracts.RecordOptions{}); err != nil {
		m.log.Warn("recording not started", "err", err)
	}
}

// stopOnEmergency заканчивает идущую запись после экстренной остановки, вырезая само
// сочетание остановки (всё с момента, когда в последний раз не было нажатых клавиш).
func (m *Module) stopOnEmergency() {
	m.mu.Lock()
	s := m.sess
	cut := -1
	if s != nil {
		cut = s.quiet
	}
	m.mu.Unlock()
	if s == nil {
		return
	}
	if _, err := m.stop(s, cut); err != nil {
		m.log.Warn("recording not saved", "err", err)
	}
}

// devices возвращает устройства ввода по путям (для имён и классов в заголовке записи).
func (m *Module) devices() map[string]contracts.InputDevice {
	out := map[string]contracts.InputDevice{}
	if m.input == nil {
		return out
	}
	for _, d := range m.input.Devices() {
		out[d.Info.Path] = d
	}
	return out
}

// now возвращает текущее время часов модуля.
func (m *Module) now() time.Time { return m.clk.Now() }

// Проверка на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module   = (*Module)(nil)
	_ contracts.Recorder = (*Module)(nil)
	_ contracts.Player   = (*Module)(nil)
)
