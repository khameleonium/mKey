package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"mkey/internal/contracts"
	"mkey/internal/i18n"
)

// Команды записи и воспроизведения ввода (этап 6, FR-REC-2, FR-REC-4):
//
//	mkey rec [имя]             записать до левый Ctrl + правый Alt + Пробел (в любой программе) или Ctrl+C здесь
//	mkey rec list | stop | delete <имя>
//	mkey play <имя> [--speed 2] [--repeat 5] [--loop] [--no-moves]

// newRecCmd создаёт команду `mkey rec` с подкомандами.
func newRecCmd(tr *i18n.Translator) *cobra.Command {
	var (
		countdown int
		kinds     []string
	)
	cmd := &cobra.Command{
		Use:   "rec [имя]",
		Short: tr.T("cli.rec.short"),
		Long:  tr.T("cli.rec.long"),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runRec(cmd, tr, name, countdown, kinds)
		},
	}
	cmd.Flags().IntVar(&countdown, "countdown", 3, tr.T("cli.rec.flag.countdown"))
	cmd.Flags().StringSliceVar(&kinds, "devices", nil, tr.T("cli.rec.flag.devices"))

	// mkey rec list — список записей.
	list := &cobra.Command{
		Use: "list", Short: tr.T("cli.rec.list.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var resp struct {
				Recordings []contracts.RecordingInfo `json:"recordings"`
				Current    *contracts.RecordingInfo  `json:"current"`
				Dir        string                    `json:"dir"`
			}
			if err := c.do(cmd.Context(), "GET", "/api/v1/recordings", nil, &resp); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			// Где лежат файлы записей: их можно открыть любым текстовым редактором.
			out := cmd.OutOrStdout()
			if resp.Dir != "" {
				printf(out, "%s\n\n", tr.T("cli.rec.list.dir", i18n.A("dir", resp.Dir)))
			}
			if resp.Current != nil {
				printf(out, "%s\n\n", tr.T("cli.rec.list.current", i18n.A("name", resp.Current.Name)))
			}
			if len(resp.Recordings) == 0 {
				printf(out, "%s\n", tr.T("cli.rec.list.empty"))
				return nil
			}
			for _, r := range resp.Recordings {
				// Файл с ошибкой: что и в какой строке поправить.
				if r.Problem != nil {
					printf(out, "  %-24s %s\n", r.Name, tr.T("cli.rec.list.problem", i18n.A("problem", r.Problem.Message)))
					continue
				}
				printf(out, "  %-24s %8s   %s\n", r.Name, seconds(r.DurationMS),
					tr.T("cli.rec.list.row", i18n.A("events", r.Events), i18n.A("date", r.Created.Local().Format("02.01.2006 15:04"))))
			}
			printf(out, "\n%s\n", tr.T("cli.rec.list.hint"))
			return nil
		},
	}

	// mkey rec stop — закончить запись, начатую в другом окне или сочетанием.
	stop := &cobra.Command{
		Use: "stop", Short: tr.T("cli.rec.stop.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var info contracts.RecordingInfo
			if err := c.do(cmd.Context(), "POST", "/api/v1/recordings/stop", map[string]string{}, &info); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			printSaved(cmd.OutOrStdout(), tr, info)
			return nil
		},
	}

	// mkey rec delete <имя> — удалить запись.
	del := &cobra.Command{
		Use: "delete <имя>", Short: tr.T("cli.rec.delete.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			if err := c.do(cmd.Context(), "DELETE", "/api/v1/recordings/"+url.PathEscape(args[0]), nil, nil); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.rec.deleted", i18n.A("name", args[0])))
			return nil
		},
	}
	// mkey rec convert <имя> — превратить запись в блоки конструктора (новый выключенный проект).
	var simplify, noSimplify bool
	conv := &cobra.Command{
		Use: "convert <имя>", Short: tr.T("cli.rec.convert.short"), Long: tr.T("cli.rec.convert.long"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Флаги взаимоисключающие (проверяем сами, чтобы ошибка была на языке пользователя).
			if simplify && noSimplify {
				return userError(tr.T("cli.rec.convert.flags_conflict"))
			}
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}

			// Упрощать ли движения мыши: из флага, иначе спрашиваем (без терминала — упрощаем).
			simple := true
			switch {
			case simplify:
			case noSimplify:
				simple = false
			default:
				d := dialog{cmd: cmd, tr: tr, yes: !isTerminal(os.Stdin)}
				simple = d.ask("cli.rec.convert.ask_simplify", true)
			}

			// Превращение; новый проект выключен — его нужно проверить и включить.
			var resp struct {
				Project string `json:"project"`
			}
			path := "/api/v1/recordings/" + url.PathEscape(args[0]) + "/convert"
			if err := c.do(cmd.Context(), "POST", path, contracts.ConvertOptions{Simplify: simple}, &resp); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			printf(cmd.OutOrStdout(), "%s\n", tr.T(guiKey("cli.rec.converted"), i18n.A("name", args[0]), i18n.A("project", resp.Project)))
			return nil
		},
	}
	conv.Flags().BoolVar(&simplify, "simplify", false, tr.T("cli.rec.convert.flag.simplify"))
	conv.Flags().BoolVar(&noSimplify, "no-simplify", false, tr.T("cli.rec.convert.flag.no_simplify"))

	cmd.AddCommand(list, stop, del, conv)
	return cmd
}

// runRec записывает ввод: отсчёт, начало записи, ожидание конца (сочетание в любой программе
// или Ctrl+C в терминале), итог.
func runRec(cmd *cobra.Command, tr *i18n.Translator, name string, countdown int, kinds []string) error {
	// Ctrl+C прерывает отсчёт или заканчивает запись.
	ctx, stopSignals := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	c := newClient(runtimeDir(), tr.Lang())
	if err := ensureDaemon(ctx, c, tr, true); err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	// Отсчёт перед началом: время переключиться в нужное окно.
	if !countDown(ctx, out, tr, "cli.rec.countdown", countdown) {
		printf(out, "\n%s\n", tr.T("cli.rec.cancelled"))
		return nil
	}

	// Начинаем запись.
	var info contracts.RecordingInfo
	if err := c.do(ctx, "POST", "/api/v1/recordings/start", contracts.RecordOptions{Name: name, Kinds: kinds}, &info); err != nil {
		return userError(formatAPIError(tr, err, ""))
	}
	if info.StopHotkey != "" {
		printf(out, "%s\n", tr.T("cli.rec.started", i18n.A("name", info.Name), i18n.A("hotkey", info.StopHotkey)))
	} else {
		printf(out, "%s\n", tr.T("cli.rec.started_nohotkey", i18n.A("name", info.Name)))
	}

	// Ждём конца записи: сочетанием в любой программе (ответ придёт сам) или Ctrl+C здесь.
	err := c.do(ctx, "POST", "/api/v1/recordings/wait", map[string]string{}, &info)
	if ctx.Err() != nil {
		// Ctrl+C: заканчиваем запись сами, вырезая само сочетание Ctrl+C.
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = c.do(sctx, "POST", "/api/v1/recordings/stop", map[string]string{"cut": "^{Ctrl}{C}"}, &info)
		printf(out, "\n")
	}
	if err != nil {
		return userError(formatAPIError(tr, err, ""))
	}
	printSaved(out, tr, info)
	return nil
}

// printSaved печатает итог записи и подсказку, как её повторить.
func printSaved(out io.Writer, tr *i18n.Translator, info contracts.RecordingInfo) {
	printf(out, "%s\n", tr.T("cli.rec.saved", i18n.A("name", info.Name), i18n.A("seconds", seconds(info.DurationMS)),
		i18n.A("events", info.Events), i18n.A("play", "mkey play "+shellQuote(info.Name)), i18n.A("file", info.Path)))
}

// newPlayCmd создаёт команду `mkey play <имя>`.
func newPlayCmd(tr *i18n.Translator) *cobra.Command {
	var (
		opts      contracts.PlayOptions
		loop      bool
		countdown int
	)
	cmd := &cobra.Command{
		Use:   "play <имя>",
		Short: tr.T("cli.play.short"),
		Long:  tr.T("cli.play.long"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Ctrl+C прерывает отсчёт и воспроизведение (демон отпустит нажатые клавиши).
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			c := newClient(runtimeDir(), tr.Lang())
			if err := ensureDaemon(ctx, c, tr, true); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if loop {
				opts.Repeat = -1
			}

			// Проверяем, что запись есть, до отсчёта: иначе ошибка появилась бы только через несколько секунд.
			var list struct {
				Recordings []contracts.RecordingInfo `json:"recordings"`
			}
			if err := c.do(ctx, "GET", "/api/v1/recordings", nil, &list); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			found := false
			for _, r := range list.Recordings {
				found = found || r.Name == args[0]
			}
			if !found {
				return userError(tr.T("cli.play.not_found", i18n.A("name", args[0])))
			}

			// Отсчёт: время переключиться в игру или нужную программу.
			if !countDown(ctx, out, tr, "cli.play.countdown", countdown) {
				printf(out, "\n%s\n", tr.T("cli.play.stopped"))
				return nil
			}
			printf(out, "%s\n", tr.T("cli.play.started", i18n.A("name", args[0])))

			// Воспроизведение до конца или до Ctrl+C.
			body := map[string]any{"name": args[0], "speed": opts.Speed, "repeat": opts.Repeat, "skip_moves": opts.SkipMoves}
			err := c.do(ctx, "POST", "/api/v1/play", body, nil)
			var ae *apiError
			switch {
			case ctx.Err() != nil, errors.As(err, &ae) && ae.Code == "api.stopped":
				printf(out, "\n%s\n", tr.T("cli.play.stopped"))
				return nil
			case err != nil:
				return userError(formatAPIError(tr, err, ""))
			}
			printf(out, "%s\n", tr.T("cli.play.done"))
			return nil
		},
	}
	cmd.Flags().Float64Var(&opts.Speed, "speed", 1, tr.T("cli.play.flag.speed"))
	cmd.Flags().IntVar(&opts.Repeat, "repeat", 1, tr.T("cli.play.flag.repeat"))
	cmd.Flags().BoolVar(&loop, "loop", false, tr.T("cli.play.flag.loop"))
	cmd.Flags().BoolVar(&opts.SkipMoves, "no-moves", false, tr.T("cli.play.flag.no_moves"))
	cmd.Flags().IntVar(&countdown, "countdown", 3, tr.T("cli.play.flag.countdown"))
	return cmd
}

// countDown печатает обратный отсчёт по секундам («3… 2… 1…»); false — прерван (Ctrl+C).
func countDown(ctx context.Context, out io.Writer, tr *i18n.Translator, key string, secs int) bool {
	if secs <= 0 {
		return ctx.Err() == nil
	}
	printf(out, "%s ", tr.T(key, i18n.A("seconds", secs)))
	for i := secs; i > 0; i-- {
		printf(out, "%d… ", i)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	printf(out, "\n")
	return true
}

// seconds записывает миллисекунды секундами с одним знаком: «12.3 с» не нужен — только число.
func seconds(ms int64) string {
	return fmt.Sprintf("%.1f", float64(ms)/1000)
}

// shellQuote заключает имя в кавычки, если в нём есть пробелы (для подсказки команды).
func shellQuote(s string) string {
	for _, r := range s {
		if r == ' ' || r == '(' || r == ')' {
			return "\"" + s + "\""
		}
	}
	return s
}
