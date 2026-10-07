package shell

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/scriptfiles"
)

// Файлы скриптов bash для раздела «Скрипты» окна (FR-UI-1.7, contracts.ScriptLanguage): папка
// modules.shell.scripts_dir, файлы *.sh; проверка — «<shell> -n» (разбор без выполнения команд).

// checkTimeout — предел проверки синтаксиса: разбор даже большого скрипта занимает миллисекунды.
const checkTimeout = 5 * time.Second

// lineRe находит номер строки в сообщении интерпретатора на любом языке системы: «bash: line 3: …»,
// «bash: строка 3: …», «sh: 3: Syntax error…» — число после имени интерпретатора и не больше
// одного слова.
var lineRe = regexp.MustCompile(`^[^:]*: (?:[^\s:]+ )?(\d+): `)

// shellLanguage — язык «bash» в точке PointScriptLanguage.
type shellLanguage struct {
	m      *Module
	folder scriptfiles.Folder
}

// Meta возвращает метаданные языка: ID shell, подсветка shell.
func (l shellLanguage) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{ID: "shell", NameKey: "script_lang.shell", Icon: "shell", Provider: ModuleID}
}

// Dir возвращает папку скриптов bash.
func (l shellLanguage) Dir() string { return l.folder.Dir }

// Ext возвращает расширение файлов bash.
func (l shellLanguage) Ext() string { return l.folder.Ext }

// Files возвращает файлы *.sh папки.
func (l shellLanguage) Files() ([]contracts.ScriptFile, error) {
	files, err := l.folder.Files()
	if err != nil {
		return nil, err
	}
	out := make([]contracts.ScriptFile, len(files))
	for i, f := range files {
		out[i] = contracts.ScriptFile{Lang: "shell", Name: f.Name, Size: f.Size, Modified: f.Modified}
	}
	return out, nil
}

// Read возвращает текст файла.
func (l shellLanguage) Read(name string) ([]byte, error) { return l.folder.Read(name) }

// Write записывает файл.
func (l shellLanguage) Write(name string, data []byte) error { return l.folder.Write(name, data) }

// Delete удаляет файл.
func (l shellLanguage) Delete(name string) error { return l.folder.Delete(name) }

// Check проверяет синтаксис командой «<shell> -n» (текст — на вход): команды не выполняются.
// Интерпретатор не запустился — проверки нет (nil): скрипт всё равно проверится при запуске.
func (l shellLanguage) Check(code []byte) *contracts.ScriptProblem {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, l.m.cfg.Shell, "-n")
	cmd.Stdin = bytes.NewReader(code)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return nil
	}

	// Сообщение интерпретатора и номер строки из него.
	msg := strings.TrimSpace(stderr.String())
	if i := strings.IndexByte(msg, '\n'); i > 0 {
		msg = msg[:i]
	}
	p := &contracts.ScriptProblem{Message: msg}
	if m := lineRe.FindStringSubmatch(msg); m != nil {
		p.Line, _ = strconv.Atoi(m[1])
	}
	return p
}

var _ contracts.ScriptLanguage = shellLanguage{}
