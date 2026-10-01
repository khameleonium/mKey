package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mkey/internal/api"
	"mkey/internal/i18n"
	"mkey/internal/platform/detect"
)

// errNotRunning — демон не запущен (нет сокета или он не отвечает).
var errNotRunning = errors.New("mkey daemon is not running")

// runtimeDir возвращает каталог времени выполнения текущего пользователя.
func runtimeDir() string {
	dir, _ := detect.RuntimeDir(os.Getenv, os.Getuid())
	return dir
}

// client — HTTP-клиент API демона через Unix-сокет (D9).
type client struct {
	// http — клиент с транспортом через сокет.
	http *http.Client
	// lang — язык, на котором демон должен отвечать.
	lang string
}

// apiError — ошибка, которую вернул демон в едином формате.
type apiError struct {
	Status  int
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// Error возвращает переведённое сообщение демона.
func (e *apiError) Error() string { return e.Message }

// newClient создаёт клиент для сокета в каталоге dir.
func newClient(dir, lang string) *client {
	sock := filepath.Join(dir, api.SocketFile)
	return &client{lang: lang, http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}}
}

// do выполняет запрос к API и разбирает ответ в out (если out не nil; *[]byte — тело как есть,
// например файл профиля).
// Возвращает errNotRunning, если демон не запущен, и *apiError для ошибок API.
func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	// Готовим запрос с телом в JSON и языком ответа.
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://mkey"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", c.lang)

	// Отправляем; ошибка соединения означает, что демон не запущен.
	resp, err := c.http.Do(req)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return errNotRunning
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Ошибка API в едином формате.
	if resp.StatusCode >= 400 {
		var eb struct {
			Error apiError `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&eb); err != nil {
			return fmt.Errorf("mkey daemon: HTTP %d", resp.StatusCode)
		}
		eb.Error.Status = resp.StatusCode
		return &eb.Error
	}

	// Успешный ответ: как есть или из JSON.
	if out == nil {
		return nil
	}
	if raw, ok := out.(*[]byte); ok {
		*raw, err = io.ReadAll(resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ensureDaemon проверяет, что демон отвечает, и при необходимости запускает его в фоне.
// Так команды вроде `mkey send` работают без ручного запуска демона.
func ensureDaemon(ctx context.Context, c *client, tr *i18n.Translator, autostart bool) error {
	// Демон уже работает.
	err := c.do(ctx, http.MethodGet, "/api/v1/status", nil, nil)
	if !errors.Is(err, errNotRunning) {
		return err
	}
	if !autostart {
		return errors.New(tr.T("cli.daemon.not_running"))
	}

	// Запускаем `mkey daemon` отдельным процессом в новой сессии: он переживёт закрытие терминала.
	fmt.Fprintln(os.Stderr, tr.T("cli.daemon.autostart"))
	// Установленная копия предпочтительнее: демон должен быть тем же, что запускается при входе.
	exe := newInstallEnv().Exe
	if ienv := newInstallEnv(); ienv.Installed() {
		exe = ienv.BinPath()
	}
	if err := spawnDaemon(exe, tr.Lang()); err != nil {
		return errors.New(tr.T("cli.daemon.autostart_failed", i18n.A("error", err)))
	}

	// Ждём, пока демон начнёт отвечать (до 5 секунд).
	if !waitDaemon(ctx, c) {
		return errors.New(tr.T("cli.daemon.autostart_failed", i18n.A("error", "timeout")))
	}
	return nil
}

// userError — готовое сообщение для пользователя: печатается как есть, без префикса «Ошибка:».
type userError string

// Error возвращает текст сообщения.
func (e userError) Error() string { return string(e) }

// formatAPIError превращает ошибку API в понятный текст. Для ошибки в макросе добавляет
// место ошибки и строку макроса со стрелкой под ошибочным символом.
func formatAPIError(tr *i18n.Translator, err error, src string) string {
	var ae *apiError
	if !errors.As(err, &ae) {
		return err.Error()
	}

	// Позиция есть только у ошибок в тексте макроса.
	pos, ok := ae.Details["pos"].(map[string]any)
	if !ok {
		return ae.Message
	}
	line, _ := pos["line"].(float64)
	col, _ := pos["col"].(float64)
	msg := tr.T("dsl.error_at", i18n.A("line", int(line)), i18n.A("col", int(col)), i18n.A("message", ae.Message))

	// Строка макроса и стрелка «^» под символом с ошибкой.
	lines := strings.Split(src, "\n")
	if l := int(line) - 1; l >= 0 && l < len(lines) && col >= 1 {
		msg += "\n\n  " + lines[l] + "\n  " + strings.Repeat(" ", int(col)-1) + "^"
	}
	return msg
}
