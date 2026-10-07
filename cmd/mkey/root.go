package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"mkey/internal/i18n"
	"mkey/internal/lib/buildinfo"
)

// newRootCmd создаёт корневую команду mkey со всеми подкомандами.
func newRootCmd(tr *i18n.Translator) *cobra.Command {
	// Корневая команда: без аргументов пока показывает справку
	// (позже — откроет GUI или мастер установки, SPEC §5.10).
	root := &cobra.Command{
		Use:           "mkey",
		Short:         tr.T("cli.root.short"),
		Long:          tr.T(guiKey("cli.root.long")) + "\n\n" + tr.T("app.creator", i18n.A("creator", buildinfo.Creator)),
		SilenceUsage:  true,
		SilenceErrors: true,
		// Без параметров: не установлен — мастер установки, установлен — окно программы.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDefault(cmd, tr)
		},
	}

	// Глобальные флаги. Значение --lang уже учтено в main до разбора, здесь флаг
	// объявлен, чтобы cobra его принимал и показывал в справке.
	root.PersistentFlags().String("lang", "", tr.T("cli.flag.lang"))
	root.PersistentFlags().BoolP("verbose", "v", false, tr.T("cli.flag.verbose"))

	// Подкоманды.
	root.AddCommand(
		newSendCmd(tr),
		newRecCmd(tr),
		newPlayCmd(tr),
		newHotkeysCmd(tr),
		newStopCmd(tr),
		newPanicCmd(tr),
		newStatusCmd(tr),
		newDevicesCmd(tr),
		newProjectCmd(tr),
		newEventCmd(tr),
		newRunCmd(tr, "run"),
		newVarCmd(tr),
		newWaitCmd(tr),
		newResumeCmd(tr),
		newDoctorCmd(tr),
		newSetupCmd(tr),
		newUninstallCmd(tr),
		newDaemonCmd(tr),
		newLogsCmd(tr),
		newPathsCmd(tr),
		newPluginCmd(tr),
		newUpdateCmd(tr),
		newVersionCmd(tr),
		newPrivilegedCmd(tr),
	)
	// Команда окна программы — только в полной сборке (консольная об окне не знает, ADR-0023).
	if buildinfo.GUI {
		root.AddCommand(newGUICmd(tr))
	}
	return root
}

// newVersionCmd создаёт команду `mkey version`.
func newVersionCmd(tr *i18n.Translator) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: tr.T("cli.version.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			// Машиночитаемый вывод для скриптов.
			if asJSON {
				return json.NewEncoder(out).Encode(map[string]string{
					"version": buildinfo.Version,
					"commit":  buildinfo.Commit,
					"date":    buildinfo.Date,
					"creator": buildinfo.Creator,
				})
			}

			// Человекочитаемый вывод на языке пользователя; консольная сборка отмечена отдельно.
			_, err := fmt.Fprintf(out, "%s\n%s\n", tr.T("cli.version.output",
				i18n.A("version", buildinfo.Version),
				i18n.A("commit", buildinfo.Commit),
				i18n.A("date", buildinfo.Date),
			), tr.T("app.creator", i18n.A("creator", buildinfo.Creator)))
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, tr.T("cli.flag.json"))
	return cmd
}
