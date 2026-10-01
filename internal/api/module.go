package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/config"
	"mkey/internal/lib/paths"
)

// ModuleID — идентификатор модуля.
const ModuleID = "api"

// Имена файлов в каталоге времени выполнения.
const (
	// SocketFile — Unix-сокет API для CLI.
	SocketFile = "mkey.sock"
	// TokenFile — токен доступа к TCP API.
	TokenFile = "token"
	// InfoFile — сведения о запущенном API (порт, PID) для CLI и GUI.
	InfoFile = "api.json"
)

// DefaultPort — порт веб-интерфейса по умолчанию.
const DefaultPort = 17420

// Config — настройки модуля из секции modules.api.
type Config struct {
	// Port — TCP-порт окна программы на 127.0.0.1 (0 — не открывать TCP, только Unix-сокет;
	// в консольной сборке без окна не открывается никогда).
	Port int `json:"port"`
	// RuntimeDir — каталог сокета и токена; пусто — из модуля platform.
	RuntimeDir string `json:"runtime_dir"`
	// LogFile — журнал демона для /logs (по умолчанию ~/.local/state/mkey/mkey.log).
	LogFile string `json:"log_file"`
	// ConfigFile — файл настроек, куда сохраняются системные сочетания (по умолчанию ~/.config/mkey/config.yaml).
	ConfigFile string `json:"config_file"`
	// Theme — тема окна программы: system (как в системе, по умолчанию), light или dark.
	Theme string `json:"theme"`
}

// Info — содержимое api.json: как подключиться к запущенному демону.
type Info struct {
	// Port — TCP-порт веб-интерфейса (0, если TCP не открыт).
	Port int `json:"port"`
	// PID — идентификатор процесса демона.
	PID int `json:"pid"`
}

// Module — модуль HTTP API.
type Module struct {
	// log — логгер; cfg — настройки; tr — переводчик (язык ответа выбирается по запросу); bus — шина.
	log *slog.Logger
	cfg Config
	tr  contracts.Translator
	bus contracts.Bus
	// svc — сервисы других модулей (любой может быть nil).
	svc services
	// static — файлы веб-интерфейса (nil — без GUI).
	static fs.FS

	// token — секрет доступа к TCP API.
	token string
	// servers — запущенные HTTP-серверы; wg ждёт их завершения.
	mu      sync.Mutex
	servers []*http.Server
	wg      sync.WaitGroup
	// port — фактически открытый TCP-порт.
	port int
}

// services — сервисы, которыми пользуются обработчики.
type services struct {
	runner   contracts.SequenceRunner
	devices  contracts.VirtualDevices
	input    contracts.InputSource
	doctor   contracts.Doctor
	modules  contracts.Modules
	life     contracts.Lifecycle
	layouts  contracts.LayoutProvider
	platform contracts.Platform
	projects contracts.Projects
	events   contracts.Events
	keyState contracts.KeyState
	convert  contracts.ActionConverter
	recorder contracts.Recorder
	player   contracts.Player
	inspect  contracts.Inspector
	vdevs    contracts.VirtualDeviceManager
	plugins  contracts.Plugins
	updater  contracts.Updater
	ext      contracts.ExtensionRegistry
}

// New создаёт модуль. static — файлы веб-интерфейса (например, web.FS()) или nil.
// Без веб-интерфейса (консольная сборка) TCP-порт по умолчанию не открывается: командам
// терминала хватает Unix-сокета.
func New(static fs.FS) *Module {
	port := DefaultPort
	if static == nil {
		port = 0
	}
	return &Module{static: static, cfg: Config{Port: port, Theme: "system"}}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки и собирает сервисы других модулей. Отсутствующие сервисы допустимы.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Настройки.
	m.log = host.Logger()
	m.tr = host.I18n()
	m.bus = host.Bus()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}

	// Сервисы (lookup возвращает нулевое значение, если модуль-поставщик отключён).
	s := host.Services()
	m.svc = services{
		runner:   lookup[contracts.SequenceRunner](s),
		devices:  lookup[contracts.VirtualDevices](s),
		input:    lookup[contracts.InputSource](s),
		doctor:   lookup[contracts.Doctor](s),
		modules:  lookup[contracts.Modules](s),
		life:     lookup[contracts.Lifecycle](s),
		layouts:  lookup[contracts.LayoutProvider](s),
		platform: lookup[contracts.Platform](s),
		projects: lookup[contracts.Projects](s),
		events:   lookup[contracts.Events](s),
		keyState: lookup[contracts.KeyState](s),
		convert:  lookup[contracts.ActionConverter](s),
		recorder: lookup[contracts.Recorder](s),
		player:   lookup[contracts.Player](s),
		inspect:  lookup[contracts.Inspector](s),
		vdevs:    lookup[contracts.VirtualDeviceManager](s),
		plugins:  lookup[contracts.Plugins](s),
		updater:  lookup[contracts.Updater](s),
		ext:      host.Extensions(),
	}

	// Каталог времени выполнения: из настроек или из модуля platform.
	if m.cfg.RuntimeDir == "" && m.svc.platform != nil {
		m.cfg.RuntimeDir = m.svc.platform.Info().RuntimeDir
	}
	if m.cfg.RuntimeDir == "" {
		return fmt.Errorf("%s: runtime directory is unknown", ModuleID)
	}
	if m.cfg.LogFile == "" {
		m.cfg.LogFile = filepath.Join(paths.State(os.Getenv), "mkey.log")
	}
	if m.cfg.ConfigFile == "" {
		m.cfg.ConfigFile = filepath.Join(paths.Config(os.Getenv), config.FileName)
	}
	if !slices.Contains(interfaceThemes, m.cfg.Theme) {
		return fmt.Errorf("%s: theme must be one of system, light, dark (got %q)", ModuleID, m.cfg.Theme)
	}

	// Файл настроек и журнал — в списке «Где что лежит».
	for _, p := range []contracts.StaticPlace{
		{M: contracts.ExtensionMeta{ID: contracts.PlaceConfig, NameKey: "place.config", DescriptionKey: "place.config.description", Provider: ModuleID}, P: m.cfg.ConfigFile, N: 10},
		{M: contracts.ExtensionMeta{ID: contracts.PlaceLog, NameKey: "place.log", DescriptionKey: "place.log.description", Provider: ModuleID}, P: m.cfg.LogFile, N: 90},
	} {
		if err := host.Extensions().Register(contracts.PointPlace, p); err != nil {
			return err
		}
	}

	// Адрес веб-интерфейса нужен значку в трее.
	return contracts.ProvideService[contracts.GUIServer](s, m)
}

// GUIURL возвращает адрес входа в веб-интерфейс (contracts.GUIServer).
// Порт и токен задаются в Start, до запуска модулей, которые пользуются этим адресом.
func (m *Module) GUIURL() (string, error) {
	if m.port == 0 || m.token == "" {
		return "", errors.New("web interface is not listening")
	}
	return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", m.port, m.token), nil
}

// lookup возвращает сервис контракта T или нулевое значение, если его нет.
func lookup[T any](s contracts.ServiceRegistry) T {
	v, _ := contracts.LookupService[T](s)
	return v
}

// Start создаёт токен, открывает Unix-сокет и TCP-порт и запускает серверы.
func (m *Module) Start(context.Context) error {
	dir := m.cfg.RuntimeDir

	// Личный каталог времени выполнения (0700, владелец — текущий пользователь).
	if err := paths.EnsurePrivateDir(dir); err != nil {
		return fmt.Errorf("%s: runtime dir: %w", ModuleID, err)
	}

	// Новый токен при каждом запуске.
	token, err := newToken()
	if err != nil {
		return err
	}
	m.token = token
	if err := os.WriteFile(filepath.Join(dir, TokenFile), []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("%s: write token: %w", ModuleID, err)
	}

	// Unix-сокет для CLI: старый файл (после аварийного завершения) удаляется.
	sock := filepath.Join(dir, SocketFile)
	_ = os.Remove(sock)
	ul, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("%s: listen %s: %w", ModuleID, sock, err)
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = ul.Close()
		return err
	}
	m.serve(ul, m.routes(true))

	// TCP для веб-интерфейса; занятый порт не мешает работе CLI. Без веб-интерфейса (консольная
	// сборка) порт не открывается, даже если задан в config.yaml: командам хватает Unix-сокета.
	if m.cfg.Port > 0 && m.static != nil {
		tl, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(m.cfg.Port)))
		if err != nil {
			m.log.Warn("web interface port unavailable", "port", m.cfg.Port, "err", err)
		} else {
			m.port = tl.Addr().(*net.TCPAddr).Port
			m.serve(tl, m.secure(m.routes(false)))
		}
	}

	// Сведения для клиентов.
	info, _ := json.Marshal(Info{Port: m.port, PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, InfoFile), info, 0o600); err != nil {
		return fmt.Errorf("%s: write info: %w", ModuleID, err)
	}
	m.log.Info("api started", "socket", sock, "port", m.port)
	return nil
}

// serve запускает HTTP-сервер на слушателе l в фоне.
func (m *Module) serve(l net.Listener, h http.Handler) {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	m.mu.Lock()
	m.servers = append(m.servers, srv)
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.log.Error("api server stopped", "err", err)
		}
	}()
}

// Stop останавливает серверы и удаляет файлы сокета, токена и сведений.
func (m *Module) Stop(ctx context.Context) error {
	// Корректно закрываем серверы; долгие запросы (выполняющиеся макросы) прерываются по таймауту.
	m.mu.Lock()
	servers := m.servers
	m.servers = nil
	m.mu.Unlock()
	sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var errs []error
	for _, srv := range servers {
		if err := srv.Shutdown(sctx); err != nil {
			errs = append(errs, srv.Close())
		}
	}
	m.wg.Wait()

	// Убираем файлы, чтобы клиенты не пытались подключиться к остановленному демону.
	for _, f := range []string{SocketFile, TokenFile, InfoFile} {
		if err := os.Remove(filepath.Join(m.cfg.RuntimeDir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// newToken создаёт случайный токен (256 бит).
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("%s: token: %w", ModuleID, err)
	}
	return hex.EncodeToString(b), nil
}

// Проверка на этапе компиляции, что Module реализует контракт.
var _ contracts.Module = (*Module)(nil)
