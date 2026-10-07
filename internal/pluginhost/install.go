package pluginhost

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/khameleonium/mKey/internal/contracts"
)

// Установка плагина (FR-PLG-4): из папки или архива .zip — в папку плагинов пользователя,
// выключенным. Файлы лежат в ~/.local/share/mkey/plugins/<id>: полное удаление mKey убирает
// эту папку вместе с остальными данными.

// maxUnzip — предел распакованного размера архива: защита от «zip-бомбы» (плагину хватит 200 МиБ).
const maxUnzip = 200 << 20

// Install копирует плагин из папки или архива .zip src в папку плагинов пользователя
// (contracts.Plugins). Плагин остаётся выключенным.
func (m *Module) Install(src string) (contracts.PluginInfo, error) {
	// Архив — распаковать во временную папку рядом (на том же диске: потом — переименование).
	if err := os.MkdirAll(m.cfg.Dir, 0o700); err != nil {
		return contracts.PluginInfo{}, err
	}
	dir := src
	if strings.HasSuffix(strings.ToLower(src), ".zip") {
		tmp, err := os.MkdirTemp(m.cfg.Dir, ".install-")
		if err != nil {
			return contracts.PluginInfo{}, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		if err := unzip(src, tmp); err != nil {
			return contracts.PluginInfo{}, fmt.Errorf("%w: %w", contracts.ErrBadPlugin, err)
		}
		dir = pluginRoot(tmp)
	}

	// Манифест — проверить до копирования; ID не должен быть занят.
	man, err := readManifest(dir)
	if err != nil {
		return contracts.PluginInfo{}, err
	}
	target := filepath.Join(m.cfg.Dir, man.ID)
	if _, err := os.Stat(target); err == nil {
		return contracts.PluginInfo{}, fmt.Errorf("%w: %s", contracts.ErrPluginExists, man.ID)
	}
	m.mu.Lock()
	_, known := m.plugins[man.ID]
	m.mu.Unlock()
	if known {
		return contracts.PluginInfo{}, fmt.Errorf("%w: %s", contracts.ErrPluginExists, man.ID)
	}

	// Копия (во временную папку, затем переименование — плагин не бывает скопирован наполовину).
	stage, err := os.MkdirTemp(m.cfg.Dir, ".copy-")
	if err != nil {
		return contracts.PluginInfo{}, err
	}
	if err := copyTree(dir, stage); err != nil {
		_ = os.RemoveAll(stage)
		return contracts.PluginInfo{}, err
	}
	if err := os.Rename(stage, target); err != nil {
		_ = os.RemoveAll(stage)
		return contracts.PluginInfo{}, err
	}
	m.log.Info("plugin installed", "plugin", man.ID, "from", src)

	// Найти и вернуть сведения.
	m.scan()
	m.mu.Lock()
	p := m.plugins[man.ID]
	m.mu.Unlock()
	if p == nil {
		return contracts.PluginInfo{}, fmt.Errorf("%w: %s not found after install", contracts.ErrBadPlugin, man.ID)
	}
	return p.info(), nil
}

// pluginRoot — папка с plugin.yaml в распакованном архиве: сам корень или единственная папка в нём
// (архив часто содержит папку плагина целиком).
func pluginRoot(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, ManifestFile)); err == nil {
		return dir
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(dir, entries[0].Name())
	}
	return dir
}

// unzip распаковывает архив src в dst: пути вне dst и ссылки запрещены, общий размер ограничен.
func unzip(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	var total int64
	for _, f := range r.File {
		// Путь внутри dst (защита от «../»).
		path := filepath.Join(dst, filepath.FromSlash(f.Name))
		if rel, err := filepath.Rel(dst, path); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry %q points outside the plugin folder", f.Name)
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
			continue
		case !mode.IsRegular():
			return fmt.Errorf("archive entry %q is not a regular file", f.Name)
		}

		// Файл с правами из архива (исполняемый бит важен для плагина-процесса).
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode.Perm()|0o600)
		if err != nil {
			_ = in.Close()
			return err
		}
		n, err := io.Copy(out, io.LimitReader(in, maxUnzip-total+1))
		_ = in.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		if total += n; total > maxUnzip {
			return fmt.Errorf("archive is larger than %d MiB", maxUnzip>>20)
		}
	}
	return nil
}

// copyTree копирует папку src в dst (обычные файлы и папки с правами; ссылки пропускаются).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case !info.Mode().IsRegular():
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()|0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}
