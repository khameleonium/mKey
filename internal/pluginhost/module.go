package pluginhost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/khameleonium/mKey/internal/contracts"
	"github.com/khameleonium/mKey/internal/lib/buildinfo"
	"github.com/khameleonium/mKey/internal/lib/paths"
)

// ModuleID — идентификатор модуля: имя секции в config.yaml (modules.plugins) и префикс
// i18n-ключей. Пакет называется pluginhost (карта репозитория), модуль — plugins (понятнее в файле).
const ModuleID = "plugins"

// Config — настройки модуля из секции modules.plugins в config.yaml.
type Config struct {
	// Active — ID включённых плагинов (новый плагин выключен, пока его не включат).
	Active []string `json:"active"`
	// Dir — папка плагинов пользователя ("" — ~/.local/share/mkey/plugins).
	Dir string `json:"dir"`
	// SystemDir — папка плагинов из пакетов ("" — /usr/share/mkey/plugins).
	SystemDir string `json:"system_dir"`
	// LogDir — папка журналов плагинов ("" — ~/.local/state/mkey/plugins).
	LogDir string `json:"log_dir"`
}

// DefaultSystemDir — папка плагинов, установленных пакетами.
const DefaultSystemDir = "/usr/share/mkey/plugins"

// Module — реализация contracts.Module для модуля «plugins».
type Module struct {
	// log, tr, bus, ext — из Init; cfg — настройки; version — версия mKey для initialize.
	log     *slog.Logger
	tr      contracts.Translator
	bus     contracts.Bus
	ext     contracts.ExtensionRegistry
	cfg     Config
	version string
	// runner, events, notifier — сервисы для запросов плагинов (любой может быть nil).
	runner   contracts.SequenceRunner
	events   contracts.Events
	notifier contracts.Notifier
	// lua — загрузчик Lua-плагинов (модуль lua; nil — Lua-плагины не запускаются).
	lua contracts.LuaPluginLoader

	// mu защищает plugins; plugins — найденные плагины по ID.
	mu      sync.Mutex
	plugins map[string]*plugin
	// broken — папки с неверным манифестом (ID — имя папки).
	broken map[string]contracts.PluginInfo
}

// New создаёт модуль. Зависимости модуль получает в Init, а не в конструкторе.
func New() *Module {
	return &Module{cfg: Config{Active: []string{}}, plugins: map[string]*plugin{}, broken: map[string]contracts.PluginInfo{}}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки, находит сервисы для запросов плагинов и публикует сервис Plugins.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	// Логгер, шина, реестр и настройки.
	m.log, m.tr, m.bus, m.ext = host.Logger(), host.I18n(), host.Bus(), host.Extensions()
	m.version = buildinfo.Version
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	if m.cfg.Dir == "" {
		m.cfg.Dir = filepath.Join(paths.Data(os.Getenv), "plugins")
	}
	if m.cfg.SystemDir == "" {
		m.cfg.SystemDir = DefaultSystemDir
	}
	if m.cfg.LogDir == "" {
		m.cfg.LogDir = filepath.Join(paths.State(os.Getenv), "plugins")
	}

	// Сервисы для запросов плагинов: нажатия, переменные, уведомления.
	s := host.Services()
	m.runner, _ = contracts.LookupService[contracts.SequenceRunner](s)
	m.events, _ = contracts.LookupService[contracts.Events](s)
	m.notifier, _ = contracts.LookupService[contracts.Notifier](s)
	m.lua, _ = contracts.LookupService[contracts.LuaPluginLoader](s)

	// Папка плагинов — в «Где что лежит»; сервис — окну и командам.
	if err := m.ext.Register(contracts.PointPlace, contracts.StaticPlace{
		M: contracts.ExtensionMeta{ID: contracts.PlacePlugins, NameKey: "place.plugins", DescriptionKey: "place.plugins.description", Provider: ModuleID},
		P: m.cfg.Dir, Dir: true, N: 40,
	}); err != nil {
		return err
	}
	return contracts.ProvideService[contracts.Plugins](s, m)
}

// Start находит плагины и запускает включённые.
func (m *Module) Start(context.Context) error {
	m.scan()
	m.mu.Lock()
	var start []*plugin
	for _, id := range m.cfg.Active {
		if p := m.plugins[id]; p != nil {
			p.active = true
			start = append(start, p)
		}
	}
	m.mu.Unlock()
	for _, p := range start {
		m.launch(p)
	}
	return nil
}

// Stop останавливает все плагины.
func (m *Module) Stop(context.Context) error {
	m.mu.Lock()
	list := make([]*plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		list = append(list, p)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range list {
		wg.Go(func() { m.shutdown(p) })
	}
	wg.Wait()
	return nil
}

// scan находит плагины в папках (пользователя — важнее системной). Уже известные плагины не
// трогаются; пропавшие папки выключенных плагинов забываются.
func (m *Module) scan() {
	found := map[string]bool{}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broken = map[string]contracts.PluginInfo{}
	for _, root := range []struct {
		dir    string
		system bool
	}{{m.cfg.Dir, false}, {m.cfg.SystemDir, true}} {
		entries, _ := os.ReadDir(root.dir)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root.dir, e.Name())
			man, err := readManifest(dir)
			if err != nil {
				if _, ok := m.broken[e.Name()]; !ok && !found[e.Name()] {
					m.broken[e.Name()] = contracts.PluginInfo{ID: e.Name(), Dir: dir, System: root.system, State: contracts.PluginBroken, Error: err.Error()}
				}
				continue
			}
			if found[man.ID] {
				continue
			}
			found[man.ID] = true
			if _, ok := m.plugins[man.ID]; !ok {
				m.plugins[man.ID] = &plugin{h: m, man: man, dir: dir, system: root.system, state: contracts.PluginOff,
					types: map[contracts.ExtensionPoint][]string{}, triggers: map[string]*armed{}}
			}
		}
	}
	for id, p := range m.plugins {
		if !found[id] && !p.active {
			delete(m.plugins, id)
		}
	}
}

// launch запускает включённый плагин по его виду.
func (m *Module) launch(p *plugin) {
	switch p.man.Kind {
	case KindProcess:
		p.start()
	case KindLua:
		m.startLua(p)
	case KindData:
		m.startData(p)
	}
}

// shutdown останавливает плагин любого вида и убирает его виды из реестров.
func (m *Module) shutdown(p *plugin) {
	p.halt()
	m.unregister(p)
	p.mu.Lock()
	lp := p.lua
	p.lua = nil
	p.mu.Unlock()
	if lp != nil {
		lp.Close()
	}
}

// register регистрирует виды плагина из ответа на initialize, заменяя прежние. Вид, ID которого
// уже занят встроенным или чужим видом, пропускается с предупреждением.
func (m *Module) register(p *plugin, res initResult) {
	exts := map[contracts.ExtensionPoint][]contracts.Extension{}
	for _, g := range []struct {
		point contracts.ExtensionPoint
		list  []typeInfo
		make  func(proxy) contracts.Extension
	}{
		{contracts.PointAction, res.Actions, func(x proxy) contracts.Extension { return actionProxy{x} }},
		{contracts.PointCondition, res.Conditions, func(x proxy) contracts.Extension { return conditionProxy{x} }},
		{contracts.PointTrigger, res.Triggers, func(x proxy) contracts.Extension { return triggerProxy{x} }},
	} {
		for _, t := range g.list {
			meta := contracts.ExtensionMeta{ID: t.ID, Category: t.Category, Icon: t.Icon, ParamsSchema: t.ParamsSchema,
				Provider: p.man.ID, Names: t.Name, Descriptions: t.Description}
			if meta.Category == "" {
				meta.Category = "plugins"
			}
			exts[g.point] = append(exts[g.point], g.make(proxy{p: p, meta: meta}))
		}
	}
	m.registerExt(p, exts)
}

// registerExt регистрирует расширения плагина (по точкам) и сообщает о смене набора видов.
// Расширение, ID которого занят встроенным или чужим видом, пропускается с предупреждением.
func (m *Module) registerExt(p *plugin, exts map[contracts.ExtensionPoint][]contracts.Extension) {
	m.unregisterQuiet(p)
	reg := map[contracts.ExtensionPoint][]string{}
	for point, list := range exts {
		for _, e := range list {
			id := e.Meta().ID
			if err := m.ext.Register(point, e); err != nil {
				m.log.Warn("plugin type not registered", "plugin", p.man.ID, "type", id, "err", err)
				continue
			}
			reg[point] = append(reg[point], id)
		}
	}
	p.mu.Lock()
	p.types = reg
	p.mu.Unlock()
	m.bus.Publish(contracts.TopicExtensionsChanged, nil)
}

// unregister убирает виды плагина из реестров и сообщает об этом.
func (m *Module) unregister(p *plugin) {
	if m.unregisterQuiet(p) {
		m.bus.Publish(contracts.TopicExtensionsChanged, nil)
	}
}

// unregisterQuiet убирает виды плагина без уведомления; true — что-то было убрано.
func (m *Module) unregisterQuiet(p *plugin) bool {
	p.mu.Lock()
	types := p.types
	p.types = map[contracts.ExtensionPoint][]string{}
	p.mu.Unlock()
	removed := false
	for point, ids := range types {
		for _, id := range ids {
			removed = m.ext.Unregister(point, id) || removed
		}
	}
	return removed
}

// notifyFailed сообщает человеку, что плагин отключён после частых падений.
func (m *Module) notifyFailed(p *plugin, why string) {
	if m.notifier == nil {
		return
	}
	body := m.tr.T("plugins.failed", contracts.Arg{Name: "plugin", Value: p.title(m.tr.Lang())}, contracts.Arg{Name: "error", Value: why})
	_ = m.notifier.Notify(context.Background(), "mKey", body)
}

// title — название плагина на языке lang (иначе ID).
func (p *plugin) title(lang string) string {
	for _, l := range []string{lang, "en", "ru"} {
		if s := p.man.Name[l]; s != "" {
			return s
		}
	}
	return p.man.ID
}

// List возвращает все найденные плагины, по ID (contracts.Plugins).
func (m *Module) List() []contracts.PluginInfo {
	m.scan()
	m.mu.Lock()
	list := make([]*plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		list = append(list, p)
	}
	out := make([]contracts.PluginInfo, 0, len(list)+len(m.broken))
	for _, b := range m.broken {
		out = append(out, b)
	}
	m.mu.Unlock()
	for _, p := range list {
		out = append(out, p.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// SetActive включает или выключает плагин сейчас (contracts.Plugins).
func (m *Module) SetActive(id string, on bool) error {
	m.scan()
	m.mu.Lock()
	p := m.plugins[id]
	if p == nil {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", contracts.ErrPluginNotFound, id)
	}
	p.active = on
	m.cfg.Active = slices.DeleteFunc(m.cfg.Active, func(x string) bool { return x == id })
	if on {
		m.cfg.Active = append(m.cfg.Active, id)
	}
	m.mu.Unlock()

	// Включение — запуск (после отключения из-за падений — заново); выключение — остановка.
	m.shutdown(p)
	if on {
		m.launch(p)
		return nil
	}
	p.setState(contracts.PluginOff, "")
	return nil
}

// Active возвращает ID включённых плагинов (contracts.Plugins).
func (m *Module) Active() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.cfg.Active)
}

// Dir возвращает папку плагинов пользователя (contracts.Plugins).
func (m *Module) Dir() string { return m.cfg.Dir }

// Remove выключает и удаляет плагин пользователя (contracts.Plugins).
func (m *Module) Remove(id string) error {
	m.scan()
	m.mu.Lock()
	p := m.plugins[id]
	b, isBroken := m.broken[id]
	m.mu.Unlock()

	// Неисправный плагин (ошибка в манифесте) — просто удалить папку.
	if p == nil && isBroken && !b.System {
		m.log.Info("plugin removed", "plugin", id)
		return os.RemoveAll(b.Dir)
	}
	if p == nil {
		return fmt.Errorf("%w: %s", contracts.ErrPluginNotFound, id)
	}
	if p.system {
		return fmt.Errorf("%w: %s", contracts.ErrPluginSystem, p.dir)
	}
	if err := m.SetActive(id, false); err != nil {
		return err
	}
	if err := os.RemoveAll(p.dir); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.plugins, id)
	m.mu.Unlock()
	m.log.Info("plugin removed", "plugin", id)
	return nil
}

// Log возвращает последние lines строк журнала плагина (contracts.Plugins).
func (m *Module) Log(id string, lines int) ([]string, error) {
	m.mu.Lock()
	_, ok := m.plugins[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", contracts.ErrPluginNotFound, id)
	}
	f, err := os.Open(m.logPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// Журнал не больше 1 МиБ — читаем целиком и оставляем хвост.
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLog)
	for sc.Scan() {
		out = append(out, strings.TrimRight(sc.Text(), "\r"))
		if lines > 0 && len(out) > lines {
			out = out[1:]
		}
	}
	return out, sc.Err()
}

// Проверки на этапе компиляции: модуль и сервис.
var (
	_ contracts.Module  = (*Module)(nil)
	_ contracts.Plugins = (*Module)(nil)
)
