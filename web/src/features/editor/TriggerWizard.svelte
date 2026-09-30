<!--
  TriggerWizard — мастер выбора триггера «Когда…» (FR-UI-5): сначала понятный выбор
  («Когда я нажимаю клавиши…», «По времени…»), затем параметры выбранного вида.
  Триггеры окна и пикселя показаны недоступными с пояснением (появятся в следующей версии).
  Props: trigger — изменяемый триггер (нет — новый); onsave(trigger); onclose().
-->
<script lang="ts">
  import Modal from "../../lib/components/Modal.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { defaultValue, isObject } from "../../lib/schema";
  import type { Trigger } from "../../lib/types";
  import SchemaForm from "./SchemaForm.svelte";
  import { useEditor } from "./editor.svelte";

  let {
    trigger,
    onsave,
    onclose,
  }: { trigger?: Trigger; onsave: (t: Trigger) => void; onclose: () => void } = $props();
  const ed = useEditor();

  /** Черновик: вид и параметры (правятся здесь, в проект попадают по «Готово»).
      Берётся начальное значение триггера — окно создаётся заново для каждого изменения. */
  // svelte-ignore state_referenced_locally
  let draft = $state<{ type: string; params: unknown }>({
    type: trigger?.type ?? "",
    params: structuredClone($state.snapshot(trigger?.params) ?? {}),
  });

  /** Выбранный вид из реестра. */
  let entry = $derived(draft.type ? ed.entry("trigger", draft.type) : undefined);

  /** choose выбирает вид триггера и заполняет параметры по умолчанию. */
  function choose(type: string): void {
    draft.type = type;
    draft.params = defaultValue(ed.entry("trigger", type)?.params_schema) ?? {};
  }

  /** Будущие виды (фаза 8): показываются, но недоступны. */
  const future = ["trigger.future.window", "trigger.future.pixel"];
</script>

<Modal title={t(trigger ? "editor.trigger_edit" : "editor.trigger_new")} wide {onclose}>
  {#if !entry}
    <!-- Шаг 1: что должно произойти -->
    <div class="choices">
      {#each ed.reg.trigger ?? [] as e (e.id)}
        <button class="choice" onclick={() => choose(e.id)}>
          <b>{e.name}</b>
          {#if e.description}<span class="muted">{e.description}</span>{/if}
        </button>
      {/each}
      {#each future as key (key)}
        <button class="choice" disabled title={t("editor.trigger_future_hint")}>
          <b>{t(key)}</b>
          <span class="muted">{t("editor.trigger_future_hint")}</span>
        </button>
      {/each}
    </div>
  {:else}
    <!-- Шаг 2: подробности выбранного вида -->
    <div class="params">
      <div class="row">
        <b>{entry.name}</b>
        <button class="small ghost" onclick={() => (draft.type = "")}
          >{t("editor.trigger_change")}</button
        >
      </div>
      {#if entry.description}<p class="muted">{entry.description}</p>{/if}
      {#if entry.params_schema && (entry.params_schema.properties || entry.params_schema.oneOf)}
        <SchemaForm
          schema={entry.params_schema}
          value={draft.params}
          onchange={(v) => (draft.params = v)}
        />
      {/if}
    </div>
  {/if}

  {#snippet footer()}
    <button onclick={onclose}>{t("common.cancel")}</button>
    <button
      class="primary"
      disabled={!entry}
      onclick={() => {
        const p = $state.snapshot(draft.params);
        onsave(
          isObject(p) && Object.keys(p).length
            ? { type: draft.type, params: p }
            : { type: draft.type },
        );
      }}
    >
      {t("common.done")}
    </button>
  {/snippet}
</Modal>

<style>
  .choices {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(230px, 1fr));
    gap: 10px;
  }
  .choice {
    flex-direction: column;
    align-items: flex-start;
    white-space: normal;
    text-align: left;
    padding: 12px;
    gap: 4px;
  }
  .choice span {
    font-size: 0.85rem;
  }
  .params {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .params p {
    margin: 0;
  }
</style>
