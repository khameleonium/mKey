<!--
  PalettePicker — окно выбора нового блока действия по категориям (для кнопки «+ Блок»).
  Props: onpick(type) — выбран вид блока; onclose() — закрыть без выбора.
-->
<script lang="ts">
  import Modal from "../../lib/components/Modal.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { useEditor } from "./editor.svelte";
  import { groupByCategory } from "./palette";

  let { onpick, onclose }: { onpick: (type: string) => void; onclose: () => void } = $props();
  const ed = useEditor();
  let groups = $derived(groupByCategory(ed.reg.action ?? []));
</script>

<Modal title={t("editor.pick_block")} wide {onclose}>
  <div class="groups">
    {#each groups as g (g.id)}
      <section>
        <h3>{g.name}</h3>
        {#each g.items as item (item.id)}
          <button class="item" title={item.description ?? ""} onclick={() => onpick(item.id)}>
            <b>{item.name}</b>
            {#if item.description}<span class="muted">{item.description}</span>{/if}
          </button>
        {/each}
      </section>
    {/each}
  </div>
</Modal>

<style>
  .groups {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
    gap: 14px;
  }
  h3 {
    margin: 0 0 6px;
    font-size: 0.9rem;
    color: var(--muted);
  }
  .item {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    white-space: normal;
    text-align: left;
    width: 100%;
    margin-bottom: 6px;
    gap: 2px;
  }
  .item span {
    font-size: 0.82rem;
  }
</style>
