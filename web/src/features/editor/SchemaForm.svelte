<!--
  SchemaForm — поля параметров блока, построенные по JSON Schema вида из реестра (SPEC §4.3):
  варианты (oneOf) — выпадающим списком, списки значений — выпадающим списком с подписями,
  клавиши — кнопкой «Нажмите клавишу…», вложенные блоки — списками блоков. Виды блоков
  в интерфейсе не зашиты: новый вид от модуля или плагина получает форму автоматически.
  Props: schema — схема; value — значение; onchange(value) — новое значение (для объектов
  поля меняются на месте через редактор, onchange вызывается при смене варианта или скаляра);
  label — подпись поля (для флажков).
-->
<script lang="ts">
  import CodeEditor from "../../lib/components/CodeEditor.svelte";
  import KeyCapture from "../../lib/components/KeyCapture.svelte";
  import Toggle from "../../lib/components/Toggle.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import {
    defaultValue,
    enumLabel,
    fields,
    isObject,
    schemaType,
    switchVariant,
    variantIndex,
    type Field,
  } from "../../lib/schema";
  import type { Schema } from "../../lib/types";
  import BlockList from "./BlockList.svelte";
  import CondList from "./CondList.svelte";
  import MacroInput from "./MacroInput.svelte";
  import SchemaForm from "./SchemaForm.svelte";
  import { useEditor } from "./editor.svelte";

  let {
    schema,
    value,
    onchange,
    label = "",
  }: {
    schema: Schema;
    value: unknown;
    onchange: (v: unknown) => void;
    label?: string;
  } = $props();

  const ed = useEditor();

  /** Основной тип и подсказка виджета. */
  let type = $derived(schemaType(schema));
  let widget = $derived(schema["x-widget"] ?? "");

  /** Поля объекта: обычные и «Подробнее». */
  let all = $derived(schema.properties ? fields(schema) : []);
  let main = $derived(all.filter((f) => !f.advanced));
  let advanced = $derived(all.filter((f) => f.advanced));

  /** setField меняет поле объекта; пустое необязательное поле удаляется. */
  function setField(f: Field, v: unknown): void {
    if (!isObject(value)) return;
    if ((v === undefined || v === "") && !f.required) ed.unset(value, f.name);
    else ed.set(value, f.name, v);
  }

  /** toNumber переводит текст поля в число; пусто — undefined. */
  function toNumber(s: string, integer: boolean): number | undefined {
    if (s.trim() === "") return undefined;
    const n = Number(s);
    if (Number.isNaN(n)) return undefined;
    return integer ? Math.round(n) : n;
  }

  /** parseValue переводит текст в значение переменной: true/false, число или строка. */
  function parseValue(s: string): unknown {
    if (s === "true" || s === "false") return s === "true";
    if (s.trim() !== "" && !Number.isNaN(Number(s))) return Number(s);
    return s;
  }

  /** Имена событий проекта и переменных — для выбора в полях. */
  let eventIDs = $derived(ed.project.events.map((e) => e.data.id));
  let varNames = $derived(Object.keys(ed.project.data.variables ?? {}));
</script>

{#snippet field(f: Field)}
  <!-- Одно поле объекта: подпись и поле ввода; вложенные блоки — на всю ширину -->
  {@const w = f.schema["x-widget"] ?? ""}
  {@const v = isObject(value) ? value[f.name] : undefined}
  {#if w === "actions" || w === "conditions"}
    <div class="nested">
      <div class="nested-title">{f.title}</div>
      {#if Array.isArray(v)}
        <SchemaForm schema={f.schema} value={v} onchange={(x) => setField(f, x)} />
      {:else}
        <button class="small ghost add" onclick={() => setField(f, [])}>
          + {t("editor.add_section", { name: f.title })}
        </button>
      {/if}
    </div>
  {:else if schemaType(f.schema) === "boolean"}
    <span class="field" title={f.schema.description ?? ""}>
      <SchemaForm schema={f.schema} value={v} label={f.title} onchange={(x) => setField(f, x)} />
    </span>
  {:else}
    <label class="field" title={f.schema.description ?? ""}>
      <span class="label">{f.title}</span>
      <SchemaForm schema={f.schema} value={v} onchange={(x) => setField(f, x)} />
    </label>
  {/if}
{/snippet}

{#if schema.oneOf}
  <!-- Варианты: выбор способа и поля выбранного варианта -->
  {@const i = variantIndex(schema, value)}
  <span class="variants">
    <select
      value={i}
      onchange={(e) => onchange(switchVariant(schema, value, Number(e.currentTarget.value)))}
    >
      {#each schema.oneOf as variant, vi (vi)}
        <option value={vi}>{variant.title ?? t("editor.variant", { n: vi + 1 })}</option>
      {/each}
    </select>
    <SchemaForm schema={schema.oneOf[i] ?? {}} {value} {onchange} />
  </span>
{:else if type === "enum"}
  <!-- Выбор из списка значений с понятными подписями -->
  <select
    value={String(value ?? "")}
    onchange={(e) => onchange(schema.enum?.find((x) => String(x) === e.currentTarget.value))}
  >
    {#each schema.enum ?? [] as opt (String(opt))}
      <option value={String(opt)}>{enumLabel(schema, opt)}</option>
    {/each}
  </select>
{:else if widget === "actions"}
  <BlockList list={value as never} />
{:else if widget === "conditions"}
  <CondList list={value as never} />
{:else if type === "object"}
  <!-- Объект: поля в строку, дополнительные — под «Подробнее» -->
  {#if !isObject(value)}
    <button class="small" onclick={() => onchange(defaultValue(schema))}>{t("editor.fill")}</button>
  {:else}
    <span class="object">
      {#each main as f (f.name)}{@render field(f)}{/each}
      {#if advanced.length}
        <details class="advanced">
          <summary>{t("common.more")}</summary>
          <span class="object">
            {#each advanced as f (f.name)}{@render field(f)}{/each}
          </span>
        </details>
      {/if}
    </span>
  {/if}
{:else if widget === "key" || widget === "keys"}
  <KeyCapture
    value={String(value ?? "")}
    combo
    braces={widget === "keys"}
    onchange={(v) => onchange(v)}
  />
{:else if widget === "macro"}
  <MacroInput value={String(value ?? "")} onchange={(v) => onchange(v)} />
{:else if widget === "multiline"}
  <textarea rows="2" value={String(value ?? "")} onchange={(e) => onchange(e.currentTarget.value)}
  ></textarea>
{:else if widget === "code"}
  <div class="code">
    <CodeEditor
      value={String(value ?? "")}
      lang={schema["x-lang"] === "lua" ? "lua" : "shell"}
      onchange={(v) => onchange(v)}
    />
  </div>
{:else if widget === "event"}
  <select value={String(value ?? "")} onchange={(e) => onchange(e.currentTarget.value)}>
    <option value="">—</option>
    {#each eventIDs as id, i (i)}<option value={id}>{id}</option>{/each}
  </select>
{:else if widget === "project"}
  <select value={String(value ?? "")} onchange={(e) => onchange(e.currentTarget.value)}>
    <option value="">—</option>
    {#each ed.projects as p (p.id)}<option value={p.id}>{p.name || p.id}</option>{/each}
  </select>
{:else if widget === "variable"}
  <input
    list="mkey-vars"
    value={String(value ?? "")}
    onchange={(e) => onchange(e.currentTarget.value)}
  />
  <datalist id="mkey-vars">
    {#each varNames as n, i (i)}<option value={n}></option>{/each}
  </datalist>
{:else if widget === "device"}
  <input
    list="mkey-devices"
    value={String(value ?? "")}
    onchange={(e) => onchange(e.currentTarget.value)}
  />
  <datalist id="mkey-devices">
    {#each ed.devices as n, i (i)}<option value={n}></option>{/each}
  </datalist>
{:else if widget === "time"}
  <input
    type="time"
    value={String(value ?? "")}
    onchange={(e) => onchange(e.currentTarget.value)}
  />
{:else if widget === "value"}
  <input
    value={value === undefined ? "" : String(value)}
    onchange={(e) => onchange(parseValue(e.currentTarget.value))}
  />
{:else if type === "integer" || type === "number"}
  <span class="num">
    <input
      type="number"
      min={schema.minimum}
      step={type === "integer" ? 1 : "any"}
      value={typeof value === "number" ? value : ""}
      onchange={(e) => onchange(toNumber(e.currentTarget.value, type === "integer"))}
    />
    {#if widget === "ms"}<span class="muted">{t("editor.ms")}</span>{/if}
  </span>
{:else if type === "boolean"}
  <Toggle checked={value === true} {label} onchange={(v) => onchange(v)} />
{:else}
  <input
    value={value === undefined || value === null ? "" : String(value)}
    onchange={(e) => onchange(e.currentTarget.value)}
  />
{/if}

<style>
  .variants,
  .object {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 14px;
  }
  .object {
    width: 100%;
  }
  .field {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .label {
    color: var(--muted);
    font-size: 0.88rem;
  }
  .nested {
    width: 100%;
  }
  .nested-title {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--muted);
    margin: 2px 0 4px;
  }
  .advanced {
    width: 100%;
  }
  .advanced summary {
    cursor: pointer;
    color: var(--muted);
    font-size: 0.88rem;
  }
  .advanced .object {
    margin-top: 6px;
  }
  .num {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .code {
    width: 100%;
    min-width: 280px;
  }
  textarea {
    min-width: 260px;
    width: 100%;
    resize: vertical;
  }
  .add {
    color: var(--accent);
  }
</style>
