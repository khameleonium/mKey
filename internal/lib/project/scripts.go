package project

import (
	"fmt"
	"slices"
	"sort"
)

// Скрипты в проекте (SEC-7): действия lua и shell выполняют любой код от имени пользователя.
// Перед включением чужого проекта человеку показывают их код.

// ScriptActions — виды действий, которые выполняют произвольный код.
var ScriptActions = []string{"lua", "shell"}

// Script — скрипт в проекте: событие, вид действия и код (или файл скрипта).
type Script struct {
	Event string `json:"event"`
	Type  string `json:"type"`
	Code  string `json:"code,omitempty"`
	File  string `json:"file,omitempty"`
}

// Scripts находит все скрипты проекта, включая вложенные в repeat/if и подобные действия.
func Scripts(p Project) []Script {
	var out []Script
	for _, e := range p.Events {
		for _, a := range e.Actions {
			out = appendScripts(out, e.ID, a.Type, a.Value)
		}
	}
	return out
}

// appendScripts добавляет скрипт действия typ со значением v и ищет вложенные действия в v.
func appendScripts(out []Script, event, typ string, v any) []Script {
	// Само действие — скрипт: код строкой или {code}/{file}.
	if isScript(typ) {
		s := Script{Event: event, Type: typ}
		switch x := v.(type) {
		case string:
			s.Code = x
		case map[string]any:
			s.Code, _ = x["code"].(string)
			s.File, _ = x["file"].(string)
		default:
			s.Code = fmt.Sprint(v)
		}
		return append(out, s)
	}

	// Вложенные действия: в YAML они — карты с одним ключом-видом ({lua: …}) внутри списков.
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			out = appendScripts(out, event, "", item)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(x) == 1 && isScript(k) {
				out = appendScripts(out, event, k, x[k])
				continue
			}
			out = appendScripts(out, event, "", x[k])
		}
	}
	return out
}

// isScript сообщает, что вид действия выполняет произвольный код.
func isScript(typ string) bool { return slices.Contains(ScriptActions, typ) }
