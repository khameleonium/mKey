package main

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"mkey/internal/contracts"
	"mkey/internal/i18n"
)

// daemonClient возвращает клиент демона, при необходимости запустив демон.
func daemonClient(cmd *cobra.Command, tr *i18n.Translator) (*client, error) {
	c := newClient(runtimeDir(), tr.Lang())
	return c, ensureDaemon(cmd.Context(), c, tr, true)
}

// newProjectCmd создаёт группу `mkey project …` (T3.9).
func newProjectCmd(tr *i18n.Translator) *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: tr.T("cli.project.short")}

	// mkey project list — список проектов.
	list := &cobra.Command{
		Use: "list", Short: tr.T("cli.project.list.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var resp struct {
				Dir      string `json:"dir"`
				Projects []struct {
					ID      string `json:"id"`
					Name    string `json:"name"`
					Enabled bool   `json:"enabled"`
					Events  int    `json:"events"`
					Error   string `json:"error"`
				} `json:"projects"`
			}
			if err := c.do(cmd.Context(), http.MethodGet, "/api/v1/projects", nil, &resp); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			printf(out, "%s\n\n", tr.T("cli.project.dir", i18n.A("dir", resp.Dir)))
			if len(resp.Projects) == 0 {
				printf(out, "%s\n", tr.T("cli.project.none"))
			}
			for _, p := range resp.Projects {
				state := tr.T("cli.state.off")
				if p.Enabled {
					state = tr.T("cli.state.on")
				}
				printf(out, "  %-20s %-10s %s (%s)\n", p.ID, state, p.Name, tr.T("cli.project.events", i18n.A("count", p.Events)))
				if p.Error != "" {
					printf(out, "      %s\n", tr.T("cli.project.error", i18n.A("error", p.Error)))
				}
			}
			return nil
		},
	}

	// mkey project enable|disable ID.
	toggle := func(action string, on bool) *cobra.Command {
		return &cobra.Command{
			Use: action + " <project>", Short: tr.T("cli.project." + action + ".short"), Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := daemonClient(cmd, tr)
				if err != nil {
					return err
				}
				if err := c.do(cmd.Context(), http.MethodPost, "/api/v1/projects/"+url.PathEscape(args[0])+"/"+action, nil, nil); err != nil {
					return err
				}
				key := "cli.project.disabled"
				if on {
					key = "cli.project.enabled"
				}
				printf(cmd.OutOrStdout(), "%s\n", tr.T(key, i18n.A("id", args[0])))
				return nil
			},
		}
	}

	// mkey project import FILE — импорт (проект выключен до проверки).
	imp := &cobra.Command{
		Use: "import <file>", Short: tr.T("cli.project.import.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var resp struct {
				ID string `json:"id"`
			}
			if err := c.do(cmd.Context(), http.MethodPost, "/api/v1/projects/import", map[string]string{"name": args[0], "content": string(data)}, &resp); err != nil {
				return err
			}
			printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.project.imported", i18n.A("id", resp.ID)))
			return nil
		},
	}
	cmd.AddCommand(list, toggle("enable", true), toggle("disable", false), imp)
	return cmd
}

// splitEvent разбирает "проект/событие".
func splitEvent(tr *i18n.Translator, s string) (string, string, error) {
	p, e, ok := strings.Cut(s, "/")
	if !ok || p == "" || e == "" {
		return "", "", errors.New(tr.T("cli.event.bad_ref", i18n.A("ref", s)))
	}
	return p, e, nil
}

// newEventCmd создаёт группу `mkey event …`.
func newEventCmd(tr *i18n.Translator) *cobra.Command {
	cmd := &cobra.Command{Use: "event", Short: tr.T("cli.event.short")}

	// mkey event list — события всех проектов.
	list := &cobra.Command{
		Use: "list", Short: tr.T("cli.event.list.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			var resp struct {
				Events []contracts.EventStatus `json:"events"`
			}
			if err := c.do(cmd.Context(), http.MethodGet, "/api/v1/events", nil, &resp); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(resp.Events) == 0 {
				printf(out, "%s\n", tr.T("cli.event.none"))
			}
			for _, e := range resp.Events {
				state := tr.T("cli.state.off")
				if e.Enabled {
					state = tr.T("cli.state.on")
				}
				extra := ""
				if e.Running > 0 {
					extra += " " + tr.T("cli.event.running", i18n.A("count", e.Running))
				}
				if e.Toggled {
					extra += " " + tr.T("cli.event.toggled")
				}
				printf(out, "  %-30s %-10s %-24s %s%s\n", e.Project+"/"+e.Event, state, strings.Join(e.Triggers, ","), e.Name, extra)
			}
			return nil
		},
	}

	// mkey event enable|disable P/E.
	toggle := func(action string) *cobra.Command {
		return &cobra.Command{
			Use: action + " <project/event>", Short: tr.T("cli.event." + action + ".short"), Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				p, e, err := splitEvent(tr, args[0])
				if err != nil {
					return err
				}
				c, err := daemonClient(cmd, tr)
				if err != nil {
					return err
				}
				return c.do(cmd.Context(), http.MethodPost, "/api/v1/events/"+url.PathEscape(p)+"/"+url.PathEscape(e)+"/"+action, nil, nil)
			},
		}
	}
	cmd.AddCommand(list, newRunCmd(tr, "run"), toggle("enable"), toggle("disable"))
	return cmd
}

// newRunCmd создаёт команду запуска события вручную: `mkey run P/E` и `mkey event run P/E`.
func newRunCmd(tr *i18n.Translator, use string) *cobra.Command {
	return &cobra.Command{
		Use: use + " <project/event>", Short: tr.T("cli.run.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, e, err := splitEvent(tr, args[0])
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			return c.do(ctx, http.MethodPost, "/api/v1/events/"+url.PathEscape(p)+"/"+url.PathEscape(e)+"/run", nil, nil)
		},
	}
}

// newVarCmd создаёт группу `mkey var get|set|list` (проект — флаг --project или MKEY_PROJECT).
func newVarCmd(tr *i18n.Translator) *cobra.Command {
	var proj string
	cmd := &cobra.Command{Use: "var", Short: tr.T("cli.var.short")}
	cmd.PersistentFlags().StringVarP(&proj, "project", "p", os.Getenv("MKEY_PROJECT"), tr.T("cli.var.flag.project"))

	// projectOrErr возвращает проект или понятную ошибку.
	projectOrErr := func() (string, error) {
		if proj == "" {
			return "", errors.New(tr.T("cli.var.no_project"))
		}
		return proj, nil
	}

	// vars читает переменные проекта.
	vars := func(cmd *cobra.Command) (map[string]any, error) {
		p, err := projectOrErr()
		if err != nil {
			return nil, err
		}
		c, err := daemonClient(cmd, tr)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Vars map[string]any `json:"vars"`
		}
		err = c.do(cmd.Context(), http.MethodGet, "/api/v1/vars/"+url.PathEscape(p), nil, &resp)
		return resp.Vars, err
	}

	get := &cobra.Command{
		Use: "get <name>", Short: tr.T("cli.var.get.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			all, err := vars(cmd)
			if err != nil {
				return err
			}
			v, ok := all[args[0]]
			if !ok {
				return errors.New(tr.T("cli.var.not_found", i18n.A("name", args[0])))
			}
			printf(cmd.OutOrStdout(), "%v\n", v)
			return nil
		},
	}
	list := &cobra.Command{
		Use: "list", Short: tr.T("cli.var.list.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			all, err := vars(cmd)
			if err != nil {
				return err
			}
			for k, v := range all {
				printf(cmd.OutOrStdout(), "%s = %v\n", k, v)
			}
			return nil
		},
	}
	set := &cobra.Command{
		Use: "set <name> <value>", Short: tr.T("cli.var.set.short"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := projectOrErr()
			if err != nil {
				return err
			}
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			return c.do(cmd.Context(), http.MethodPut, "/api/v1/vars/"+url.PathEscape(p)+"/"+url.PathEscape(args[0]),
				map[string]any{"value": parseValue(args[1])}, nil)
		},
	}
	cmd.AddCommand(get, set, list)
	return cmd
}

// parseValue переводит строку из командной строки в число, логическое значение или строку.
func parseValue(s string) any {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	return s
}

// newWaitCmd создаёт команду `mkey wait key <клавиша> [--timeout]` — для bash-скриптов.
func newWaitCmd(tr *i18n.Translator) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{Use: "wait", Short: tr.T("cli.wait.short")}
	key := &cobra.Command{
		Use: "key <key>", Short: tr.T("cli.wait.key.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			err = c.do(ctx, http.MethodPost, "/api/v1/wait/key", map[string]any{"key": args[0], "timeout_ms": timeout.Milliseconds()}, nil)
			var ae *apiError
			if errors.As(err, &ae) && ae.Status == http.StatusRequestTimeout {
				return userError(tr.T("cli.wait.timeout"))
			}
			return err
		},
	}
	key.Flags().DurationVar(&timeout, "timeout", 0, tr.T("cli.wait.flag.timeout"))
	cmd.AddCommand(key)
	return cmd
}

// newResumeCmd создаёт команду `mkey resume` — снова разрешить перехват после экстренной остановки.
func newResumeCmd(tr *i18n.Translator) *cobra.Command {
	return &cobra.Command{
		Use: "resume", Short: tr.T("cli.resume.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := daemonClient(cmd, tr)
			if err != nil {
				return err
			}
			if err := c.do(cmd.Context(), http.MethodPost, "/api/v1/resume", nil, nil); err != nil {
				return err
			}
			printf(cmd.OutOrStdout(), "%s\n", tr.T("cli.resume.done"))
			return nil
		},
	}
}
