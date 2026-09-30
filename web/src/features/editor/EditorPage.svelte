<!--
  EditorPage — страница редактора проекта: загружает реестр видов блоков (на языке интерфейса)
  и проект, затем показывает EditorView. После перечитывания проекта редактор создаётся заново.
  Props: id — ID проекта.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { lang, t } from "../../lib/i18n/index.svelte";
  import { href } from "../../lib/router.svelte";
  import { errorText } from "../../lib/toast.svelte";
  import EditorView from "./EditorView.svelte";
  import { Editor } from "./editor.svelte";

  let { id }: { id: string } = $props();

  /** ed — редактор загруженного проекта; raw — текст файла; loadError — ошибка загрузки. */
  let ed = $state<Editor | null>(null);
  let raw = $state("");
  let loadError = $state("");
  /** tab — открытая вкладка (сохраняется при перечитывании проекта). */
  let tab = $state<"blocks" | "yaml">("blocks");

  /** load загружает реестр, проект и списки для полей выбора. */
  async function load(): Promise<void> {
    loadError = "";
    try {
      const [reg, file] = await Promise.all([api.registry(), api.project(id)]);
      const next = new Editor(id, reg);
      next.load(file.project);
      raw = file.raw;
      ed = next;
      void loadLists(next);
    } catch (e) {
      loadError = errorText(e);
    }
  }

  /** loadLists загружает проекты и устройства для полей выбора (не критично). */
  async function loadLists(target: Editor): Promise<void> {
    try {
      target.projects = (await api.projects()).projects;
      // Названия устройств без повторов: у одного USB-приёмника бывает несколько устройств с одним именем.
      target.devices = [...new Set((await api.devices()).devices.map((d) => d.info.name))];
    } catch {
      // Без списков поля остаются обычными полями ввода.
    }
  }

  // Загрузка при открытии и при смене языка (названия блоков приходят с сервера).
  $effect(() => {
    void lang();
    void id;
    void load();
  });
</script>

{#if loadError}
  <div class="note error">{loadError}</div>
  <p><a href={href("projects")}>← {t("nav.projects")}</a></p>
{:else if !ed}
  <p class="muted">{t("common.loading")}</p>
{:else}
  {#key ed}
    <EditorView {ed} {raw} bind:tab onreload={load} />
  {/key}
{/if}
