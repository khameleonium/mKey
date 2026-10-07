package lua

import (
	"bytes"
	"errors"

	glua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/scriptfiles"
)

// Файлы скриптов Lua для раздела «Скрипты» окна (FR-UI-1.7, contracts.ScriptLanguage): папка
// modules.lua.scripts_dir, файлы *.lua; проверка — разбор и компиляция без выполнения.

// luaLanguage — язык «Lua» в точке PointScriptLanguage.
type luaLanguage struct {
	m      *Module
	folder scriptfiles.Folder
}

// Meta возвращает метаданные языка: ID lua, подсветка lua.
func (l luaLanguage) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: "lua", NameKey: "script_lang.lua", Icon: "lua", Provider: ModuleID}
}

// Dir возвращает папку скриптов Lua.
func (l luaLanguage) Dir() string { return l.folder.Dir }

// Ext возвращает расширение файлов Lua.
func (l luaLanguage) Ext() string { return l.folder.Ext }

// Files возвращает файлы *.lua папки.
func (l luaLanguage) Files() ([]contracts.ScriptFile, error) {
	files, err := l.folder.Files()
	if err != nil {
		return nil, err
	}
	out := make([]contracts.ScriptFile, len(files))
	for i, f := range files {
		out[i] = contracts.ScriptFile{Lang: "lua", Name: f.Name, Size: f.Size, Modified: f.Modified}
	}
	return out, nil
}

// Read возвращает текст файла.
func (l luaLanguage) Read(name string) ([]byte, error) { return l.folder.Read(name) }

// Write записывает файл.
func (l luaLanguage) Write(name string, data []byte) error { return l.folder.Write(name, data) }

// Delete удаляет файл.
func (l luaLanguage) Delete(name string) error { return l.folder.Delete(name) }

// Check разбирает и компилирует текст Lua, ничего не выполняя: синтаксическая ошибка — со строкой.
func (l luaLanguage) Check(code []byte) *contracts.ScriptProblem {
	// Разбор: ошибка разборщика знает строку.
	chunk, err := parse.Parse(bytes.NewReader(code), "script")
	if err != nil {
		var pe *parse.Error
		if errors.As(err, &pe) {
			// Ошибка в конце файла (незакрытый блок) — последняя строка текста.
			line := pe.Pos.Line
			if line < 0 {
				line = bytes.Count(bytes.TrimRight(code, "\n"), []byte("\n")) + 1
			}
			return &contracts.ScriptProblem{Line: line, Message: pe.Message}
		}
		return &contracts.ScriptProblem{Message: err.Error()}
	}

	// Компиляция ловит то, что разбор пропускает (например, goto без метки).
	if _, err := glua.Compile(chunk, "script"); err != nil {
		var ce *glua.CompileError
		if errors.As(err, &ce) {
			return &contracts.ScriptProblem{Line: ce.Line, Message: ce.Message}
		}
		return &contracts.ScriptProblem{Message: err.Error()}
	}
	return nil
}

var _ contracts.ScriptLanguage = luaLanguage{}
