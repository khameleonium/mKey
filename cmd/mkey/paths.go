package main

import (
	"github.com/spf13/cobra"

	"mkey/internal/i18n"
)

// placeInfo — место на диске из ответа /api/v1/places («Где что лежит»).
type placeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Display     string `json:"display"`
	IsDir       bool   `json:"is_dir"`
	Exists      bool   `json:"exists"`
}

// newPathsCmd создаёт команду `mkey paths`: где mKey хранит файлы пользователя (настройки,
// проекты, записи, скрипты, журнал). Список собирают модули демона, поэтому он верен и при
// папках, изменённых в настройках.
func newPathsCmd(tr *i18n.Translator) *cobra.Command {
	return &cobra.Command{
		Use: "paths", Short: tr.T("cli.paths.short"), Long: tr.T("cli.paths.long"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Места — у запущенного демона.
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var resp struct {
				Places []placeInfo `json:"places"`
			}
			if err := c.do(cmd.Context(), "GET", "/api/v1/places", nil, &resp); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}

			// Название, под ним путь и что там лежит; отсутствующие отмечаем.
			out := cmd.OutOrStdout()
			printf(out, "%s\n", tr.T("cli.paths.title"))
			for _, p := range resp.Places {
				path := p.Display
				if !p.Exists {
					path += "   " + tr.T("cli.paths.missing")
				}
				printf(out, "\n  %s\n    %s\n", p.Name, path)
				if p.Description != "" {
					printf(out, "    %s\n", p.Description)
				}
			}
			printf(out, "\n%s\n", tr.T("cli.paths.hint"))
			return nil
		},
	}
}
