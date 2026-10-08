package bundle

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// Формат самостоятельного файла макроса (ADR-0042):
//
//	[программа mkey][zip с содержимым][хвост 24 байта]
//
// Хвост: магическая строка "MKEYBNDL" (8 байт), версия формата (uint32 LE), запас (4 байта),
// длина zip-части (uint64 LE). Ядро запускает файл как обычную программу (лишние байты после
// неё ему не мешают), а сама программа при запуске смотрит, есть ли в её конце такой хвост.

// Format — версия формата содержимого.
const Format = 1

// magic — метка хвоста; trailerSize — длина хвоста.
const (
	magic       = "MKEYBNDL"
	trailerSize = 24
)

// Пути внутри архива.
const (
	// ManifestFile — сведения о сборке (Manifest).
	ManifestFile = "manifest.json"
	// ProjectFile — файл проекта (как есть, с комментариями).
	ProjectFile = "project.mkey.yaml"
	// RecordingsDir, LuaDir, ShellDir — записи (*.mkrec) и скрипты, на которые ссылается проект.
	RecordingsDir = "recordings"
	LuaDir        = "scripts/lua"
	ShellDir      = "scripts/shell"
)

// Режимы работы собранного файла.
const (
	// ModeEvents — работать, как включённый проект (горячие клавиши, таймеры…), пока не выйдут.
	ModeEvents = "events"
	// ModeOnce — выполнить одно событие и завершиться.
	ModeOnce = "once"
)

// ErrNoBundle — в файле нет содержимого mKey (обычная программа mkey).
var ErrNoBundle = errors.New("bundle: no macro inside")

// Manifest — сведения о собранном файле.
type Manifest struct {
	// Format — версия формата (Format).
	Format int `json:"format"`
	// Name — название (проекта) для человека; Project — ID проекта.
	Name    string `json:"name"`
	Project string `json:"project"`
	// Mode — ModeEvents или ModeOnce; Event — ID события для ModeOnce.
	Mode  string `json:"mode"`
	Event string `json:"event,omitempty"`
	// Created — когда собран (RFC 3339); Version — версия mKey, которой собран; Creator —
	// создатель mKey (виден в сведениях о файле).
	Created string `json:"created"`
	Version string `json:"version"`
	Creator string `json:"creator"`
}

// Validate проверяет сведения: формат, название, режим и событие для ModeOnce.
func (m Manifest) Validate() error {
	switch {
	case m.Format != Format:
		return fmt.Errorf("bundle: format %d is not supported (need %d)", m.Format, Format)
	case m.Project == "":
		return errors.New("bundle: no project")
	case m.Mode != ModeEvents && m.Mode != ModeOnce:
		return fmt.Errorf("bundle: unknown mode %q", m.Mode)
	case m.Mode == ModeOnce && m.Event == "":
		return errors.New("bundle: mode once needs an event")
	}
	return nil
}

// Contents — содержимое: сведения, файл проекта и прочие файлы по путям внутри архива
// ("recordings/игра.mkrec", "scripts/lua/fish.lua").
type Contents struct {
	Manifest Manifest
	Project  []byte
	Files    map[string][]byte
}

// safePath сообщает, что путь внутри архива безопасен: относительный, без «..» и только в
// известных папках (чтобы распаковка не писала мимо своей папки).
func safePath(p string) bool {
	clean := path.Clean(p)
	if clean != p || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return false
	}
	for _, dir := range []string{RecordingsDir, LuaDir, ShellDir} {
		if strings.HasPrefix(p, dir+"/") && !strings.Contains(strings.TrimPrefix(p, dir+"/"), "/") {
			return true
		}
	}
	return p == ManifestFile || p == ProjectFile
}

// Write дописывает содержимое c к программе exe и записывает результат в w. Если в exe уже есть
// содержимое (собирают из собранного файла), оно заменяется.
func Write(w io.Writer, exe io.ReaderAt, size int64, c Contents) error {
	// Проверяем сведения и пути файлов.
	if err := c.Manifest.Validate(); err != nil {
		return err
	}
	for p := range c.Files {
		if !safePath(p) || p == ManifestFile || p == ProjectFile {
			return fmt.Errorf("bundle: bad path %q", p)
		}
	}

	// Программа без прежнего содержимого.
	base := size
	if off, _, err := locate(exe, size); err == nil {
		base = off
	}
	if _, err := io.Copy(w, io.NewSectionReader(exe, 0, base)); err != nil {
		return fmt.Errorf("bundle: copy program: %w", err)
	}

	// Архив: сведения, проект, файлы по алфавиту (одинаковое содержимое — одинаковый файл).
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	manifest, err := json.MarshalIndent(c.Manifest, "", "  ")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(c.Files))
	for p := range c.Files {
		names = append(names, p)
	}
	sort.Strings(names)
	files := map[string][]byte{ManifestFile: manifest, ProjectFile: c.Project}
	for _, p := range append([]string{ManifestFile, ProjectFile}, names...) {
		data, ok := files[p]
		if !ok {
			data = c.Files[p]
		}
		f, err := zw.Create(p)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}

	// Архив и хвост.
	trailer := make([]byte, trailerSize)
	copy(trailer, magic)
	binary.LittleEndian.PutUint32(trailer[8:], Format)
	binary.LittleEndian.PutUint64(trailer[16:], uint64(buf.Len()))
	if _, err := w.Write(buf.Bytes()); err != nil {
		return err
	}
	_, err = w.Write(trailer)
	return err
}

// locate находит архив в конце файла: смещение начала архива и его длину. ErrNoBundle — хвоста нет.
func locate(r io.ReaderAt, size int64) (int64, int64, error) {
	if size < trailerSize {
		return 0, 0, ErrNoBundle
	}
	trailer := make([]byte, trailerSize)
	if _, err := r.ReadAt(trailer, size-trailerSize); err != nil {
		return 0, 0, err
	}
	if string(trailer[:8]) != magic {
		return 0, 0, ErrNoBundle
	}
	if v := binary.LittleEndian.Uint32(trailer[8:]); v != Format {
		return 0, 0, fmt.Errorf("bundle: format %d is not supported (need %d); build the macro again", v, Format)
	}
	n := int64(binary.LittleEndian.Uint64(trailer[16:]))
	if n <= 0 || n > size-trailerSize {
		return 0, 0, errors.New("bundle: damaged file (bad archive size)")
	}
	return size - trailerSize - n, n, nil
}

// Read читает содержимое из файла r размером size. ErrNoBundle — содержимого нет.
func Read(r io.ReaderAt, size int64) (Contents, error) {
	// Архив в конце файла.
	off, n, err := locate(r, size)
	if err != nil {
		return Contents{}, err
	}
	zr, err := zip.NewReader(io.NewSectionReader(r, off, n), n)
	if err != nil {
		return Contents{}, fmt.Errorf("bundle: damaged file: %w", err)
	}

	// Файлы архива: только безопасные пути.
	c := Contents{Files: map[string][]byte{}}
	var manifest []byte
	for _, f := range zr.File {
		if !safePath(f.Name) {
			return Contents{}, fmt.Errorf("bundle: bad path %q", f.Name)
		}
		data, err := readZip(f)
		if err != nil {
			return Contents{}, err
		}
		switch f.Name {
		case ManifestFile:
			manifest = data
		case ProjectFile:
			c.Project = data
		default:
			c.Files[f.Name] = data
		}
	}

	// Сведения и проект обязательны.
	if manifest == nil || c.Project == nil {
		return Contents{}, errors.New("bundle: damaged file (no manifest or project)")
	}
	if err := json.Unmarshal(manifest, &c.Manifest); err != nil {
		return Contents{}, fmt.Errorf("bundle: manifest: %w", err)
	}
	return c, c.Manifest.Validate()
}

// maxFile — наибольший размер одного файла внутри (защита от «zip-бомбы»): записи и скрипты
// бывают в мегабайты, не в сотни.
const maxFile = 256 << 20

// readZip читает файл архива целиком (не больше maxFile).
func readZip(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFile {
		return nil, fmt.Errorf("bundle: %s is too large", f.Name)
	}
	return data, nil
}

// ReadFile читает содержимое из файла по пути (обычно — своей программы, os.Executable).
func ReadFile(name string) (Contents, error) {
	f, err := os.Open(name)
	if err != nil {
		return Contents{}, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return Contents{}, err
	}
	return Read(f, st.Size())
}

// Extract раскладывает файлы содержимого (записи, скрипты) в папки: dirs — папка для каждого
// раздела архива (RecordingsDir, LuaDir, ShellDir; раздел без папки пропускается). Права файлов —
// 0600, скриптов bash — 0700.
func Extract(c Contents, dirs map[string]string) error {
	for p, data := range c.Files {
		dir := dirs[path.Dir(p)]
		if dir == "" {
			continue
		}
		mode := fs.FileMode(0o600)
		if path.Dir(p) == ShellDir {
			mode = 0o700
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dir+"/"+path.Base(p), data, mode); err != nil {
			return err
		}
	}
	return nil
}
