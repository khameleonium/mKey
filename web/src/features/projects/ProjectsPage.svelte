<!--
  ProjectsPage — список проектов (FR-UI-1.2): включение, открыть в редакторе, создать пустой
  или из шаблона, импорт (новый проект выключен до проверки, SEC-7), экспорт файлом, удаление.
  Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import Modal from "../../lib/components/Modal.svelte";
  import PlaceHint from "../../lib/components/PlaceHint.svelte";
  import Toggle from "../../lib/components/Toggle.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { href, navigate } from "../../lib/router.svelte";
  import { onTopic } from "../../lib/stream.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { ProjectInfo, ProjectScript, Template } from "../../lib/types";

  /** Данные страницы. */
  let projects = $state<ProjectInfo[]>([]);
  let loaded = $state(false);
  /** Окна: создание и шаблоны. */
  let creating = $state(false);
  let newName = $state("");
  let templates = $state<Template[] | null>(null);
  /** imported — загруженный проект со скриптами: окно с их кодом до включения (SEC-7). */
  let imported = $state<{ id: string; scripts: ProjectScript[] } | null>(null);

  /** load перечитывает список проектов. */
  async function load(): Promise<void> {
    try {
      const r = await api.projects();
      projects = r.projects;
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      loaded = true;
    }
  }

  // Загрузка при открытии и при любом изменении проектов.
  $effect(() => {
    void load();
    return onTopic("store.projects_changed", () => void load());
  });

  /** toggle включает или выключает проект. */
  async function toggle(p: ProjectInfo, on: boolean): Promise<void> {
    try {
      await api.setProjectEnabled(p.id, on);
    } catch (e) {
      toast(errorText(e), "error");
    }
    await load();
  }

  /** create создаёт пустой проект с названием newName и открывает его в редакторе. */
  async function create(): Promise<void> {
    const name = newName.trim();
    if (!name) return;
    try {
      const { id } = await api.createProject(name);
      creating = false;
      newName = "";
      navigate("editor", id);
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** openTemplates показывает готовые шаблоны. */
  async function openTemplates(): Promise<void> {
    try {
      templates = (await api.templates()).templates ?? [];
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** fromTemplate создаёт проект из шаблона и открывает его. */
  async function fromTemplate(tpl: Template): Promise<void> {
    try {
      const { id, scripts } = await api.createProject(tpl.name, tpl.id);
      templates = null;
      // Шаблон со скриптами (из плагина) — сначала показать их код (SEC-7).
      if (scripts?.length) imported = { id, scripts };
      else navigate("editor", id);
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** importFile загружает проект из выбранного файла (он будет выключен до проверки). */
  async function importFile(e: Event): Promise<void> {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    try {
      const { id, scripts } = await api.importProject(file.name, await file.text());
      toast(t("projects.imported"));
      // Есть скрипты — сначала показать их код и предупредить; иначе — сразу в редактор.
      if (scripts?.length) imported = { id, scripts };
      else navigate("editor", id);
    } catch (err) {
      toast(errorText(err), "error");
    }
  }

  /** exportFile скачивает файл проекта для обмена: mKey дописывает в него имена кнопок
   * устройств, которые встречаются в проекте ({Геймпад.Старт}, ADR-0027). */
  function exportFile(p: ProjectInfo): void {
    const a = document.createElement("a");
    a.href = `/api/v1/projects/${encodeURIComponent(p.id)}/export`;
    a.download = `${p.id}.mkey.yaml`;
    a.click();
  }

  /** remove удаляет проект после подтверждения. */
  async function remove(p: ProjectInfo): Promise<void> {
    if (!confirm(t("projects.delete_confirm", { name: p.name || p.id }))) return;
    try {
      await api.deleteProject(p.id);
      toast(t("projects.deleted"));
    } catch (e) {
      toast(errorText(e), "error");
    }
    await load();
  }
</script>

<div class="row head">
  <h1>{t("projects.title")}</h1>
  <span class="spacer"></span>
  <button class="primary" onclick={() => (creating = true)}>+ {t("projects.new")}</button>
  <button onclick={openTemplates}>★ {t("projects.from_template")}</button>
  <label class="btn">
    ⇪ {t("projects.import")}
    <input type="file" accept=".yaml,.yml" hidden onchange={importFile} />
  </label>
</div>

{#if loaded && projects.length === 0}
  <div class="note">{t("projects.empty")}</div>
{/if}

<div class="list">
  {#each projects as p (p.id)}
    <div class="card project" class:off={!p.enabled}>
      <Toggle checked={p.enabled} title={t("projects.enabled")} onchange={(on) => toggle(p, on)} />
      <div class="info">
        <a class="name" href={href("editor", p.id)}>{p.name || p.id}</a>
        <span class="muted">{t("projects.events_count", { n: p.events })}</span>
        {#if p.error}<div class="err">⚠ {p.error}</div>{/if}
      </div>
      <span class="spacer"></span>
      <a class="btn" href={href("editor", p.id)}>✎ {t("projects.edit")}</a>
      <button class="ghost" title={t("projects.export")} onclick={() => exportFile(p)}>⇩</button>
      <button class="ghost" title={t("common.delete")} onclick={() => remove(p)}>✕</button>
    </div>
  {/each}
</div>

<PlaceHint id="projects" />

{#if imported}
  {@const imp = imported}
  <Modal title={t("projects.scripts_title")} wide onclose={() => (imported = null)}>
    <div class="note warn">{t("projects.scripts_warning", { count: imp.scripts.length })}</div>
    {#each imp.scripts as s, i (i)}
      <h3 class="script-head">{s.event} — {s.type}</h3>
      {#if s.file}
        <p class="muted">{t("projects.script_file", { file: s.file })}</p>
      {:else}
        <pre class="script">{s.code}</pre>
      {/if}
    {/each}
    {#snippet footer()}
      <button onclick={() => (imported = null)}>{t("common.close")}</button>
      <button class="primary" onclick={() => navigate("editor", imp.id)}
        >{t("projects.scripts_open")}</button
      >
    {/snippet}
  </Modal>
{/if}

{#if creating}
  <Modal title={t("projects.new")} onclose={() => (creating = false)}>
    <label class="field">
      {t("projects.name")}
      <!-- svelte-ignore a11y_autofocus -->
      <input
        bind:value={newName}
        autofocus
        placeholder={t("projects.name_placeholder")}
        onkeydown={(e) => {
          if (e.key === "Enter") void create();
        }}
      />
    </label>
    <p class="muted">{t("projects.new_hint")}</p>
    {#snippet footer()}
      <button onclick={() => (creating = false)}>{t("common.cancel")}</button>
      <button class="primary" disabled={!newName.trim()} onclick={create}
        >{t("common.create")}</button
      >
    {/snippet}
  </Modal>
{/if}

{#if templates}
  <Modal title={t("projects.templates")} wide onclose={() => (templates = null)}>
    <div class="templates">
      {#each templates as tpl (tpl.id)}
        <button class="tpl" onclick={() => fromTemplate(tpl)}>
          <b>{tpl.name}</b>
          <span class="muted">{tpl.description}</span>
        </button>
      {/each}
    </div>
    <p class="muted">{t("projects.template_hint")}</p>
  </Modal>
{/if}

<style>
  .script-head {
    margin: 12px 0 4px;
    font-size: 1em;
  }
  .script {
    max-height: 30vh;
    overflow: auto;
    font-family: var(--mono);
    font-size: 0.85em;
    white-space: pre-wrap;
    background: var(--bg-soft, rgba(127, 127, 127, 0.08));
    padding: 8px;
    border-radius: 6px;
  }
  .head {
    margin-bottom: 12px;
  }
  .head h1 {
    margin: 0;
  }
  .list {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .project {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 12px 16px;
  }
  .project.off .name {
    color: var(--muted);
  }
  .info {
    display: flex;
    flex-direction: column;
  }
  .name {
    font-weight: 600;
    font-size: 1.05rem;
    text-decoration: none;
    color: var(--text);
  }
  .err {
    color: var(--danger);
    font-size: 0.88rem;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .templates {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 10px;
  }
  .tpl {
    flex-direction: column;
    align-items: flex-start;
    white-space: normal;
    text-align: left;
    padding: 12px;
    gap: 4px;
  }
</style>
