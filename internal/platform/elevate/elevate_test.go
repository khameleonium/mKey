package elevate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/khameleonium/mKey/internal/contracts"
)

// call — записанный вызов внешней команды.
type call struct {
	name        string
	args        []string
	interactive bool
}

// testEnv создаёт окружение с заданными программами и записью вызовов.
func testEnv(programs []string, tty, graphical bool, calls *[]call) Env {
	set := map[string]bool{}
	for _, p := range programs {
		set[p] = true
	}
	return Env{
		Getenv:    func(string) string { return "" },
		LookPath:  func(n string) bool { return set[n] },
		IsTTY:     tty,
		Graphical: graphical,
		Exec: func(_ context.Context, name string, args []string, interactive bool) error {
			*calls = append(*calls, call{name, args, interactive})
			return nil
		},
		Prompt:     "mKey needs administrator rights",
		PressEnter: "Press Enter",
	}
}

// available возвращает ID доступных бэкендов по порядку.
func available(env Env) []string {
	var ids []string
	for _, e := range All(env) {
		if e.Available() {
			ids = append(ids, e.Meta().ID)
		}
	}
	return ids
}

// TestAvailability проверяет выбор бэкендов для разных окружений.
func TestAvailability(t *testing.T) {
	t.Parallel()
	var calls []call

	// Таблица: программы, терминал, графика → доступные бэкенды.
	cases := []struct {
		name     string
		programs []string
		tty, gui bool
		want     string
	}{
		{"desktop with polkit, from terminal", []string{"pkexec", "sudo", "konsole"}, true, true, "pkexec,sudo,terminal-sudo,manual"},
		{"desktop with polkit, from GUI", []string{"pkexec", "sudo", "konsole"}, false, true, "pkexec,terminal-sudo,manual"},
		{"no polkit, doas, GUI", []string{"doas", "foot"}, false, true, "terminal-doas,manual"},
		{"console only", []string{"sudo"}, true, false, "sudo,manual"},
		{"nothing", nil, false, false, "manual"},
	}
	for _, c := range cases {
		got := strings.Join(available(testEnv(c.programs, c.tty, c.gui, &calls)), ",")
		if got != c.want {
			t.Errorf("%s: available = %s, want %s", c.name, got, c.want)
		}
	}
}

// TestRun проверяет команды, которые запускают бэкенды.
func TestRun(t *testing.T) {
	t.Parallel()
	var calls []call
	env := testEnv([]string{"pkexec", "sudo", "konsole"}, true, true, &calls)
	argv := []string{"/home/user/.local/bin/mkey", "privileged", "install-rules"}
	elevators := All(env)

	// pkexec и sudo запускаются напрямую; sudo — интерактивно.
	_ = elevators[0].Run(context.Background(), argv)
	_ = elevators[1].Run(context.Background(), argv)
	if calls[0].name != "pkexec" || calls[1].name != "sudo" || !calls[1].interactive || calls[1].args[1] != "privileged" {
		t.Fatalf("calls = %+v", calls)
	}

	// Окно терминала: konsole -e sh -c '<пояснение>; sudo …; read'.
	_ = elevators[3].Run(context.Background(), argv)
	term := calls[2]
	if term.name != "konsole" || term.args[0] != "-e" || term.args[1] != "sh" || !strings.Contains(term.args[3], "sudo /home/user/.local/bin/mkey privileged install-rules") {
		t.Fatalf("terminal call = %+v", term)
	}

	// Ручной бэкенд ничего не запускает и возвращает команду.
	err := elevators[5].Run(context.Background(), argv)
	var manual *contracts.ManualActionError
	if !errors.As(err, &manual) || !errors.Is(err, contracts.ErrManualAction) || manual.Command != "sudo /home/user/.local/bin/mkey privileged install-rules" {
		t.Fatalf("manual err = %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("manual must not execute anything, calls = %+v", calls)
	}
}

// TestQuote проверяет экранирование для shell.
func TestQuote(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/usr/bin/mkey": "/usr/bin/mkey",
		"install-rules": "install-rules",
		"with space":    "'with space'",
		"it's":          `'it'\''s'`,
		"":              "''",
		"$(rm -rf /)":   "'$(rm -rf /)'",
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}
