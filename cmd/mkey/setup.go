package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"mkey/internal/api"
	"mkey/internal/app"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/platform"
	"mkey/internal/platform/detect"
	"mkey/internal/platform/devaccess"
	"mkey/internal/platform/elevate"
	"mkey/internal/platform/initsys"
	"mkey/internal/registry"
	"mkey/internal/session"
	"mkey/internal/setup"
	"mkey/internal/setup/install"
	"mkey/internal/setup/manifest"
)

// outputTestText — что mKey напечатает при проверке нажатий: только цифры и пробел,
// они одинаково набираются в любой раскладке.
const outputTestText = "12345 67890"

// newSetupCmd создаёт команду `mkey setup` — мастер установки в терминале (T4.4, FR-INST-2).
func newSetupCmd(tr *i18n.Translator) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use: "setup", Short: tr.T("cli.setup.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runSetup(cmd, tr, yes) },
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, tr.T("cli.setup.flag.yes"))
	return cmd
}

// newUninstallCmd создаёт команду `mkey uninstall [--keep-config|--all] [--yes]` (T4.5, FR-INST-5).
func newUninstallCmd(tr *i18n.Translator) *cobra.Command {
	var keep, all, yes bool
	cmd := &cobra.Command{
		Use: "uninstall", Short: tr.T("cli.uninstall.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if keep && all {
				return errors.New(tr.T("cli.uninstall.flags_conflict"))
			}
			var keepConfig *bool
			switch {
			case keep:
				keepConfig = &keep
			case all:
				f := false
				keepConfig = &f
			}
			return runUninstall(cmd, tr, keepConfig, yes)
		},
	}
	cmd.Flags().BoolVar(&keep, "keep-config", false, tr.T("cli.uninstall.flag.keep"))
	cmd.Flags().BoolVar(&all, "all", false, tr.T("cli.uninstall.flag.all"))
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, tr.T("cli.uninstall.flag.yes"))
	return cmd
}

// newGUICmd создаёт команду `mkey gui` — открыть веб-интерфейс в браузере.
func newGUICmd(tr *i18n.Translator) *cobra.Command {
	return &cobra.Command{
		Use: "gui", Short: tr.T("cli.gui.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runGUI(cmd, tr) },
	}
}

// runDefault — действие `mkey` без параметров: не установлен — мастер установки
// (в терминале или в новом окне терминала), установлен — открыть интерфейс.
func runDefault(cmd *cobra.Command, tr *i18n.Translator) error {
	ienv := newInstallEnv()
	if ienv.Installed() {
		return runGUI(cmd, tr)
	}

	// Запуск из терминала — мастер прямо здесь.
	if isTerminal(os.Stdin) {
		return runSetup(cmd, tr, false)
	}

	// Двойной щелчок в файловом менеджере: открываем окно терминала с мастером.
	term, prefix, ok := elevate.FindTerminal(os.Getenv, lookPath)
	if !ok {
		return errors.New(tr.T("cli.setup.no_terminal", i18n.A("exe", ienv.Exe)))
	}
	args := append(append([]string{}, prefix...), ienv.Exe, "setup")
	return exec.Command(term, args...).Start()
}

// newInstallEnv возвращает пути установки для текущего пользователя.
func newInstallEnv() install.Env {
	exe, _ := os.Executable()
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return install.NewEnv(os.Getenv, runtimeDir(), exe)
}

// lookPath сообщает, установлена ли программа.
func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// dialog — вопросы пользователю в терминале.
type dialog struct {
	cmd *cobra.Command
	tr  *i18n.Translator
	yes bool
}

// say печатает переведённое сообщение.
func (d dialog) say(key string, args ...contracts.Arg) {
	printf(d.cmd.OutOrStdout(), "%s\n", d.tr.T(key, args...))
}

// ask задаёт вопрос «да/нет». def — ответ по умолчанию (Enter). С --yes возвращает def.
func (d dialog) ask(key string, def bool, args ...contracts.Arg) bool {
	if d.yes {
		return def
	}
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	printf(d.cmd.OutOrStdout(), "%s %s: ", d.tr.T(key, args...), hint)
	switch a := strings.ToLower(strings.TrimSpace(readLine(d.cmd.InOrStdin()))); a {
	case "":
		return def
	case "y", "yes", "д", "да":
		return true
	default:
		return false
	}
}

// readLine читает одну строку побайтово (без упреждающего чтения, чтобы не «съесть» следующие ответы).
func readLine(r io.Reader) string {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				return b.String()
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			return b.String()
		}
	}
}

// autostartEnv собирает окружение способов автозапуска.
func autostartEnv(ienv install.Env, m *manifest.Manifest) contracts.AutostartEnv {
	return contracts.AutostartEnv{
		Home: ienv.Home, ConfigHome: ienv.ConfigHome, Exe: ienv.BinPath(),
		Session: session.Detect(os.Getenv), Platform: detect.Detect(detect.OS{}),
		Runner: devaccess.ExecRunner{}, Files: manifest.Writer{M: m, Owner: "autostart"},
	}
}

// runSetup — мастер установки: файлы, права, автозапуск, запуск и проверка.
func runSetup(cmd *cobra.Command, tr *i18n.Translator, yes bool) error {
	d := dialog{cmd: cmd, tr: tr, yes: yes}
	interactive := isTerminal(os.Stdin)
	if !interactive && !yes {
		return userError(tr.T("cli.setup.need_yes"))
	}
	ctx := cmd.Context()
	ienv := newInstallEnv()
	m, err := manifest.Load(ienv.ManifestPath())
	if err != nil {
		return err
	}

	// Приветствие.
	d.say("cli.setup.welcome")

	// Шаг 1: программа и ярлык в меню.
	d.say("cli.setup.step.files", i18n.A("bin", ienv.BinPath()))
	if !d.ask("cli.setup.ask.files", true) {
		d.say("cli.setup.cancelled")
		return nil
	}
	if err := install.Files(ienv, manifest.Writer{M: m, Owner: "program"}); err != nil {
		return err
	}
	d.say("cli.setup.done.files")

	// Шаг 2: права на устройства ввода (как `mkey doctor --fix`).
	d.say("cli.setup.step.access")
	if err := setupAccess(cmd, tr, yes); err != nil {
		return err
	}

	// Шаг 3: автозапуск.
	aenv := autostartEnv(ienv, m)
	auto := initsys.Select(initsys.All(), aenv)
	autoOK := false
	switch id := auto.Meta().ID; id {
	case "manual":
		d.say("cli.setup.autostart.manual", i18n.A("command", auto.Target(aenv)))
	default:
		key := "cli.setup.autostart." + id
		if id != "xdg" && id != "systemd-user" {
			key = "cli.setup.autostart.compositor"
		}
		d.say(key, i18n.A("target", auto.Target(aenv)))
		if d.ask("cli.setup.ask.autostart", true) {
			if err := auto.Install(ctx, aenv); err != nil {
				d.say("cli.setup.autostart.failed", i18n.A("error", err))
			} else {
				autoOK = true
				d.say("cli.setup.done.autostart")
			}
		}
	}

	// Шаг 4: запускаем установленную копию (прежний демон перезапускается).
	d.say("cli.setup.step.start")
	_ = stopDaemonQuiet(ctx, tr)
	started := false
	if autoOK {
		started = auto.Start(ctx, aenv) == nil
	}
	if !started {
		if err := spawnDaemon(ienv.BinPath(), tr.Lang()); err != nil {
			return err
		}
	}
	c := newClient(runtimeDir(), tr.Lang())
	if !waitDaemon(ctx, c) {
		return errors.New(tr.T("cli.daemon.autostart_failed", i18n.A("error", "timeout")))
	}
	d.say("cli.setup.done.start")

	// Шаг 5: проверка, что mKey видит и нажимает клавиши (только в интерактивном режиме).
	if interactive && !yes {
		setupCheck(ctx, d, c)
	}

	// Готово.
	d.say("cli.setup.finished", i18n.A("bin", ienv.BinPath()))
	return nil
}

// setupAccess проверяет доступ к устройствам и при необходимости выдаёт его (с объяснением и согласием).
func setupAccess(cmd *cobra.Command, tr *i18n.Translator, yes bool) error {
	entries := []registry.Entry{
		{Module: session.New(), Core: true},
		{Module: platform.New(), Core: true},
		{Module: setup.New(), Core: true},
	}
	return withModules(cmd.Context(), cmd, tr, entries, func(a *app.App) error {
		doctor, err := contracts.LookupService[contracts.Doctor](a.Manager.Services())
		if err != nil {
			return err
		}
		plat, err := contracts.LookupService[contracts.Platform](a.Manager.Services())
		if err != nil {
			return err
		}
		checks := doctor.Run(cmd.Context())
		if !hasFix(checks) {
			printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.setup.access.ok"))
			return nil
		}
		return runFix(cmd.Context(), cmd, tr, doctor, plat, checks, yes)
	})
}

// setupCheck просит нажать пробел (mKey видит нажатия) и печатает цифры в терминал (mKey нажимает клавиши).
func setupCheck(ctx context.Context, d dialog, c *client) {
	// Ввод: ждём пробел.
	d.say("cli.setup.check.press_space")
	err := c.do(ctx, http.MethodPost, "/api/v1/wait/key", map[string]any{"key": "Space", "timeout_ms": 20000}, nil)
	if err != nil {
		d.say("cli.setup.check.input_failed")
	} else {
		d.say("cli.setup.check.input_ok")
	}
	_ = readLine(d.cmd.InOrStdin()) // пробел остался во вводе терминала — дочитываем строку после Enter

	// Вывод: mKey печатает цифры в это окно и нажимает Enter; проверяем, что строка пришла.
	d.say("cli.setup.check.typing")
	go func() {
		time.Sleep(700 * time.Millisecond)
		_ = c.do(context.WithoutCancel(ctx), http.MethodPost, "/api/v1/send", map[string]any{"sequence": `{"` + outputTestText + `"}{Enter}`}, nil)
	}()
	if strings.Contains(readLine(d.cmd.InOrStdin()), outputTestText) {
		d.say("cli.setup.check.output_ok")
	} else {
		d.say("cli.setup.check.output_failed")
	}
}

// runUninstall удаляет mKey: демон, автозапуск, правила доступа, файлы по манифесту, данные.
// keepConfig == nil — спросить пользователя.
func runUninstall(cmd *cobra.Command, tr *i18n.Translator, keepConfig *bool, yes bool) error {
	d := dialog{cmd: cmd, tr: tr, yes: yes}
	if !isTerminal(os.Stdin) && !yes {
		return userError(tr.T("cli.uninstall.need_yes"))
	}
	ctx := cmd.Context()
	ienv := newInstallEnv()

	// Что удаляется и согласие.
	d.say("cli.uninstall.intro")
	var keep bool
	if keepConfig != nil {
		keep = *keepConfig
	} else {
		keep = d.ask("cli.uninstall.ask.keep", true, i18n.A("config", ienv.Config))
	}
	if !d.ask("cli.uninstall.ask.confirm", yes) {
		d.say("cli.setup.cancelled")
		return nil
	}
	var problems []string

	// 1. Останавливаем демон: он отпускает клавиши и уничтожает виртуальные устройства.
	_ = stopDaemonQuiet(ctx, tr)

	// 2. Автозапуск — всеми способами, которые настроены.
	m, err := manifest.Load(ienv.ManifestPath())
	if err != nil {
		return err
	}
	aenv := autostartEnv(ienv, m)
	for _, a := range initsys.All() {
		if a.Installed(aenv) {
			if err := a.Uninstall(ctx, aenv); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}

	// 3. Правила доступа к устройствам (нужны права администратора).
	info := detect.Detect(detect.OS{})
	if access := pickAccess(info); access != nil && access.Installed("/") {
		d.say("cli.uninstall.rules")
		if err := removeRules(ctx, d, ienv.Exe); err != nil {
			problems = append(problems, err.Error())
		}
		if access.RequiresRelogin() {
			d.say("cli.uninstall.group_note")
		}
	}

	// 4. Файлы по манифесту (программа — последней, её удаление не мешает работе этой команды).
	for _, err := range m.Undo(nil) {
		problems = append(problems, err.Error())
	}

	// 5. Данные: полностью или с сохранением настроек и проектов.
	for _, err := range install.RemoveData(ienv, keep) {
		problems = append(problems, err.Error())
	}

	// Итог.
	if len(problems) == 0 {
		if keep {
			d.say("cli.uninstall.done_keep", i18n.A("config", ienv.Config))
		} else {
			d.say("cli.uninstall.done_all")
		}
		return nil
	}
	d.say("cli.uninstall.problems")
	for _, p := range problems {
		printf(cmd.OutOrStdout(), "  - %s\n", p)
	}
	return nil
}

// removeRules удаляет правила доступа через лучший доступный способ повышения прав.
func removeRules(ctx context.Context, d dialog, exe string) error {
	env := elevate.Env{
		Getenv: os.Getenv, LookPath: lookPath, IsTTY: isTerminal(os.Stdin),
		Graphical: session.Detect(os.Getenv).Graphical(), Exec: elevate.RealExec,
		Prompt: d.tr.T("platform.elevate.prompt"), PressEnter: d.tr.T("platform.elevate.press_enter"),
	}
	argv := []string{exe, "privileged", "uninstall-rules"}
	for _, e := range elevate.All(env) {
		if !e.Available() {
			continue
		}
		err := e.Run(ctx, argv)
		var manual *contracts.ManualActionError
		if errors.As(err, &manual) {
			d.say("cli.doctor.fix.manual", i18n.A("command", manual.Command))
			return nil
		}
		return err
	}
	return nil
}

// runGUI открывает веб-интерфейс в браузере (при необходимости запустив демон).
func runGUI(cmd *cobra.Command, tr *i18n.Translator) error {
	c, err := daemonClient(cmd, tr)
	if err != nil {
		return err
	}

	// Порт и токен из каталога времени выполнения.
	dir := runtimeDir()
	var info api.Info
	data, err := os.ReadFile(filepath.Join(dir, api.InfoFile))
	if err == nil {
		err = json.Unmarshal(data, &info)
	}
	token, terr := os.ReadFile(filepath.Join(dir, api.TokenFile))
	if err != nil || terr != nil || info.Port == 0 {
		return errors.New(tr.T("cli.gui.unavailable"))
	}
	_ = c

	// Открываем адрес со входом по токену.
	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", info.Port, strings.TrimSpace(string(token)))
	printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.gui.opening", i18n.A("url", fmt.Sprintf("http://127.0.0.1:%d", info.Port))))
	return exec.Command("xdg-open", url).Start()
}

// spawnDaemon запускает `exe daemon` отдельным процессом в новой сессии (переживёт закрытие терминала).
func spawnDaemon(exe, lang string) error {
	cmd := exec.Command(exe, "daemon", "--lang", lang)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// waitDaemon ждёт, пока демон начнёт отвечать (до 5 секунд).
func waitDaemon(ctx context.Context, c *client) bool {
	for range 50 {
		if err := c.do(ctx, http.MethodGet, "/api/v1/status", nil, nil); err == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// stopDaemonQuiet останавливает демон, если он запущен, и ждёт выхода процесса.
func stopDaemonQuiet(ctx context.Context, tr *i18n.Translator) error {
	c := newClient(runtimeDir(), tr.Lang())
	var st struct {
		PID int `json:"pid"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/status", nil, &st); err != nil {
		return nil
	}
	_ = c.do(ctx, http.MethodPost, "/api/v1/shutdown", nil, nil)
	for range 100 {
		if err := syscall.Kill(st.PID, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New(tr.T("cli.daemon.stop_timeout"))
}
