package app

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"mkey/internal/lib/buildinfo"
	"mkey/internal/lib/config"
)

// templateDiffs — настройки, у которых значение в шаблоне законно отличается от значения
// по умолчанию в коде (модуль.ключ → почему).
var templateDiffs = map[string]string{
	// Пусто в коде = «создать пример»; в файле понятнее явное true.
	"store.example": "nil means true",
}

// TestConfigTemplate проверяет шаблон config.yaml: он разбирается строго, в нём есть каждая
// настройка каждого модуля (поля Config с json-тегами), нет лишних ключей, а значения совпадают
// со значениями по умолчанию в коде модулей.
func TestConfigTemplate(t *testing.T) {
	t.Parallel()

	// Шаблон — правильный config.yaml.
	cfg, err := config.Parse(ConfigTemplate)
	if err != nil {
		t.Fatal(err)
	}

	// Модули сборки: у каждого с настройками (поле cfg) — полная секция в шаблоне.
	seen := map[string]bool{}
	for _, e := range Modules() {
		id := e.Module.ID()
		seen[id] = true
		field := reflect.ValueOf(e.Module).Elem().FieldByName("cfg")
		if field.IsValid() {
			// Поле не экспортировано: читаем его значение через адрес (только в тесте).
			field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
		}
		sec := cfg.Section(id)
		if !field.IsValid() {
			if sec != nil {
				t.Errorf("%s: module has no settings, but the template has %s", id, sec)
			}
			continue
		}

		// Все поля настроек модуля есть в шаблоне.
		var keys map[string]any
		_ = json.Unmarshal(sec, &keys)
		typ := field.Type()
		for i := range typ.NumField() {
			tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			if _, ok := keys[tag]; tag != "" && tag != "-" && !ok && tag != "enabled" {
				t.Errorf("%s.%s: missing in the template", id, tag)
			}
		}

		// Строгий разбор секции поверх значений по умолчанию: лишних ключей нет, значения совпадают.
		def := reflect.New(typ)
		def.Elem().Set(field)
		got := reflect.New(typ)
		got.Elem().Set(field)
		dec := json.NewDecoder(bytes.NewReader(sec))
		dec.DisallowUnknownFields()
		if len(sec) > 0 {
			if err := dec.Decode(got.Interface()); err != nil {
				t.Errorf("%s: %v", id, err)
				continue
			}
		}
		for i := range typ.NumField() {
			tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			key := id + "." + tag
			if _, ok := templateDiffs[key]; ok || (key == "api.port" && !buildinfo.GUI) {
				continue
			}
			if a, b := def.Elem().Field(i).Interface(), got.Elem().Field(i).Interface(); !reflect.DeepEqual(a, b) {
				t.Errorf("%s: template %v, default in code %v", key, b, a)
			}
		}
	}

	// В шаблоне нет секций неизвестных модулей (значок в трее есть только в полной сборке).
	for id := range cfg.Modules {
		if !seen[id] && id != "tray" {
			t.Errorf("template section %q: no such module", id)
		}
	}
}
