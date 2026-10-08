package contracts

import (
	"context"
	"errors"
)

// Самостоятельный файл макроса (FR-BUILD-1, ADR-0042): модуль builder собирает из проекта
// исполняемый файл, который работает без установленного mKey.

// Режимы собранного файла: работать, как включённый проект, или выполнить одно событие и выйти.
const (
	BuildModeEvents = "events"
	BuildModeOnce   = "once"
)

// BuildRequest — что собрать.
type BuildRequest struct {
	// Project — ID проекта.
	Project string `json:"project"`
	// Mode — BuildModeEvents (по умолчанию) или BuildModeOnce; Event — событие для BuildModeOnce.
	Mode  string `json:"mode,omitempty"`
	Event string `json:"event,omitempty"`
	// Output — полный путь файла ("" — в папку PlaceBuilds, имя — по названию проекта).
	Output string `json:"output,omitempty"`
}

// BuildResult — что получилось.
type BuildResult struct {
	// Path — полный путь собранного файла; Size — его размер в байтах.
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Files — что вошло кроме проекта: записи и скрипты ("recordings/бег.mkrec").
	Files []string `json:"files"`
}

// MacroBuilder — сборка самостоятельного файла макроса (модуль builder).
type MacroBuilder interface {
	// Build собирает файл. Ошибки: ErrBuildProject (нет проекта или в нём ошибка), ErrBuildEvent
	// (нет события для «выполнить и выйти»), ErrBuildMissing (нет записи или скрипта, на которые
	// ссылается проект), ErrBuildPlugin (проект использует действия плагинов — их в файл не
	// положить); в тексте ошибки — что именно.
	Build(ctx context.Context, req BuildRequest) (BuildResult, error)
}

var (
	// ErrBuildProject — проекта нет или в нём ошибка.
	ErrBuildProject = errors.New("project cannot be built")
	// ErrBuildEvent — события для «выполнить и выйти» нет в проекте.
	ErrBuildEvent = errors.New("event not found")
	// ErrBuildMissing — файла записи или скрипта, на который ссылается проект, нет.
	ErrBuildMissing = errors.New("referenced file not found")
	// ErrBuildPlugin — проект использует действия плагинов.
	ErrBuildPlugin = errors.New("project uses plugin actions")
)
