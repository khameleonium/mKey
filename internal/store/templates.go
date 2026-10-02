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
	{ID: "second_gamepad", Content: `version: 1
name: "Второй геймпад"
# Клавиатура как геймпад Xbox 360 (pad2): WASD — левый стик, стрелки — правый, Пробел — A,
# C — B, R — X, F — Y, Q/E — бамперы, Z/X — курки, Enter — Start, Tab — Back.
# hide: true — пока проект включён, эти клавиши видят только игры (как кнопки геймпада), а не
# другие программы. Свою раскладку удобнее собрать мастером: «Устройства» → «Второй геймпад».
events: []
virtual_devices:
  - name: pad2
    template: xbox360
bindings:
  - { from: "{W}", to: "{pad2.LY}", value: -1, hide: true }
  - { from: "{S}", to: "{pad2.LY}", value: 1, hide: true }
  - { from: "{A}", to: "{pad2.LX}", value: -1, hide: true }
  - { from: "{D}", to: "{pad2.LX}", value: 1, hide: true }
  - { from: "{Up}", to: "{pad2.RY}", value: -1, hide: true }
  - { from: "{Down}", to: "{pad2.RY}", value: 1, hide: true }
  - { from: "{Left}", to: "{pad2.RX}", value: -1, hide: true }
  - { from: "{Right}", to: "{pad2.RX}", value: 1, hide: true }
  - { from: "{Space}", to: "{pad2.South}", hide: true }
  - { from: "{C}", to: "{pad2.East}", hide: true }
  - { from: "{R}", to: "{pad2.West}", hide: true }
  - { from: "{F}", to: "{pad2.North}", hide: true }
  - { from: "{Q}", to: "{pad2.LB}", hide: true }
  - { from: "{E}", to: "{pad2.RB}", hide: true }
  - { from: "{Z}", to: "{pad2.LT}", hide: true }
  - { from: "{X}", to: "{pad2.RT}", hide: true }
  - { from: "{Enter}", to: "{pad2.Start}", hide: true }
  - { from: "{Tab}", to: "{pad2.Select}", hide: true }
`},
	{ID: "replay", Content: `version: 1
name: "Повтор записи"
# F9 повторяет запись действий. Впишите в блок «Воспроизвести запись» имя своей записи
# (список — в разделе «Записи»; записать: левый Ctrl + правый Alt + Пробел).
events:
  - id: replay
    name: "F9 — повторить запись"
    trigger: { type: hotkey, keys: "{F9}", consume: true }
    actions:
      - play: { name: "моя запись" }
`},
}
