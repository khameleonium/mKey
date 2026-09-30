package main

import (
	"errors"
	"os"
	"os/user"

	"github.com/spf13/cobra"

	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/platform/detect"
	"mkey/internal/platform/devaccess"
)

// newPrivilegedCmd создаёт скрытую команду `mkey privileged <install-rules|uninstall-rules>` (T1.6, SEC-6).
//
// Её запускает сам mKey через pkexec/sudo/doas. Команда принимает только фиксированные
// подкоманды без аргументов: пути и содержимое системных файлов зашиты в бинарник,
// поэтому через неё нельзя записать произвольный файл с правами root.
func newPrivilegedCmd(tr *i18n.Translator) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "privileged",
		Short:  tr.T("cli.privileged.short"),
		Hidden: true,
	}
	cmd.AddCommand(
		newPrivilegedAction(tr, "install-rules", "cli.privileged.install.short", true),
		newPrivilegedAction(tr, "uninstall-rules", "cli.privileged.uninstall.short", false),
	)
	return cmd
}

// newPrivilegedAction создаёт подкоманду установки или удаления правил доступа.
func newPrivilegedAction(tr *i18n.Translator, use, shortKey string, install bool) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: tr.T(shortKey),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Команда имеет смысл только от root.
			if os.Geteuid() != 0 {
				return errors.New(tr.T("cli.privileged.not_root"))
			}

			// Определяем систему и подходящий способ выдачи доступа.
			info := detect.Detect(detect.OS{})
			access := pickAccess(info)
			if access == nil {
				return errors.New(tr.T("cli.privileged.no_method"))
			}

			// Пользователь, для которого выдаются права (нужен способам с группой input).
			name := invokingUser(os.Getenv)
			if install && name == "" && access.RequiresRelogin() {
				return errors.New(tr.T("cli.privileged.no_user"))
			}
			env := contracts.PrivilegedEnv{Root: "/", Runner: devaccess.ExecRunner{}, User: name, Platform: info}

			// Устанавливаем или удаляем.
			out := cmd.OutOrStdout()
			method := i18n.A("method", tr.T(access.Meta().NameKey))
			if !install {
				if err := access.Uninstall(cmd.Context(), env); err != nil {
					return err
				}
				printf(out, "%s\n", tr.T("cli.privileged.uninstalled", method))
				return nil
			}
			if err := access.Install(cmd.Context(), env); err != nil {
				return err
			}
			printf(out, "%s\n", tr.T("cli.privileged.installed", method))
			if access.RequiresRelogin() {
				printf(out, "%s\n", tr.T("cli.privileged.relogin"))
			}
			return nil
		},
	}
}

// pickAccess возвращает первый подходящий для системы способ выдачи доступа.
func pickAccess(info contracts.PlatformInfo) contracts.DeviceAccess {
	for _, a := range devaccess.All() {
		if a.Applicable(info) {
			return a
		}
	}
	return nil
}

// invokingUser определяет пользователя, который запустил повышение прав:
// pkexec передаёт PKEXEC_UID, sudo — SUDO_USER, doas — DOAS_USER.
func invokingUser(getenv func(string) string) string {
	// pkexec очищает окружение, но оставляет uid вызвавшего пользователя.
	if uid := getenv("PKEXEC_UID"); uid != "" {
		if u, err := user.LookupId(uid); err == nil {
			return u.Username
		}
	}

	// sudo и doas передают имя пользователя.
	for _, v := range []string{"SUDO_USER", "DOAS_USER"} {
		if name := getenv(v); name != "" && name != "root" {
			return name
		}
	}
	return ""
}
