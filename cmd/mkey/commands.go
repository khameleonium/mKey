package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"mkey/internal/contracts"
	"mkey/internal/i18n"
)

// newSendCmd создаёт команду `mkey send '<макрос>'` — выполнить макрос (T2.6).
func newSendCmd(tr *i18n.Translator) *cobra.Command {
	var (
		delay       time.Duration
		dryRun      bool
		noAutostart bool
	)
	cmd := &cobra.Command{
		Use:   "send <macro | ->",
		Short: tr.T("cli.send.short"),
		Long:  tr.T("cli.send.long"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Текст макроса: из аргумента или, если указан "-", из стандартного ввода.
			src := args[0]
			if src == "-" {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				src = string(data)
			}

			// Ctrl+C прерывает и ожидание, и выполнение макроса (демон отпустит клавиши).
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			c := newClient(runtimeDir(), tr.Lang())
			if err := ensureDaemon(ctx, c, tr, !noAutostart); err != nil {
				return err
			}

			// Пауза перед выполнением, чтобы переключиться в нужное окно.
			out := cmd.OutOrStdout()
			if delay > 0 && !dryRun {
				printf(out, "%s\n", tr.T("cli.send.countdown", i18n.A("seconds", delay.Seconds())))
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return nil
				}
			}

			// Отправляем макрос демону и ждём окончания.
			err := c.do(ctx, "POST", "/api/v1/send", map[string]any{"sequence": src, "dry_run": dryRun}, nil)
			if err != nil {
				if ctx.Err() != nil {
					printf(out, "%s\n", tr.T("cli.send.stopped"))
					return nil
				}
				return userError(formatAPIError(tr, err, src))
			}
			if dryRun {
				printf(out, "%s\n", tr.T("cli.send.dry_ok"))
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&delay, "delay", 0, tr.T("cli.send.flag.delay"))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, tr.T("cli.send.flag.dry_run"))
	cmd.Flags().BoolVar(&noAutostart, "no-autostart", false, tr.T("cli.flag.no_autostart"))
	return cmd
}

// statusInfo — ответ демона на /status (нужные CLI поля).
type statusInfo struct {
	Version   string                  `json:"version"`
	PID       int                     `json:"pid"`
	StartedAt time.Time               `json:"started_at"`
	Port      int                     `json:"port"`
	Running   int                     `json:"running"`
	Input     *contracts.InputStatus  `json:"input"`
	Output    *contracts.OutputStatus `json:"output"`
	// GrabSuspended — перехват отключён после экстренной остановки.
	GrabSuspended bool `json:"grab_suspended"`
	// Playing — идущие воспроизведения; Recording — идущая запись.
	Playing   int                      `json:"playing"`
	Recording *contracts.RecordingInfo `json:"recording"`
}

// newStatusCmd создаёт команду `mkey status`.
func newStatusCmd(tr *i18n.Translator) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: tr.T("cli.status.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			c := newClient(runtimeDir(), tr.Lang())

			// Запрашиваем состояние; не запущен — сообщаем, как запустить.
			var st statusInfo
			var raw json.RawMessage
			err := c.do(cmd.Context(), "GET", "/api/v1/status", nil, &raw)
			if errors.Is(err, errNotRunning) {
				printf(out, "%s\n", tr.T("cli.status.not_running"))
				return nil
			}
			if err != nil {
				return err
			}
			if asJSON {
				printf(out, "%s\n", raw)
				return nil
			}
			if err := json.Unmarshal(raw, &st); err != nil {
				return err
			}

			// Человекочитаемый вывод.
			printf(out, "%s\n", tr.T("cli.status.running", i18n.A("version", st.Version), i18n.A("pid", st.PID),
				i18n.A("since", st.StartedAt.Local().Format("02.01.2006 15:04"))))
			if st.Output != nil {
				if st.Output.Available {
					printf(out, "  %s\n", tr.T("cli.status.output.ok"))
				} else {
					printf(out, "  %s\n", tr.T("cli.status.output.unavailable"))
				}
			}
			if st.Input != nil {
				printf(out, "  %s\n", tr.T("cli.status.input", i18n.A("open", st.Input.Open), i18n.A("denied", len(st.Input.Denied))))
			}
			printf(out, "  %s\n", tr.T("cli.status.macros", i18n.A("count", st.Running)))
			if st.Playing > 0 {
				printf(out, "  %s\n", tr.T("cli.status.playing", i18n.A("count", st.Playing)))
			}
			if st.Recording != nil {
				printf(out, "  %s\n", tr.T("cli.status.recording", i18n.A("name", st.Recording.Name),
					i18n.A("seconds", st.Recording.DurationMS/1000)))
			}
			if st.GrabSuspended {
				printf(out, "  %s\n", tr.T("cli.status.grab_suspended"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, tr.T("cli.flag.json"))
	return cmd
}

// newStopCmd создаёт команду `mkey stop` — прервать все выполняющиеся макросы.
func newStopCmd(tr *i18n.Translator) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: tr.T("cli.stop.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var resp struct {
				Stopped int `json:"stopped"`
			}
			err := newClient(runtimeDir(), tr.Lang()).do(cmd.Context(), "POST", "/api/v1/stop", nil, &resp)
			if errors.Is(err, errNotRunning) {
				printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.status.not_running"))
				return nil
			}
			if err != nil {
				return err
			}
			printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.stop.done", i18n.A("count", resp.Stopped)))
			return nil
		},
	}
}

// newPanicCmd создаёт команду `mkey panic` — экстренная остановка (SEC-1).
func newPanicCmd(tr *i18n.Translator) *cobra.Command {
	return &cobra.Command{
		Use:   "panic",
		Short: tr.T("cli.panic.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := newClient(runtimeDir(), tr.Lang()).do(cmd.Context(), "POST", "/api/v1/panic", nil, nil)
			if errors.Is(err, errNotRunning) {
				printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.status.not_running"))
				return nil
			}
			if err != nil {
				return err
			}
			printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.panic.done"))
			return nil
		},
	}
}

// newDevicesCmd создаёт команду `mkey devices` — устройства ввода, которые видит демон.
func newDevicesCmd(tr *i18n.Translator) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "devices",
		Short: tr.T("cli.devices.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			c := newClient(runtimeDir(), tr.Lang())
			if err := ensureDaemon(cmd.Context(), c, tr, true); err != nil {
				return err
			}

			// Запрашиваем список.
			var resp struct {
				Devices []contracts.InputDevice `json:"devices"`
				Status  contracts.InputStatus   `json:"status"`
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), "GET", "/api/v1/devices", nil, &raw); err != nil {
				return err
			}
			if asJSON {
				printf(out, "%s\n", raw)
				return nil
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}

			// Таблица: путь, VID:PID, классы, имя.
			if len(resp.Devices) == 0 {
				printf(out, "%s\n", tr.T("cli.debug.devices.none"))
			}
			for _, d := range resp.Devices {
				printf(out, "%-20s %s  %-22s %s\n", d.Info.Path, d.Info.ID, kindsString(d.Kinds), d.Info.Name)
			}
			if n := len(resp.Status.Denied); n > 0 {
				printf(out, "\n%s\n", tr.T("cli.debug.devices.denied", i18n.A("count", n)))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, tr.T("cli.flag.json"))
	cmd.AddCommand(newDevicesWatchCmd(tr), newDevicesInspectCmd(tr))
	return cmd
}

// newLogsCmd создаёт команду `mkey logs [-f]` — показать журнал демона.
func newLogsCmd(tr *i18n.Translator) *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: tr.T("cli.logs.short"),
		Long:  tr.T("cli.logs.long", i18n.A("path", logPath())),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			// Открываем журнал; его нет, если демон ещё ни разу не запускался.
			f, err := os.Open(logPath())
			if errors.Is(err, os.ErrNotExist) {
				printf(out, "%s\n", tr.T("cli.logs.none", i18n.A("path", logPath())))
				return nil
			}
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()

			// Печатаем имеющиеся строки; с -f продолжаем следить за новыми до Ctrl+C.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			rd := bufio.NewReader(f)
			for {
				line, err := rd.ReadString('\n')
				if line != "" {
					printf(out, "%s", strings.TrimRight(line, "\n")+"\n")
				}
				if errors.Is(err, io.EOF) {
					if !follow {
						return nil
					}
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(300 * time.Millisecond):
					}
					continue
				}
				if err != nil {
					return err
				}
			}
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, tr.T("cli.logs.flag.follow"))
	return cmd
}
