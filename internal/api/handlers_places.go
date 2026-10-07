package api

import (
	"cmp"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/khameleonium/mKey/internal/contracts"
)

// placeEntry — место на диске в ответе /places: на языке клиента и с путём, удобным для чтения.
type placeEntry struct {
	// ID — идентификатор места (contracts.PlaceProjects…); IDs — все места с этим путём
	// (например, скрипты Lua и bash в одной папке); Name и Description — на языке клиента.
	ID          string   `json:"id"`
	IDs         []string `json:"ids"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	// Path — полный путь; Display — тот же путь с «~» вместо домашней папки (короче для глаз).
	Path    string `json:"path"`
	Display string `json:"display"`
	// IsDir — это папка; Exists — папка или файл уже есть на диске.
	IsDir  bool `json:"is_dir"`
	Exists bool `json:"exists"`
}

// handlePlaces возвращает места, где mKey хранит файлы пользователя («Где что лежит»):
// их регистрируют модули в точке расширения PointPlace. Порядок — по Order, затем по ID;
// места с одинаковым путём (скрипты Lua и bash в одной папке) показываются одной строкой.
func (m *Module) handlePlaces(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"places": m.places(m.translator(r))})
}

// places собирает места из реестра на языке tr.
func (m *Module) places(tr contracts.Translator) []placeEntry {
	// Места из точки расширения, по порядку.
	var list []contracts.Place
	if m.svc.ext == nil {
		return []placeEntry{}
	}
	for _, e := range m.svc.ext.List(contracts.PointPlace) {
		if p, ok := e.(contracts.Place); ok {
			list = append(list, p)
		}
	}
	slices.SortStableFunc(list, func(a, b contracts.Place) int {
		return cmp.Or(cmp.Compare(a.Order(), b.Order()), cmp.Compare(a.Meta().ID, b.Meta().ID))
	})

	// Названия, путь для чтения и наличие на диске.
	home, _ := os.UserHomeDir()
	out := make([]placeEntry, 0, len(list))
	for _, p := range list {
		meta := p.Meta()
		name := meta.ID
		if s, ok := lookupText(tr, meta.NameKey); ok {
			name = s
		}
		desc, _ := lookupText(tr, meta.DescriptionKey)

		// Тот же путь уже есть — дописываем название и описание к нему.
		if i := slices.IndexFunc(out, func(e placeEntry) bool { return e.Path == p.Path() && e.IsDir == p.IsDir() }); i >= 0 {
			e := &out[i]
			e.IDs = append(e.IDs, meta.ID)
			e.Name += ", " + name
			e.Description = strings.TrimSpace(e.Description + " " + desc)
			continue
		}

		// Новое место.
		_, err := os.Stat(p.Path())
		out = append(out, placeEntry{
			ID: meta.ID, IDs: []string{meta.ID}, Name: name, Description: desc,
			Path: p.Path(), Display: shortPath(p.Path(), home), IsDir: p.IsDir(), Exists: err == nil,
		})
	}
	return out
}

// placePath возвращает путь места по ID ("" — места нет: модуль отключён).
func (m *Module) placePath(id string) string {
	if m.svc.ext == nil {
		return ""
	}
	if e, ok := m.svc.ext.Get(contracts.PointPlace, id); ok {
		if p, ok := e.(contracts.Place); ok {
			return p.Path()
		}
	}
	return ""
}

// shortPath заменяет домашнюю папку в начале пути на «~» (~/.config/mkey вместо /home/имя/.config/mkey).
func shortPath(path, home string) string {
	if home == "" || home == "/" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return path
}
