package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/i18n"
)

// inspectWidth — ширина строки, по которой переносятся длинные списки кнопок.
const inspectWidth = 100

// newDevicesInspectCmd создаёт команду `mkey devices inspect <устройство>`: всё об одном
// устройстве (FR-DEV-1) — постоянные имена, кнопки с именами для макросов, оси с диапазонами.
func newDevicesInspectCmd(tr *i18n.Translator) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "inspect <устройство>",
		Short: tr.T("cli.inspect.short"),
		Long:  tr.T("cli.inspect.long"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Подробности — у демона (инспектор устройств).
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), "GET", "/api/v1/devices/inspect?ref="+url.QueryEscape(args[0]), nil, &raw); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			if asJSON {
				printf(cmd.OutOrStdout(), "%s\n", raw)
				return nil
			}
			var resp struct {
				Device contracts.DeviceDetails `json:"device"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}
			printDevice(cmd.OutOrStdout(), tr, resp.Device)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, tr.T("cli.flag.json"))
	return cmd
}

// printDevice печатает подробности устройства понятным текстом.
func printDevice(out io.Writer, tr *i18n.Translator, d contracts.DeviceDetails) {
	// Заголовок: название и общие сведения.
	printf(out, "%s\n", d.Info.Name)
	field := func(key, value string) {
		if value != "" {
			printf(out, "  %-16s %s\n", tr.T(key), value)
		}
	}
	kinds := make([]string, 0, len(d.Kinds))
	for _, k := range d.Kinds {
		kinds = append(kinds, tr.T("device.kind."+string(k)))
	}
	field("cli.inspect.kind", strings.Join(kinds, ", "))
	field("cli.inspect.name", d.DeviceName)
	field("cli.inspect.auto_id", d.AutoID)
	field("cli.inspect.file", d.Info.Path)
	field("cli.inspect.by_id", d.ByID)
	field("cli.inspect.by_path", d.ByPath)
	field("cli.inspect.id", tr.T("cli.inspect.id_value", i18n.A("id", d.Info.ID.String()),
		i18n.A("version", fmt.Sprintf("%04x", d.Info.ID.Version)), i18n.A("bus", d.Bus)))
	field("cli.inspect.phys", d.Info.Phys)
	field("cli.inspect.uniq", d.Info.Uniq)
	field("cli.inspect.props", strings.Join(d.Props, ", "))

	// Кнопки: с именем для макросов — именами в строку; с авто-ID — авто-ID и именем ядра;
	// без того и другого — именами ядра.
	var named, labeled, unnamed []string
	for _, k := range d.Keys {
		switch {
		case k.Name != "":
			named = append(named, k.Name)
		case k.Label != "":
			labeled = append(labeled, fmt.Sprintf("{%s}=%s", k.Label, k.Kernel))
		default:
			unnamed = append(unnamed, fmt.Sprintf("%s (0x%x)", k.Kernel, k.Code))
		}
	}
	if len(named) > 0 {
		printf(out, "\n%s\n", tr.T("cli.inspect.keys", i18n.A("count", len(named)), i18n.A("example", "{"+named[0]+"}")))
		printWrapped(out, named)
	}
	if len(labeled) > 0 {
		printf(out, "\n%s\n", tr.T("cli.inspect.labeled", i18n.A("count", len(labeled))))
		printWrapped(out, labeled)
	}
	if len(unnamed) > 0 {
		printf(out, "\n%s\n", tr.T("cli.inspect.unnamed", i18n.A("count", len(unnamed))))
		printWrapped(out, unnamed)
	}

	// Оси: имя, код ядра, диапазон и мёртвая зона.
	if len(d.Axes) > 0 {
		printf(out, "\n%s\n", tr.T("cli.inspect.axes", i18n.A("count", len(d.Axes))))
		for _, a := range d.Axes {
			name := a.Name
			switch {
			case name != "":
			case a.Label != "":
				name = "{" + a.Label + "}"
			default:
				name = "—"
			}
			printf(out, "  %-14s %-22s %s\n", name, a.Kernel, tr.T("cli.inspect.range", i18n.A("min", a.Minimum), i18n.A("max", a.Maximum), i18n.A("flat", a.Flat)))
		}
	}

	// Остальное — именами ядра.
	for _, g := range []struct {
		key  string
		list []contracts.DeviceControl
	}{{"cli.inspect.rel", d.Rel}, {"cli.inspect.switches", d.Switches}, {"cli.inspect.leds", d.LEDs}, {"cli.inspect.ff", d.FF}} {
		if len(g.list) == 0 {
			continue
		}
		names := make([]string, 0, len(g.list))
		for _, c := range g.list {
			if c.Label != "" {
				names = append(names, fmt.Sprintf("{%s}=%s", c.Label, c.Kernel))
				continue
			}
			names = append(names, c.Kernel)
		}
		printf(out, "\n%s\n", tr.T(g.key))
		printWrapped(out, names)
	}
}

// printWrapped печатает слова через пробел с отступом, перенося строки по ширине inspectWidth.
func printWrapped(out io.Writer, words []string) {
	line := " "
	for _, w := range words {
		if len([]rune(line))+1+len([]rune(w)) > inspectWidth && line != " " {
			printf(out, "%s\n", line)
			line = " "
		}
		line += " " + w
	}
	if line != " " {
		printf(out, "%s\n", line)
	}
}

// newDevicesAutoIDsCmd создаёт команду `mkey devices auto-ids [режим]`: каким устройствам давать
// автоматические имена UnKey (FR-DEV-2). Без режима — показывает текущий и варианты.
func newDevicesAutoIDsCmd(tr *i18n.Translator) *cobra.Command {
	return &cobra.Command{
		Use:   "auto-ids [smart|all|unusual]",
		Short: tr.T("cli.autoids.short"),
		Long:  tr.T("cli.autoids.long"),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var resp struct {
				AutoIDs string `json:"auto_ids"`
			}

			// Без режима — текущий режим и что значит каждый.
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				if err := c.do(cmd.Context(), "GET", "/api/v1/settings/devices", nil, &resp); err != nil {
					return userError(formatAPIError(tr, err, ""))
				}
				printf(out, "%s\n\n", tr.T("cli.autoids.current", i18n.A("mode", resp.AutoIDs), i18n.A("text", tr.T("cli.autoids.mode."+resp.AutoIDs))))
				for _, mode := range []string{"smart", "all", "unusual"} {
					printf(out, "  %-8s %s\n", mode, tr.T("cli.autoids.mode."+mode))
				}
				printf(out, "\n%s\n", tr.T("cli.autoids.hint"))
				return nil
			}

			// Смена режима.
			req := map[string]string{"auto_ids": args[0]}
			if err := c.do(cmd.Context(), "PUT", "/api/v1/settings/devices", req, &resp); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}
			printf(out, "%s\n", tr.T("cli.autoids.set", i18n.A("mode", resp.AutoIDs), i18n.A("text", tr.T("cli.autoids.mode."+resp.AutoIDs))))
			return nil
		},
	}
}

// newDevicesRenameCmd создаёт команду `mkey devices rename <устройство> <имя> [--button N] [--clear]`:
// имя устройства или его кнопки для макросов (FR-DEV-3). Старые имена (UnKey001) работают всегда.
func newDevicesRenameCmd(tr *i18n.Translator) *cobra.Command {
	var button string
	var clear bool
	cmd := &cobra.Command{
		Use:   "rename <устройство> [имя]",
		Short: tr.T("cli.rename.short"),
		Long:  tr.T("cli.rename.long"),
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Новое имя или его сброс (--clear) — что-то одно.
			name := ""
			switch {
			case clear && len(args) == 2, !clear && len(args) == 1:
				return userError(tr.T("cli.rename.need_name"))
			case !clear:
				name = args[1]
			}

			// Переименование у демона.
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			req := map[string]string{"device": args[0], "control": button, "name": name}
			if err := c.do(cmd.Context(), "POST", "/api/v1/devices/rename", req, nil); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}

			// Итог: как теперь писать в макросах.
			key := "cli.rename.device_done"
			if button != "" {
				key = "cli.rename.button_done"
			}
			if clear {
				key += "_clear"
			}
			printf(cmd.OutOrStdout(), "%s\n", tr.T(key, i18n.A("device", args[0]), i18n.A("button", button), i18n.A("name", name)))
			return nil
		},
	}
	cmd.Flags().StringVar(&button, "button", "", tr.T("cli.rename.flag.button"))
	cmd.Flags().BoolVar(&clear, "clear", false, tr.T("cli.rename.flag.clear"))
	return cmd
}

// newDevicesVirtualCmd создаёт команду `mkey devices virtual`: виртуальные устройства включённых
// проектов (FR-VD-1) — имя в макросах, шаблон, проект, файл устройства или почему не создано.
func newDevicesVirtualCmd(tr *i18n.Translator) *cobra.Command {
	return &cobra.Command{
		Use:   "virtual",
		Short: tr.T("cli.virtual.short"),
		Long:  tr.T("cli.virtual.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var resp struct {
				Devices   []contracts.VirtualDeviceInfo `json:"devices"`
				Templates []string                      `json:"templates"`
			}
			if err := c.do(cmd.Context(), "GET", "/api/v1/devices/virtual", nil, &resp); err != nil {
				return userError(formatAPIError(tr, err, ""))
			}

			// Устройства или подсказка, как их завести.
			out := cmd.OutOrStdout()
			if len(resp.Devices) == 0 {
				printf(out, "%s\n", tr.T("cli.virtual.none"))
			}
			for _, d := range resp.Devices {
				state := d.Node
				if d.Error != "" {
					state = tr.T("cli.virtual.error", i18n.A("error", d.Error))
				}
				printf(out, "  %-12s %-12s %-16s %s\n", d.Name, d.Template, tr.T("cli.virtual.project", i18n.A("project", d.Project)), state)
			}
			printf(out, "\n%s\n", tr.T("cli.virtual.templates", i18n.A("list", strings.Join(resp.Templates, ", "))))
			return nil
		},
	}
}
