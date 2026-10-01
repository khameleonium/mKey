package contracts

import (
	"context"
	"errors"
)

// Обновление mKey (ADR-0030, FR-INST-6): модуль update.

// TopicUpdateAvailable — найдена новая версия (при включённой проверке); Payload: UpdateInfo.
const TopicUpdateAvailable = "update.available"

// ErrCannotUpdate — mKey не может обновить себя сам (установлен пакетом, сборка разработчика);
// причина — в UpdateInfo.Reason.
var ErrCannotUpdate = errors.New("mKey cannot update itself")

// Причины, по которым mKey не обновляет себя сам.
const (
	// UpdatePackage — установлен пакетом: обновляет менеджер пакетов.
	UpdatePackage = "package"
	// UpdateDev — сборка разработчика: версию не с чем сравнить.
	UpdateDev = "dev"
)

// UpdateInfo — сведения о версиях.
type UpdateInfo struct {
	// Current — версия этой программы; Latest — последний выпуск ("" — ещё не проверяли).
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	// Available — выпуск новее этой программы; URL — страница выпуска (что нового).
	Available bool   `json:"available"`
	URL       string `json:"url,omitempty"`
	// CanApply — mKey может обновиться сам; иначе Reason — UpdatePackage или UpdateDev.
	CanApply bool   `json:"can_apply"`
	Reason   string `json:"reason,omitempty"`
	// Check — проверка раз в сутки включена.
	Check bool `json:"check"`
}

// Updater — проверка и установка обновлений.
type Updater interface {
	// Info возвращает сведения по последней проверке (без обращения к сети).
	Info() UpdateInfo
	// Check спрашивает последний выпуск (обращается к сети — только по просьбе человека или при
	// включённой проверке).
	Check(ctx context.Context) (UpdateInfo, error)
	// Apply скачивает и проверяет новую версию, заменяет программу и перезапускает mKey.
	// ErrCannotUpdate — обновить сам нельзя; нет новой версии — ошибка.
	Apply(ctx context.Context) (UpdateInfo, error)
	// SetCheck включает или выключает проверку раз в сутки (сохраняет вызывающий — в config.yaml).
	SetCheck(on bool)
}
