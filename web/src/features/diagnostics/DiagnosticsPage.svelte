<!--
  DiagnosticsPage — диагностика (FR-UI-1.9): проверки простым языком с кнопкой «Исправить»
  (выдать доступ к устройствам — система спросит пароль), состояние частей программы
  и журнал работы mKey.
  Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { t } from "../../lib/i18n/index.svelte";
  import { href } from "../../lib/router.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { DoctorCheck, ModuleStatus } from "../../lib/types";

  /** Данные страницы. */
  let checks = $state<DoctorCheck[]>([]);
  let modules = $state<ModuleStatus[]>([]);
  let logs = $state<string[] | null>(null);
  /** fixing — идёт исправление; manual — команда, которую нужно выполнить вручную. */
  let fixing = $state(false);
  let manual = $state("");

  /** load выполняет проверки и читает состояние модулей. */
  async function load(): Promise<void> {
    try {
      checks = (await api.doctor()).checks;
      modules = (await api.status()).modules ?? [];
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  $effect(() => {
    void load();
  });

  /** fix выдаёт доступ к устройствам (окно ввода пароля системы) и повторяет проверки. */
  async function fix(): Promise<void> {
    fixing = true;
    manual = "";
    try {
      const r = await api.doctorFix();
      if (r.manual_command) manual = r.manual_command;
      else toast(t("diag.fixed"));
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      fixing = false;
    }
    await load();
  }

  /** loadLogs показывает последние строки журнала. */
  async function loadLogs(): Promise<void> {
    try {
      logs = (await api.logs(300)).lines;
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** Значки состояний проверок. */
  const ICONS: Record<string, string> = { ok: "✔", info: "ℹ", warn: "⚠", fail: "✖" };
  /** Кнопка «Исправить» нужна, если у любой непройденной проверки есть исправление
      (в том числе «доступна только часть устройств» — это предупреждение, а не ошибка). */
  let hasFix = $derived(checks.some((c) => c.fix && c.status !== "ok" && c.status !== "info"));
</script>

<div class="row">
  <h1>{t("diag.title")}</h1>
  <span class="spacer"></span>
  <a class="btn" href={href("setup")}>{t("diag.wizard")}</a>
  <button onclick={load}>↻ {t("diag.recheck")}</button>
</div>

<!-- Проверки -->
<div class="card">
  <ul class="checks">
    {#each checks as c, i (i)}
      <li class={c.status}><span class="icon">{ICONS[c.status]}</span> {c.message}</li>
    {/each}
  </ul>
  {#if hasFix}
    <div class="row fix">
      <button class="primary" disabled={fixing} onclick={fix}>🔧 {t("diag.fix")}</button>
      <span class="muted">{t("diag.fix_hint")}</span>
    </div>
  {/if}
  {#if manual}
    <div class="note warn">
      {t("diag.manual")}
      <pre>{manual}</pre>
    </div>
  {/if}
</div>

<!-- Части программы -->
<details class="card section">
  <summary>{t("diag.modules")}</summary>
  <ul class="modules">
    {#each modules as m (m.id)}
      <li>
        <code>{m.id}</code> — {t("diag.state." + m.state)}
        {#if m.error}<span class="err">{m.error}</span>{/if}
      </li>
    {/each}
  </ul>
</details>

<!-- Журнал -->
<details
  class="card section"
  ontoggle={(e) => (e.currentTarget.open && logs === null ? void loadLogs() : null)}
>
  <summary>{t("diag.logs")}</summary>
  <div class="row">
    <span class="muted">{t("diag.logs_hint")}</span>
    <span class="spacer"></span>
    <button class="small" onclick={loadLogs}>↻</button>
  </div>
  <pre class="logs">{(logs ?? []).join("\n")}</pre>
</details>

<style>
  .checks {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  .checks li {
    padding: 6px 0;
    border-bottom: 1px solid var(--surface-2);
  }
  .icon {
    display: inline-block;
    width: 1.4em;
  }
  .ok .icon {
    color: var(--ok);
  }
  .warn .icon {
    color: var(--warn);
  }
  .fail .icon {
    color: var(--danger);
  }
  .info .icon {
    color: var(--accent);
  }
  .fix {
    margin-top: 12px;
  }
  .section {
    margin-top: 14px;
  }
  summary {
    cursor: pointer;
    font-weight: 600;
  }
  .modules {
    margin: 8px 0 0;
  }
  .err {
    color: var(--danger);
    margin-left: 6px;
  }
  .logs {
    max-height: 420px;
    overflow: auto;
    background: var(--surface-2);
    padding: 8px;
    border-radius: var(--radius-s);
    font-size: 0.8rem;
    white-space: pre-wrap;
    word-break: break-all;
  }
  pre {
    user-select: all;
  }
</style>
