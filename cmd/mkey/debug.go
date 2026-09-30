package main

import (
	"errors"
	"fmt"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"mkey/internal/app"
	"mkey/internal/contracts"
	"mkey/internal/i18n"
	"mkey/internal/input"
	ev "mkey/internal/lib/evdev"
	"mkey/internal/lib/keys"
	"mkey/internal/output"
	"mkey/internal/registry"
)

// newDebugCmd создаёт группу отладочных команд `mkey debug …`.
// Они нужны для ручной проверки фазы 1 до появления демона (фаза 2) и будут заменены
// командами `mkey send` и `mkey devices`.
func newDebugCmd(tr *i18n.Translator) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: tr.T("cli.debug.short"),
	}
	cmd.AddCommand(newDebugTapCmd(tr), newDebugDevicesCmd(tr))
	return cmd
}

// newDebugTapCmd создаёт команду `mkey debug tap <клавиша> [--delay 3s] [--hold 30ms]`.
func newDebugTapCmd(tr *i18n.Translator) *cobra.Command {
	var delay, hold time.Duration
	cmd := &cobra.Command{
		Use:   "tap <key>",
		Short: tr.T("cli.debug.tap.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Находим клавишу по имени; при опечатке подсказываем ближайшую.
			key, ok := keys.Lookup(args[0])
			if !ok {
				if s := keys.Suggest(args[0]); s != "" {
					return errors.New(tr.T("cli.debug.unknown_key_suggest", i18n.A("name", args[0]), i18n.A("suggestion", s)))
				}
				return errors.New(tr.T("cli.debug.unknown_key", i18n.A("name", args[0])))
			}

			// Ctrl+C прерывает ожидание и нажатие; модуль output всё равно отпустит клавишу.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Поднимаем только модуль виртуальных устройств.
			entries := []registry.Entry{{Module: output.New(), Core: true}}
			return withModules(ctx, cmd, tr, entries, func(a *app.App) error {
				// Кнопки мыши отправляем через виртуальную мышь, остальное — через клавиатуру.
				vd, err := contracts.LookupService[contracts.VirtualDevices](a.Manager.Services())
				if err != nil {
					return err
				}
				dev, err := vd.Keyboard()
				if key.Code >= ev.BtnMouse && key.Code < ev.BtnJoystick {
					dev, err = vd.Mouse()
				}
				if err != nil {
					return errors.New(tr.T("cli.debug.output_unavailable", i18n.A("error", err)))
				}

				// Даём пользователю время переключиться в нужное окно.
				out := cmd.OutOrStdout()
				printf(out, "%s\n", tr.T("cli.debug.tap.countdown", i18n.A("seconds", delay.Seconds()), i18n.A("key", key.Name)))
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}

				// Нажимаем.
				if err := dev.Tap(ctx, key.Code, hold); err != nil {
					return err
				}
				printf(out, "%s\n", tr.T("cli.debug.tap.done", i18n.A("key", key.Name), i18n.A("device", dev.Name())))
				return nil
			})
		},
	}
	cmd.Flags().DurationVar(&delay, "delay", 3*time.Second, tr.T("cli.debug.tap.flag.delay"))
	cmd.Flags().DurationVar(&hold, "hold", 30*time.Millisecond, tr.T("cli.debug.tap.flag.hold"))
	return cmd
}

// newDebugDevicesCmd создаёт команду `mkey debug devices [--watch]`.
func newDebugDevicesCmd(tr *i18n.Translator) *cobra.Command {
	var watch bool
	cmd := &cobra.Command{
		Use:   "devices",
		Short: tr.T("cli.debug.devices.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Поднимаем только модуль чтения устройств.
			entries := []registry.Entry{{Module: input.New(), Core: true}}
			return withModules(ctx, cmd, tr, entries, func(a *app.App) error {
				src, err := contracts.LookupService[contracts.InputSource](a.Manager.Services())
				if err != nil {
					return err
				}

				// Список устройств: путь, VID:PID, классы, имя.
				out := cmd.OutOrStdout()
				devs := src.Devices()
				if len(devs) == 0 {
					printf(out, "%s\n", tr.T("cli.debug.devices.none"))
				}
				for _, d := range devs {
					printf(out, "%-20s %s  %-22s %s\n", d.Info.Path, d.Info.ID, kindsString(d.Kinds), d.Info.Name)
				}
				if denied := src.Status().Denied; len(denied) > 0 {
					printf(out, "\n%s\n", tr.T("cli.debug.devices.denied", i18n.A("count", len(denied))))
				}
				if !watch {
					return nil
				}

				// Поток событий до Ctrl+C (SYN_REPORT пропускаем, чтобы не засорять вывод).
				printf(out, "\n%s\n", tr.T("cli.debug.devices.watching"))
				events, cancel := src.Subscribe(256)
				defer cancel()
				for {
					select {
					case <-ctx.Done():
						return nil
					case e, ok := <-events:
						if !ok {
							return nil
						}
						if e.Event.Type != ev.EvSyn {
							printf(out, "%s  %s\n", e.Device, describe(e.Event))
						}
					}
				}
			})
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, tr.T("cli.debug.devices.flag.watch"))
	return cmd
}

// kindsString склеивает классы устройства через запятую.
func kindsString(kinds []ev.Kind) string {
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = string(k)
	}
	return strings.Join(parts, ",")
}

// describe возвращает описание события с именем клавиши mKey, если оно есть: "EV_KEY KEY_A 1 {A}".
func describe(e ev.Event) string {
	s := e.String()
	if e.Type == ev.EvKey {
		if name, ok := keys.NameOf(e.Code); ok {
			s += fmt.Sprintf("  {%s}", name)
		}
	}
	return s
}
