<!--
  BlockList — список блоков действий («Делать», тело «Повторять», ветки «Если»): блоки можно
  перетаскивать внутри списка, между списками и из палитры (FR-UI-3); список можно показать
  текстом макроса и вернуть в блоки (FR-UI-4).
  Props: list — массив блоков (часть проекта в редакторе);
  bad — номер блока с ошибкой последней проверки (подсвечивается; -2 — нет).
-->
<script lang="ts">
  import { t } from "../../lib/i18n/index.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { ActionBlock } from "../../lib/blocks";
  import Block from "./Block.svelte";
  import PalettePicker from "./PalettePicker.svelte";
  import { useEditor } from "./editor.svelte";

  let { list, bad = -2 }: { list: ActionBlock[]; bad?: number } = $props();
  const ed = useEditor();

  /** over — номер места, над которым сейчас перетаскивают блок (-1 — ни над каким). */
  let over = $state(-1);
  /** picking — открыт выбор нового блока. */
  let picking = $state(false);
  /** text — текст макроса в режиме «Текстом» (null — режим блоков). */
  let text = $state<string | null>(null);
  let busy = $state(false);

  /** onDragOver разрешает бросить блок на место index. */
  function onDragOver(e: DragEvent, index: number): void {
    if (!ed.drag) return;
    e.preventDefault();
    e.stopPropagation();
    over = index;
  }

  /** onDrop вставляет перетаскиваемый блок на место index. */
  function onDrop(e: DragEvent, index: number): void {
    e.preventDefault();
    e.stopPropagation();
    over = -1;
    ed.drop(list, index);
  }

  /** asText переводит блоки в текст макроса. */
  async function asText(): Promise<void> {
    busy = true;
    try {
      text = await ed.toDSL(list);
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      busy = false;
    }
  }

  /** asBlocks возвращает блоки из текста макроса. */
  async function asBlocks(): Promise<void> {
    busy = true;
    try {
      await ed.fromDSL(list, text ?? "");
      text = null;
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      busy = false;
    }
  }
</script>

{#snippet zone(index: number)}
  <!-- Место, куда можно бросить блок -->
  <div
    class="zone"
    class:active={ed.drag !== null}
    class:over={over === index}
    role="presentation"
    ondragover={(e) => onDragOver(e, index)}
    ondragleave={() => (over = -1)}
    ondrop={(e) => onDrop(e, index)}
  ></div>
{/snippet}

<!-- Щелчок или фокус внутри списка делает его целью палитры (клавиатура — через onfocusin).
     Событие не всплывает дальше: иначе внешний список (например, «Делать» события)
     перехватил бы цель у вложенного («Делать» внутри «Повторять»). -->
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<div
  class="list"
  class:target={ed.target === list}
  role="list"
  onfocusin={(e) => {
    e.stopPropagation();
    ed.target = list;
  }}
  onclick={(e) => {
    e.stopPropagation();
    ed.target = list;
  }}
>
  {#if text !== null}
    <!-- Режим «Текстом»: весь список — один макрос -->
    <textarea class="dsl" rows="3" spellcheck="false" bind:value={text}></textarea>
    <div class="row">
      <button class="small primary" disabled={busy} onclick={asBlocks}
        >{t("editor.dsl_back")}</button
      >
      <button class="small ghost" onclick={() => (text = null)}>{t("common.cancel")}</button>
      <span class="muted hint">{t("editor.dsl_hint")}</span>
    </div>
  {:else}
    <!-- Блоки с местами для перетаскивания между ними -->
    {#each list as block, i (block.uid)}
      {@render zone(i)}
      <Block {block} {list} bad={bad === i} />
    {/each}
    {#if list.length === 0}
      <div
        class="empty"
        class:over={over === 0}
        role="presentation"
        ondragover={(e) => onDragOver(e, 0)}
        ondragleave={() => (over = -1)}
        ondrop={(e) => onDrop(e, 0)}
      >
        {t("editor.drop_here")}
      </div>
    {:else}
      {@render zone(list.length)}
    {/if}

    <!-- Добавить блок и показать текстом -->
    <div class="row tools">
      <button class="small ghost add" onclick={() => (picking = true)}
        >+ {t("editor.add_block")}</button
      >
      {#if list.length}
        <button class="small ghost" disabled={busy} title={t("editor.dsl_title")} onclick={asText}>
          ⟨/⟩ {t("editor.as_text")}
        </button>
      {/if}
    </div>
  {/if}
</div>

{#if picking}
  <PalettePicker
    onpick={(type) => {
      ed.insert(list, ed.newAction(type));
      picking = false;
    }}
    onclose={() => (picking = false)}
  />
{/if}

<style>
  .list {
    display: flex;
    flex-direction: column;
  }
  /* Список, в который палитра добавляет блоки по щелчку: его кнопка «+ Блок» выделена. */
  .list.target > .tools .add {
    font-weight: 600;
  }
  .zone {
    height: 4px;
    border-radius: 3px;
    transition: height 0.1s;
  }
  .zone.active {
    height: 12px;
  }
  .zone.over {
    height: 28px;
    background: var(--accent-soft);
    outline: 2px dashed var(--accent);
  }
  .empty {
    padding: 14px;
    border: 2px dashed var(--border);
    border-radius: var(--radius-s);
    color: var(--muted);
    text-align: center;
    font-size: 0.9rem;
  }
  .empty.over {
    border-color: var(--accent);
    background: var(--accent-soft);
  }
  .tools {
    gap: 4px;
  }
  .add {
    color: var(--accent);
  }
  .dsl {
    font-family: var(--mono);
    width: 100%;
    resize: vertical;
    margin-bottom: 6px;
  }
  .hint {
    font-size: 0.85rem;
  }
</style>
