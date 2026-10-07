package main

import (
	"github.com/spf13/cobra"

	"github.com/khameleonium/mKey/internal/i18n"
)

// newHotkeysCmd создаёт команду `mkey hotkeys` — системные сочетания mKey:
//
//	mkey hotkeys                                      показать
//	mkey hotkeys set record "^{Ctrl}^{Alt}{R}"        начать/закончить запись ("off" — выключить)
//	mkey hotkeys set emergency "^{Esc}^{Backspace}{Enter}"   экстренная остановка
func newHotkeysCmd(tr *i18n.Translator) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hotkeys",
		Short: tr.T("cli.hotkeys.short"),
		Long:  tr.T("cli.hotkeys.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var hk map[string]string
			if err := c.do(cmd.Context(), "GET", "/api/v1/settings/hotkeys", nil, &hk); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			printHotkeys(cmd, tr, hk)
			return nil
		},
	}

	// mkey hotkeys set <record|emergency> <сочетание>
	set := &cobra.Command{
		Use:       "set <record|emergency> <сочетание>",
		Short:     tr.T("cli.hotkeys.set.short"),
		Args:      cobra.ExactArgs(2),
		ValidArgs: []string{"record", "emergency"},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Какое сочетание меняем; для записи "off" выключает сочетание.
			value := args[1]
			body := map[string]string{}
			switch args[0] {
			case "record":
				if value == "off" {
					value = ""
				}
				body["record"] = value
			case "emergency":
				body["emergency"] = value
			default:
				return userError(tr.T("cli.hotkeys.unknown", i18n.A("name", args[0])))
			}

			// Отправляем; ошибка проверки — понятным текстом со стрелкой под местом ошибки.
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var hk map[string]string
			if err := c.do(cmd.Context(), "PUT", "/api/v1/settings/hotkeys", body, &hk); err != nil {
				return userError(formatAPIError(tr, err, value))
			}
			printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.hotkeys.saved"))
			printHotkeys(cmd, tr, hk)
			return nil
		},
	}
	cmd.AddCommand(set)
	return cmd
}

// printHotkeys печатает системные сочетания.
func printHotkeys(cmd *cobra.Command, tr *i18n.Translator, hk map[string]string) {
	out := cmd.OutOrStdout()
	record := hk["record"]
	if record == "" {
		record = tr.T("cli.hotkeys.off")
	}
	printf(out, "  %-28s %s\n", tr.T("cli.hotkeys.record"), record)
	printf(out, "  %-28s %s\n", tr.T("cli.hotkeys.emergency"), hk["emergency"])
}
