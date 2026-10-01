<!--
  PluginsPage — раздел «Плагины» (FR-PLG-4, ADR-0029): установленные плагины, их состояние и
  что они добавляют; включение (с показом разрешений и честным предупреждением), выключение,
  журнал, удаление; установка из архива .zip или папки. Плагины появляются и в конструкторе
  событий — как обычные блоки. Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import Modal from "../../lib/components/Modal.svelte";
  import PlaceHint from "../../lib/components/PlaceHint.svelte";
  import { lang, t } from "../../lib/i18n/index.svelte";
  import { onTopic } from "../../lib/stream.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { PluginInfo } from "../../lib/types";

  /** Плагины и папка плагинов; error — ошибка загрузки списка. */
  let plugins = $state<PluginInfo[]>([]);
  let dir = $state("");
  let error = $state("");
  /** enabling — плагин, который хотят включить (окно с разрешениями). */
  let enabling = $state<PluginInfo | null>(null);
  /** log — журнал открытого плагина. */
  let log = $state<{ plugin: PluginInfo; lines: string[] } | null>(null);
  /** path — путь к папке или архиву для установки; busy — идёт установка. */
  let path = $state("");
  let busy = $state(false);

  /** text выбирает текст на языке интерфейса (иначе английский, иначе любой). */
  function text(m: Record<string, string> | undefined, fallback = ""): string {
    if (!m) return fallback;
    return m[lang()] ?? m.en ?? Object.values(m)[0] ?? fallback;
  }

  /** load перечитывает список плагинов. */
  async function load(): Promise<void> {
    try {
      const r = await api.plugins();
      plugins = r.plugins;
      dir = r.dir;
      error = "";
    } catch (e) {
      error = errorText(e);
    }
  }

  // Загрузка при открытии; состояние плагинов меняется — список перечитывается (с запасом
  // на запуск процесса плагина).
  $effect(() => {
    void load();
    const off = onTopic("registry.extensions_changed", () => void load());
    const timer = setInterval(() => void load(), 3000);
    return () => {
      off();
      clearInterval(timer);
    };
  });

  /** setActive включает или выключает плагин. */
  async function setActive(p: PluginInfo, on: boolean): Promise<void> {
    try {
      await api.setPluginActive(p.id, on);
      toast(t(on ? "plugins.enabled" : "plugins.disabled", { name: text(p.name, p.id) }));
      enabling = null;
      await load();
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** remove удаляет плагин после подтверждения. */
  async function remove(p: PluginInfo): Promise<void> {
    if (!confirm(t("plugins.remove_confirm", { name: text(p.name, p.id) }))) return;
    try {
      await api.removePlugin(p.id);
      toast(t("plugins.removed", { name: text(p.name, p.id) }));
      await load();
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** showLog открывает журнал плагина. */
  async function showLog(p: PluginInfo): Promise<void> {
    try {
      log = { plugin: p, lines: (await api.pluginLog(p.id)).lines };
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** installed сообщает об установке и предлагает включить. */
  async function installed(p: PluginInfo): Promise<void> {
    toast(t("plugins.installed", { name: text(p.name, p.id) }));
    path = "";
    await load();
    enabling = p;
  }

  /** installPath устанавливает плагин из папки или архива на этом компьютере. */
  async function installPath(): Promise<void> {
    busy = true;
    try {
      await installed(await api.installPlugin(path.trim()));
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      busy = false;
    }
  }

  /** installFile устанавливает плагин из выбранного архива .zip. */
  async function installFile(e: Event): Promise<void> {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    busy = true;
    try {
      await installed(await api.uploadPlugin(file));
    } catch (err) {
      toast(errorText(err), "error");
    } finally {
      busy = false;
      input.value = "";
    }
  }

  /** types — что плагин добавляет, одной строкой. */
  function types(p: PluginInfo): string {
    return [
      ...(p.actions ?? []),
      ...(p.conditions ?? []),
      ...(p.triggers ?? []),
      ...(p.templates ?? []),
    ].join(", ");
  }
</script>

<h1>{t("plugins.title")}</h1>
<p class="muted">{t("plugins.hint")}</p>
<PlaceHint id="plugins" />

{#if error}<div class="note error">{error}</div>{/if}

<!-- Установка -->
<div class="card install">
  <h2>{t("plugins.install")}</h2>
  <p class="muted">{t("plugins.install_hint")}</p>
  <div class="row">
    <label class="file">
      <input type="file" accept=".zip,application/zip" disabled={busy} onchange={installFile} />
      <span class="button">📦 {t("plugins.install_zip")}</span>
    </label>
    <span class="muted">{t("plugins.or")}</span>
    <input class="path" bind:value={path} placeholder={t("plugins.path_placeholder")} />
    <button disabled={busy || !path.trim()} onclick={() => void installPath()}
      >{t("plugins.install_path")}</button
    >
  </div>
</div>

<!-- Список -->
{#if plugins.length === 0 && !error}
  <div class="card"><p class="muted">{t("plugins.none", { dir })}</p></div>
{/if}
{#each plugins as p (p.id)}
  <div class="card plugin" class:off={!p.active}>
    <div class="head">
      <div>
        <h2>{text(p.name, p.id)} <span class="muted ver">{p.version ?? ""}</span></h2>
        <code class="muted">{p.id}</code>
        <span class="badge {p.state}">{t("plugins.state." + p.state)}</span>
        <span class="muted">{t("plugins.kind." + p.kind)}</span>
      </div>
      <div class="actions">
        {#if p.state !== "broken"}
          {#if p.active}
            <button onclick={() => void setActive(p, false)}>{t("plugins.disable")}</button>
          {:else}
            <button class="primary" onclick={() => (enabling = p)}>{t("plugins.enable")}</button>
          {/if}
        {/if}
        {#if p.kind === "process"}
          <button class="ghost" onclick={() => void showLog(p)}>{t("plugins.log")}</button>
        {/if}
        {#if !p.system}
          <button class="ghost danger-text" onclick={() => void remove(p)}
            >{t("plugins.remove")}</button
          >
        {/if}
      </div>
    </div>
    {#if p.description}<p>{text(p.description)}</p>{/if}
    {#if p.error}<div class="note error">{p.error}</div>{/if}
    {#if types(p)}<p class="muted">{t("plugins.types", { list: types(p) })}</p>{/if}
    {#if p.permissions?.length}
      <p class="muted">
        {t("plugins.asks")}
        {p.permissions.map((x) => t("plugins.perm." + x)).join(", ")}
      </p>
    {/if}
  </div>
{/each}

<!-- Включение: что плагин просит -->
{#if enabling}
  {@const p = enabling}
  <Modal
    title={t("plugins.enable_title", { name: text(p.name, p.id) })}
    onclose={() => (enabling = null)}
  >
    {#if p.permissions?.length}
      <p>{t("plugins.asks")}</p>
      <ul>
        {#each p.permissions as perm (perm)}<li>{t("plugins.perm." + perm)}</li>{/each}
      </ul>
    {:else}
      <p>{t("plugins.no_permissions")}</p>
    {/if}
    <div class="note warn">{t("plugins.trust")}</div>
    {#snippet footer()}
      <button onclick={() => (enabling = null)}>{t("common.cancel")}</button>
      <button class="primary" onclick={() => void setActive(p, true)}>{t("plugins.enable")}</button>
    {/snippet}
  </Modal>
{/if}

<!-- Журнал плагина -->
{#if log}
  <Modal
    title={t("plugins.log_title", { name: text(log.plugin.name, log.plugin.id) })}
    wide
    onclose={() => (log = null)}
  >
    {#if log.lines.length === 0}
      <p class="muted">{t("plugins.log_empty")}</p>
    {:else}
      <pre class="log">{log.lines.join("\n")}</pre>
    {/if}
  </Modal>
{/if}

<style>
  .install .row {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: center;
  }
  .path {
    flex: 1;
    min-width: 16em;
  }
  .file input {
    display: none;
  }
  .file .button {
    display: inline-block;
    padding: 6px 12px;
    border: 1px solid var(--border);
    border-radius: 6px;
    cursor: pointer;
  }
  .plugin {
    margin-top: 12px;
  }
  .plugin.off {
    opacity: 0.85;
  }
  .head {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    align-items: flex-start;
  }
  .head h2 {
    margin: 0 0 4px;
  }
  .ver {
    font-size: 0.8em;
    font-weight: normal;
  }
  .actions {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .badge {
    display: inline-block;
    margin: 0 8px;
    padding: 1px 8px;
    border-radius: 10px;
    font-size: 0.85em;
    background: var(--accent-soft);
  }
  .badge.running {
    background: var(--ok-soft, var(--accent-soft));
  }
  .badge.failed,
  .badge.broken {
    background: var(--danger);
    color: white;
  }
  .danger-text {
    color: var(--danger);
  }
  .log {
    max-height: 60vh;
    overflow: auto;
    font-family: var(--mono);
    font-size: 0.85em;
    white-space: pre-wrap;
  }
</style>
