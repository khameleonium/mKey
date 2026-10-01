<!--
  PlaceHint — подсказка «где лежат файлы»: папка или файл mKey с кнопкой «Скопировать путь».
  Props: id — место (contracts.Place*: "projects", "recordings", "log"…);
  describe — показать и описание места (что там лежит), по умолчанию да.
  Ничего не показывает, если места нет (модуль отключён или демон недоступен).
-->
<script lang="ts">
  import { t } from "../i18n/index.svelte";
  import { copyPath, loadPlaces, place } from "../places.svelte";
  import { toast } from "../toast.svelte";

  let { id, describe = true }: { id: string; describe?: boolean } = $props();

  // Список мест загружается при первом показе (и после смены языка).
  $effect(() => {
    void loadPlaces();
  });
  const p = $derived(place(id));

  /** copy копирует полный путь и сообщает, получилось ли. */
  async function copy(path: string): Promise<void> {
    if (await copyPath(path)) toast(t("places.copied", { path }));
    else toast(t("places.copy_failed"), "error");
  }
</script>

{#if p}
  <div class="place">
    <span class="icon" aria-hidden="true">{p.is_dir ? "📁" : "📄"}</span>
    <span class="body">
      <span>
        {t(p.is_dir ? "places.folder" : "places.file")}
        <code class="path" title={p.path}>{p.display}</code>
        {#if !p.exists}<span class="muted">({t("places.missing")})</span>{/if}
      </span>
      {#if describe && p.description}<span class="muted desc">{p.description}</span>{/if}
    </span>
    <button class="small ghost" title={p.path} onclick={() => copy(p.path)}
      >⧉ {t("places.copy")}</button
    >
  </div>
{/if}

<style>
  .place {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.5rem 0.7rem;
    border: 1px dashed var(--border);
    border-radius: 8px;
    margin: 0.6rem 0;
  }
  .body {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    flex: 1;
    min-width: 0;
  }
  .desc {
    font-size: 0.85em;
  }
  /* Путь выделяется целиком одним щелчком — его легко скопировать и вручную. */
  .path {
    user-select: all;
    word-break: break-all;
  }
</style>
