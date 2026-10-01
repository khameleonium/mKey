package dsl

import (
	"fmt"
	"sort"
	"strings"
)

// Коды ошибок языка макросов. Для каждого кода есть перевод в i18n
// (ключ совпадает с кодом), в тексте используются параметры из Error.Args.
const (
	ErrUnexpectedChar   = "dsl.unexpected_char"    // {char}
	ErrUnexpectedEnd    = "dsl.unexpected_end"     // конец текста внутри записи
	ErrTextOutside      = "dsl.text_outside"       // {char}: текст вне {"..."}
	ErrUnclosedBrace    = "dsl.unclosed_brace"     // нет "}"
	ErrUnclosedPause    = "dsl.unclosed_pause"     // нет "]"
	ErrUnclosedGroup    = "dsl.unclosed_group"     // нет ")"
	ErrUnexpectedClose  = "dsl.unexpected_close"   // {char}: лишняя закрывающая скобка
	ErrUnclosedText     = "dsl.unclosed_text"      // нет закрывающей кавычки
	ErrBadEscape        = "dsl.bad_escape"         // {escape}
	ErrEmptyBraces      = "dsl.empty_braces"       // {}
	ErrUnknownKey       = "dsl.unknown_key"        // {name}
	ErrUnknownKeyHint   = "dsl.unknown_key_hint"   // {name}, {suggestion}
	ErrBadNumber        = "dsl.bad_number"         // {text}
	ErrBadDuration      = "dsl.bad_duration"       // {text}
	ErrTooLong          = "dsl.too_long"           // {max}: пауза/удержание больше часа
	ErrBadRepeat        = "dsl.bad_repeat"         // {max}: повтор вне 1..10000
	ErrPrefixNotAllowed = "dsl.prefix_not_allowed" // ^/~ с текстом, командой, повтором…
	ErrStarOnlyRelease  = "dsl.star_only_release"  // {*} без ~
	ErrRepeatAndHold    = "dsl.repeat_and_hold"    // {A*3 500}
	ErrPauseRange       = "dsl.pause_range"        // [300..100]
	ErrAxisChord        = "dsl.axis_chord"         // значение оси у сочетания
	ErrBadCommandArgs   = "dsl.bad_command_args"   // {command}, {usage}
	ErrUnknownDevice    = "dsl.unknown_device"     // {device}
	ErrUnknownButton    = "dsl.unknown_button"     // {device}, {button}: у устройства нет такой кнопки
	ErrCannotSend       = "dsl.cannot_send"        // {key}, {kernel}: эту кнопку mKey пока не умеет нажимать
	ErrNotSupported     = "dsl.not_supported"      // {what}: возможность появится позже
	ErrUntypeable       = "dsl.untypeable_char"    // {char}, {layouts}: символа нет в раскладках
	ErrChord            = "dsl.chord_in_braces"    // {keys}, {macro}, {hotkey}: {Ctrl+C} — в скобках одна клавиша
	ErrBraceKey         = "dsl.brace_key"          // {char}: {{} или {}} — такой клавиши нет
	ErrAlreadyHeld      = "dsl.already_held"       // {key}: ^{A} или {A}, когда A уже зажата
	ErrNotHeld          = "dsl.not_held"           // {key}: ~{A} без ^{A}
	ErrBadHotkey        = "dsl.bad_hotkey"         // сочетание горячей клавиши записано не так
)

// AllErrorCodes — все коды ошибок языка (тест i18n проверяет, что у каждого есть перевод).
var AllErrorCodes = []string{
	ErrUnexpectedChar, ErrUnexpectedEnd, ErrTextOutside, ErrUnclosedBrace, ErrUnclosedPause,
	ErrUnclosedGroup, ErrUnexpectedClose, ErrUnclosedText, ErrBadEscape, ErrEmptyBraces,
	ErrUnknownKey, ErrUnknownKeyHint, ErrBadNumber, ErrBadDuration, ErrTooLong, ErrBadRepeat,
	ErrPrefixNotAllowed, ErrStarOnlyRelease, ErrRepeatAndHold, ErrPauseRange, ErrAxisChord,
	ErrBadCommandArgs, ErrUnknownDevice, ErrUnknownButton, ErrCannotSend, ErrNotSupported, ErrUntypeable,
	ErrChord, ErrBraceKey, ErrAlreadyHeld, ErrNotHeld, ErrBadHotkey,
}

// Error — ошибка в макросе с позицией и параметрами для понятного сообщения.
type Error struct {
	// Pos — место ошибки.
	Pos Pos `json:"pos"`
	// Code — i18n-код ошибки (dsl.*).
	Code string `json:"code"`
	// Args — параметры сообщения.
	Args map[string]string `json:"args,omitempty"`
}

// Error возвращает техническое описание ошибки на английском (для логов).
// Пользователю показывается перевод по Code (см. internal/i18n).
func (e *Error) Error() string {
	// Параметры выводятся в стабильном порядке.
	keys := make([]string, 0, len(e.Args))
	for k := range e.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, e.Args[k]))
	}
	return fmt.Sprintf("line %d, col %d: %s %s", e.Pos.Line, e.Pos.Col, e.Code, strings.Join(parts, " "))
}

// newError создаёт ошибку с параметрами, переданными парами «имя, значение».
func newError(pos Pos, code string, kv ...string) *Error {
	return NewError(pos, code, kv...)
}

// NewError создаёт ошибку языка с кодом code и параметрами сообщения парами «имя, значение»
// (для модулей, которые сами проверяют имена устройств и кнопок, например inspector).
func NewError(pos Pos, code string, kv ...string) *Error {
	e := &Error{Pos: pos, Code: code}
	if len(kv) > 0 {
		e.Args = map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			e.Args[kv[i]] = kv[i+1]
		}
	}
	return e
}
