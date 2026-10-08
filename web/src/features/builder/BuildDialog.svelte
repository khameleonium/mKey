<!--
  BuildDialog — «Собрать в файл» (FR-BUILD-1, ADR-0042): из проекта собирается один файл-программа,
  которая работает без установленного mKey. Человек выбирает, что делать файлу: «работать, как
  проект» или «выполнить событие и выйти»; несохранённый проект сначала сохраняется. Потом —
  что получилось, «Скачать файл» и как его запускать.
  Props: project — ID проекта; name — название; events — события проекта (ID и название);
  save() — сохранить проект, если есть изменения (false — не получилось); onclose() — закрыть.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import Modal from "../../lib/components/Modal.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { errorText } from "../../lib/toast.svelte";
  import type { BuildResult } from "../../lib/types";

  let {
    project,
    name,
    events,
    save,
    onclose,
  }: {
    project: string;
    name: string;
    events: { id: string; name?: string }[];
    save: () => Promise<boolean>;
    onclose: () => void;
  } = $props();

  /** Выбор: режим и событие; busy — идёт сборка; result и error — итог. */
  let mode = $state<"events" | "once">("events");
  let event = $state("");
  let busy = $state(false);
  let result = $state<BuildResult | null>(null);
  let error = $state("");

  /** ready — можно собирать: для «выполнить и выйти» выбрано событие. */
  let ready = $derived(mode === "events" || event !== "");

  /** build сохраняет проект (если нужно) и собирает файл. */
  async function build(): Promise<void> {
    busy = true;
    error = "";
    try {
      if (!(await save())) return;
      result = await api.buildProject(project, mode === "once" ? { mode, event } : { mode });
    } catch (e) {
      error = errorText(e);
    } finally {
      busy = false;
    }
  }

  /** download скачивает собранный файл браузером (в папку загрузок). */
  function download(url: string): void {
    const a = document.createElement("a");
    a.href = url;
    a.download = "";
    a.click();
  }

  /** megabytes — размер файла в мегабайтах с одной цифрой после запятой. */
  function megabytes(n: number): string {
    return (n / (1 << 20)).toFixed(1);
  }
</script>

<Modal title={t("build.title")} wide {onclose}>
  {#if result}
    <!-- Готово: что получилось и как запускать -->
    <div class="note ok">{t("build.done", { name, size: megabytes(result.size) })}</div>
    <p class="path">{t("build.where", { path: result.path })}</p>
    {#if result.files?.length}
      <p class="muted">{t("build.files", { files: result.files.join(", ") })}</p>
    {/if}
    <p>{t("build.how")}</p>
    <p class="muted">{t("build.trust")}</p>
  {:else}
    <p>{t("build.intro")}</p>

    <!-- Что делать файлу -->
    <fieldset class="modes">
      <legend>{t("build.mode")}</legend>
      <label class="check"
        ><input
          type="radio"
          name="build-mode"
          checked={mode === "events"}
          onchange={() => (mode = "events")}
        />
        <span
          ><b>{t("build.mode_events")}</b><br /><span class="muted"
            >{t("build.mode_events_hint")}</span
          ></span
        ></label
      >
      <label class="check"
        ><input
          type="radio"
          name="build-mode"
          checked={mode === "once"}
          onchange={() => (mode = "once")}
        />
        <span
          ><b>{t("build.mode_once")}</b><br /><span class="muted">{t("build.mode_once_hint")}</span
          ></span
        ></label
      >
    </fieldset>
    {#if mode === "once"}
      <label class="row"
        >{t("build.event")}
        <select bind:value={event}>
          <option value="">{t("build.pick_event")}</option>
          {#each events as e (e.id)}
            <option value={e.id}>{e.name || e.id}</option>
          {/each}
        </select></label
      >
    {/if}
    {#if error}<div class="note error">{error}</div>{/if}
  {/if}

  {#snippet footer()}
    {#if result}
      {#if result.download}
        <button class="primary" onclick={() => download(result?.download ?? "")}
          >⬇ {t("build.download")}</button
        >
      {/if}
      <button onclick={onclose}>{t("common.close")}</button>
    {:else}
      <button onclick={onclose}>{t("common.cancel")}</button>
      <button class="primary" disabled={busy || !ready} onclick={() => void build()}
        >{busy ? t("build.building") : t("build.start")}</button
      >
    {/if}
  {/snippet}
</Modal>

<style>
  .modes {
    border: none;
    padding: 0;
    margin: 12px 0;
  }
  .modes legend {
    font-weight: 600;
    padding: 0;
    margin-bottom: 6px;
  }
  .check {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    margin: 8px 0;
  }
  .check input {
    margin-top: 4px;
  }
  .path {
    font-family: var(--mono);
    overflow-wrap: anywhere;
  }
</style>
