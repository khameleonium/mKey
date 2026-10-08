package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/khameleonium/mKey/internal/app"
	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
	"github.com/khameleonium/mKey/internal/lib/bundle"
	"github.com/khameleonium/mKey/internal/registry"
)

// Самостоятельный файл макроса (FR-BUILD-1, ADR-0042): программа mkey, к концу которой модуль
// builder дописал проект, записи и скрипты (lib/bundle). При запуске такой файл не ищет
// установленный mKey: раскладывает содержимое во временную папку (свои настройки, проекты,
// записи — через переменные XDG), запускает модули без API, плагинов, обновлений и сборки,
// включает проект и работает, как он, — или выполняет одно событие и завершается.

// standaloneSkip — модули, которые собранному файлу не нужны: окно и команды терминала (api),
// плагины (их нет в файле), обновления и сборка.
var standaloneSkip = []string{"api", "plugins", "update", "builder"}

// eventWait — сколько ждать, пока включённый проект «взведётся» (движок получает его после
// перечитывания проектов), прежде чем выполнять событие в режиме «выполнить и выйти».
const eventWait = 5 * time.Second

// standaloneMain запускает собранный файл, если в программе есть содержимое: true — это
// собранный файл и code — код завершения; false — обычная программа mkey (или аргументы для
// обычных команд: например, `privileged` для установки прав).
func standaloneMain(tr *i18n.Translator, args []string) (handled bool, code int) {
	// Есть ли содержимое в конце своей программы.
	exe, err := os.Executable()
	if err != nil {
		return false, 0
	}
	c, err := bundle.ReadFile(exe)
	if errors.Is(err, bundle.ErrNoBundle) {
		return false, 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, tr.T("cli.standalone.damaged", i18n.A("error", err)))
		return true, 1
	}

	// Аргументы: без них — запуск; --info, --setup-access, --help — свои; прочие — обычные команды mkey.
	switch {
	case len(args) == 0:
		return true, runStandalone(tr, exe, c)
	case args[0] == "--info":
		printf(os.Stdout, "%s\n", standaloneInfo(tr, c))
		return true, 0
	case args[0] == "--setup-access":
		root := newRootCmd(tr)
		root.SetArgs([]string{"doctor", "--fix"})
		if err := root.Execute(); err != nil {
			fmt.Fprintln(os.Stderr, tr.T("cli.error.prefix", i18n.A("message", err)))
			return true, 1
		}
		return true, 0
	case args[0] == "--help" || args[0] == "-h":
		printf(os.Stdout, "%s\n\n%s\n", standaloneInfo(tr, c), tr.T("cli.standalone.help", i18n.A("file", filepath.Base(exe))))
		return true, 0
	}
	return false, 0
}

// standaloneInfo — сведения о собранном файле для человека.
func standaloneInfo(tr *i18n.Translator, c bundle.Contents) string {
	mode := tr.T("cli.standalone.mode.events")
	if c.Manifest.Mode == bundle.ModeOnce {
		mode = tr.T("cli.standalone.mode.once", i18n.A("event", c.Manifest.Event))
	}
	files := make([]string, 0, len(c.Files))
	for p := range c.Files {
		files = append(files, p)
	}
	slices.Sort(files)
	if len(files) == 0 {
		files = []string{tr.T("cli.standalone.no_files")}
	}
	return tr.T("cli.standalone.info", i18n.A("name", c.Manifest.Name), i18n.A("mode", mode),
		i18n.A("created", c.Manifest.Created), i18n.A("version", c.Manifest.Version),
		i18n.A("files", strings.Join(files, ", ")), i18n.A("creator", tr.T("app.creator")))
}

// runStandalone раскладывает содержимое во временную папку, запускает модули и работает, пока
// человек не выйдет (или выполняет событие и выходит). Возвращает код завершения.
func runStandalone(tr *i18n.Translator, exe string, c bundle.Contents) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, tr.T("cli.error.prefix", i18n.A("message", err)))
		return 1
	}

	// Временная личная папка: свои настройки, проекты, записи и журнал — установленный mKey и
	// его файлы не затрагиваются. Удаляется при выходе.
	root, err := os.MkdirTemp("", "mkey-macro-")
	if err != nil {
		return fail(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	for k, sub := range map[string]string{"XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state"} {
		_ = os.Setenv(k, filepath.Join(root, sub))
	}

	// Проект — до запуска (хранилище прочитает его при старте).
	projDir := filepath.Join(root, "config", "mkey", "projects")
	if err := os.MkdirAll(projDir, 0o700); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, c.Manifest.Project+".mkey.yaml"), c.Project, 0o600); err != nil {
		return fail(err)
	}

	// Журнал: предупреждения — в терминал, если он есть; иначе молча.
	logOut := io.Discard
	if isTerminal(os.Stderr) {
		logOut = os.Stderr
	}
	logger := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Модули без лишних; без проекта-примера; без устройств — только для проверок (MKEY_FAKE_BACKENDS).
	// В режиме «выполнить и выйти» нет и значка в трее: он появился бы на секунду и пропал, а
	// служба значков некоторых окружений падает от быстро исчезающих значков (SPEC §14, вопрос 5).
	skip := standaloneSkip
	if c.Manifest.Mode == bundle.ModeOnce {
		skip = append(slices.Clone(skip), "tray")
	}
	var modules []registry.Entry
	for _, e := range app.Modules() {
		if !slices.Contains(skip, e.Module.ID()) {
			modules = append(modules, e)
		}
	}
	a, err := app.New(app.Options{
		Lang:         tr.Lang(),
		Logger:       logger,
		Modules:      modules,
		FakeBackends: os.Getenv("MKEY_FAKE_BACKENDS") == "1",
		Config: func(id string) contracts.ConfigSection {
			if id == "store" {
				return registry.RawConfig(`{"example": false}`)
			}
			return nil
		},
	})
	if err != nil {
		return fail(err)
	}
	life := &lifecycle{started: time.Now(), done: make(chan struct{})}
	if err := contracts.ProvideService[contracts.Lifecycle](a.Manager.Services(), life); err != nil {
		return fail(err)
	}

	// Запуск; остановка — всегда (модули отпускают клавиши и убирают виртуальные устройства).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := a.Start(ctx); err != nil {
		return fail(err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Stop(sctx)
	}()
	s := a.Manager.Services()
	notify := func(title, body string) {
		if isTerminal(os.Stdout) {
			printf(os.Stdout, "%s\n", strings.TrimSpace(title+"\n"+body))
		}
		if n, err := contracts.LookupService[contracts.Notifier](s); err == nil {
			_ = n.Notify(context.Background(), title, body)
		}
	}
	title := tr.T("cli.standalone.title", i18n.A("name", c.Manifest.Name))

	// Нет доступа к устройствам ввода — объяснить, как его дать, и выйти.
	if dev, err := contracts.LookupService[contracts.VirtualDevices](s); err != nil || !hasOutput(dev) {
		notify(title, tr.T("cli.standalone.no_access", i18n.A("file", exe)))
		return 1
	}

	// Записи и скрипты — в папки модулей (места известны после запуска), затем включаем проект.
	if err := bundle.Extract(c, map[string]string{
		bundle.RecordingsDir: placeDir(a, contracts.PlaceRecordings),
		bundle.LuaDir:        placeDir(a, contracts.PlaceLuaScripts),
		bundle.ShellDir:      placeDir(a, contracts.PlaceShellScripts),
	}); err != nil {
		return fail(err)
	}
	projects, err := contracts.LookupService[contracts.Projects](s)
	if err != nil {
		return fail(err)
	}
	if err := projects.SetEnabled(c.Manifest.Project, true); err != nil {
		return fail(err)
	}

	// «Выполнить и выйти»: событие, когда проект «взведётся».
	if c.Manifest.Mode == bundle.ModeOnce {
		if err := runOnce(ctx, s, c.Manifest.Project, c.Manifest.Event); err != nil && ctx.Err() == nil {
			notify(title, tr.T("cli.standalone.event_failed", i18n.A("error", err)))
			return 1
		}
		return 0
	}

	// «Работать, как проект»: до выхода из меню значка, Ctrl+C или завершения сеанса.
	notify(title, tr.T("cli.standalone.started"))
	select {
	case <-ctx.Done():
	case <-life.done:
	}
	return 0
}

// hasOutput сообщает, что виртуальная клавиатура mKey создана (есть доступ к /dev/uinput).
func hasOutput(dev contracts.VirtualDevices) bool {
	_, err := dev.Keyboard()
	return err == nil
}

// placeDir — папка места id ("" — места нет: модуль не запущен).
func placeDir(a *app.App, id string) string {
	e, ok := a.Manager.Extensions().Get(contracts.PointPlace, id)
	if !ok {
		return ""
	}
	if p, ok := e.(contracts.Place); ok {
		return p.Path()
	}
	return ""
}

// runOnce выполняет событие проекта: ждёт, пока проект включится в движке (не дольше eventWait),
// и выполняет событие до конца.
func runOnce(ctx context.Context, s contracts.ServiceRegistry, project, event string) error {
	events, err := contracts.LookupService[contracts.Events](s)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(eventWait)
	for {
		err := events.RunEvent(ctx, project, event)
		if !errors.Is(err, contracts.ErrEventInactive) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
