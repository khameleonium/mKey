<!--
  SettingsPage — настройки (FR-UI-1.8): язык, тема оформления, системные сочетания mKey
  (запись, экстренная остановка), настройки записи по умолчанию и удаление программы
  (с сохранением настроек или полностью, FR-INST-5). Всё, кроме удаления, хранится в config.yaml —
  его можно править и в текстовом редакторе. Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { comboText } from "../../lib/combo";
  import KeyCapture from "../../lib/components/KeyCapture.svelte";
  import Modal from "../../lib/components/Modal.svelte";
  import PlacesList from "../../lib/components/PlacesList.svelte";
  import { lang, setLang, t } from "../../lib/i18n/index.svelte";
  import { LANGS, type Lang } from "../../lib/i18n/translate";
  import { setTheme, theme, type Theme } from "../../lib/theme.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { RecordSettings } from "../../lib/types";

  /** KINDS — устройства, которые можно записывать (порядок — как в списке). */
  const KINDS = [
    "keyboard",
    "mouse",
    "touchpad",
    "touchscreen",
    "tablet",
    "gamepad",
    "joystick",
    "other",
  ];

  /** saveInterface запоминает язык и тему в config.yaml (их же видят команды и меню значка). */
  function saveInterface(): void {
    api
      .setInterfaceSettings({ language: lang(), theme: theme() })
      .catch((e: unknown) => toast(errorText(e), "error"));
  }

  /** Настройки записи: rec — загруженные (null — ещё нет или запись недоступна); recError — ошибка. */
  let rec = $state<RecordSettings | null>(null);
  let recError = $state("");

  // Загружаем настройки записи при открытии.
  $effect(() => {
    api
      .recordSettings()
      .then((s) => (rec = s))
      .catch((e: unknown) => (recError = errorText(e)));
  });

  /** toggleKind включает или выключает запись устройств вида kind. */
  function toggleKind(kind: string, on: boolean): void {
    if (!rec) return;
    rec.kinds = on ? [...rec.kinds, kind] : rec.kinds.filter((k) => k !== kind);
  }

  /** saveRecord проверяет и сохраняет настройки записи (действуют со следующей записи). */
  async function saveRecord(): Promise<void> {
    if (!rec) return;
    recError = "";
    try {
      rec = await api.setRecordSettings(rec);
      toast(t("settings.rec_saved"));
    } catch (e) {
      recError = errorText(e);
    }
  }

  /** Системные сочетания: record — запись (пусто — выключено), emergency — экстренная остановка. */
  let record = $state("");
  let emergency = $state("");
  let hkError = $state("");

  // Загружаем сочетания при открытии.
  $effect(() => {
    api
      .hotkeys()
      .then((h) => {
        record = h.record ?? "";
        emergency = h.emergency ?? "";
      })
      .catch((e: unknown) => (hkError = errorText(e)));
  });

  /** saveHotkeys проверяет и сохраняет сочетания (действуют сразу). */
  async function saveHotkeys(): Promise<void> {
    hkError = "";
    try {
      const h = await api.setHotkeys({ record, emergency });
      record = h.record ?? "";
      emergency = h.emergency ?? "";
      toast(t("settings.hotkeys_saved"));
    } catch (e) {
      hkError = errorText(e);
    }
  }

  /** Окно удаления: открыто, что сохранить, идёт удаление, удаление запущено. */
  let removing = $state(false);
  let keepConfig = $state(true);
  let busy = $state(false);
  let started = $state(false);

  /** uninstall запускает удаление mKey. */
  async function uninstall(): Promise<void> {
    busy = true;
    try {
      await api.uninstall(keepConfig);
      started = true;
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      busy = false;
    }
  }
</script>

<h1>{t("settings.title")}</h1>
<p class="muted">{t("settings.file_hint")}</p>

<div class="card grid">
  <!-- Язык интерфейса -->
  <label for="lang">{t("settings.language")}</label>
  <select
    id="lang"
    value={lang()}
    onchange={(e) => {
      setLang(e.currentTarget.value as Lang);
      saveInterface();
    }}
  >
    {#each LANGS as l (l)}<option value={l}>{t("settings.lang." + l)}</option>{/each}
  </select>

  <!-- Тема оформления -->
  <label for="theme">{t("settings.theme")}</label>
  <select
    id="theme"
    value={theme()}
    onchange={(e) => {
      setTheme(e.currentTarget.value as Theme);
      saveInterface();
    }}
  >
    <option value="system">{t("settings.theme.system")}</option>
    <option value="light">{t("settings.theme.light")}</option>
    <option value="dark">{t("settings.theme.dark")}</option>
  </select>
</div>

<!-- Системные сочетания mKey -->
<div class="card hotkeys">
  <h2>{t("settings.hotkeys")}</h2>
  <p class="muted">{t("settings.hotkeys_hint")}</p>
  <div class="grid">
    <span>{t("settings.hotkey_record")}</span>
    <span class="row">
      <KeyCapture value={record} combo onchange={(v) => (record = v)} />
      {#if record}
        <span class="muted">{comboText(record, t)}</span>
        <button class="small ghost" onclick={() => (record = "")}>{t("settings.hotkey_off")}</button
        >
      {:else}
        <span class="muted">{t("settings.hotkey_is_off")}</span>
      {/if}
    </span>
    <span>{t("settings.hotkey_emergency")}</span>
    <KeyCapture value={emergency} combo onchange={(v) => (emergency = v)} />
  </div>
  {#if hkError}<div class="note error">{hkError}</div>{/if}
  <button class="primary" onclick={saveHotkeys}>{t("common.save")}</button>
</div>

<!-- Запись действий: с какими настройками запись начинается без вопросов -->
<div class="card hotkeys">
  <h2>{t("settings.rec")}</h2>
  <p class="muted">{t("settings.rec_hint")}</p>
  {#if rec}
    <div class="grid">
      <span>{t("settings.rec_kinds")}</span>
      <span class="kinds">
        {#each KINDS as k (k)}
          <label class="check"
            ><input
              type="checkbox"
              checked={rec.kinds.includes(k)}
              onchange={(e) => toggleKind(k, e.currentTarget.checked)}
            />
            {t("devices.kind." + k)}</label
          >
        {/each}
      </span>
      <span>{t("settings.rec_moves")}</span>
      <label class="check"
        ><input type="checkbox" bind:checked={rec.moves} />
        <span class="muted">{t("settings.rec_moves_hint")}</span></label
      >
      <span>{t("settings.rec_merge")}</span>
      <label class="check"
        ><input
          type="number"
          min="0"
          max="1000"
          step="1"
          disabled={!rec.moves}
          bind:value={rec.merge_moves_ms}
        />
        <span class="muted">{t("settings.rec_merge_hint")}</span></label
      >
      <span>{t("settings.rec_center")}</span>
      <label class="check"
        ><input type="checkbox" bind:checked={rec.center_pointer} />
        <span class="muted">{t("settings.rec_center_hint")}</span></label
      >
      <span>{t("settings.rec_coalesce")}</span>
      <label class="check"
        ><input type="number" min="0" max="1000" step="1" bind:value={rec.coalesce_ms} />
        <span class="muted">{t("settings.rec_coalesce_hint")}</span></label
      >
    </div>
  {/if}
  {#if recError}<div class="note error">{recError}</div>{/if}
  {#if rec}<button class="primary" onclick={saveRecord}>{t("common.save")}</button>{/if}
</div>

<!-- Где что лежит: папки и файлы mKey -->
<div class="card places">
  <h2>{t("places.title")}</h2>
  <p class="muted">{t("places.hint")}</p>
  <PlacesList />
</div>

<!-- Удаление программы -->
<div class="card danger-zone">
  <h2>{t("settings.uninstall")}</h2>
  <p class="muted">{t("settings.uninstall_hint")}</p>
  <button class="danger" onclick={() => (removing = true)}>{t("settings.uninstall_button")}</button>
</div>

{#if removing}
  <Modal title={t("settings.uninstall")} onclose={() => (removing = false)}>
    {#if started}
      <div class="note ok">{t("settings.uninstall_started")}</div>
    {:else}
      <p>{t("settings.uninstall_what")}</p>
      <label class="opt">
        <input type="radio" name="keep" checked={keepConfig} onchange={() => (keepConfig = true)} />
        <span
          ><b>{t("settings.keep_config")}</b><br /><span class="muted"
            >{t("settings.keep_config_hint")}</span
          ></span
        >
      </label>
      <label class="opt">
        <input
          type="radio"
          name="keep"
          checked={!keepConfig}
          onchange={() => (keepConfig = false)}
        />
        <span
          ><b>{t("settings.remove_all")}</b><br /><span class="muted"
            >{t("settings.remove_all_hint")}</span
          ></span
        >
      </label>
      <p class="muted">{t("settings.uninstall_password")}</p>
    {/if}
    {#snippet footer()}
      {#if started}
        <button onclick={() => (removing = false)}>{t("common.close")}</button>
      {:else}
        <button onclick={() => (removing = false)}>{t("common.cancel")}</button>
        <button class="danger" disabled={busy} onclick={uninstall}
          >{t("settings.uninstall_confirm")}</button
        >
      {/if}
    {/snippet}
  </Modal>
{/if}

<style>
  .grid {
    display: grid;
    grid-template-columns: max-content minmax(0, 260px);
    gap: 12px 16px;
    align-items: center;
    margin-bottom: 16px;
  }
  .hotkeys {
    margin-bottom: 16px;
    display: flex;
    flex-direction: column;
    gap: 10px;
    align-items: flex-start;
  }
  .hotkeys h2,
  .hotkeys p {
    margin: 0;
  }
  .hotkeys .grid {
    margin: 0;
    grid-template-columns: max-content minmax(0, 1fr);
  }
  .kinds {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 14px;
  }
  .check {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .check input[type="number"] {
    width: 6em;
  }
  .danger-zone {
    border-left: 6px solid var(--danger);
  }
  .opt {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    margin: 10px 0;
    cursor: pointer;
  }
  .opt input {
    margin-top: 4px;
  }
</style>
