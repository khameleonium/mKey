package main

import (
	"net/http"

	"github.com/spf13/cobra"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
)

// newUpdateCmd создаёт команду `mkey update [--check] [--auto on|off] [--yes]` (T10.3, ADR-0030).
func newUpdateCmd(tr *i18n.Translator) *cobra.Command {
	var check, yes bool
	var auto string
	cmd := &cobra.Command{
		Use: "update", Short: tr.T("cli.update.short"), Long: tr.T("cli.update.long"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// Включение или выключение проверки раз в сутки.
			if auto != "" {
				if auto != "on" && auto != "off" {
					return userError(tr.T("cli.update.auto_bad"))
				}
				if err := c.do(cmd.Context(), http.MethodPut, "/api/v1/settings/update", map[string]bool{"check": auto == "on"}, nil); err != nil {
					return userError(formatAPIError(tr, err, ""))
				}
				key := "cli.update.auto_off"
				if auto == "on" {
					key = "cli.update.auto_on"
				}
				printf(out, "%s\n", tr.T(key))
				return nil
			}

			// Проверка.
			var info contracts.UpdateInfo
			if err := c.do(cmd.Context(), http.MethodPost, "/api/v1/update/check", nil, &info); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			printf(out, "%s\n", tr.T("cli.update.current", i18n.A("version", info.Current)))
			if info.Latest != "" {
				printf(out, "%s\n", tr.T("cli.update.latest", i18n.A("version", info.Latest), i18n.A("url", info.URL)))
			}
			if !info.Available {
				printf(out, "%s\n", tr.T("cli.update.none"))
				return nil
			}
			if !info.CanApply {
				printf(out, "%s\n", tr.T("cli.update.cannot", i18n.A("reason", tr.T("update.reason."+info.Reason))))
				return nil
			}
			if check {
				return nil
			}

			// Установка с согласия.
			d := dialog{cmd: cmd, tr: tr, yes: yes}
			if !d.ask("cli.update.ask", yes, i18n.A("version", info.Latest)) {
				d.say("cli.setup.cancelled")
				return nil
			}
			if err := c.do(cmd.Context(), http.MethodPost, "/api/v1/update/apply", nil, &info); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			printf(out, "%s\n", tr.T("cli.update.done", i18n.A("version", info.Latest)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, tr.T("cli.update.flag.check"))
	cmd.Flags().StringVar(&auto, "auto", "", tr.T("cli.update.flag.auto"))
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, tr.T("cli.update.flag.yes"))
	return cmd
}
