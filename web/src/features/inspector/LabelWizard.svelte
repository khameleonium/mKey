<!--
  LabelWizard — мастер разметки (FR-DEV-5): «нажимайте по очереди кнопки устройства» — mKey
  показывает каждую нажатую кнопку (подсвечивает её на миг) и предлагает сразу дать ей имя;
  кнопку можно пропустить — останется авто-ID. Имена сохраняются разом; проект, где они
  используются, при сохранении в файл унесёт их с собой (ADR-0027).
  Props: device — подробности устройства (с авто-ID); onclose() — закрыть; onsaved() — имена сохранены.
-->
<script lang="ts">
  import { onDestroy } from "svelte";
  import { api } from "../../lib/api";
  import Modal from "../../lib/components/Modal.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { DeviceControl, DeviceDetails, WatchEntry } from "../../lib/types";

  let {
    device,
    onclose,
    onsaved,
  }: { device: DeviceDetails; onclose: () => void; onsaved: () => void } = $props();

  /** Строка мастера: кнопка или ось, которую нажали. */
  interface Row {
    /** key — «вид:код» (кнопка и ось с одним кодом — разные строки). */
    key: string;
    control: DeviceControl;
    /** name — вводимое имя; error — ошибка сохранения этого имени; flash — только что нажата. */
    name: string;
    error: string;
    flash: boolean;
  }

  /** rows — нажатые кнопки по порядку первого нажатия; saving — идёт сохранение. */
  let rows = $state<Row[]>([]);
  let saving = $state(false);

  /** controlOf находит кнопку или ось устройства по виду записи монитора и коду. */
  function controlOf(e: WatchEntry): DeviceControl | undefined {
    switch (e.kind) {
      case "key":
        return device.keys?.find((k) => k.code === e.code);
      case "axis":
        return device.axes?.find((a) => a.code === e.code);
      case "wheel":
        return device.rel?.find((r) => r.code === e.code);
    }
    return undefined;
  }

  /** press отмечает нажатие: новая кнопка — новая строка, известная — подсветка. */
  function press(e: WatchEntry): void {
    const control = controlOf(e);
    if (!control) return;
    const key = `${e.kind}:${e.code}`;
    let i = rows.findIndex((r) => r.key === key);
    if (i < 0) {
      rows.push({ key, control, name: control.custom_name ?? "", error: "", flash: false });
      i = rows.length - 1;
    }
    const row = rows[i];
    if (!row) return;
    row.flash = true;
    setTimeout(() => (row.flash = false), 600);
  }

  // Поток нажатий: только этого устройства, нажатия кнопок и движения осей.
  const source = new EventSource("/api/v1/input/watch");
  source.addEventListener("input", (m: MessageEvent<string>) => {
    const e = JSON.parse(m.data) as WatchEntry;
    if (e.device === device.info.path && (e.kind !== "key" || e.action === "down")) press(e);
  });
  source.onerror = () => toast(t("devices.watch_lost"), "error");
  onDestroy(() => source.close());

  /** save сохраняет изменённые имена по одному; ошибки — у своих строк. */
  async function save(): Promise<void> {
    saving = true;
    let failed = false;
    for (const r of rows) {
      const number = r.control.number;
      if (!number || r.name.trim() === (r.control.custom_name ?? "")) continue;
      try {
        await api.renameDevice(device.auto_id ?? device.info.path, number, r.name.trim());
        r.control.custom_name = r.name.trim();
        r.error = "";
      } catch (e) {
        r.error = errorText(e);
        failed = true;
      }
    }
    saving = false;
    if (!failed) {
      toast(t("inspector.wizard_saved"));
      onsaved();
    }
  }
</script>

<Modal
  title={t("inspector.wizard_title", { device: device.device_name || device.info.name })}
  wide
  {onclose}
>
  <p>{t("inspector.wizard_hint")}</p>
  {#if rows.length === 0}
    <div class="note">{t("inspector.wizard_waiting")}</div>
  {/if}
  <div class="rows">
    {#each rows as r (r.key)}
      <div class="row-item" class:flash={r.flash}>
        <code class="what" title={r.control.kernel}
          >{r.control.label
            ? `{${r.control.label}}`
            : r.control.name
              ? `{${r.control.name}}`
              : r.control.kernel}</code
        >
        {#if r.control.number}
          <input
            placeholder={t("inspector.wizard_placeholder")}
            bind:value={r.name}
            onkeydown={(e) => e.key === "Enter" && void save()}
          />
        {:else}
          <span class="muted">{t("inspector.wizard_has_name")}</span>
        {/if}
        <span class="muted kernel">{r.control.kernel}</span>
        {#if r.error}<div class="note error full">{r.error}</div>{/if}
      </div>
    {/each}
  </div>
  {#snippet footer()}
    <button onclick={onclose}>{t("common.close")}</button>
    <button class="primary" disabled={saving || rows.length === 0} onclick={() => void save()}
      >{t("inspector.wizard_save")}</button
    >
  {/snippet}
</Modal>

<style>
  .rows {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 10px;
  }
  .row-item {
    display: grid;
    grid-template-columns: minmax(10em, 1fr) minmax(10em, 1.2fr) minmax(8em, 1fr);
    gap: 8px;
    align-items: center;
    padding: 4px 8px;
    border-radius: 6px;
    transition: background 0.3s;
  }
  /* Только что нажатая кнопка подсвечивается. */
  .row-item.flash {
    background: var(--accent-soft);
  }
  .what {
    font-family: var(--mono);
  }
  .kernel {
    font-size: 0.85em;
  }
  .full {
    grid-column: 1 / -1;
  }
</style>
