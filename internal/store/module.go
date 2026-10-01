package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.yaml.in/yaml/v3"

	"mkey/internal/contracts"
	"mkey/internal/lib/paths"
	"mkey/internal/lib/project"
)

// ModuleID — идентификатор модуля.
const ModuleID = "store"

// debounce — пауза после изменения файла перед перечитыванием (редакторы пишут файл в несколько шагов).
const debounce = 150 * time.Millisecond

// Config — настройки модуля из секции modules.store.
type Config struct {
	// Dir — каталог проектов (по умолчанию ~/.config/mkey/projects).
	Dir string `json:"dir"`
	// Example — создавать проект-пример при первом запуске (по умолчанию да).
	Example *bool `json:"example"`
}

// Module — хранилище проектов, реализует contracts.Projects.
type Module struct {
	// log — логгер; bus — шина; cfg — настройки; ext — реестр (шаблоны проектов из плагинов).
	log *slog.Logger
	bus contracts.Bus
	cfg Config
	ext contracts.ExtensionRegistry

	// ctx живёт до Stop; wg ждёт фоновые горутины; watcher следит за каталогом.
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	watcher *fsnotify.Watcher

	// mu защищает projects и raw.
	mu sync.RWMutex
	// projects — загруженные проекты по ID.
	projects map[string]*contracts.ProjectState
	// raw — последнее прочитанное содержимое файлов (чтобы не публиковать изменения без изменений).
	raw map[string][]byte
	// timers — отложенные перечитывания файлов (debounce).
	timers map[string]*time.Timer
}

// New создаёт модуль.
func New() *Module {
	return &Module{projects: map[string]*contracts.ProjectState{}, raw: map[string][]byte{}, timers: map[string]*time.Timer{}}
}

// ID возвращает идентификатор модуля.
func (m *Module) ID() string { return ModuleID }

// Init читает настройки и публикует сервис проектов.
func (m *Module) Init(_ context.Context, host contracts.Host) error {
	m.log = host.Logger()
	m.bus = host.Bus()
	if err := host.Config().Decode(&m.cfg); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	if m.cfg.Dir == "" {
		m.cfg.Dir = filepath.Join(paths.Config(os.Getenv), "projects")
	}

	// Папка проектов — в списке «Где что лежит».
	m.ext = host.Extensions()
	if err := host.Extensions().Register(contracts.PointPlace, contracts.StaticPlace{
		M: contracts.ExtensionMeta{ID: contracts.PlaceProjects, NameKey: "place.projects", DescriptionKey: "place.projects.description", Provider: ModuleID},
		P: m.cfg.Dir, Dir: true, N: 20,
	}); err != nil {
		return err
	}
	return contracts.ProvideService[contracts.Projects](host.Services(), m)
}

// Start создаёт каталог (и пример при первом запуске), загружает проекты и начинает следить за изменениями.
func (m *Module) Start(context.Context) error {
	m.ctx, m.cancel = context.WithCancel(context.Background())

	// Первый запуск: каталога ещё нет — создаём его и проект-пример.
	_, statErr := os.Stat(m.cfg.Dir)
	if err := os.MkdirAll(m.cfg.Dir, 0o700); err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	if errors.Is(statErr, os.ErrNotExist) && (m.cfg.Example == nil || *m.cfg.Example) {
		if err := writeAtomic(filepath.Join(m.cfg.Dir, exampleID+project.FileSuffix), []byte(exampleProject)); err != nil {
			m.log.Warn("cannot create example project", "err", err)
		}
	}

	// Следим за каталогом до загрузки, чтобы не пропустить изменения между ними.
	w, err := fsnotify.NewWatcher()
	if err == nil {
		err = w.Add(m.cfg.Dir)
	}
	if err != nil {
		m.log.Warn("project hot reload disabled", "err", err)
	} else {
		m.watcher = w
		m.wg.Add(1)
		go m.watch()
	}

	// Загружаем все проекты и сообщаем о них.
	entries, err := os.ReadDir(m.cfg.Dir)
	if err != nil {
		return fmt.Errorf("%s: %w", ModuleID, err)
	}
	var ids []string
	for _, e := range entries {
		if id, ok := project.IDFromFile(e.Name()); ok && !e.IsDir() {
			m.load(id)
			ids = append(ids, id)
		}
	}
	m.bus.Publish(contracts.TopicProjectsChanged, ids)
	m.log.Info("projects loaded", "dir", m.cfg.Dir, "count", len(ids))
	return nil
}

// Stop прекращает слежение за каталогом.
func (m *Module) Stop(context.Context) error {
	if m.cancel == nil {
		return nil
	}
	m.cancel()
	if m.watcher != nil {
		_ = m.watcher.Close()
	}
	m.mu.Lock()
	for _, t := range m.timers {
		t.Stop()
	}
	m.mu.Unlock()
	m.wg.Wait()
	return nil
}

// Dir возвращает каталог проектов.
func (m *Module) Dir() string { return m.cfg.Dir }

// List возвращает все проекты, отсортированные по ID.
func (m *Module) List() []contracts.ProjectState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]contracts.ProjectState, 0, len(m.projects))
	for _, st := range m.projects {
		out = append(out, *st)
	}
	slices.SortFunc(out, func(a, b contracts.ProjectState) int { return strings.Compare(a.Project.ID, b.Project.ID) })
	return out
}

// Get возвращает проект по ID.
func (m *Module) Get(id string) (contracts.ProjectState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.projects[id]
	if !ok {
		return contracts.ProjectState{}, false
	}
	return *st, true
}

// SetEnabled включает или выключает проект, меняя поле enabled в файле.
func (m *Module) SetEnabled(id string, enabled bool) error {
	return m.edit(id, func(doc *yaml.Node) error {
		setKey(doc, "enabled", enabled)
		return nil
	})
}

// SetEventEnabled включает или выключает событие проекта, меняя его поле enabled в файле.
func (m *Module) SetEventEnabled(projectID, eventID string, enabled bool) error {
	return m.edit(projectID, func(doc *yaml.Node) error {
		events := mapValue(doc, "events")
		if events == nil || events.Kind != yaml.SequenceNode {
			return fmt.Errorf("project %q has no events", projectID)
		}
		for _, e := range events.Content {
			if id := mapValue(e, "id"); id != nil && id.Value == eventID {
				setKey(e, "enabled", enabled)
				return nil
			}
		}
		return fmt.Errorf("event %q not found in project %q", eventID, projectID)
	})
}

// idCleanRe — символы, недопустимые в идентификаторе проекта из имени файла.
var idCleanRe = regexp.MustCompile(`[^\p{L}\p{N}_\-]+`)

// Import сохраняет новый проект из содержимого файла. Проект проверяется и сохраняется выключенным (SEC-7).
func (m *Module) Import(name string, data []byte) (string, error) {
	// Идентификатор из имени файла: только буквы, цифры, "_" и "-"; не занятый другим проектом.
	base, _ := strings.CutSuffix(filepath.Base(name), project.FileSuffix)
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")
	base = strings.Trim(idCleanRe.ReplaceAllString(base, "-"), "-")
	if base == "" {
		base = "imported"
	}
	id := base
	for i := 2; m.exists(id); i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}

	// Проверяем содержимое и выключаем проект, сохраняя комментарии.
	if _, err := project.Parse(data, id); err != nil {
		return "", err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	setKey(rootMap(&doc), "enabled", false)
	out, err := encode(&doc)
	if err != nil {
		return "", err
	}

	// Сохраняем и сразу загружаем.
	if err := writeAtomic(m.path(id), out); err != nil {
		return "", err
	}
	m.reload(id)
	return id, nil
}

// Raw возвращает содержимое файла проекта.
func (m *Module) Raw(id string) ([]byte, error) {
	if !validID(id) {
		return nil, fmt.Errorf("invalid project id %q", id)
	}
	return os.ReadFile(m.path(id))
}

// Save сохраняет проект из структуры (конструктор GUI). Комментарии файла при этом теряются.
func (m *Module) Save(id string, p project.Project) error {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(p); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return m.SaveRaw(id, b.Bytes())
}

// SaveRaw сохраняет проект из текста YAML после проверки структуры и сразу применяет его.
func (m *Module) SaveRaw(id string, data []byte) error {
	if !validID(id) {
		return fmt.Errorf("invalid project id %q", id)
	}
	if _, err := project.Parse(data, id); err != nil {
		return err
	}
	if err := writeAtomic(m.path(id), data); err != nil {
		return err
	}
	m.reload(id)
	return nil
}

// Create создаёт новый выключенный проект с содержимым data (пусто — пустой проект).
func (m *Module) Create(id string, data []byte) (string, error) {
	if len(data) == 0 {
		data = []byte("version: 1\nname: " + strconv.Quote(id) + "\nevents: []\n")
	}
	return m.Import(id+project.FileSuffix, data)
}

// Delete удаляет файл проекта (проект сразу снимается).
func (m *Module) Delete(id string) error {
	if !validID(id) {
		return fmt.Errorf("invalid project id %q", id)
	}
	if err := os.Remove(m.path(id)); err != nil {
		return err
	}
	m.log.Info("project deleted", "project", id)
	m.reload(id)
	return nil
}

// Templates возвращает шаблоны проектов: встроенные, затем из плагинов (PointProjectTemplate).
func (m *Module) Templates() []contracts.Template {
	out := slices.Clone(templates)
	if m.ext == nil {
		return out
	}
	for _, e := range m.ext.List(contracts.PointProjectTemplate) {
		if t, ok := e.(contracts.ProjectTemplate); ok {
			out = append(out, t.Template())
		}
	}
	return out
}

// validID сообщает, что ID проекта безопасен как имя файла (без путей и служебных символов).
func validID(id string) bool {
	return id != "" && !idCleanRe.MatchString(id)
}

// exists сообщает, есть ли проект или файл с таким ID.
func (m *Module) exists(id string) bool {
	if _, ok := m.Get(id); ok {
		return true
	}
	_, err := os.Stat(m.path(id))
	return err == nil
}

// path возвращает путь к файлу проекта.
func (m *Module) path(id string) string {
	return filepath.Join(m.cfg.Dir, id+project.FileSuffix)
}

// edit меняет файл проекта через дерево YAML (комментарии сохраняются), проверяет результат
// и сохраняет его атомарно.
func (m *Module) edit(id string, change func(doc *yaml.Node) error) error {
	// Читаем текущий файл.
	data, err := os.ReadFile(m.path(id))
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}

	// Меняем и проверяем, что проект остался правильным.
	if err := change(rootMap(&doc)); err != nil {
		return err
	}
	out, err := encode(&doc)
	if err != nil {
		return err
	}
	if _, err := project.Parse(out, id); err != nil {
		return err
	}

	// Сохраняем и сразу применяем, не дожидаясь уведомления о файле.
	if err := writeAtomic(m.path(id), out); err != nil {
		return err
	}
	m.reload(id)
	return nil
}

// watch обрабатывает изменения каталога проектов до остановки.
func (m *Module) watch() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			m.log.Warn("project watcher error", "err", err)
		case e, ok := <-m.watcher.Events:
			if !ok {
				return
			}
			if id, isProject := project.IDFromFile(filepath.Base(e.Name)); isProject {
				m.schedule(id)
			}
		}
	}
}

// schedule откладывает перечитывание файла на debounce (несколько событий подряд — одно чтение).
func (m *Module) schedule(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.timers[id]; ok {
		t.Stop()
	}
	m.timers[id] = time.AfterFunc(debounce, func() {
		if m.ctx.Err() == nil {
			m.reload(id)
		}
	})
}

// reload перечитывает проект и сообщает об изменении, если содержимое изменилось.
func (m *Module) reload(id string) {
	if m.load(id) {
		m.bus.Publish(contracts.TopicProjectsChanged, []string{id})
	}
}

// load читает файл проекта. Удалённый файл убирает проект; файл с ошибкой оставляет прежнюю
// версию и запоминает ошибку. Возвращает true, если состояние проекта изменилось.
func (m *Module) load(id string) bool {
	path := m.path(id)
	data, err := os.ReadFile(path)

	// Файл удалён — проекта больше нет.
	if errors.Is(err, os.ErrNotExist) {
		m.mu.Lock()
		_, had := m.projects[id]
		delete(m.projects, id)
		delete(m.raw, id)
		m.mu.Unlock()
		if had {
			m.log.Info("project removed", "project", id)
		}
		return had
	}

	// Содержимое не изменилось — ничего не делаем.
	m.mu.RLock()
	same := err == nil && bytes.Equal(m.raw[id], data)
	m.mu.RUnlock()
	if same {
		return false
	}

	// Разбор; ошибка — прежняя версия продолжает работать (FR-EV-8).
	var p project.Project
	if err == nil {
		p, err = project.Parse(data, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		st, ok := m.projects[id]
		if !ok {
			st = &contracts.ProjectState{Project: project.Project{ID: id}, Path: path}
			m.projects[id] = st
		}
		st.Error = err.Error()
		m.raw[id] = data
		m.log.Warn("project has errors, previous version kept", "project", id, "err", err)
		m.bus.Publish(contracts.TopicProjectError, contracts.ProjectError{ID: id, Error: err.Error()})
		return false
	}
	m.projects[id] = &contracts.ProjectState{Project: p, Path: path}
	m.raw[id] = data
	m.log.Info("project loaded", "project", id, "events", len(p.Events), "enabled", p.IsEnabled())
	return true
}

// rootMap возвращает корневую карту документа YAML (создаёт её для пустого документа).
func rootMap(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
		}
		return doc.Content[0]
	}
	return doc
}

// mapValue возвращает значение ключа key в карте YAML или nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setKey задаёт логическое значение ключа в карте YAML (добавляет ключ, если его нет).
func setKey(m *yaml.Node, key string, value bool) {
	v := "false"
	if value {
		v = "true"
	}
	if n := mapValue(m, key); n != nil {
		n.Kind, n.Tag, n.Value = yaml.ScalarNode, "!!bool", v
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v},
	)
}

// encode записывает дерево YAML с отступом в 2 пробела.
func encode(doc *yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return b.Bytes(), enc.Close()
}

// writeAtomic записывает файл через временный файл и переименование (права 0600).
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// Проверки на этапе компиляции, что Module реализует контракты.
var (
	_ contracts.Module   = (*Module)(nil)
	_ contracts.Projects = (*Module)(nil)
)
