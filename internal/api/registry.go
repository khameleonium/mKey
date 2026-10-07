package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/khameleonium/mKey/internal/contracts"
)

// registryEntry — вид триггера, условия или действия для конструктора GUI:
// метаданные плюс названия на языке клиента.
type registryEntry struct {
	contracts.ExtensionMeta
	// Name и Description — название и описание на языке клиента (Name — ID, если перевода нет).
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// CategoryName — название категории палитры на языке клиента.
	CategoryName string `json:"category_name,omitempty"`
}

// handleRegistry возвращает все зарегистрированные виды триггеров, условий и действий
// с метаданными и схемами параметров (для конструктора блоков GUI, SPEC §4.3).
// В схемы добавляются подписи на языке клиента: title у полей и вариантов, x-enum-labels у списков.
func (m *Module) handleRegistry(w http.ResponseWriter, r *http.Request) {
	tr := m.translator(r)
	out := map[string][]registryEntry{}
	for _, p := range contracts.AllExtensionPoints() {
		for _, e := range m.svc.ext.List(p) {
			// Названия вида и категории.
			meta := e.Meta()
			entry := registryEntry{ExtensionMeta: meta, Name: meta.ID}
			if s, ok := lookupText(tr, meta.NameKey); ok {
				entry.Name = s
			} else if s := pickLang(meta.Names, tr.Lang()); s != "" {
				entry.Name = s
			}
			entry.Description, _ = lookupText(tr, meta.DescriptionKey)
			if entry.Description == "" {
				entry.Description = pickLang(meta.Descriptions, tr.Lang())
			}
			if meta.Category != "" {
				entry.CategoryName, _ = lookupText(tr, "category."+meta.Category)
			}

			// Подписи полей в схеме: ключи вида "<name_key>.field.<поле>".
			entry.ParamsSchema = localizeSchema(tr, meta.NameKey, meta.ParamsSchema)
			out[string(p)] = append(out[string(p)], entry)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// pickLang выбирает текст на языке lang, иначе английский, иначе любой (тексты плагинов).
func pickLang(texts map[string]string, lang string) string {
	for _, l := range []string{lang, "en", "ru"} {
		if s := texts[l]; s != "" {
			return s
		}
	}
	for _, s := range texts {
		return s
	}
	return ""
}

// lookupText возвращает перевод ключа; false — перевода нет (или ключ пуст).
func lookupText(tr contracts.Translator, key string) (string, bool) {
	if key == "" {
		return "", false
	}
	s := tr.T(key)
	return s, s != key
}

// localizeSchema добавляет в JSON Schema подписи на языке клиента. Ключи переводов:
//   - <prefix>.variant.<i> — название i-го варианта oneOf;
//   - <prefix>.field.<поле> — подпись поля объекта (и <…>.hint — подсказка);
//   - <prefix>.field.<поле>.<значение> — подпись значения списка (enum) поля;
//   - <prefix>.value.<значение> — подпись значения, если сама схема — список.
//
// Порядок полей сохраняется: по нему GUI расставляет поля в блоке.
// Схема, которую не удаётся разобрать, возвращается без изменений.
func localizeSchema(tr contracts.Translator, prefix string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	root, err := decodeOrdered(json.NewDecoder(bytes.NewReader(raw)))
	obj, ok := root.(*orderedObject)
	if err != nil || !ok {
		return raw
	}
	enumLabels(tr, prefix+".value", obj)
	localizeNode(tr, prefix, obj)
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

// localizeNode подписывает варианты oneOf и поля объекта в узле схемы.
func localizeNode(tr contracts.Translator, prefix string, node *orderedObject) {
	// Варианты: у всех вариантов общие подписи полей.
	if variants, ok := node.get("oneOf").([]any); ok {
		for i, v := range variants {
			vm, ok := v.(*orderedObject)
			if !ok {
				continue
			}
			if s, ok := lookupText(tr, prefix+".variant."+strconv.Itoa(i)); ok {
				vm.set("title", s)
			}
			enumLabels(tr, prefix+".value", vm)
			localizeNode(tr, prefix, vm)
		}
	}

	// Поля объекта и подписи их значений.
	props, _ := node.get("properties").(*orderedObject)
	if props == nil {
		return
	}
	for _, name := range props.keys {
		pm, ok := props.values[name].(*orderedObject)
		if !ok {
			continue
		}
		key := prefix + ".field." + name
		if s, ok := lookupText(tr, key); ok {
			pm.set("title", s)
		}
		if s, ok := lookupText(tr, key+".hint"); ok {
			pm.set("description", s)
		}
		enumLabels(tr, key, pm)
	}
}

// enumLabels добавляет к списку значений (enum) узла подписи x-enum-labels (по порядку значений).
func enumLabels(tr contracts.Translator, prefix string, node *orderedObject) {
	values, ok := node.get("enum").([]any)
	if !ok {
		return
	}
	labels := make([]string, len(values))
	found := false
	for i, v := range values {
		s, _ := v.(string)
		labels[i] = s
		if t, ok := lookupText(tr, prefix+"."+s); ok && s != "" {
			labels[i], found = t, true
		}
	}
	if found {
		node.set("x-enum-labels", labels)
	}
}

// orderedObject — объект JSON с сохранённым порядком ключей (обычная карта Go его теряет).
type orderedObject struct {
	keys   []string
	values map[string]any
}

// get возвращает значение ключа (nil, если его нет).
func (o *orderedObject) get(key string) any { return o.values[key] }

// set задаёт значение ключа; новый ключ добавляется в конец.
func (o *orderedObject) set(key string, v any) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
}

// MarshalJSON записывает объект с ключами в исходном порядке.
func (o *orderedObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// decodeOrdered читает следующее значение JSON: объекты — как *orderedObject, массивы — []any.
func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	// Объект: пары ключ — значение до закрывающей скобки.
	case json.Delim('{'):
		obj := &orderedObject{values: map[string]any{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, _ := kt.(string)
			v, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			obj.set(key, v)
		}
		_, err := dec.Token()
		return obj, err

	// Массив: значения до закрывающей скобки.
	case json.Delim('['):
		arr := []any{}
		for dec.More() {
			v, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	}

	// Простое значение (строка, число, true/false, null).
	return tok, nil
}
