package contracts

import (
	"time"

	"github.com/khameleonium/mKey/internal/lib/scriptfiles"
)

// ScriptFile — файл скрипта в папке языка.
type ScriptFile struct {
	// Lang — язык (ID расширения: "lua", "shell").
	Lang string `json:"lang"`
	// Name — имя файла без папки ("кликер.lua").
	Name string `json:"name"`
	// Size — размер в байтах; Modified — время последнего изменения.
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// ScriptProblem — ошибка в тексте скрипта, найденная проверкой синтаксиса.
type ScriptProblem struct {
	// Line — номер строки с 1 (0 — неизвестен).
	Line int `json:"line,omitempty"`
	// Message — текст ошибки от интерпретатора.
	Message string `json:"message"`
}

// ScriptLanguage — язык файлов-скриптов (точка PointScriptLanguage). Meta().ID — "lua" или "shell";
// Meta().Icon — подсветка в редакторе окна ("lua", "shell").
type ScriptLanguage interface {
	Extension
	// Dir возвращает папку файлов языка; Ext — расширение его файлов (".lua", ".sh").
	Dir() string
	Ext() string
	// Files возвращает файлы языка в его папке по имени (папки нет — пусто, не ошибка).
	Files() ([]ScriptFile, error)
	// Read возвращает текст файла name. ErrScriptName — недопустимое имя; файла нет — ошибка
	// с os.ErrNotExist.
	Read(name string) ([]byte, error)
	// Write записывает файл name целиком (атомарно, создавая папку). ErrScriptName — недопустимое имя.
	Write(name string, data []byte) error
	// Delete удаляет файл name. ErrScriptName — недопустимое имя.
	Delete(name string) error
	// Check проверяет синтаксис текста, ничего не выполняя: nil — ошибок нет.
	Check(code []byte) *ScriptProblem
}

// ErrScriptName — имя файла скрипта недопустимо: пустое, с папками, начинается с точки или с другим
// расширением.
var ErrScriptName = scriptfiles.ErrName
