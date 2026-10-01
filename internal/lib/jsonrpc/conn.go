package jsonrpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// Коды ошибок: стандартные JSON-RPC и свои (ADR-0029, docs/plugins.md).
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	// CodeFailed — ошибка выполнения; текст — для человека.
	CodeFailed = -32000
	// CodeForbidden — нет разрешения.
	CodeForbidden = -32001
	// CodeCancelled — запрос отменён.
	CodeCancelled = -32002
)

// MethodCancel — уведомление об отмене запроса: {"id": …}.
const MethodCancel = "$/cancelRequest"

// maxLine — предел длины одного сообщения: защита от «бесконечной» строки (16 МиБ хватает
// и на большие схемы параметров).
const maxLine = 16 << 20

// ErrClosed — соединение закрыто (другая сторона завершилась или вызван Close).
var ErrClosed = errors.New("jsonrpc: connection closed")

// Error — ошибка JSON-RPC: код, сообщение и необязательные данные.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error возвращает сообщение ошибки.
func (e *Error) Error() string { return e.Message }

// Errorf создаёт ошибку с кодом и текстом.
func Errorf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Handler обрабатывает входящий запрос или уведомление (notify — ответ не нужен). Возвращённое
// значение кодируется в result; ошибка *Error передаётся как есть, другая — с кодом CodeFailed.
type Handler func(ctx context.Context, method string, params json.RawMessage, notify bool) (any, error)

// message — сообщение протокола (запрос, уведомление или ответ).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Conn — соединение JSON-RPC 2.0.
type Conn struct {
	// w и wmu — запись сообщений (одно сообщение — одна строка, под блокировкой).
	w   io.Writer
	wmu sync.Mutex
	// handler — обработчик входящих запросов.
	handler Handler

	// mu защищает next, pending, incoming и err.
	mu sync.Mutex
	// next — номер следующего своего запроса; pending — ожидающие ответа.
	next    int64
	pending map[string]chan *message
	// incoming — отмена входящих запросов по их id.
	incoming map[string]context.CancelFunc
	// err — причина закрытия; done закрывается при закрытии соединения.
	err  error
	done chan struct{}
	// wg ждёт обработчиков входящих запросов.
	wg sync.WaitGroup
}

// New создаёт соединение и начинает читать сообщения из r; ответы и запросы пишутся в w.
// h может быть nil — тогда на все запросы отвечается «метода нет».
func New(r io.Reader, w io.Writer, h Handler) *Conn {
	c := &Conn{w: w, handler: h, pending: map[string]chan *message{}, incoming: map[string]context.CancelFunc{}, done: make(chan struct{})}
	go c.read(r)
	return c
}

// Done закрывается, когда соединение закрыто; Err — причина.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err возвращает причину закрытия (nil — соединение открыто).
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close закрывает соединение: ожидающие вызовы получают ErrClosed, входящие запросы отменяются.
// Поток чтения завершится, когда закроется r (это дело владельца потоков).
func (c *Conn) Close() { c.shutdown(ErrClosed) }

// Wait ждёт завершения обработчиков входящих запросов (после закрытия соединения).
func (c *Conn) Wait() { c.wg.Wait() }

// shutdown закрывает соединение с причиной err (один раз).
func (c *Conn) shutdown(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	for _, cancel := range c.incoming {
		cancel()
	}
	pending := c.pending
	c.pending = map[string]chan *message{}
	c.mu.Unlock()
	close(c.done)
	for _, ch := range pending {
		close(ch)
	}
}

// Call вызывает метод другой стороны и ждёт ответа; result (указатель или nil) заполняется из
// result ответа. Отмена ctx отправляет $/cancelRequest и возвращает ctx.Err().
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	// Номер запроса и место для ответа.
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.next++
	id := strconv.FormatInt(c.next, 10)
	ch := make(chan *message, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}

	// Отправка.
	raw, err := marshalParams(params)
	if err != nil {
		forget()
		return err
	}
	if err := c.write(message{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method, Params: raw}); err != nil {
		forget()
		return err
	}

	// Ответ, отмена или закрытие.
	select {
	case resp, ok := <-ch:
		if !ok {
			return c.Err()
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("jsonrpc: %s: decode result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		forget()
		_ = c.Notify(MethodCancel, map[string]any{"id": json.RawMessage(id)})
		return ctx.Err()
	}
}

// Notify отправляет уведомление (ответа не ждёт).
func (c *Conn) Notify(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.write(message{JSONRPC: "2.0", Method: method, Params: raw})
}

// marshalParams кодирует параметры (nil — без params).
func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: encode params: %w", err)
	}
	return raw, nil
}

// write отправляет одно сообщение одной строкой.
func (c *Conn) write(m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("jsonrpc: encode: %w", err)
	}
	data = append(data, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.Err(); err != nil {
		return err
	}
	if _, err := c.w.Write(data); err != nil {
		c.shutdown(fmt.Errorf("jsonrpc: write: %w", err))
		return c.Err()
	}
	return nil
}

// read читает сообщения построчно, пока поток не закончится; конец потока закрывает соединение.
func (c *Conn) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		// Одна строка — одно сообщение; пустые строки пропускаются.
		line, err := readLine(br)
		if len(bytes.TrimSpace(line)) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = ErrClosed
			}
			c.shutdown(err)
			return
		}
	}
}

// readLine читает строку до '\n' (без него) с пределом maxLine.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxLine {
			return nil, fmt.Errorf("jsonrpc: message longer than %d bytes", maxLine)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimSuffix(line, []byte("\n")), err
	}
}

// dispatch разбирает сообщение: ответ — ожидающему вызову, запрос — обработчику.
func (c *Conn) dispatch(line []byte) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		_ = c.write(message{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: Errorf(CodeParse, "parse error: %v", err)})
		return
	}

	// Ответ на свой запрос.
	if m.Method == "" {
		id := idKey(m.ID)
		c.mu.Lock()
		ch, ok := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ok {
			ch <- &m
		}
		return
	}

	// Отмена входящего запроса.
	if m.Method == MethodCancel {
		var p struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			c.mu.Lock()
			if cancel, ok := c.incoming[idKey(p.ID)]; ok {
				cancel()
			}
			c.mu.Unlock()
		}
		return
	}

	// Запрос или уведомление — обработчику в отдельной горутине со своим контекстом.
	notify := len(m.ID) == 0 || string(m.ID) == "null"
	ctx, cancel := context.WithCancel(context.Background())
	key := idKey(m.ID)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		cancel()
		return
	}
	if !notify {
		c.incoming[key] = cancel
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		defer cancel()
		result, err := c.handle(ctx, m, notify)
		if !notify {
			c.mu.Lock()
			delete(c.incoming, key)
			c.mu.Unlock()
			c.reply(m.ID, result, err)
		}
	}()
}

// handle вызывает обработчик (нет обработчика — «метода нет»).
func (c *Conn) handle(ctx context.Context, m message, notify bool) (any, error) {
	if c.handler == nil {
		return nil, Errorf(CodeMethodNotFound, "method %q not found", m.Method)
	}
	return c.handler(ctx, m.Method, m.Params, notify)
}

// reply отправляет ответ на запрос id: результат или ошибку.
func (c *Conn) reply(id json.RawMessage, result any, err error) {
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			code := CodeFailed
			if errors.Is(err, context.Canceled) {
				code = CodeCancelled
			}
			e = &Error{Code: code, Message: err.Error()}
		}
		_ = c.write(message{JSONRPC: "2.0", ID: id, Error: e})
		return
	}
	raw, mErr := json.Marshal(result)
	if mErr != nil {
		_ = c.write(message{JSONRPC: "2.0", ID: id, Error: Errorf(CodeInternal, "encode result: %v", mErr)})
		return
	}
	if string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	_ = c.write(message{JSONRPC: "2.0", ID: id, Result: raw})
}

// idKey приводит id к ключу карты: число и строка "1" — один ключ (свои id — числа).
func idKey(id json.RawMessage) string {
	s := string(bytes.TrimSpace(id))
	if uq, err := strconv.Unquote(s); err == nil {
		return uq
	}
	return s
}
