// Package manifest — манифест установки mKey (FR-INST-4, ADR-0011).
//
// Манифест — файл ~/.local/share/mkey/install-manifest.json со списком всего, что mKey
// создал или изменил в системе пользователя: файлы (программа, ярлык, иконка, автозапуск)
// и блоки внутри чужих файлов (конфиги композиторов). Деинсталляция удаляет ровно то, что
// перечислено в манифесте, поэтому «без следов» не зависит от памяти программиста.
//
// Файлы записываются только через Writer (реализует contracts.FileWriter), который сразу
// дописывает запись в манифест. Системные файлы (правила udev) пишет `mkey privileged`
// от root; их удаление — `mkey privileged uninstall-rules`, в манифесте они не учитываются.
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/fileblock"
)

// FileName — имя файла манифеста в каталоге данных mKey.
const FileName = "install-manifest.json"

// Виды записей манифеста.
const (
	// KindFile — файл, созданный mKey целиком.
	KindFile = "file"
	// KindBlock — блок mKey внутри чужого файла.
	KindBlock = "block"
)

// Entry — одна запись манифеста.
type Entry struct {
	// Kind — вид записи: file или block.
	Kind string `json:"kind"`
	// Path — путь к файлу.
	Path string `json:"path"`
	// Comment — префикс комментария маркеров блока (только для block).
	Comment string `json:"comment,omitempty"`
	// Owner — кто записал: "binary", "menu", "autostart:xdg" и т.п.
	Owner string `json:"owner"`
	// At — когда записано.
	At time.Time `json:"at"`
}

// Manifest — содержимое файла манифеста.
type Manifest struct {
	// Version — версия формата.
	Version int `json:"version"`
	// Entries — записи в порядке создания.
	Entries []Entry `json:"entries"`

	// path — файл манифеста; mu защищает Entries.
	path string
	mu   sync.Mutex
}

// Load читает манифест из файла path (отсутствующий файл — пустой манифест).
func Load(path string) (*Manifest, error) {
	m := &Manifest{Version: 1, path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", path, err)
	}
	m.path = path
	return m, nil
}

// Path возвращает путь к файлу манифеста.
func (m *Manifest) Path() string { return m.path }

// List возвращает копию записей.
func (m *Manifest) List() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.Entries)
}

// Add добавляет запись (повтор того же вида и пути заменяет прежнюю) и сохраняет манифест.
func (m *Manifest) Add(e Entry) error {
	m.mu.Lock()
	m.Entries = slices.DeleteFunc(m.Entries, func(x Entry) bool { return x.Kind == e.Kind && x.Path == e.Path })
	m.Entries = append(m.Entries, e)
	m.mu.Unlock()
	return m.Save()
}

// Remove убирает запись вида kind с путём path и сохраняет манифест.
func (m *Manifest) Remove(kind, path string) error {
	m.mu.Lock()
	m.Entries = slices.DeleteFunc(m.Entries, func(x Entry) bool { return x.Kind == kind && x.Path == path })
	m.mu.Unlock()
	return m.Save()
}

// Save записывает манифест атомарно (права 0600).
func (m *Manifest) Save() error {
	m.mu.Lock()
	data, err := json.MarshalIndent(struct {
		Version int     `json:"version"`
		Entries []Entry `json:"entries"`
	}{m.Version, m.Entries}, "", "  ")
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return writeAtomic(m.path, data, 0o600)
}

// Writer — contracts.FileWriter, записывающий файлы и учитывающий их в манифесте от имени owner.
type Writer struct {
	M     *Manifest
	Owner string
}

// WriteFile записывает файл и добавляет его в манифест.
func (w Writer) WriteFile(path string, data []byte, perm os.FileMode) error {
	if err := writeAtomic(path, data, perm); err != nil {
		return err
	}
	return w.M.Add(Entry{Kind: KindFile, Path: path, Owner: w.Owner, At: time.Now()})
}

// SetBlock записывает блок mKey в чужой файл (создаёт файл, если его нет) и добавляет запись.
func (w Writer) SetBlock(path, comment string, lines []string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	perm := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	if err := writeAtomic(path, []byte(fileblock.SetWith(string(data), comment, lines, false)), perm); err != nil {
		return err
	}
	return w.M.Add(Entry{Kind: KindBlock, Path: path, Comment: comment, Owner: w.Owner, At: time.Now()})
}

// RemoveFile удаляет файл и запись о нём (отсутствие файла — не ошибка).
func (w Writer) RemoveFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return w.M.Remove(KindFile, path)
}

// RemoveBlock убирает блок mKey из чужого файла и запись о нём.
func (w Writer) RemoveBlock(path, comment string) error {
	if err := removeBlock(path, comment); err != nil {
		return err
	}
	return w.M.Remove(KindBlock, path)
}

// Undo удаляет всё, что перечислено в манифесте, в обратном порядке, кроме записей,
// для которых keep возвращает true. Возвращает ошибки по каждой неудавшейся записи.
func (m *Manifest) Undo(keep func(Entry) bool) []error {
	var errs []error
	entries := m.List()
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if keep != nil && keep(e) {
			continue
		}
		var err error
		switch e.Kind {
		case KindFile:
			err = os.Remove(e.Path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		case KindBlock:
			err = removeBlock(e.Path, e.Comment)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Path, err))
			continue
		}
		if rerr := m.Remove(e.Kind, e.Path); rerr != nil {
			errs = append(errs, rerr)
		}
	}
	return errs
}

// removeBlock убирает блок mKey из файла (отсутствие файла или блока — не ошибка).
func removeBlock(path, comment string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fileblock.Has(string(data)) {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return writeAtomic(path, []byte(fileblock.StripWith(string(data), comment)), fi.Mode().Perm())
}

// writeAtomic записывает файл через временный файл и переименование, создавая каталоги.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+strings.TrimPrefix(filepath.Base(path), ".")+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, perm)); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// Проверка на этапе компиляции, что Writer реализует контракт.
var _ contracts.FileWriter = Writer{}
