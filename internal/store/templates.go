package store

import "mkey/internal/contracts"

// templates — встроенные шаблоны проектов (FR-UI-1, п. 2). Проект из шаблона создаётся выключенным.
// Названия и описания — i18n-ключи template.<id>.name и template.<id>.description.
var templates = []contracts.Template{
	{ID: "autoclicker", Content: `version: 1
name: "Автокликер"
events:
  - id: autoclick
    name: "F8 — включить/выключить автоклик"
    trigger: { type: hotkey, keys: "{F8}", on: toggle, consume: true }
    actions:
      - repeat:
          while: toggled
          do:
            - send: "{Mouse0}"
            - pause: 50
`},
	{ID: "hold_mouse", Content: `version: 1
name: "Удержание кнопки мыши"
events:
  - id: hold
    name: "Ctrl+Alt+H — держать левую кнопку 1,5 с"
    trigger: { type: hotkey, keys: "^{Ctrl}^{Alt}{H}", consume: true }
    actions:
      - key_down: Mouse0
      - pause: 1500
      - key_up: Mouse0
`},
	{ID: "capslock_esc", Content: `version: 1
name: "CapsLock как Esc"
events: []
remaps:
  - { from: "{CapsLock}", to: "{Esc}" }
`},
	{ID: "text_shortcut", Content: `version: 1
name: "Сокращения текста"
events:
  - id: btw
    name: "btw → by the way"
    trigger: { type: hotstring, text: "btw", replace: "by the way" }
    actions: []
`},
	{ID: "break_reminder", Content: `version: 1
name: "Напоминание о перерыве"
events:
  - id: reminder
    name: "Каждые 45 минут — уведомление"
    trigger: { type: timer, every_ms: 2700000 }
    actions:
      - notify: { title: "Перерыв", body: "Пора встать и размяться" }
`},
}
