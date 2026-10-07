// Сборка редактора CodeMirror 6 для CodeEditor.svelte. Отдельный модуль загружается
// динамически (import()), поэтому CodeMirror не входит в основной файл интерфейса.
import { autocompletion, type CompletionContext } from "@codemirror/autocomplete";
import { yaml } from "@codemirror/lang-yaml";
import {
  HighlightStyle,
  StreamLanguage,
  defaultHighlightStyle,
  syntaxHighlighting,
} from "@codemirror/language";
import { lua } from "@codemirror/legacy-modes/mode/lua";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { EditorView, basicSetup } from "codemirror";

/** Функции Lua API mKey для подсказок (docs/lua-api.md). */
const MKEY_LUA = [
  ["send", "mkey.send(dsl)"],
  ["tap", "mkey.tap(key)"],
  ["down", "mkey.down(key)"],
  ["up", "mkey.up(key)"],
  ["hold", "mkey.hold(key, ms)"],
  ["type", "mkey.type(text)"],
  ["sleep", "mkey.sleep(ms)"],
  ["move_rel", "mkey.move_rel(dx, dy)"],
  ["click", "mkey.click([button])"],
  ["is_down", "mkey.is_down(key)"],
  ["toggled", "mkey.toggled()"],
  ["held", "mkey.held()"],
  ["var", "mkey.var.<name>"],
  ["event", "mkey.event"],
  ["notify", "mkey.notify(text[, title])"],
  ["run", "mkey.run(event[, project])"],
  ["log", "mkey.log(...)"],
  ["on_stop", "mkey.on_stop(fn)"],
] as const;

/** mkeyCompletions подсказывает функции после «mkey.». */
function mkeyCompletions(ctx: CompletionContext) {
  const word = ctx.matchBefore(/mkey\.\w*/);
  if (!word) return null;
  return {
    from: word.from + "mkey.".length,
    options: MKEY_LUA.map(([label, detail]) => ({ label, detail, type: "function" })),
  };
}

/**
 * themedHighlight — подсветка CodeMirror по умолчанию, но каждый цвет — переменная темы
 * (--cm-708 для #708) с исходным цветом по умолчанию: в тёмной теме app.css задаёт светлые
 * оттенки, иначе тёмные цвета кода на тёмном фоне были бы неразличимы (контраст WCAG).
 */
const themedHighlight = HighlightStyle.define(
  defaultHighlightStyle.specs.map((s) =>
    typeof s.color === "string" && s.color.startsWith("#")
      ? { ...s, color: `var(--cm-${s.color.slice(1)}, ${s.color})` }
      : s,
  ),
);

/**
 * createEditor создаёт редактор в host с текстом value; onchange получает новый текст;
 * label — подпись поля для экранного диктора.
 */
export function createEditor(
  host: HTMLElement,
  value: string,
  lang: "yaml" | "lua" | "shell",
  onchange: (text: string) => void,
  label: string,
): EditorView {
  // Подсветка языка; для Lua — ещё подсказки mkey.*.
  const language =
    lang === "yaml"
      ? [yaml()]
      : lang === "lua"
        ? [StreamLanguage.define(lua), autocompletion({ override: [mkeyCompletions] })]
        : [StreamLanguage.define(shell)];

  // Редактор с базовыми возможностями (номера строк, отмена, поиск) и переносом строк.
  return new EditorView({
    doc: value,
    parent: host,
    extensions: [
      basicSetup,
      syntaxHighlighting(themedHighlight),
      ...language,
      EditorView.lineWrapping,
      // Подпись поля для экранного диктора (у поля редактора нет своего <label>).
      EditorView.contentAttributes.of({ "aria-label": label }),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) onchange(u.state.doc.toString());
      }),
    ],
  });
}
