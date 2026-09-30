<!--
  SettingsPage — настройки (FR-UI-1.8): язык, тема оформления, системные сочетания mKey
  (запись, экстренная остановка) и удаление программы (с сохранением настроек или полностью, FR-INST-5).
  Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import KeyCapture from "../../lib/components/KeyCapture.svelte";
  import Modal from "../../lib/components/Modal.svelte";
  import { lang, setLang, t } from "../../lib/i18n/index.svelte";
  import { LANGS, type Lang } from "../../lib/i18n/translate";
  import { setTheme, theme, type Theme } from "../../lib/theme.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";

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

<div class="card grid">
  <!-- Язык интерфейса -->
  <label for="lang">{t("settings.language")}</label>
  <select id="lang" value={lang()} onchange={(e) => setLang(e.currentTarget.value as Lang)}>
    {#each LANGS as l (l)}<option value={l}>{t("settings.lang." + l)}</option>{/each}
  </select>

  <!-- Тема оформления -->
  <label for="theme">{t("settings.theme")}</label>
  <select id="theme" value={theme()} onchange={(e) => setTheme(e.currentTarget.value as Theme)}>
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
