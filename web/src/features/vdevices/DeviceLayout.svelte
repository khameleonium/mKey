<!--
  DeviceLayout — раскладка одного виртуального устройства проекта в редакторе (FR-VD-8): таблица
  «что нажимается ← чем» (RowsEditor), «прятать клавиши» и «Добавить» (кнопку или направление
  оси, клавишу назначают потом). Изменения сразу превращаются в привязки проекта; сохраняет их
  кнопка «Сохранить» редактора.
  Props: device — устройство из проекта; bindings — все привязки проекта; info — состав шаблона
  (для «Добавить»); onchange(bindings) — новые привязки проекта (чужие привязки не трогаются).
-->
<script lang="ts">
  import { t } from "../../lib/i18n/index.svelte";
  import type { Binding, VirtualDeviceSpec, VirtualTemplateInfo } from "../../lib/types";
  import { rowLabel } from "./labels";
  import { KINDS, rowBindings, rowsFromBindings, sourceDevice, targets, type Row } from "./presets";
  import RowsEditor from "./RowsEditor.svelte";

  let {
    device,
    bindings,
    info,
    onchange,
  }: {
    device: VirtualDeviceSpec;
    bindings: Binding[];
    info?: VirtualTemplateInfo;
    onchange: (bindings: Binding[]) => void;
  } = $props();

  /** ONE_SIDED — оси «от нуля» (в «Добавить» — одно направление). */
  const ONE_SIDED = new Set(["LT", "RT", "Gas", "Brake", "Clutch", "Throttle", "Slider"]);

  /** rows — строки раскладки (свои, чтобы очищенная строка не исчезала до назначения клавиши);
   *  emitted — привязки устройства, которые отдали мы сами, текстом (их приход не перечитывает
   *  строки; сравнение по содержимому — проект хранит копию в реактивной обёртке). */
  let rows = $state<Row[]>([]);
  let emitted = "";
  // Строки — из привязок проекта, когда те пришли извне (открытие, «Загрузить с диска»).
  $effect(() => {
    if (ownKey(bindings) !== emitted) rows = rowsFromBindings(bindings, device.name);
  });

  /** ownKey — привязки этого устройства текстом (для сравнения). */
  function ownKey(list: Binding[]): string {
    return JSON.stringify($state.snapshot(list.filter((b) => targets(b, device.name))));
  }

  /** source — устройство-источник привязок; hide — какая-то привязка прячет клавиши. */
  let source = $derived(sourceDevice(bindings, device.name));
  let hide = $derived(bindings.some((b) => targets(b, device.name) && b.hide));

  /** emit заменяет привязки устройства новыми по строкам (на месте первой из прежних). */
  function emit(next: Row[], nextHide = hide): void {
    rows = next;
    const own = rowBindings(next, device.name, source, nextHide);
    const first = bindings.findIndex((b) => targets(b, device.name));
    const others = bindings.filter((b) => !targets(b, device.name));
    const at =
      first < 0
        ? others.length
        : bindings.slice(0, first).filter((b) => !targets(b, device.name)).length;
    const out = [...others.slice(0, at), ...own, ...others.slice(at)];
    emitted = ownKey(out);
    onchange(out);
  }

  /** choices — что можно добавить: кнопки и направления осей (у осей с центром — два). */
  let choices = $derived.by(() => {
    const out: Row[] = [];
    for (const a of info?.axes ?? []) {
      if (ONE_SIDED.has(a)) out.push({ id: `${a}+`, to: a, from: "", opts: { value: 1 } });
      else
        out.push(
          { id: `${a}-`, to: a, from: "", opts: { value: -1 } },
          { id: `${a}+`, to: a, from: "", opts: { value: 1 } },
        );
    }
    for (const b of info?.buttons ?? []) out.push({ id: b, to: b, from: "" });
    return out;
  });
  let adding = $state("");

  /** add добавляет пустую строку выбранного вида (клавишу назначают кнопкой «Нажмите клавишу…»). */
  function add(): void {
    const c = choices.find((x) => x.id === adding);
    if (!c) return;
    rows = [...rows, { ...c, id: `new${Date.now()}:${c.id}` }];
    adding = "";
  }

  /** icon — значок вида устройства. */
  let icon = $derived(KINDS.find((k) => k.id === device.template)?.icon ?? "🔌");
</script>

<div class="device">
  <h3>
    <span aria-hidden="true">{icon}</span> mKey {device.name} · {t(`vd.kind.${device.template}`)}
  </h3>
  <RowsEditor {rows} device={source} onchange={(r) => emit(r)} />
  <div class="tools">
    {#if choices.length}
      <select bind:value={adding} aria-label={t("vd.add_row")}>
        <option value="">{t("vd.add_row")}…</option>
        {#each choices as c (c.id)}
          <option value={c.id}>{rowLabel(c, t)}</option>
        {/each}
      </select>
      <button class="small" disabled={!adding} onclick={add}>＋ {t("vd.add_row")}</button>
    {/if}
    <label class="row"
      ><input
        type="checkbox"
        checked={hide}
        onchange={(e) => emit(rows, e.currentTarget.checked)}
      />
      {t("vd.hide")}</label
    >
  </div>
</div>

<style>
  .device + :global(.device) {
    margin-top: 18px;
  }
  h3 {
    margin: 0 0 8px;
  }
  .tools {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 16px;
    align-items: center;
    margin-top: 10px;
  }
</style>
