<!--
  ScriptsPage — раздел «Скрипты» (FR-UI-1.7): файлы скриптов Lua и bash из папок модулей lua и
  shell. Слева — список файлов по языкам и «Новый скрипт», справа — редактор выбранного файла
  (подсветка, подсказки mkey.* для Lua), «Сохранить», «Проверить» (синтаксис без запуска),
  «Удалить». Ошибка синтаксиса не мешает сохранить: показывается строкой с номером.
  Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import CodeEditor from "../../lib/components/CodeEditor.svelte";
  import Modal from "../../lib/components/Modal.svelte";
  import PlaceHint from "../../lib/components/PlaceHint.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { Registry, ScriptFile, ScriptLang, ScriptProblem } from "../../lib/types";

  /** Языки и файлы; reg — реестр видов (названия блоков «Скрипт Lua», «Команда bash»). */
  let langs = $state<ScriptLang[]>([]);
  let files = $state<ScriptFile[]>([]);
  let reg = $state<Registry>({});
  let loadError = $state("");

  /** Открытый файл: язык, имя, текст, изменён ли, ошибка синтаксиса последней проверки. */
  let open = $state<{ lang: ScriptLang; name: string } | null>(null);
  let text = $state("");
  let dirty = $state(false);
  let problem = $state<ScriptProblem | null>(null);

  /** Окно «Новый скрипт»: язык и имя. */
  let creating = $state(false);
  let newLang = $state("lua");
  let newName = $state("");

  /** load перечитывает языки и файлы. */
  async function load(): Promise<void> {
    try {
      const r = await api.scripts();
      langs = r.languages;
      files = r.files;
      loadError = "";
    } catch (e) {
      loadError = errorText(e);
    }
  }

  // Список и реестр — при открытии раздела.
  $effect(() => {
    void load();
    api
      .registry()
      .then((r) => (reg = r))
      .catch(() => {});
  });

  /** leave спрашивает, можно ли уйти от несохранённых изменений; true — можно. */
  function leave(): boolean {
    return !dirty || !open || confirm(t("scripts.unsaved_confirm", { name: open.name }));
  }

  /** select открывает файл в редакторе. */
  async function select(f: ScriptFile): Promise<void> {
    const lang = langs.find((l) => l.id === f.lang);
    if (!lang || !leave()) return;
    try {
      const r = await api.script(f.lang, f.name);
      open = { lang, name: f.name };
      text = r.content;
      dirty = false;
      problem = null;
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** fileName — имя нового файла с расширением языка. */
  function fileName(name: string, lang: ScriptLang): string {
    const n = name.trim();
    return n.endsWith(lang.ext) ? n : n + lang.ext;
  }

  /** create создаёт скрипт с заготовкой и открывает его. */
  async function create(): Promise<void> {
    const lang = langs.find((l) => l.id === newLang);
    if (!lang || !newName.trim()) return;
    const name = fileName(newName, lang);
    if (files.some((f) => f.lang === lang.id && f.name === name)) {
      toast(t("scripts.exists", { name }), "error");
      return;
    }
    try {
      const content = t(lang.id === "lua" ? "scripts.template_lua" : "scripts.template_shell");
      await api.saveScript(lang.id, name, content);
      creating = false;
      newName = "";
      await load();
      open = { lang, name };
      text = content;
      dirty = false;
      problem = null;
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** save сохраняет открытый файл; ошибка синтаксиса — подсказкой (файл сохранён). */
  async function save(): Promise<void> {
    if (!open) return;
    try {
      const r = await api.saveScript(open.lang.id, open.name, text);
      dirty = false;
      problem = r.problem ?? null;
      if (problem) toast(t("scripts.saved_problem"), "error");
      else toast(t("scripts.saved", { name: open.name }));
      await load();
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** check проверяет синтаксис, ничего не сохраняя. */
  async function check(): Promise<void> {
    if (!open) return;
    try {
      problem = (await api.checkScript(open.lang.id, text)).problem ?? null;
      if (!problem) toast(t("scripts.check_ok"));
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** remove удаляет открытый файл после подтверждения. */
  async function remove(): Promise<void> {
    if (!open || !confirm(t("scripts.delete_confirm", { name: open.name }))) return;
    try {
      await api.deleteScript(open.lang.id, open.name);
      toast(t("scripts.deleted", { name: open.name }));
      open = null;
      dirty = false;
      await load();
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** problemText — ошибка синтаксиса строкой. */
  function problemText(p: ScriptProblem): string {
    return p.line
      ? t("scripts.problem", { line: p.line, message: p.message })
      : t("scripts.problem_noline", { message: p.message });
  }

  /** actionName — название блока, который запускает файлы языка (ID действия = ID языка). */
  function actionName(lang: string): string {
    return reg.action?.find((a) => a.id === lang)?.name ?? lang;
  }

  // Предупреждение при уходе со страницы с несохранёнными изменениями.
  $effect(() => {
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty) e.preventDefault();
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  });
</script>

<h1>{t("scripts.title")}</h1>
<p class="muted">{t("scripts.hint")}</p>
<!-- Папки скриптов: у Lua и bash по умолчанию одна и та же — тогда показываем её один раз -->
<PlaceHint id="lua_scripts" describe={false} />
{#if new Set(langs.map((l) => l.dir)).size > 1}
  <PlaceHint id="shell_scripts" describe={false} />
{/if}
{#if loadError}<div class="note error">{loadError}</div>{/if}

<div class="layout">
  <!-- Список файлов по языкам -->
  <aside class="card list">
    <button class="primary" disabled={langs.length === 0} onclick={() => (creating = true)}
      >+ {t("scripts.new")}</button
    >
    {#each langs as lang (lang.id)}
      <h2>{lang.name}</h2>
      <ul>
        {#each files.filter((f) => f.lang === lang.id) as f (f.name)}
          <li>
            <button
              class="ghost file"
              class:active={open?.lang.id === lang.id && open?.name === f.name}
              onclick={() => void select(f)}>{f.name}</button
            >
          </li>
        {:else}
          <li class="muted">{t("scripts.empty")}</li>
        {/each}
      </ul>
    {/each}
  </aside>

  <!-- Редактор открытого файла -->
  <section class="card editor">
    {#if open}
      <div class="row head">
        <h2>{open.name}</h2>
        {#if dirty}<span class="unsaved">● {t("editor.unsaved_short")}</span>{/if}
        <span class="spacer"></span>
        <button class="primary" onclick={save}>{t("scripts.save")}</button>
        <button onclick={check}>{t("scripts.check")}</button>
        <button class="danger" onclick={remove}>{t("common.delete")}</button>
      </div>
      {#key open.lang.id + "/" + open.name}
        <CodeEditor
          value={text}
          lang={open.lang.highlight}
          minLines={20}
          label={open.name}
          onchange={(v) => {
            text = v;
            dirty = true;
          }}
        />
      {/key}
      {#if problem}<div class="note error">{problemText(problem)}</div>{/if}
      <p class="muted">
        {t("scripts.use", { action: actionName(open.lang.id), name: open.name })}
      </p>
    {:else}
      <p class="muted">{t("scripts.choose")}</p>
    {/if}
  </section>
</div>

<!-- Новый скрипт: язык и имя файла -->
{#if creating}
  <Modal title={t("scripts.new")} onclose={() => (creating = false)}>
    <label class="field"
      >{t("scripts.language")}
      <select bind:value={newLang}>
        {#each langs as lang (lang.id)}<option value={lang.id}>{lang.name}</option>{/each}
      </select></label
    >
    <label class="field"
      >{t("scripts.name")}
      <input
        bind:value={newName}
        placeholder={t("scripts.name_hint", {
          ext: langs.find((l) => l.id === newLang)?.ext ?? "",
        })}
        onkeydown={(e) => {
          if (e.key === "Enter") void create();
        }}
      /></label
    >
    {#snippet footer()}
      <button onclick={() => (creating = false)}>{t("common.cancel")}</button>
      <button class="primary" disabled={!newName.trim()} onclick={create}
        >{t("scripts.create")}</button
      >
    {/snippet}
  </Modal>
{/if}

<style>
  .layout {
    display: grid;
    grid-template-columns: minmax(200px, 260px) 1fr;
    gap: 16px;
    align-items: start;
  }
  .list h2 {
    font-size: 1rem;
    margin: 16px 0 6px;
  }
  .list ul {
    list-style: none;
    margin: 0 0 8px;
    padding: 0;
  }
  .file {
    width: 100%;
    text-align: left;
    overflow-wrap: anywhere;
  }
  .file.active {
    background: var(--surface-2);
    font-weight: 600;
  }
  .editor {
    min-width: 0;
  }
  .head h2 {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .unsaved {
    color: var(--warn);
    font-size: 0.85rem;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-bottom: 10px;
  }
  @media (max-width: 760px) {
    .layout {
      grid-template-columns: 1fr;
    }
  }
</style>
