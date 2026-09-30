package store

// exampleID — идентификатор проекта-примера.
const exampleID = "example"

// exampleProject — проект-пример, который создаётся при первом запуске (выключенным).
const exampleProject = `# Проект-пример mKey. Он выключен: включите его командой
#   mkey project enable example
# Файл можно править в любом текстовом редакторе — изменения применяются сразу.
# Правила записи макросов: docs/dsl.md

version: 1
name: "Пример"
enabled: false

variables:
  clicks: { type: int, value: 0 }

events:
  # F8 — включить/выключить автокликер: левая кнопка мыши каждые 100 мс.
  - id: autoclick
    name: "Автокликер на F8"
    trigger: { type: hotkey, keys: "{F8}", on: toggle, consume: true }
    actions:
      - repeat:
          while: toggled
          do:
            - send: "{Mouse0}[100]"
            - set_var: { name: clicks, add: 1 }

  # Ctrl+Alt+H — зажать левую кнопку мыши на 1,5 секунды.
  - id: long_hold
    name: "Удержание ЛКМ"
    trigger: { type: hotkey, keys: "^{Ctrl}^{Alt}{H}", consume: true }
    actions:
      - send: "^{Mouse0}[1500]~{Mouse0}"

  # Наберите «btw» и пробел — слово заменится на «by the way».
  - id: btw
    name: "Сокращение btw"
    trigger: { type: hotstring, text: "btw", replace: "by the way" }
    actions:
      - notify: "mKey: заменил btw"

# Переназначение клавиш: раскомментируйте, чтобы CapsLock работал как Esc.
# remaps:
#   - { from: "{CapsLock}", to: "{Esc}" }
`
