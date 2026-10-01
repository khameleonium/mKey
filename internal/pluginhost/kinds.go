package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"mkey/internal/contracts"
	"mkey/internal/lib/project"
)

// Lua-плагины и плагины-данные (FR-PLG-1 п. 1 и 3): работают внутри mKey, без процесса.

// startLua загружает Lua-плагин модулем lua и регистрирует его действия и условия.
func (m *Module) startLua(p *plugin) {
	// Без модуля lua (выключен в config.yaml) Lua-плагин не запустить.
	if m.lua == nil {
		p.setState(contracts.PluginBroken, "the lua module is turned off (modules.lua.enabled)")
		return
	}
	path, err := p.man.entryPath(p.dir)
	if err != nil {
		p.setState(contracts.PluginBroken, err.Error())
		return
	}
	lp, err := m.lua.LoadPlugin(p.man.ID, path, p.man.Permissions)
	if err != nil {
		p.setState(contracts.PluginBroken, err.Error())
		return
	}

	// Виды — представители, вызывающие функции плагина.
	exts := map[contracts.ExtensionPoint][]contracts.Extension{}
	for _, t := range lp.Actions() {
		exts[contracts.PointAction] = append(exts[contracts.PointAction], luaActionProxy{lp: lp, meta: luaMeta(p, t)})
	}
	for _, t := range lp.Conditions() {
		exts[contracts.PointCondition] = append(exts[contracts.PointCondition], luaConditionProxy{lp: lp, meta: luaMeta(p, t)})
	}
	p.mu.Lock()
	p.lua = lp
	p.mu.Unlock()
	m.registerExt(p, exts)
	p.setState(contracts.PluginRunning, "")
	m.log.Info("lua plugin loaded", "plugin", p.man.ID, "actions", len(lp.Actions()), "conditions", len(lp.Conditions()))
}

// luaMeta — метаданные вида Lua-плагина.
func luaMeta(p *plugin, t contracts.PluginType) contracts.ExtensionMeta {
	cat := t.Category
	if cat == "" {
		cat = "plugins"
	}
	return contracts.ExtensionMeta{ID: t.ID, Category: cat, ParamsSchema: t.ParamsSchema, Provider: p.man.ID, Names: t.Names, Descriptions: t.Descriptions}
}

// luaActionProxy — действие Lua-плагина.
type luaActionProxy struct {
	lp   contracts.LuaPlugin
	meta contracts.ExtensionMeta
}

// Meta возвращает метаданные.
func (x luaActionProxy) Meta() contracts.ExtensionMeta { return x.meta }

// Validate проверяет параметры функцией плагина.
func (x luaActionProxy) Validate(a project.Action) error { return x.lp.Validate(x.meta.ID, a.Value) }

// Run выполняет действие.
func (x luaActionProxy) Run(ctx context.Context, rc contracts.RunContext, a project.Action) error {
	return x.lp.RunAction(ctx, rc, x.meta.ID, a.Value)
}

// luaConditionProxy — условие Lua-плагина.
type luaConditionProxy struct {
	lp   contracts.LuaPlugin
	meta contracts.ExtensionMeta
}

// Meta возвращает метаданные.
func (x luaConditionProxy) Meta() contracts.ExtensionMeta { return x.meta }

// Validate проверяет параметры функцией плагина.
func (x luaConditionProxy) Validate(c project.Condition) error {
	return x.lp.Validate(x.meta.ID, c.Params)
}

// Check вычисляет условие.
func (x luaConditionProxy) Check(ctx context.Context, rc contracts.RunContext, c project.Condition) (bool, error) {
	return x.lp.CheckCondition(ctx, rc, x.meta.ID, c.Params)
}

// startData регистрирует шаблоны проектов плагина-данных: файлы templates/*.mkey.yaml (название —
// поле name проекта, описание — описание плагина). Файл с ошибкой пропускается с предупреждением.
func (m *Module) startData(p *plugin) {
	files, _ := filepath.Glob(filepath.Join(p.dir, "templates", "*"+project.FileSuffix))
	var exts []contracts.Extension
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(f), project.FileSuffix)
		pr, err := project.Parse(data, id)
		if err != nil {
			m.log.Warn("plugin template skipped", "plugin", p.man.ID, "file", f, "err", err)
			continue
		}
		name := pr.Name
		if name == "" {
			name = id
		}
		exts = append(exts, dataTemplate{meta: contracts.ExtensionMeta{ID: id, Provider: p.man.ID},
			t: contracts.Template{ID: id, Content: string(data), Names: map[string]string{"en": name}, Descriptions: p.man.Description}})
	}
	m.registerExt(p, map[contracts.ExtensionPoint][]contracts.Extension{contracts.PointProjectTemplate: exts})
	p.setState(contracts.PluginRunning, "")
}

// dataTemplate — шаблон проекта из плагина-данных.
type dataTemplate struct {
	meta contracts.ExtensionMeta
	t    contracts.Template
}

// Meta возвращает метаданные.
func (d dataTemplate) Meta() contracts.ExtensionMeta { return d.meta }

// Template возвращает шаблон.
func (d dataTemplate) Template() contracts.Template { return d.t }

// Проверки на этапе компиляции.
var (
	_ contracts.ActionType      = luaActionProxy{}
	_ contracts.ConditionType   = luaConditionProxy{}
	_ contracts.ProjectTemplate = dataTemplate{}
)
