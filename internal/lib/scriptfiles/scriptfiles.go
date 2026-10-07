// Package scriptfiles — файлы скриптов одного языка в одной папке (раздел «Скрипты», FR-UI-1.7):
// список, чтение, атомарная запись и удаление с проверкой имени. Библиотека без жизненного
// цикла: её используют модули lua и shell в своих contracts.ScriptLanguage.
package scriptfiles

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ErrName — имя файла скрипта недопустимо: пустое, с папками, начинается с точки или с другим
// расширением (ErrName — это же значение).
var ErrName = errors.New("invalid script file name")

// File — файл скрипта: имя без папки, размер в байтах, время изменения.
type File struct {
	Name     string
	Size     int64
	Modified time.Time
}

// maxSize — наибольший размер файла скрипта, который читает и пишет окно (1 МиБ): больше — это
// уже не скрипт, а данные.
const maxSize = 1 << 20

// Folder — папка скриптов одного языка: Dir — папка, Ext — расширение файлов языка (".lua").
type Folder struct {
	Dir string
	Ext string
}

// checkName проверяет имя файла: без папок и управляющих символов, не начинается с точки,
// с расширением языка и непустой основой. Ошибка — ErrName с пояснением.
func (f Folder) checkName(name string) error {
	switch {
	case name == "" || strings.ContainsAny(name, "/\\\x00") || strings.HasPrefix(name, "."):
		return fmt.Errorf("%w: %q", ErrName, name)
	case !strings.HasSuffix(name, f.Ext) || len(name) == len(f.Ext):
		return fmt.Errorf("%w: %q must end with %s", ErrName, name, f.Ext)
	case len(name) > 200:
		return fmt.Errorf("%w: name is too long", ErrName)
	}
	for _, r := range name {
		if r < 0x20 {
			return fmt.Errorf("%w: %q", ErrName, name)
		}
	}
	return nil
}

// Files возвращает файлы языка по имени; папки нет — пустой список.
func (f Folder) Files() ([]File, error) {
	entries, err := os.ReadDir(f.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []File{}, nil
	}
	if err != nil {
		return nil, err
	}

	// Только обычные файлы с расширением языка и допустимым именем.
	out := []File{}
	for _, e := range entries {
		if !e.Type().IsRegular() || f.checkName(e.Name()) != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, File{Name: e.Name(), Size: info.Size(), Modified: info.ModTime()})
	}
	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Read возвращает текст файла name (не больше maxSize).
func (f Folder) Read(name string) ([]byte, error) {
	if err := f.checkName(name); err != nil {
		return nil, err
	}
	path := filepath.Join(f.Dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSize {
		return nil, fmt.Errorf("%s: file is larger than %d bytes", name, maxSize)
	}
	return os.ReadFile(path)
}

// Write записывает файл name целиком: во временный файл рядом, затем переименование — модуль,
// который как раз запускает скрипт, никогда не прочитает его наполовину записанным.
func (f Folder) Write(name string, data []byte) error {
	// Проверки: имя и размер.
	if err := f.checkName(name); err != nil {
		return err
	}
	if len(data) > maxSize {
		return fmt.Errorf("%s: script is larger than %d bytes", name, maxSize)
	}

	// Папка (только для владельца) и временный файл.
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(f.Dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	// Запись, права (bash-скрипт можно запустить и вручную) и замена.
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o700); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(f.Dir, name))
}

// Delete удаляет файл name.
func (f Folder) Delete(name string) error {
	if err := f.checkName(name); err != nil {
		return err
	}
	return os.Remove(filepath.Join(f.Dir, name))
}
