<!--
  UpdateCard — карточка «Обновления» в настройках (ADR-0030): установленная версия, проверка
  по кнопке, установка новой версии (mKey перезапускается) и галочка «проверять раз в сутки»
  (сохраняется в config.yaml). Если mKey установлен пакетом или это сборка разработчика —
  объясняет, почему обновить сам он не может. Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { t } from "../../lib/i18n/index.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { UpdateInfo } from "../../lib/types";

  /** info — сведения (null — модуль обновлений выключен); busy — идёт проверка или установка. */
  let info = $state<UpdateInfo | null>(null);
  let busy = $state<"" | "check" | "apply">("");

  // Сведения без обращения к сети — при открытии.
  $effect(() => {
    api
      .updateInfo()
      .then((i) => (info = i))
      .catch(() => (info = null));
  });

  /** check спрашивает последний выпуск. */
  async function check(): Promise<void> {
    busy = "check";
    try {
      info = await api.checkUpdate();
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      busy = "";
    }
  }

  /** apply устанавливает новую версию; mKey перезапускается, окно переподключается само. */
  async function apply(): Promise<void> {
    busy = "apply";
    try {
      const i = await api.applyUpdate();
      toast(t("update.done", { version: i.latest ?? "" }));
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      busy = "";
    }
  }

  /** setAuto включает или выключает проверку раз в сутки. */
  async function setAuto(on: boolean): Promise<void> {
    try {
      info = await api.setUpdateCheck(on);
    } catch (e) {
      toast(errorText(e), "error");
    }
  }
</script>

{#if info}
  <div class="card update">
    <h2>{t("update.title")}</h2>
    <p>
      {t("update.version", { version: info.current })}
      {#if info.latest && !info.available}{t("update.latest", { version: info.latest })}
        {t("update.none")}{/if}
    </p>
    {#if info.available && info.latest}
      <div class="note ok">
        {t("update.new", { version: info.latest })}
        {#if info.url}<a href={info.url} target="_blank" rel="noreferrer">{t("update.whats_new")}</a
          >{/if}
      </div>
    {/if}
    {#if info.reason}<p class="muted">{t("update.reason." + info.reason)}</p>{/if}
    <div class="row">
      <button disabled={busy !== ""} onclick={() => void check()}
        >{busy === "check" ? t("update.checking") : t("update.check_now")}</button
      >
      {#if info.available && info.can_apply && info.latest}
        <button class="primary" disabled={busy !== ""} onclick={() => void apply()}
          >{busy === "apply"
            ? t("update.installing")
            : t("update.install", { version: info.latest })}</button
        >
      {/if}
    </div>
    <label class="check"
      ><input
        type="checkbox"
        checked={info.check}
        onchange={(e) => void setAuto(e.currentTarget.checked)}
      />
      <span>{t("update.auto")}<br /><span class="muted">{t("update.auto_hint")}</span></span></label
    >
  </div>
{/if}

<style>
  .update {
    margin-bottom: 16px;
  }
  .row {
    display: flex;
    gap: 8px;
    margin: 8px 0;
  }
  .check {
    display: flex;
    gap: 10px;
    align-items: flex-start;
  }
  .check input {
    margin-top: 4px;
  }
</style>
