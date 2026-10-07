// Package elevate — бэкенды повышения прав (contracts.Elevator) для модуля platform.
//
// Бэкенды в порядке предпочтения:
//   - pkexec — графическое окно запроса пароля (нужен polkit и графическая сессия);
//   - sudo, doas — в текущем терминале (когда mKey запущен из терминала);
//   - terminal-sudo, terminal-doas — открыть окно терминала с sudo/doas и пояснением
//     (графическая сессия без polkit, например на части систем без systemd);
//   - manual — ничего не выполнять, а показать команду для копирования.
//
// Все внешние вызовы идут через Env, поэтому поведение бэкендов проверяется тестами.
package elevate

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/khameleonium/mKey/internal/contracts"
)

// Env — окружение, в котором работают бэкенды.
type Env struct {
	// Getenv возвращает переменную окружения.
	Getenv func(string) string
	// LookPath сообщает, установлена ли программа.
	LookPath func(string) bool
	// IsTTY — mKey запущен из терминала (stdin — терминал).
	IsTTY bool
	// Graphical — запущена графическая сессия.
	Graphical bool
	// Exec выполняет команду; interactive — подключить stdin/stdout/stderr текущего процесса.
	Exec func(ctx context.Context, name string, args []string, interactive bool) error
	// Prompt — пояснение, которое показывается в окне терминала перед запросом пароля (уже переведено).
	Prompt string
	// PressEnter — подсказка «нажмите Enter, чтобы закрыть окно» (уже переведена).
	PressEnter string
}

// All возвращает все встроенные бэкенды в порядке предпочтения (включая недоступные).
func All(env Env) []contracts.Elevator {
	return []contracts.Elevator{
		&direct{id: "pkexec", tool: "pkexec", env: env, needTTY: false},
		&direct{id: "sudo", tool: "sudo", env: env, needTTY: true},
		&direct{id: "doas", tool: "doas", env: env, needTTY: true},
		&terminal{id: "terminal-sudo", tool: "sudo", env: env},
		&terminal{id: "terminal-doas", tool: "doas", env: env},
		&manual{env: env},
	}
}

// meta собирает метаданные бэкенда с i18n-ключом имени.
func meta(id string) contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: id, NameKey: "platform.elevator." + id, Provider: "platform"}
}

// direct — бэкенд, запускающий команду через pkexec, sudo или doas напрямую.
type direct struct {
	id, tool string
	env      Env
	// needTTY — бэкенду нужен терминал для ввода пароля (sudo/doas).
	needTTY bool
}

// Meta возвращает метаданные бэкенда.
func (d *direct) Meta() contracts.ExtensionMeta { return meta(d.id) }

// Available: программа установлена; pkexec — в графической сессии, sudo/doas — в терминале.
func (d *direct) Available() bool {
	if !d.env.LookPath(d.tool) {
		return false
	}
	if d.needTTY {
		return d.env.IsTTY
	}
	return d.env.Graphical
}

// Command возвращает команду для показа пользователю.
func (d *direct) Command(argv []string) string { return d.tool + " " + Join(argv) }

// Run выполняет команду; ввод пароля идёт через терминал или графический агент polkit.
func (d *direct) Run(ctx context.Context, argv []string) error {
	return d.env.Exec(ctx, d.tool, argv, d.env.IsTTY)
}

// terminal — бэкенд, открывающий окно терминала с sudo/doas.
type terminal struct {
	id, tool string
	env      Env
}

// Meta возвращает метаданные бэкенда.
func (t *terminal) Meta() contracts.ExtensionMeta { return meta(t.id) }

// Available: графическая сессия, установлен sudo/doas и найден эмулятор терминала.
func (t *terminal) Available() bool {
	if !t.env.Graphical || !t.env.LookPath(t.tool) {
		return false
	}
	_, _, ok := findTerminal(t.env)
	return ok
}

// Command возвращает команду, которая будет выполнена в окне терминала.
func (t *terminal) Command(argv []string) string { return t.tool + " " + Join(argv) }

// Run открывает окно терминала: пояснение, команда с sudo/doas, ожидание Enter.
// Многие терминалы возвращают управление сразу, поэтому результат нужно перепроверить.
func (t *terminal) Run(ctx context.Context, argv []string) error {
	// Находим эмулятор терминала и способ передать ему команду.
	term, prefix, ok := findTerminal(t.env)
	if !ok {
		return contracts.ErrUnsupported
	}

	// Скрипт для окна: пояснение, команда, пауза, чтобы пользователь увидел результат.
	script := "printf '%s\\n\\n' " + Quote(t.env.Prompt) + "; " + t.Command(argv) +
		"; printf '\\n%s' " + Quote(t.env.PressEnter) + "; read _"
	args := append(append([]string{}, prefix...), "sh", "-c", script)
	return t.env.Exec(ctx, term, args, false)
}

// manual — бэкенд «сделайте сами»: возвращает команду для копирования.
type manual struct {
	env Env
}

// Meta возвращает метаданные бэкенда.
func (m *manual) Meta() contracts.ExtensionMeta { return meta("manual") }

// Available: доступен всегда — это последний вариант.
func (m *manual) Available() bool { return true }

// Command возвращает команду с sudo, doas или su — что есть в системе.
func (m *manual) Command(argv []string) string {
	switch {
	case m.env.LookPath("sudo"):
		return "sudo " + Join(argv)
	case m.env.LookPath("doas"):
		return "doas " + Join(argv)
	default:
		return "su -c " + Quote(Join(argv))
	}
}

// Run ничего не выполняет и возвращает команду, которую пользователь должен ввести сам.
func (m *manual) Run(_ context.Context, argv []string) error {
	return &contracts.ManualActionError{Command: m.Command(argv)}
}

// terminals — известные эмуляторы терминала и аргументы перед командой, в порядке предпочтения.
var terminals = []struct {
	name   string
	prefix []string
}{
	{"x-terminal-emulator", []string{"-e"}},
	{"konsole", []string{"-e"}},
	{"gnome-terminal", []string{"--"}},
	{"kgx", []string{"--"}},
	{"xfce4-terminal", []string{"-x"}},
	{"mate-terminal", []string{"-x"}},
	{"alacritty", []string{"-e"}},
	{"kitty", nil},
	{"foot", nil},
	{"wezterm", []string{"start", "--"}},
	{"xterm", []string{"-e"}},
}

// FindTerminal выбирает эмулятор терминала ($TERMINAL, затем известные по списку) и возвращает
// его имя и аргументы, после которых идёт команда (например, "konsole", ["-e"]).
func FindTerminal(getenv func(string) string, lookPath func(string) bool) (name string, prefix []string, ok bool) {
	return findTerminal(Env{Getenv: getenv, LookPath: lookPath})
}

// findTerminal выбирает эмулятор терминала: $TERMINAL, затем известные по списку.
func findTerminal(env Env) (name string, prefix []string, ok bool) {
	// Предпочтение пользователя из переменной TERMINAL (флаг -e понимает большинство терминалов).
	if t := env.Getenv("TERMINAL"); t != "" && env.LookPath(t) {
		for _, known := range terminals {
			if known.name == t {
				return t, known.prefix, true
			}
		}
		return t, []string{"-e"}, true
	}

	// Первый установленный из известных.
	for _, known := range terminals {
		if env.LookPath(known.name) {
			return known.name, known.prefix, true
		}
	}
	return "", nil, false
}

// Join склеивает аргументы в строку команды для shell, экранируя каждый.
func Join(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = Quote(a)
	}
	return strings.Join(parts, " ")
}

// Quote экранирует строку для POSIX shell: безопасные строки возвращаются как есть,
// остальные заключаются в одинарные кавычки.
func Quote(s string) string {
	// Строка только из безопасных символов не требует кавычек.
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !shellSafe(r) }) < 0 {
		return s
	}

	// Одинарные кавычки; сама кавычка внутри записывается как '\''.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellSafe сообщает, можно ли оставить символ в shell-команде без кавычек.
func shellSafe(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@%+,", r)
}

// RealExec выполняет команду в настоящей системе. В интерактивном режиме подключает
// терминал текущего процесса (для ввода пароля), иначе возвращает вывод команды в ошибке.
func RealExec(ctx context.Context, name string, args []string, interactive bool) error {
	cmd := exec.CommandContext(ctx, name, args...)

	// Интерактивный режим: пароль вводится в текущем терминале.
	if interactive {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}

	// Неинтерактивный режим: сохраняем вывод, чтобы показать его при ошибке.
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) > 0 {
		return &execError{err: err, output: strings.TrimSpace(string(out))}
	}
	return err
}

// execError — ошибка внешней команды вместе с её выводом.
type execError struct {
	err    error
	output string
}

// Error возвращает текст ошибки с выводом команды.
func (e *execError) Error() string { return e.err.Error() + ": " + e.output }

// Unwrap возвращает исходную ошибку.
func (e *execError) Unwrap() error { return e.err }
