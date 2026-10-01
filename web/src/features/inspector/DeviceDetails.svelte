<!--
  DeviceDetails — всё об одном устройстве ввода (модуль inspector, FR-DEV-1): постоянные имена,
  модель и подключение, кнопки с именами для макросов (без имени — именами ядра), оси с
  диапазонами, колёса, переключатели, индикаторы, отдача. Устройство и его кнопки без
  стандартного имени можно переименовать (FR-DEV-3): {UnKey001} → {Sega.Start}.
  Props: path — путь устройства ("/dev/input/event6"); сведения загружаются при показе.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { t } from "../../lib/i18n/index.svelte";
  import { errorText } from "../../lib/toast.svelte";
  import type { DeviceControl, DeviceDetails } from "../../lib/types";
  import LabelWizard from "./LabelWizard.svelte";
  import RenameDialog from "./RenameDialog.svelte";

  let { path }: { path: string } = $props();

  /** d — подробности; error — текст ошибки загрузки. */
  let d = $state<DeviceDetails | null>(null);
  let error = $state("");

  /** reload — счётчик перезагрузок (после переименования). */
  let reload = $state(0);

  // Загрузка подробностей (и повторно, если сменилось устройство или его переименовали).
  $effect(() => {
    const ref = path;
    void reload;
    error = "";
    api
      .inspectDevice(ref)
      .then((r) => (d = r.device))
      .catch((e: unknown) => (error = errorText(e)));
  });

  /** renaming — что переименовываем: устройство (control пусто) или кнопку; текущее имя. */
  let renaming = $state<{ control: string; title: string; current: string } | null>(null);

  /** wizard — открыт мастер разметки (FR-DEV-5). */
  let wizard = $state(false);

  /** openRename открывает окно переименования устройства (control "") или кнопки. */
  function openRename(control: string, title: string, current: string): void {
    renaming = { control, title, current };
  }

  /** Кнопки: с именем для макросов, с авто-ID (UnKey001) и без того и другого. */
  const named = $derived((d?.keys ?? []).filter((k) => k.name));
  const labeled = $derived((d?.keys ?? []).filter((k) => !k.name && k.label));
  const unnamed = $derived((d?.keys ?? []).filter((k) => !k.name && !k.label));

  /** Прочие группы: заголовок и коды (показываются именами ядра). */
  const groups = $derived(
    (
      [
        ["inspector.rel", d?.rel],
        ["inspector.switches", d?.switches],
        ["inspector.leds", d?.leds],
        ["inspector.ff", d?.ff],
      ] as [string, DeviceControl[] | undefined][]
    ).filter(([, list]) => list && list.length > 0),
  );

  /** hex4 записывает число четырьмя шестнадцатеричными цифрами (VID, PID, версия). */
  function hex4(n: number): string {
    return n.toString(16).padStart(4, "0");
  }
</script>

{#if error}
  <div class="note error">{error}</div>
{:else if !d}
  <span class="muted">{t("common.loading")}</span>
{:else}
  <!-- Общие сведения -->
  <dl class="facts">
    <dt>{t("inspector.device_name")}</dt>
    <dd class="row">
      {#if d.device_name}<code>{d.device_name}</code>{:else}<span class="muted"
          >{t("inspector.no_name")}</span
        >{/if}
      <button
        class="small ghost"
        onclick={() => openRename("", d?.info.name ?? "", d?.device_name ?? "")}
        >✎ {t("inspector.rename")}</button
      >
    </dd>
    {#if d.auto_id}<dt>{t("inspector.auto_id")}</dt>
      <dd><code>{d.auto_id}</code></dd>{/if}
    <dt>{t("inspector.file")}</dt>
    <dd><code>{d.info.path}</code></dd>
    {#if d.by_id}<dt>{t("inspector.by_id")}</dt>
      <dd><code>{d.by_id}</code></dd>{/if}
    {#if d.by_path}<dt>{t("inspector.by_path")}</dt>
      <dd><code>{d.by_path}</code></dd>{/if}
    <dt>{t("inspector.model")}</dt>
    <dd>
      {t("inspector.model_value", {
        id: `${hex4(d.info.id.vendor)}:${hex4(d.info.id.product)}`,
        version: hex4(d.info.id.version),
        bus: d.bus,
      })}
    </dd>
    {#if d.info.phys}<dt>{t("inspector.phys")}</dt>
      <dd><code>{d.info.phys}</code></dd>{/if}
    {#if d.info.uniq}<dt>{t("inspector.uniq")}</dt>
      <dd><code>{d.info.uniq}</code></dd>{/if}
    {#if d.props?.length}<dt>{t("inspector.props")}</dt>
      <dd>{d.props.join(", ")}</dd>{/if}
  </dl>

  <!-- Кнопки: как их писать в макросах -->
  {#if named.length}
    <h4>{t("inspector.keys", { count: named.length })}</h4>
    <div class="chips">
      {#each named as k (k.code)}<code title={`${k.kernel} (0x${k.code.toString(16)})`}
          >{"{" + k.name + "}"}</code
        >{/each}
    </div>
  {/if}
  {#if labeled.length}
    <h4>{t("inspector.labeled", { count: labeled.length })}</h4>
    <p class="muted hint row">
      {t("inspector.labeled_hint")}
      <button class="small" onclick={() => (wizard = true)}>⌨ {t("inspector.wizard")}</button>
    </p>
    <div class="chips">
      {#each labeled as k (k.code)}<button
          class="chip"
          title={`${k.kernel} (0x${k.code.toString(16)}) — ${t("inspector.rename")}`}
          onclick={() => openRename(k.number ?? "", `{${k.label}}`, k.custom_name ?? "")}
          >{"{" + k.label + "}"}</button
        >{/each}
    </div>
  {/if}
  {#if unnamed.length}
    <h4>{t("inspector.unnamed", { count: unnamed.length })}</h4>
    <p class="muted hint">{t("inspector.unnamed_hint")}</p>
    <div class="chips">
      {#each unnamed as k (k.code)}<code class="muted">{k.kernel}</code>{/each}
    </div>
  {/if}

  <!-- Оси с диапазонами -->
  {#if d.axes?.length}
    <h4>{t("inspector.axes", { count: d.axes.length })}</h4>
    <table>
      <thead>
        <tr>
          <th>{t("inspector.axis_name")}</th>
          <th>{t("inspector.axis_kernel")}</th>
          <th>{t("inspector.axis_range")}</th>
          <th>{t("inspector.axis_flat")}</th>
        </tr>
      </thead>
      <tbody>
        {#each d.axes as a (a.code)}
          <tr>
            <td>{a.name ?? (a.label ? "{" + a.label + "}" : "—")}</td>
            <td><code>{a.kernel}</code></td>
            <td>{a.min} … {a.max}</td>
            <td>{a.flat}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  <!-- Прочее: колёса, переключатели, индикаторы, отдача -->
  {#each groups as [key, list] (key)}
    <h4>{t(key)}</h4>
    <div class="chips">
      {#each list ?? [] as c (c.code)}<code class:muted={!c.label} title={c.kernel}
          >{c.label ? "{" + c.label + "}" : c.kernel}</code
        >{/each}
    </div>
  {/each}
{/if}

{#if wizard && d}
  <LabelWizard
    device={d}
    onclose={() => {
      wizard = false;
      reload++;
    }}
    onsaved={() => {
      wizard = false;
      reload++;
    }}
  />
{/if}

{#if renaming && d}
  <RenameDialog
    device={d.auto_id || d.info.path}
    control={renaming.control}
    title={renaming.title}
    current={renaming.current}
    onclose={() => (renaming = null)}
    ondone={() => {
      renaming = null;
      reload++;
    }}
  />
{/if}

<style>
  .chip {
    font-family: var(--mono);
    font-size: 0.85rem;
    padding: 1px 6px;
    border-radius: 6px;
    background: var(--surface-2);
    border: 1px solid transparent;
    cursor: pointer;
  }
  .chip:hover {
    border-color: var(--accent);
  }
  .facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 4px 12px;
    margin: 8px 0;
  }
  dt {
    color: var(--muted);
  }
  dd {
    margin: 0;
    word-break: break-all;
  }
  h4 {
    margin: 12px 0 6px;
    font-size: 0.9rem;
  }
  .hint {
    margin: 0 0 6px;
    font-size: 0.85rem;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }
  .chips code {
    padding: 1px 6px;
    border-radius: 6px;
    background: var(--surface-2);
  }
  table {
    border-collapse: collapse;
    font-size: 0.9rem;
  }
  th,
  td {
    text-align: left;
    padding: 2px 12px 2px 0;
  }
  th {
    color: var(--muted);
    font-weight: normal;
  }
</style>
