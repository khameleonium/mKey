package main

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
)

// Команды плагинов (FR-PLG-4, docs/plugins.md):
//
//	mkey plugin list               плагины, их состояние и папка
//	mkey plugin install <путь>     установить из папки или архива .zip (выключенным)
//	mkey plugin enable <id>        включить (показав разрешения)
//	mkey plugin disable <id>       выключить
//	mkey plugin remove <id>        удалить
//	mkey plugin logs <id>          журнал плагина

// newPluginCmd создаёт команду mkey plugin.
func newPluginCmd(tr *i18n.Translator) *cobra.Command {
	cmd := &cobra.Command{Use: "plugin", Short: tr.T("cli.plugin.short"), Long: tr.T("cli.plugin.long")}

	// list — плагины и их состояние.
	list := &cobra.Command{
		Use: "list", Short: tr.T("cli.plugin.list.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var resp struct {
				Plugins []contracts.PluginInfo `json:"plugins"`
				Dir     string                 `json:"dir"`
			}
			if err := c.do(cmd.Context(), http.MethodGet, "/api/v1/plugins", nil, &resp); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			out := cmd.OutOrStdout()
			if len(resp.Plugins) == 0 {
				printf(out, "%s\n", tr.T("cli.plugin.none"))
			}
			for _, p := range resp.Plugins {
				printf(out, "  %-28s %-10s %s %s\n", p.ID, tr.T("cli.plugin.state."+p.State), pluginName(p, tr.Lang()), p.Version)
				if p.Error != "" {
					printf(out, "      %s\n", tr.T("cli.plugin.error", i18n.A("error", p.Error)))
				}
				if types := append(append(append([]string{}, p.Actions...), p.Conditions...), p.Triggers...); len(types) > 0 {
					printf(out, "      %s\n", tr.T("cli.plugin.types", i18n.A("list", strings.Join(types, ", "))))
				}
			}
			printf(out, "\n%s\n", tr.T("cli.plugin.dir", i18n.A("dir", resp.Dir)))
			return nil
		},
	}

	// install — из папки или архива; путь — абсолютный (у демона своя рабочая папка).
	install := &cobra.Command{
		Use: "install <папка|архив.zip>", Short: tr.T("cli.plugin.install.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var p contracts.PluginInfo
			if err := c.do(cmd.Context(), http.MethodPost, "/api/v1/plugins/install", map[string]string{"path": path}, &p); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.plugin.installed", i18n.A("id", p.ID), i18n.A("name", pluginName(p, tr.Lang()))))
			printPermissions(cmd, tr, p)
			printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.plugin.enable_hint", i18n.A("id", p.ID)))
			return nil
		},
	}

	// enable/disable/remove — действие над плагином по ID.
	act := func(use, method, action, doneKey string) *cobra.Command {
		return &cobra.Command{
			Use: use + " <id>", Short: tr.T("cli.plugin." + use + ".short"), Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := daemonClient(cmd, tr)
				if err != nil {
					return err
				}

				// Перед включением — что плагин просит (FR-PLG-4).
				if use == "enable" {
					var resp struct {
						Plugins []contracts.PluginInfo `json:"plugins"`
					}
					if err := c.do(cmd.Context(), http.MethodGet, "/api/v1/plugins", nil, &resp); err == nil {
						for _, p := range resp.Plugins {
							if p.ID == args[0] {
								printPermissions(cmd, tr, p)
							}
						}
					}
				}
				path := "/api/v1/plugins/" + url.PathEscape(args[0])
				if action != "" {
					path += "/" + action
				}
				if err := c.do(cmd.Context(), method, path, nil, nil); err != nil {
					return userError(formatAPIError(tr, err, ""))
				}
				printf(cmd.OutOrStdout(), "%s\n", tr.T(doneKey, i18n.A("id", args[0])))
				return nil
			},
		}
	}

	// logs — журнал плагина.
	var lines int
	logs := &cobra.Command{
		Use: "logs <id>", Short: tr.T("cli.plugin.logs.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var resp struct {
				Lines []string `json:"lines"`
			}
			q := "/api/v1/plugins/" + url.PathEscape(args[0]) + "/log?lines=" + strconv.Itoa(lines)
			if err := c.do(cmd.Context(), http.MethodGet, q, nil, &resp); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			for _, l := range resp.Lines {
				printf(cmd.OutOrStdout(), "%s\n", l)
			}
			return nil
		},
	}
	logs.Flags().IntVarP(&lines, "lines", "n", 200, tr.T("cli.plugin.logs.lines"))

	cmd.AddCommand(list, install,
		act("enable", http.MethodPost, "enable", "cli.plugin.enabled"),
		act("disable", http.MethodPost, "disable", "cli.plugin.disabled"),
		act("remove", http.MethodDelete, "", "cli.plugin.removed"),
		logs)
	return cmd
}

// printPermissions печатает разрешения плагина и честную оговорку.
func printPermissions(cmd *cobra.Command, tr *i18n.Translator, p contracts.PluginInfo) {
	out := cmd.OutOrStdout()
	if len(p.Permissions) == 0 {
		printf(out, "%s\n", tr.T("cli.plugin.no_permissions"))
	} else {
		printf(out, "%s\n", tr.T("cli.plugin.permissions"))
		for _, perm := range p.Permissions {
			printf(out, "  - %s\n", tr.T("plugins.perm."+perm))
		}
	}
	printf(out, "%s\n", tr.T("cli.plugin.trust"))
}

// pluginName — название плагина на языке lang (иначе ID).
func pluginName(p contracts.PluginInfo, lang string) string {
	for _, l := range []string{lang, "en", "ru"} {
		if s := p.Name[l]; s != "" {
			return s
		}
	}
	return p.ID
}
