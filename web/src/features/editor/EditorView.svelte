<!--
  EditorView — содержимое редактора загруженного проекта: вкладка «Конструктор» (палитра
  блоков и лист событий) и вкладка «Текст YAML» (тот же проект файлом). Сохранение с проверкой,
  «Запустить сейчас», предупреждение, если файл изменили в другом месте (FR-UI-7).
  Props: ed — редактор проекта; raw — текст файла; tab — открытая вкладка (bindable);
  onreload() — перечитать проект с диска.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import CodeEditor from "../../lib/components/CodeEditor.svelte";
  import Toggle from "../../lib/components/Toggle.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { href } from "../../lib/router.svelte";
  import { onTopic } from "../../lib/stream.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { EditorEvent } from "../../lib/blocks";
  import EventRow from "./EventRow.svelte";
  import Palette from "./Palette.svelte";
  import { provideEditor, type Editor } from "./editor.svelte";

  let {
    ed,
    raw: initialRaw,
    tab = $bindable(),
    onreload,
  }: {
    ed: Editor;
    raw: string;
    tab: "blocks" | "yaml";
    onreload: () => Promise<void>;
  } = $props();

  // Редактор доступен всем вложенным компонентам.
  // svelte-ignore state_referenced_locally
  provideEditor(ed);

  /** raw — текст файла; rawDirty — текст изменён и не сохранён; rawError — ошибка сохранения. */
  // svelte-ignore state_referenced_locally
  let raw = $state(initialRaw);
  let rawDirty = $state(false);
  let rawError = $state("");
  /** changedOnDisk — файл изменили в другом месте, пока здесь есть несохранённые правки. */
  let changedOnDisk = $state(false);

  // Файл проекта изменился: без своих правок — перечитываем, иначе спрашиваем.
  $effect(() =>
    onTopic("store.projects_changed", (data) => {
      const ids = Array.isArray(data) ? (data as string[]) : [];
      if (!ids.includes(ed.id) || Date.now() - ed.savedAt < 2000) return;
      if (ed.dirty || rawDirty) changedOnDisk = true;
      else void onreload();
    }),
  );

  /** save сохраняет проект из конструктора. */
  async function save(): Promise<void> {
    if (await ed.save()) {
      toast(t("editor.saved"));
      raw = (await api.project(ed.id)).raw;
      changedOnDisk = false;
    }
  }

  /** saveRaw сохраняет текст YAML и перечитывает проект в конструктор. */
  async function saveRaw(): Promise<void> {
    rawError = "";
    try {
      ed.savedAt = Date.now();
      await api.saveProjectRaw(ed.id, raw);
      toast(t("editor.saved"));
      await onreload();
    } catch (e) {
      rawError = errorText(e);
    }
  }

  /** reloadFile перечитывает проект с диска. */
  function reloadFile(): void {
    void onreload();
  }

  /** run сохраняет изменения (если есть) и запускает событие вручную. */
  async function run(ev: EditorEvent): Promise<void> {
    if (ed.dirty && !(await ed.save())) {
      toast(ed.error, "error");
      return;
    }
    toast(t("editor.run_started", { name: ev.data.name || ev.data.id }), "info");
    try {
      await api.runEvent(ed.id, ev.data.id);
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  // Предупреждение при уходе со страницы с несохранёнными изменениями.
  $effect(() => {
    const handler = (e: BeforeUnloadEvent) => {
      if (ed.dirty || rawDirty) e.preventDefault();
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  });
</script>

<!-- Шапка: название, включение, сохранение (заголовок страницы — для экранного диктора) -->
<h1 class="sr-only">{ed.project.data.name || ed.project.data.id}</h1>
<div class="top row">
  <a href={href("projects")} class="back" aria-label={t("nav.projects")} title={t("nav.projects")}
    >←</a
  >
  <input
    class="title"
    value={ed.project.data.name}
    aria-label={t("editor.project_name")}
    onchange={(e) => ed.set(ed.project.data, "name", e.currentTarget.value)}
  />
  <Toggle
    checked={ed.project.data.enabled !== false}
    label={t("editor.project_enabled")}
    onchange={(on) => ed.set(ed.project.data, "enabled", on)}
  />
  <span class="spacer"></span>
  {#if ed.dirty}<span class="unsaved" title={t("editor.unsaved")}
      >● {t("editor.unsaved_short")}</span
    >{/if}
  <button
    onclick={async () => {
      if (await ed.check()) toast(t("editor.check_ok"));
    }}>{t("editor.check")}</button
  >
  <button class="primary" disabled={ed.saving || !ed.dirty} onclick={save}
    >{t("common.save")}</button
  >
</div>

{#if changedOnDisk}
  <div class="note warn row">
    {t("editor.changed_on_disk")}
    <span class="spacer"></span>
    <button class="small" onclick={reloadFile}>{t("editor.reload")}</button>
    <button class="small" onclick={() => (changedOnDisk = false)}>{t("editor.keep_mine")}</button>
  </div>
{/if}
{#if ed.error}
  <div class="note error">{ed.error}</div>
{/if}

<!-- Вкладки -->
<div class="tabs" role="tablist">
  <button
    role="tab"
    class:active={tab === "blocks"}
    aria-selected={tab === "blocks"}
    onclick={() => (tab = "blocks")}
  >
    {t("editor.tab_blocks")}
  </button>
  <button
    role="tab"
    class:active={tab === "yaml"}
    aria-selected={tab === "yaml"}
    onclick={() => (tab = "yaml")}
  >
    {t("editor.tab_yaml")}
  </button>
</div>

{#if tab === "blocks"}
  <div class="workspace">
    <Palette />
    <div class="sheet">
      {#each ed.project.events as ev, i (ev.uid)}
        <EventRow {ev} index={i} total={ed.project.events.length} onrun={run} />
      {/each}
      {#if ed.project.events.length === 0}
        <div class="note">{t("editor.no_events")}</div>
      {/if}
      <button class="add-event" onclick={() => ed.addEvent()}>+ {t("editor.add_event")}</button>
    </div>
  </div>
{:else}
  <!-- Текст файла проекта -->
  {#if ed.dirty}
    <div class="note warn row">
      {t("editor.yaml_unsaved")}
      <button class="small primary" onclick={save}>{t("common.save")}</button>
    </div>
  {/if}
  <p class="muted">{t("editor.yaml_hint")}</p>
  <CodeEditor
    value={raw}
    lang="yaml"
    minLines={20}
    onchange={(v) => {
      raw = v;
      rawDirty = true;
    }}
  />
  {#if rawError}<div class="note error">{rawError}</div>{/if}
  <div class="row yaml-actions">
    <button class="primary" disabled={!rawDirty} onclick={saveRaw}>{t("common.save")}</button>
    <button disabled={!rawDirty} onclick={reloadFile}>{t("editor.revert")}</button>
  </div>
{/if}

<style>
  .top {
    margin-bottom: 10px;
  }
  .back {
    text-decoration: none;
    font-size: 1.3rem;
  }
  .title {
    font-size: 1.3rem;
    font-weight: 700;
    border-color: transparent;
    background: transparent;
    min-width: 8em;
    flex: 1 1 10em;
    max-width: 20em;
  }
  .unsaved {
    color: var(--warn);
    font-size: 0.9rem;
  }
  .title:hover,
  .title:focus {
    border-color: var(--border);
  }
  .note {
    margin-bottom: 10px;
  }
  .tabs {
    display: flex;
    gap: 4px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 14px;
  }
  .tabs button {
    border: none;
    border-bottom: 3px solid transparent;
    border-radius: 0;
    background: transparent;
  }
  .tabs button.active {
    border-bottom-color: var(--accent);
    font-weight: 600;
  }
  .workspace {
    display: grid;
    grid-template-columns: 210px 1fr;
    gap: 16px;
    align-items: start;
  }
  .workspace :global(.palette) {
    position: sticky;
    top: 12px;
    max-height: calc(100vh - 24px);
    overflow: auto;
  }
  @media (max-width: 900px) {
    .workspace {
      grid-template-columns: 1fr;
    }
    .workspace :global(.palette) {
      position: static;
      max-height: none;
    }
  }
  .sheet {
    display: flex;
    flex-direction: column;
    gap: 12px;
    min-width: 0;
  }
  .add-event {
    align-self: flex-start;
    border-style: dashed;
  }
  .yaml-actions {
    margin-top: 10px;
  }
</style>
