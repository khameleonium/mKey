package pluginhost

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"mkey/internal/contracts"
)

// ManifestFile — имя манифеста в папке плагина.
const ManifestFile = "plugin.yaml"

// APIVersion — мажорная версия API плагинов, которую понимает этот mKey (ADR-0029 п. 9).
const APIVersion = 1

// Виды плагинов (FR-PLG-1).
const (
	KindProcess = "process"
	KindLua     = "lua"
	KindData    = "data"
)

// Разрешения (FR-PLG-5). checked — проверяются на каждом запросе плагина к mKey; остальные —
// объявления для человека (mKey не может запретить их процессу).
var (
	checkedPermissions = []string{"output.send", "vars.read", "vars.write", "notify"}
	declaredOnly       = []string{"net", "exec", "input.read", "input.grab", "desktop.window", "desktop.pixel", "projects.write"}
)

// idRe — ID плагина: буквы, цифры, «.», «-», «_» (обратный домен: io.example.hello).
var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// Text — текст на языках: "ru" → «Привет». В YAML можно написать и просто строку.
type Text map[string]string

// UnmarshalYAML принимает строку (один текст на все языки) или карту языков.
func (t *Text) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*t = Text{"en": n.Value}
		return nil
	}
	var m map[string]string
	if err := n.Decode(&m); err != nil {
		return err
	}
	*t = m
	return nil
}

// Manifest — содержимое plugin.yaml (FR-PLG-2, docs/plugins.md).
type Manifest struct {
	ID          string   `yaml:"id"`
	Name        Text     `yaml:"name"`
	Description Text     `yaml:"description"`
	Version     string   `yaml:"version"`
	PluginAPI   int      `yaml:"plugin_api"`
	MinMKey     string   `yaml:"min_mkey"`
	Kind        string   `yaml:"kind"`
	Entry       string   `yaml:"entry"`
	Args        []string `yaml:"args"`
	Platforms   []string `yaml:"platforms"`
	Permissions []string `yaml:"permissions"`
	// Provides — что плагин обещает (для человека до включения; настоящий список — из initialize).
	Provides struct {
		Actions    []string `yaml:"actions"`
		Conditions []string `yaml:"conditions"`
		Triggers   []string `yaml:"triggers"`
	} `yaml:"provides"`
}

// readManifest читает и проверяет манифест плагина в папке dir. Ошибка — contracts.ErrBadPlugin
// с понятным пояснением.
func readManifest(dir string) (Manifest, error) {
	// Файл и строгий разбор: опечатка в ключе — ошибка, а не молчаливое игнорирование.
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %s: %w", contracts.ErrBadPlugin, ManifestFile, err)
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %s: %w", contracts.ErrBadPlugin, ManifestFile, err)
	}
	return m, m.check(dir)
}

// check проверяет поля манифеста: ID, версию API, вид, файл запуска, разрешения.
func (m Manifest) check(dir string) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", contracts.ErrBadPlugin, fmt.Sprintf(format, args...))
	}

	// ID и версия API.
	if !idRe.MatchString(m.ID) {
		return bad("id %q: use letters, digits, '.', '-', '_' (e.g. io.example.hello)", m.ID)
	}
	if m.PluginAPI != APIVersion {
		return bad("plugin_api %d is not supported (this mKey supports %d)", m.PluginAPI, APIVersion)
	}

	// Вид и файл запуска: процессу и Lua он нужен и должен лежать внутри папки плагина.
	switch m.Kind {
	case KindProcess, KindLua:
		if m.Entry == "" {
			return bad("entry is required for kind %q", m.Kind)
		}
		path, err := m.entryPath(dir)
		if err != nil {
			return bad("%v", err)
		}
		st, err := os.Stat(path)
		if err != nil {
			return bad("entry %q: %v", m.Entry, err)
		}
		if m.Kind == KindProcess && st.Mode()&0o111 == 0 {
			return bad("entry %q is not executable (chmod +x)", m.Entry)
		}
	case KindData:
	default:
		return bad("kind %q: use process, lua or data", m.Kind)
	}

	// Разрешения — только известные.
	for _, p := range m.Permissions {
		if !slices.Contains(checkedPermissions, p) && !slices.Contains(declaredOnly, p) {
			return bad("unknown permission %q", p)
		}
	}
	return nil
}

// entryPath возвращает путь к файлу запуска; выход за пределы папки плагина — ошибка.
func (m Manifest) entryPath(dir string) (string, error) {
	path := filepath.Join(dir, filepath.FromSlash(m.Entry))
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("entry %q must be inside the plugin folder", m.Entry)
	}
	return path, nil
}

// allows сообщает, объявил ли плагин разрешение perm.
func (m Manifest) allows(perm string) bool { return slices.Contains(m.Permissions, perm) }
