package shell

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/paths"
	"github.com/khameleonium/mKey/internal/lib/project"
	"github.com/khameleonium/mKey/internal/lib/scriptfiles"
)

// ModuleID — идентификатор модуля.
const ModuleID = "shell"

// killGrace — сколько ждать после SIGTERM перед SIGKILL.
const killGrace = 2 * time.Second

// maxLogLine — наибольшая длина строки вывода скрипта в журнале.
const maxLogLine = 2000

// Config — настройки модуля из секции modules.shell.
type Config struct {
	// ScriptsDir — каталог файлов скриптов (по умолчанию ~/.config/mkey/scripts).
	ScriptsDir string `json:"scripts_dir"`
	// TimeoutMS — ограничение времени скрипта по умолчанию (60 с).
	TimeoutMS int `json:"timeout_ms"`
	// Shell — интерпретатор (по умолчанию bash, если есть, иначе sh).
	Shell string `json:"shell"`
}

// Module — модуль bash-скриптов.
type Module struct {
	cfg    Config
	socket string
	exe    string
}

// New создаёт модуль.
func New() *Module { return &Module{cfg: Config{TimeoutMS: 60_000}} }

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки, находит сокет API и регистрирует действие shell.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Настройки и значения по умолчанию.
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	if m.cfg.ScriptsDir == "" {
		m.cfg.ScriptsDir = filepath.Join(paths.Config(os.Getenv), "scripts")
	}
	if m.cfg.Shell == "" {
		m.cfg.Shell = "sh"
		if _, err := exec.LookPath("bash"); err == nil {
			m.cfg.Shell = "bash"
		}
	}

	// Сокет API и каталог mkey — для команд `mkey …` внутри скриптов.
	if p, err := contracts.LookupService[contracts.Platform](host.Services()); err == nil {
		m.socket = filepath.Join(p.Info().RuntimeDir, "mkey.sock")
	}
	m.exe, _ = os.Executable()

	// Папка скриптов bash — в списке «Где что лежит».
	if err := host.Extensions().Register(contracts.PointPlace, contracts.StaticPlace{
		M: contracts.ExtensionMeta{ID: contracts.PlaceShellScripts, NameKey: "place.shell_scripts", DescriptionKey: "place.shell_scripts.description", Provider: ModuleID},
		P: m.cfg.ScriptsDir, Dir: true, N: 41,
	}); err != nil {
		return err
	}
	// Язык файлов-скриптов для раздела «Скрипты» окна.
	lang := shellLanguage{m: m, folder: scriptfiles.Folder{Dir: m.cfg.ScriptsDir, Ext: ".sh"}}
	if err := host.Extensions().Register(contracts.PointScriptLanguage, lang); err != nil {
		return err
	}
	return host.Extensions().Register(contracts.PointAction, shellAction{m})
}

// Start ничего не делает.
func (m *Module) Start(context.Context) error { return nil }

// Stop ничего не делает: скрипты прерываются движком через контекст.
func (m *Module) Stop(context.Context) error { return nil }

// shellParams — параметры действия shell: строка с кодом или {code|file, timeout_ms}.
type shellParams struct {
	Code      string `json:"code"`
	File      string `json:"file"`
	TimeoutMS int    `json:"timeout_ms"`
}

// shellAction — вид действия shell.
type shellAction struct{ m *Module }

// Meta возвращает метаданные действия.
func (shellAction) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{
		ID: "shell", NameKey: "action.shell", DescriptionKey: "action.shell.description", Category: "script", Icon: "terminal",
		Provider:     ModuleID,
		ParamsSchema: []byte(`{"oneOf":[{"type":"string","x-widget":"code","x-lang":"shell"},{"type":"object","required":["code"],"properties":{"code":{"type":"string","x-widget":"code","x-lang":"shell"},"timeout_ms":{"type":"integer","minimum":1,"x-widget":"ms"}}},{"type":"object","required":["file"],"properties":{"file":{"type":"string","x-widget":"script-file","x-lang":"shell"},"timeout_ms":{"type":"integer","minimum":1,"x-widget":"ms"}}}]}`),
	}
}

// params разбирает параметры действия.
func (a shellAction) params(v any) (shellParams, error) {
	if s, ok := v.(string); ok {
		return shellParams{Code: s}, nil
	}
	var p shellParams
	if err := project.Decode(v, &p); err != nil {
		return p, err
	}
	if (p.Code == "") == (p.File == "") {
		return p, errors.New(`shell: set either code or file, e.g. shell: "notify-send hi"`)
	}
	return p, nil
}

// Validate проверяет параметры.
func (a shellAction) Validate(pa project.Action) error {
	p, err := a.params(pa.Value)
	if err == nil && p.Code == "" && p.File == "" {
		err = project.Required("action", "shell", "code")
	}
	return err
}

// Run выполняет скрипт с ограничением времени; остановка прерывает всю группу процессов.
func (a shellAction) Run(ctx context.Context, rc contracts.RunContext, pa project.Action) error {
	p, err := a.params(pa.Value)
	if err != nil {
		return err
	}

	// Команда: код через -c или файл из каталога скриптов (путь не выходит за его пределы).
	args := []string{"-c", p.Code}
	if p.File != "" {
		args = []string{filepath.Join(a.m.cfg.ScriptsDir, filepath.Clean("/"+p.File))}
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond
	if p.TimeoutMS <= 0 {
		timeout = time.Duration(a.m.cfg.TimeoutMS) * time.Millisecond
	}

	// Процесс в своей группе, с окружением mKey и выводом в журнал.
	cmd := exec.Command(a.m.cfg.Shell, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = a.m.env(rc)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("shell: %w", err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go logLines(&wg, stdout, func(l string) { rc.Logger().Info("shell: " + l) })
	go logLines(&wg, stderr, func(l string) { rc.Logger().Warn("shell: " + l) })

	// Ждём завершения, остановки или истечения времени.
	done := make(chan error, 1)
	go func() {
		wg.Wait()
		done <- cmd.Wait()
	}()
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("shell: %w", err)
		}
		return nil
	case <-tctx.Done():
		killGroup(cmd, done)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("shell: timeout after %v", timeout)
	}
}

// killGroup завершает группу процессов: SIGTERM, через killGrace — SIGKILL.
func killGroup(cmd *exec.Cmd, done <-chan error) {
	pgid := -cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(killGrace):
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		<-done
	}
}

// varNameRe — символы, недопустимые в имени переменной окружения.
var varNameRe = regexp.MustCompile(`[^A-Z0-9_]`)

// env собирает окружение скрипта: сокет, событие, переменные проекта, PATH с mkey.
func (m *Module) env(rc contracts.RunContext) []string {
	ref := rc.Event()
	env := append(os.Environ(),
		"MKEY_SOCKET="+m.socket,
		"MKEY_PROJECT="+ref.Project,
		"MKEY_EVENT="+ref.Event,
	)
	for name, v := range rc.Vars().All() {
		env = append(env, "MKEY_VAR_"+varNameRe.ReplaceAllString(strings.ToUpper(name), "_")+"="+fmt.Sprint(v))
	}
	if m.exe != "" {
		env = append(env, "PATH="+filepath.Dir(m.exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return env
}

// logLines читает вывод построчно и передаёт строки в журнал (длинные строки обрезаются).
func logLines(wg *sync.WaitGroup, r io.Reader, log func(string)) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if len(line) > maxLogLine {
			line = line[:maxLogLine] + "…"
		}
		log(line)
	}
}

// Проверки на этапе компиляции.
var (
	_ contracts.Module     = (*Module)(nil)
	_ contracts.ActionType = shellAction{}
)
