package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/khameleonium/mKey/internal/app"
	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
	"github.com/khameleonium/mKey/internal/lib/config"
	"github.com/khameleonium/mKey/internal/lib/logfile"
	"github.com/khameleonium/mKey/internal/lib/paths"
	"github.com/khameleonium/mKey/internal/registry"
)

// Параметры журнала демона: 5 МиБ на файл, три старых файла (NFR-8).
const (
	logMaxBytes = 5 << 20
	logBackups  = 3
)

// logPath возвращает путь к журналу демона.
func logPath() string {
	return filepath.Join(paths.State(os.Getenv), "mkey.log")
}

// newDaemonCmd создаёт команду `mkey daemon` (фоновая часть mKey) и `mkey daemon stop` (T2.4).
func newDaemonCmd(tr *i18n.Translator) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: tr.T("cli.daemon.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemon(cmd, tr)
		},
	}
	cmd.Flags().Bool("fake-backends", false, tr.T("cli.daemon.flag.fake_backends"))
	cmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: tr.T("cli.daemon.stop.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return stopDaemon(cmd, tr)
		},
	})
	return cmd
}

// runDaemon запускает все модули и работает до сигнала завершения или команды `mkey daemon stop`.
func runDaemon(cmd *cobra.Command, tr *i18n.Translator) error {
	// Личный каталог времени выполнения и блокировка: второй демон того же пользователя не запустится.
	dir := runtimeDir()
	if err := paths.EnsurePrivateDir(dir); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := acquireLock(lock); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return errors.New(tr.T("cli.daemon.already_running"))
		}
		return err
	}

	// Журнал: JSON в файл с ротацией; при запуске из терминала — ещё и читаемый текст в терминал.
	file, err := logfile.Open(logPath(), logMaxBytes, logBackups)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	level := slog.LevelInfo
	if v, _ := cmd.Flags().GetBool("verbose"); v {
		level = slog.LevelDebug
	}
	handlers := []slog.Handler{slog.NewJSONHandler(file, &slog.HandlerOptions{Level: level})}
	if isTerminal(os.Stderr) {
		handlers = append(handlers, slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	}
	logger := slog.New(fanout(handlers))

	// Настройки из config.yaml: включённые модули и их секции (ошибка в файле — не запускаемся,
	// чтобы не работать с неожиданными настройками).
	cfgPath := filepath.Join(paths.Config(os.Getenv), config.FileName)

	// Старая версия схемы настроек (после обновления mKey) — перевести, сохранив копию (ADR-0030).
	if backup, err := config.Migrate(cfgPath); err != nil {
		return fmt.Errorf("%s: %w", cfgPath, err)
	} else if backup != "" {
		logger.Info("config migrated", "path", cfgPath, "backup", backup)
	}

	// Файл настроек дополняется шаблоном: в нём всегда видны все настройки с пояснениями
	// (нет файла — создаётся; значения пользователя не меняются). Ошибку в файле покажет Load.
	if changed, err := config.Complete(cfgPath, app.ConfigTemplate); err != nil {
		logger.Warn("config not completed", "path", cfgPath, "err", err)
	} else if changed {
		logger.Info("config completed", "path", cfgPath)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("%s: %w", cfgPath, err)
	}

	// Собираем программу из всех модулей и добавляем управление жизненным циклом демона.
	fake, _ := cmd.Flags().GetBool("fake-backends")
	a, err := app.New(app.Options{
		Lang:         tr.Lang(),
		Logger:       logger,
		FakeBackends: fake,
		Enabled:      cfg.Enabled,
		Config: func(id string) contracts.ConfigSection {
			if sec := cfg.Section(id); sec != nil {
				return registry.RawConfig(sec)
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	life := &lifecycle{started: time.Now(), done: make(chan struct{})}
	if err := contracts.ProvideService[contracts.Lifecycle](a.Manager.Services(), life); err != nil {
		return err
	}

	// Запуск; сигнал завершения или команда stop — корректная остановка.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := a.Start(ctx); err != nil {
		return err
	}
	logger.Info("daemon started", "pid", os.Getpid())
	if isTerminal(os.Stdout) {
		printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.daemon.started", i18n.A("pid", os.Getpid())))
	}
	select {
	case <-ctx.Done():
	case <-life.done:
	}

	// Остановка: модули отпускают клавиши и уничтожают виртуальные устройства (SEC-2).
	logger.Info("daemon stopping")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopErr := a.Stop(sctx)

	// Перезапуск после обновления: процесс заменяется новой программой с теми же аргументами
	// (блокировка демона и сокет закрыты при остановке — файлы открыты с O_CLOEXEC).
	if exe := life.restartExe(); exe != "" {
		logger.Info("daemon restarting", "exe", exe)
		args := append([]string{exe}, os.Args[1:]...)
		if err := syscall.Exec(exe, args, os.Environ()); err != nil {
			return fmt.Errorf("restart %s: %w", exe, err)
		}
	}
	return stopErr
}

// acquireLock берёт блокировку демона. Если её держит демон, который как раз завершается
// (например, сразу после `mkey daemon stop`), ждёт до 10 секунд.
func acquireLock(lock *os.File) error {
	var err error
	for range 100 {
		if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); !errors.Is(err, unix.EWOULDBLOCK) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

// stopDaemon просит работающий демон завершиться и ждёт, пока его процесс полностью выйдет
// (модули отпускают клавиши и уничтожают виртуальные устройства уже после закрытия API).
func stopDaemon(cmd *cobra.Command, tr *i18n.Translator) error {
	c := newClient(runtimeDir(), tr.Lang())
	out := cmd.OutOrStdout()

	// Номер процесса демона; не запущен — нечего останавливать.
	var st struct {
		PID int `json:"pid"`
	}
	err := c.do(cmd.Context(), "GET", "/api/v1/status", nil, &st)
	if errors.Is(err, errNotRunning) {
		printf(out, "%s\n", tr.T("cli.daemon.not_running"))
		return nil
	}
	if err != nil {
		return err
	}

	// Просим завершиться и ждём выхода процесса (до 10 секунд).
	if err := c.do(cmd.Context(), "POST", "/api/v1/shutdown", nil, nil); err != nil && !errors.Is(err, errNotRunning) {
		return err
	}
	for range 100 {
		if err := syscall.Kill(st.PID, 0); errors.Is(err, syscall.ESRCH) {
			printf(out, "%s\n", tr.T("cli.daemon.stopped"))
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("%s", tr.T("cli.daemon.stop_timeout"))
}

// lifecycle — реализация contracts.Lifecycle для процесса демона.
type lifecycle struct {
	started time.Time
	once    sync.Once
	done    chan struct{}
	// restart — программа, которую запустить вместо демона после остановки ("" — просто выйти).
	mu      sync.Mutex
	restart string
}

// Shutdown просит демон завершиться (повторные вызовы безопасны).
func (l *lifecycle) Shutdown() { l.once.Do(func() { close(l.done) }) }

// Restart просит демон завершиться и запустить вместо себя программу exe.
func (l *lifecycle) Restart(exe string) {
	l.mu.Lock()
	l.restart = exe
	l.mu.Unlock()
	l.Shutdown()
}

// restartExe возвращает программу для перезапуска ("" — не перезапускать).
func (l *lifecycle) restartExe() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.restart
}

// StartedAt возвращает время запуска демона.
func (l *lifecycle) StartedAt() time.Time { return l.started }

// fanoutHandler — slog.Handler, передающий записи сразу нескольким обработчикам.
type fanoutHandler []slog.Handler

// fanout объединяет обработчики.
func fanout(hs []slog.Handler) slog.Handler { return fanoutHandler(hs) }

// Enabled — запись нужна, если её принимает хотя бы один обработчик.
func (f fanoutHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

// Handle передаёт запись каждому обработчику, который её принимает.
func (f fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

// WithAttrs добавляет атрибуты во все обработчики.
func (f fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanoutHandler, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

// WithGroup добавляет группу во все обработчики.
func (f fanoutHandler) WithGroup(name string) slog.Handler {
	out := make(fanoutHandler, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
