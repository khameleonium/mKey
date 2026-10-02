<!--
  Palette — палитра блоков слева от листа событий (FR-UI-3): блоки по категориям.
  Блок перетаскивают в «Делать» или щёлкают по нему — он добавится в последний выбранный список.
  Props: нет (данные — из редактора).
-->
<script lang="ts">
  import { t } from "../../lib/i18n/index.svelte";
  import { useEditor } from "./editor.svelte";
  import { groupByCategory } from "./palette";

  const ed = useEditor();
  let groups = $derived(groupByCategory(ed.reg.action ?? []));
</script>

<aside class="palette">
  <h2>{t("editor.palette")}</h2>
  <p class="muted hint">{t("editor.palette_hint")}</p>
  {#each groups as g (g.id)}
    <details open>
      <summary>{g.name}</summary>
      {#each g.items as item (item.id)}
        <div
          class="item cat-{g.id}"
          draggable="true"
          role="button"
          tabindex="0"
          title={item.description ?? ""}
          ondragstart={(e) => {
            e.dataTransfer?.setData("text/plain", item.id);
            ed.drag = { type: item.id };
          }}
          ondragend={() => (ed.drag = null)}
          onclick={() => ed.addToTarget(item.id)}
          onkeydown={(e) => {
            // Пробел не должен ещё и прокручивать страницу.
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              ed.addToTarget(item.id);
            }
          }}
        >
          {item.name}
        </div>
      {/each}
    </details>
  {/each}
</aside>

<style>
  .palette {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  h2 {
    margin: 0;
  }
  .hint {
    font-size: 0.82rem;
    margin: 0 0 4px;
  }
  summary {
    cursor: pointer;
    font-weight: 600;
    font-size: 0.88rem;
    color: var(--muted);
    margin: 4px 0;
  }
  .item {
    --cat: var(--accent);
    padding: 5px 8px;
    margin: 0 0 4px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-left: 4px solid var(--cat);
    border-radius: var(--radius-s);
    cursor: grab;
    font-size: 0.9rem;
  }
  .item:hover {
    border-color: var(--cat);
  }
  .cat-mouse {
    --cat: #0f9d8a;
  }
  .cat-touch {
    --cat: #db2777;
  }
  .cat-time {
    --cat: #d98a00;
  }
  .cat-logic {
    --cat: #8c4de0;
  }
  .cat-system {
    --cat: #6b7280;
  }
  .cat-script {
    --cat: #c2410c;
  }
</style>
