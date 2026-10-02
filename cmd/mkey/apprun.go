package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"mkey/internal/app"
	"mkey/internal/i18n"
	"mkey/internal/registry"
)

// newLogger создаёт логгер CLI: по умолчанию только предупреждения и ошибки в stderr,
// с флагом --verbose — подробный журнал.
func newLogger(cmd *cobra.Command) *slog.Logger {
	level := slog.LevelWarn
	if v, _ := cmd.Flags().GetBool("verbose"); v {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// withModules собирает приложение только из модулей entries, запускает его, выполняет fn
// и останавливает приложение (даже если fn вернул ошибку).
//
// Так работают команды, которым демон не нужен или ещё не запущен (doctor, мастер установки):
// нужные модули поднимаются прямо в их процессе.
func withModules(ctx context.Context, cmd *cobra.Command, tr *i18n.Translator, entries []registry.Entry, fn func(a *app.App) error) error {
	// Собираем и запускаем приложение из выбранных модулей.
	a, err := app.New(app.Options{Lang: tr.Lang(), Logger: newLogger(cmd), Modules: entries})
	if err != nil {
		return err
	}
	if err := a.Start(ctx); err != nil {
		return err
	}

	// Выполняем действие и всегда останавливаем модули (отпускание клавиш, закрытие устройств).
	runErr := fn(a)
	stopErr := a.Stop(context.WithoutCancel(ctx))
	return errors.Join(runErr, stopErr)
}

// printf печатает форматированную строку в w; ошибки вывода в терминал игнорируются.
func printf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
