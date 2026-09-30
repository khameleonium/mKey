package desktop

import (
	"context"
	"errors"
	"os/exec"

	"github.com/godbus/dbus/v5"

	"mkey/internal/contracts"
)

// Адрес стандартного сервиса уведомлений рабочего стола (freedesktop.org Desktop Notifications).
const (
	notifyService = "org.freedesktop.Notifications"
	notifyPath    = "/org/freedesktop/Notifications"
	notifyMethod  = "org.freedesktop.Notifications.Notify"
)

// Notify показывает уведомление через D-Bus (contracts.Notifier).
func (m *Module) Notify(ctx context.Context, title, body string) error {
	if m.conn == nil {
		return errors.New("desktop notifications are unavailable (no session D-Bus)")
	}
	// Параметры Notify: приложение, заменяемое уведомление, иконка, заголовок, текст, кнопки, подсказки, время показа.
	return m.conn.Object(notifyService, notifyPath).CallWithContext(ctx, notifyMethod, 0,
		"mKey", uint32(0), "input-keyboard", title, body, []string{}, map[string]dbus.Variant{}, int32(-1),
	).Err
}

// OpenURL открывает адрес в браузере по умолчанию (contracts.URLOpener): xdg-open,
// а если его нет — gio open (GNOME) или kde-open (KDE).
func (m *Module) OpenURL(_ context.Context, url string) error {
	for _, argv := range [][]string{{"xdg-open", url}, {"gio", "open", url}, {"kde-open", url}} {
		// Пропускаем программы, которых нет в системе.
		path, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		// Запускаем без ожидания: браузер живёт своей жизнью; Wait в фоне убирает зомби-процесс.
		cmd := exec.Command(path, argv[1:]...)
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	return errors.New("no program to open links (install xdg-utils)")
}

// watchNotices показывает пользователю важные события программы: экстренную остановку
// и ошибки в проектах (работает прежняя версия проекта).
func (m *Module) watchNotices(emergency, projectErr, engineErr <-chan contracts.Event) {
	defer m.wg.Done()
	for {
		var title, body string
		select {
		case <-m.ctx.Done():
			return
		case _, ok := <-emergency:
			if !ok {
				return
			}
			title, body = m.tr.T("notify.emergency.title"), m.tr.T("notify.emergency.body")
		case e, ok := <-projectErr:
			if !ok {
				return
			}
			title, body = m.projectErrorText(e)
		case e, ok := <-engineErr:
			if !ok {
				return
			}
			title, body = m.projectErrorText(e)
		}
		if err := m.Notify(m.ctx, title, body); err != nil {
			m.log.Debug("notification not shown", "err", err)
		}
	}
}

// projectErrorText готовит текст уведомления об ошибке в проекте.
func (m *Module) projectErrorText(e contracts.Event) (string, string) {
	pe, _ := e.Payload.(contracts.ProjectError)
	return m.tr.T("notify.project_error.title", contracts.Arg{Name: "project", Value: pe.ID}),
		m.tr.T("notify.project_error.body", contracts.Arg{Name: "error", Value: pe.Error})
}
