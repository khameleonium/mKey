package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"mkey/internal/app"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/platform"
	"mkey/internal/registry"
	"mkey/internal/session"
	"mkey/internal/setup"
)

// statusMarks — значок результата проверки для вывода в терминал.
var statusMarks = map[contracts.CheckStatus]string{
	contracts.CheckOK:   "✓",
	contracts.CheckInfo: "·",
	contracts.CheckWarn: "!",
	contracts.CheckFail: "✗",
}

// newDoctorCmd создаёт команду `mkey doctor [--json] [--fix] [--yes]` (T1.5, T1.6).
func newDoctorCmd(tr *i18n.Translator) *cobra.Command {
	var asJSON, fix, yes bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: tr.T("cli.doctor.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Для диагностики нужны только модули сессии, платформы и настройки.
			entries := []registry.Entry{
				{Module: session.New(), Core: true},
				{Module: platform.New(), Core: true},
				{Module: setup.New(), Core: true},
			}
			return withModules(cmd.Context(), cmd, tr, entries, func(a *app.App) error {
				// Получаем сервисы диагностики и платформы.
				doctor, err := contracts.LookupService[contracts.Doctor](a.Manager.Services())
				if err != nil {
					return err
				}
				plat, err := contracts.LookupService[contracts.Platform](a.Manager.Services())
				if err != nil {
					return err
				}

				// Проверяем систему и печатаем результат.
				checks := doctor.Run(cmd.Context())
				out := cmd.OutOrStdout()
				if asJSON && !fix {
					return json.NewEncoder(out).Encode(checks)
				}
				printChecks(out, tr, checks)
				if !fix {
					if !setup.Healthy(checks) && hasFix(checks) {
						printf(out, "\n%s\n", tr.T("cli.doctor.summary.hint_fix"))
					}
					return nil
				}

				// Исправляем найденные проблемы с согласия пользователя.
				return runFix(cmd.Context(), cmd, tr, doctor, plat, checks, yes)
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, tr.T("cli.flag.json"))
	cmd.Flags().BoolVar(&fix, "fix", false, tr.T("cli.doctor.flag.fix"))
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, tr.T("cli.doctor.flag.yes"))
	return cmd
}

// printChecks печатает результаты проверок и итог.
func printChecks(w io.Writer, tr *i18n.Translator, checks []contracts.Check) {
	// Заголовок и строка на каждую проверку.
	printf(w, "%s\n\n", tr.T("cli.doctor.title"))
	for _, c := range checks {
		printf(w, "  %s %s\n", statusMarks[c.Status], tr.T(c.MessageKey, checkArgs(tr, c)...))
	}

	// Итог.
	if setup.Healthy(checks) {
		printf(w, "\n%s\n", tr.T("cli.doctor.summary.ok"))
	} else {
		printf(w, "\n%s\n", tr.T("cli.doctor.summary.fail"))
	}
}

// checkArgs превращает параметры проверки в параметры перевода; параметры-ключи переводятся.
func checkArgs(tr *i18n.Translator, c contracts.Check) []contracts.Arg {
	args := make([]contracts.Arg, 0, len(c.Args)+len(c.ArgKeys))
	for k, v := range c.Args {
		args = append(args, i18n.A(k, v))
	}
	for k, key := range c.ArgKeys {
		args = append(args, i18n.A(k, tr.T(key)))
	}
	return args
}

// hasFix сообщает, есть ли среди проблемных проверок автоматически исправимые.
func hasFix(checks []contracts.Check) bool {
	return slices.ContainsFunc(checks, func(c contracts.Check) bool {
		return c.Fix != "" && (c.Status == contracts.CheckFail || c.Status == contracts.CheckWarn)
	})
}

// runFix объясняет изменения, спрашивает согласие, запрашивает права и перепроверяет систему.
func runFix(ctx context.Context, cmd *cobra.Command, tr *i18n.Translator, doctor contracts.Doctor, plat contracts.Platform, checks []contracts.Check, yes bool) error {
	out := cmd.OutOrStdout()

	// Исправлять нечего.
	if !hasFix(checks) {
		printf(out, "\n%s\n", tr.T("cli.doctor.fix.nothing"))
		return nil
	}

	// Объясняем простым языком, что будет сделано (SPEC §5.11: изменения — только с согласия).
	access, err := plat.DeviceAccess()
	if err != nil {
		printf(out, "\n%s\n", tr.T("cli.doctor.fix.no_method"))
		return nil
	}
	printf(out, "\n%s\n", tr.T("cli.doctor.fix.explain", i18n.A("method", tr.T(access.Meta().NameKey))))
	if access.RequiresRelogin() {
		printf(out, "%s\n", tr.T("cli.doctor.fix.relogin"))
	}

	// Спрашиваем согласие (или берём его из --yes).
	if !yes {
		if !isTerminal(os.Stdin) {
			printf(out, "\n%s\n", tr.T("cli.doctor.fix.need_yes"))
			return nil
		}
		printf(out, "\n%s", tr.T("cli.doctor.fix.confirm"))
		answer := readLine(cmd.InOrStdin())
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" && a != "д" && a != "да" {
			printf(out, "%s\n", tr.T("cli.doctor.fix.cancelled"))
			return nil
		}
	}

	// Запрашиваем права лучшим доступным способом.
	elevator := plat.Elevators()[0]
	printf(out, "%s\n", tr.T("cli.doctor.fix.running", i18n.A("method", tr.T(elevator.Meta().NameKey))))
	err = doctor.Fix(ctx, contracts.FixDeviceAccess, elevator)

	// Ручной способ: показываем команду для копирования.
	var manual *contracts.ManualActionError
	if errors.As(err, &manual) {
		printf(out, "\n%s\n", tr.T("cli.doctor.fix.manual", i18n.A("command", manual.Command)))
		return nil
	}
	if err != nil {
		return err
	}

	// Окно терминала могло вернуть управление до ввода пароля — просим перепроверить позже.
	if strings.HasPrefix(elevator.Meta().ID, "terminal-") {
		printf(out, "\n%s\n", tr.T("cli.doctor.fix.terminal_note"))
		return nil
	}

	// Перепроверяем систему.
	printf(out, "\n%s\n\n", tr.T("cli.doctor.fix.after"))
	printChecks(out, tr, doctor.Run(ctx))
	if access.RequiresRelogin() {
		printf(out, "%s\n", tr.T("cli.doctor.fix.relogin"))
	}
	return nil
}

// isTerminal сообщает, подключён ли файл к терминалу.
func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}
