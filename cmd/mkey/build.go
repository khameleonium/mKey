package main

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
)

// newBuildCmd создаёт команду `mkey build <проект> [-o файл] [--once событие]` — собрать
// самостоятельный файл макроса (FR-BUILD-1, ADR-0042). Собирает фоновая часть mKey (модуль
// builder): она знает проекты, записи и скрипты.
func newBuildCmd(tr *i18n.Translator) *cobra.Command {
	var out, once string
	cmd := &cobra.Command{
		Use:   "build <project>",
		Short: tr.T("cli.build.short"),
		Long:  tr.T("cli.build.long"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Запрос: режим и путь (относительный — от текущей папки).
			req := contracts.BuildRequest{Mode: contracts.BuildModeEvents}
			if once != "" {
				req.Mode, req.Event = contracts.BuildModeOnce, once
			}
			if out != "" {
				abs, err := filepath.Abs(out)
				if err != nil {
					return err
				}
				req.Output = abs
			}

			// Сборка фоновой частью mKey.
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var res contracts.BuildResult
			if err := c.do(cmd.Context(), http.MethodPost, "/api/v1/projects/"+url.PathEscape(args[0])+"/build", req, &res); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}

			// Что получилось и как запускать.
			w := cmd.OutOrStdout()
			printf(w, "%s\n", tr.T("cli.build.done", i18n.A("path", res.Path), i18n.A("size", megabytes(res.Size))))
			if len(res.Files) > 0 {
				printf(w, "%s\n", tr.T("cli.build.files", i18n.A("files", strings.Join(res.Files, ", "))))
			}
			printf(w, "%s\n", tr.T("cli.build.how", i18n.A("path", res.Path)))
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", tr.T("cli.build.flag.output"))
	cmd.Flags().StringVar(&once, "once", "", tr.T("cli.build.flag.once"))
	return cmd
}

// megabytes — размер в мегабайтах с одной цифрой после запятой («24.3»).
func megabytes(n int64) string {
	return fmt.Sprintf("%.1f", float64(n)/(1<<20))
}
