package recorder

import (
	"bytes"
	"context"
	"fmt"

	"go.yaml.in/yaml/v3"

	"mkey/internal/contracts"
	"mkey/internal/lib/mkrec"
	"mkey/internal/lib/project"
)

// ConvertRecording превращает запись в блоки конструктора (contracts.Recorder, FR-REC-6, T6.4):
// новый выключенный проект «Из записи «имя»» с одним событием «Повтор записи» (запуск вручную).
// Проект проверяется движком до сохранения: в него не попадёт ничего, что нельзя выполнить.
func (m *Module) ConvertRecording(name string, opts contracts.ConvertOptions) (string, error) {
	if m.projects == nil {
		return "", fmt.Errorf("convert: %w", errNoProjects)
	}

	// Запись и её действия.
	path, err := m.recordingPath(name)
	if err != nil {
		return "", err
	}
	rec, err := readRecording(path)
	if err != nil {
		return "", err
	}
	actions := mkrec.ToActions(rec, mkrec.ConvertOptions{Simplify: opts.Simplify})
	if len(actions) == 0 || (len(actions) == 1 && actions[0].Type == mkrec.ActionCenter) {
		return "", contracts.ErrRecordingEmpty
	}

	// Проект: выключен, одно событие с ручным запуском; горячую клавишу человек выберет сам.
	off := false
	p := project.Project{
		Version: 1,
		Name:    m.tr.T("recorder.convert.project", contracts.Arg{Name: "name", Value: name}),
		Enabled: &off,
		Events: []project.Event{{
			ID:      "replay",
			Name:    m.tr.T("recorder.convert.event", contracts.Arg{Name: "name", Value: name}),
			Trigger: &project.Trigger{Type: "manual"},
			Actions: actions,
		}},
	}

	// Проверка движком (если он есть): виды и параметры всех блоков.
	if m.events != nil {
		if err := m.events.ValidateProject(p); err != nil {
			return "", fmt.Errorf("convert: %w", err)
		}
	}

	// Сохранение новым проектом (ID — из имени записи, свободный).
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(p); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	id, err := m.projects.Create("rec-"+name, buf.Bytes())
	if err != nil {
		return "", err
	}
	m.log.Info("recording converted to blocks", "name", name, "project", id, "actions", len(actions))
	return id, nil
}

// centerAction — действие «Курсор в центр экрана» (pointer_center): калибровка перед движениями
// мыши, как перед записью (FR-REC-5). Его ставит в начало событие, сделанное из записи.
type centerAction struct{ m *Module }

// Meta возвращает метаданные действия (параметров нет).
func (centerAction) Meta() contracts.ExtensionMeta {
	return contracts.ExtensionMeta{
		ID: mkrec.ActionCenter, NameKey: "action.pointer_center", DescriptionKey: "action.pointer_center.description",
		Category: "mouse", Icon: "crosshair", Provider: ModuleID,
	}
}

// Validate — параметров нет, проверять нечего.
func (centerAction) Validate(project.Action) error { return nil }

// Run ставит указатель в центр рабочего стола.
func (a centerAction) Run(ctx context.Context, _ contracts.RunContext, _ project.Action) error {
	if a.m.devs == nil {
		return fmt.Errorf("pointer_center: %w", errNoOutput)
	}
	return a.m.devs.CenterPointer(ctx)
}
