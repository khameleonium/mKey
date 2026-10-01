package contracts

import (
	"context"
	"encoding/json"
	"errors"
)

// Плагины (ADR-0029, FR-PLG-1…6): модуль plugins (internal/pluginhost) находит плагины, запускает
// включённые и регистрирует их виды в точках расширения. Сервис Plugins — для окна и команд.

// Ошибки управления плагинами.
var (
	// ErrPluginNotFound — плагина с таким ID нет.
	ErrPluginNotFound = errors.New("plugin not found")
	// ErrPluginExists — плагин с таким ID уже установлен.
	ErrPluginExists = errors.New("plugin already installed")
	// ErrPluginSystem — плагин установлен в систему (/usr/share): удалить его можно только пакетом.
	ErrPluginSystem = errors.New("plugin is installed system-wide")
	// ErrBadPlugin — манифест или файлы плагина неверны (подробности — в тексте ошибки).
	ErrBadPlugin = errors.New("invalid plugin")
)

// Состояния плагина.
const (
	// PluginOff — выключен.
	PluginOff = "off"
	// PluginStarting — запускается (ждём ответа на initialize).
	PluginStarting = "starting"
	// PluginRunning — работает.
	PluginRunning = "running"
	// PluginRestarting — упал и будет перезапущен.
	PluginRestarting = "restarting"
	// PluginFailed — отключён после частых падений (ждёт, пока человек включит снова).
	PluginFailed = "failed"
	// PluginBroken — не запускается: ошибка в манифесте, нет файла, другая версия API.
	PluginBroken = "broken"
)

// PluginInfo — сведения о плагине для окна и команд.
type PluginInfo struct {
	// ID, Version, Kind (process, lua, data) — из манифеста.
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	Kind    string `json:"kind"`
	// Name и Description — на языках ("ru" → текст).
	Name        map[string]string `json:"name,omitempty"`
	Description map[string]string `json:"description,omitempty"`
	// Dir — папка плагина; System — установлен в систему (/usr/share/mkey/plugins).
	Dir    string `json:"dir"`
	System bool   `json:"system,omitempty"`
	// Permissions — разрешения из манифеста.
	Permissions []string `json:"permissions,omitempty"`
	// Active — включён человеком; State — состояние (PluginOff…); Error — почему не работает.
	Active bool   `json:"active"`
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
	// Actions, Conditions, Triggers — ID видов, которые плагин зарегистрировал.
	Actions    []string `json:"actions,omitempty"`
	Conditions []string `json:"conditions,omitempty"`
	Triggers   []string `json:"triggers,omitempty"`
	// Templates — шаблоны проектов из плагина-данных.
	Templates []string `json:"templates,omitempty"`
}

// Plugins — управление плагинами (модуль plugins).
type Plugins interface {
	// List возвращает все найденные плагины (по ID).
	List() []PluginInfo
	// SetActive включает или выключает плагин сейчас; сохранить выбор в config.yaml — дело
	// вызывающего (Active возвращает список включённых). ErrPluginNotFound — нет такого.
	SetActive(id string, on bool) error
	// Active возвращает ID включённых плагинов (для config.yaml: modules.plugins.active).
	Active() []string
	// Install копирует плагин из папки или архива .zip src в папку плагинов пользователя
	// (выключенным). ErrBadPlugin, ErrPluginExists.
	Install(src string) (PluginInfo, error)
	// Remove выключает и удаляет плагин пользователя. ErrPluginNotFound, ErrPluginSystem.
	Remove(id string) error
	// Log возвращает последние lines строк журнала плагина (его stderr).
	Log(id string, lines int) ([]string, error)
	// Dir возвращает папку плагинов пользователя.
	Dir() string
}

// PluginType — вид, который описал Lua-плагин: ID, названия на языках, категория, схема параметров.
type PluginType struct {
	ID           string
	Names        map[string]string
	Descriptions map[string]string
	Category     string
	ParamsSchema json.RawMessage
}

// LuaPlugin — загруженный Lua-плагин (модуль lua): его виды и их выполнение. Вызовы одного
// плагина идут по очереди (у плагина одно состояние Lua — он может хранить в нём данные).
type LuaPlugin interface {
	// Actions и Conditions — виды, которые плагин зарегистрировал (mkey.register_action…).
	Actions() []PluginType
	Conditions() []PluginType
	// Validate проверяет параметры вида typ (функцией validate плагина, если она есть).
	Validate(typ string, params any) error
	// RunAction выполняет действие typ.
	RunAction(ctx context.Context, rc RunContext, typ string, params any) error
	// CheckCondition вычисляет условие typ.
	CheckCondition(ctx context.Context, rc RunContext, typ string, params any) (bool, error)
	// Close освобождает состояние Lua.
	Close()
}

// LuaPluginLoader загружает Lua-плагины (модуль lua): выполняет файл path, где плагин
// регистрирует свои виды. permissions — разрешения из манифеста: функции mkey.* без нужного
// разрешения дают ошибку; доступа к файлам и программам (os, io) у Lua-плагина нет.
type LuaPluginLoader interface {
	LoadPlugin(id, path string, permissions []string) (LuaPlugin, error)
}
