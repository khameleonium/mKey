<!--
  CodeEditor — редактор кода CodeMirror 6 с подсветкой: YAML проекта, скрипты Lua и bash
  (для Lua — подсказки функций mkey.*). CodeMirror загружается только при первом показе
  редактора, чтобы остальные страницы открывались быстро.
  Props: value — текст; lang — "yaml" | "lua" | "shell"; minLines — высота в строках;
  label — подпись поля для экранного диктора (по умолчанию «Текст кода»); onchange(value) — текст изменён.
-->
<script lang="ts">
  import type { EditorView } from "@codemirror/view";
  import { t } from "../i18n/index.svelte";

  let {
    value,
    lang,
    minLines = 4,
    label = "",
    onchange,
  }: {
    value: string;
    lang: "yaml" | "lua" | "shell";
    minLines?: number;
    label?: string;
    onchange: (value: string) => void;
  } = $props();

  /** host — контейнер редактора; view — сам редактор после загрузки. */
  let host: HTMLDivElement;
  let view: EditorView | undefined;

  // Создаём редактор при появлении и убираем при исчезновении компонента.
  $effect(() => {
    let destroyed = false;
    void import("./codemirror").then(({ createEditor }) => {
      if (destroyed) return;
      view = createEditor(
        host,
        value,
        lang,
        (text) => onchange(text),
        label || t("editor.code_label"),
      );
    });
    return () => {
      destroyed = true;
      view?.destroy();
    };
  });

  // Внешнее изменение текста (например, перезагрузка файла) переносим в редактор.
  $effect(() => {
    const v = value;
    if (view && view.state.doc.toString() !== v) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } });
    }
  });
</script>

<div class="editor" bind:this={host} style:--min-lines={minLines}></div>

<style>
  .editor {
    border: 1px solid var(--border);
    border-radius: var(--radius-s);
    overflow: hidden;
    background: var(--surface);
  }
  .editor :global(.cm-editor) {
    min-height: calc(var(--min-lines) * 1.45em + 8px);
    font-size: 0.92rem;
  }
  .editor :global(.cm-scroller) {
    font-family: var(--mono);
  }
  .editor :global(.cm-focused) {
    outline: none;
  }
  /* Оформление CodeMirror — цветами темы окна: по умолчанию редактор рассчитан на светлый фон,
     и в тёмной теме строка с курсором, выделение, курсор и поле номеров строк были бы светлыми
     пятнами или не видны. */
  .editor :global(.cm-gutters) {
    background: var(--surface-2);
    color: var(--muted);
    border-right-color: var(--border);
  }
  .editor :global(.cm-activeLine) {
    background: var(--code-active-line);
  }
  .editor :global(.cm-activeLineGutter) {
    background: var(--code-active-line);
    color: var(--text);
  }
  .editor :global(.cm-cursor) {
    border-left-color: var(--text);
  }
  .editor :global(.cm-selectionBackground),
  .editor :global(.cm-focused .cm-selectionBackground),
  .editor :global(.cm-content ::selection) {
    background: var(--code-selection) !important;
  }
</style>
