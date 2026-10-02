package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"mkey/internal/i18n"
)

// watchEvent — запись монитора нажатий от демона (GET /api/v1/input/watch).
type watchEvent struct {
	Time       time.Time `json:"time"`
	DeviceName string    `json:"device_name"`
	Device     string    `json:"device"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Kernel     string    `json:"kernel"`
	Action     string    `json:"action"`
	Value      int32     `json:"value"`
	DY         int32     `json:"dy"`
}

// newDevicesWatchCmd создаёт команду `mkey devices watch` — показывать нажатия на всех устройствах
// (или на одном: --device) непрерывно, пока не нажат Ctrl+C (FR-DEV-8). Нажатия сохраняются
// в файл, только если человек сам попросил об этом (--out).
func newDevicesWatchCmd(tr *i18n.Translator) *cobra.Command {
	var (
		moves       bool
		device, out string
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: tr.T("cli.watch.short"),
		Long:  tr.T("cli.watch.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Ctrl+C заканчивает наблюдение.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			c := newClient(runtimeDir(), tr.Lang())
			if err := ensureDaemon(ctx, c, tr, true); err != nil {
				return err
			}
			// Файл журнала, если его попросили: создаётся заново, читать может только владелец.
			var file *os.File
			if out != "" {
				f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				file = f
			}
			screen := cmd.OutOrStdout()

			// Поток событий от демона: строки «data: {…}»; каждая — на экран и в файл. Подсказка —
			// когда демон принял поток (устройство нашлось).
			opened := func() { printf(screen, "%s\n\n", tr.T("cli.watch.started")) }
			err := c.stream(ctx, watchPath(moves, device), opened, func(data []byte) {
				var e watchEvent
				if json.Unmarshal(data, &e) != nil {
					return
				}
				line := formatWatch(tr, e)
				printf(screen, "%s\n", line)
				if file != nil {
					_, _ = fmt.Fprintln(file, line)
				}
			})
			if ctx.Err() != nil {
				printf(screen, "\n%s\n", tr.T("cli.watch.stopped"))
				if file != nil {
					printf(screen, "%s\n", tr.T("cli.watch.saved", i18n.A("path", out)))
				}
				return nil
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&moves, "moves", false, tr.T("cli.watch.flag.moves"))
	cmd.Flags().StringVar(&device, "device", "", tr.T("cli.watch.flag.device"))
	cmd.Flags().StringVar(&out, "out", "", tr.T("cli.watch.flag.out"))
	return cmd
}

// watchPath — адрес потока монитора: перемещения мыши (moves) и фильтр по устройству (device).
func watchPath(moves bool, device string) string {
	q := url.Values{}
	if moves {
		q.Set("moves", "1")
	}
	if device != "" {
		q.Set("device", device)
	}
	if len(q) == 0 {
		return "/api/v1/input/watch"
	}
	return "/api/v1/input/watch?" + q.Encode()
}

// formatWatch записывает событие одной строкой: «12:34:56.789  {Mouse0} нажата  — Logitech USB Optical Mouse».
func formatWatch(tr *i18n.Translator, e watchEvent) string {
	var what string
	switch e.Kind {
	case "key":
		key := "cli.watch.down"
		if e.Action == "up" {
			key = "cli.watch.up"
		}
		what = tr.T(key, i18n.A("key", "{"+e.Name+"}"))
	case "axis":
		what = tr.T("cli.watch.axis", i18n.A("axis", e.Name), i18n.A("value", e.Value))
	case "wheel":
		what = tr.T("cli.watch.wheel", i18n.A("value", fmt.Sprintf("%+d", e.Value)))
	default:
		what = tr.T("cli.watch.move", i18n.A("dx", fmt.Sprintf("%+d", e.Value)), i18n.A("dy", fmt.Sprintf("%+d", e.DY)))
	}
	device := e.DeviceName
	if device == "" {
		device = e.Device
	}
	return fmt.Sprintf("%s  %-28s — %s  (%s)", e.Time.Local().Format("15:04:05.000"), what, device, e.Kernel)
}

// stream выполняет GET-запрос потока Server-Sent Events: после успешного ответа вызывает onOpen,
// затем onData для данных каждого события, пока поток не закончится или не отменят ctx.
// Ошибка API (например, устройство не найдено) — *apiError.
func (c *client) stream(ctx context.Context, path string, onOpen func(), onData func([]byte)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://mkey"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept-Language", c.lang)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return readAPIError(resp)
	}
	onOpen()

	// Строки «data: …»; остальные (комментарии, «event: …», пустые) пропускаются.
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			onData([]byte(data))
		}
	}
	return sc.Err()
}
