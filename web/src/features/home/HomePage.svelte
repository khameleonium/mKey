<!--
  HomePage — главная (FR-UI-1.1): работает ли mKey, экстренная остановка и возобновление,
  предупреждения диагностики, включённые события с индикатором «выполняется» и кнопкой «Стоп».
  Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { t } from "../../lib/i18n/index.svelte";
  import { href } from "../../lib/router.svelte";
  import { live, onTopic, refreshStatus } from "../../lib/stream.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { DoctorCheck, EventStatus, ProjectInfo, Status } from "../../lib/types";

  /** Данные страницы. */
  let status = $state<Status | null>(null);
  let problems = $state<DoctorCheck[]>([]);
  let events = $state<EventStatus[]>([]);
  let projects = $state<ProjectInfo[]>([]);

  /** load перечитывает состояние, проблемы, события и проекты. */
  async function load(): Promise<void> {
    try {
      status = await api.status();
      const [doc, ev, pr] = await Promise.all([api.doctor(), api.events(), api.projects()]);
      problems = doc.checks.filter((c) => c.status === "fail" || c.status === "warn");
      events = (ev.events ?? []).filter((e) => e.enabled);
      projects = pr.projects;
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  // Загрузка при открытии и при изменениях проектов, устройств и экстренной остановке.
  $effect(() => {
    void load();
    const offs = [
      "store.projects_changed",
      "input.access_changed",
      "input.emergency",
      "input.resumed",
    ].map((topic) => onTopic(topic, () => void load()));
    return () => offs.forEach((off) => off());
  });

  /** projectName возвращает название проекта по ID. */
  function projectName(id: string): string {
    return projects.find((p) => p.id === id)?.name || id;
  }

  /** Сколько выполнений идёт сейчас по всем событиям. */
  let runningTotal = $derived(Object.values(live.running).reduce((a, b) => a + b, 0));

  /** emergency — экстренная остановка из окна. */
  async function emergency(): Promise<void> {
    await api.emergency().catch((e: unknown) => toast(errorText(e), "error"));
    await refreshStatus();
  }

  /** resume — продолжить работу после экстренной остановки. */
  async function resume(): Promise<void> {
    await api.resume().catch((e: unknown) => toast(errorText(e), "error"));
    await refreshStatus();
  }
</script>

<h1>{t("home.title")}</h1>

<!-- Состояние и главные кнопки -->
<div class="card state" class:paused={live.suspended}>
  {#if live.suspended}
    <div>
      <h2>⏸ {t("home.paused")}</h2>
      <p>{t("home.paused_text")}</p>
    </div>
    <button class="primary" onclick={resume}>{t("home.resume")}</button>
  {:else}
    <div>
      <h2>● {t("home.running")}</h2>
      <p class="muted">{t("home.running_text")}</p>
    </div>
    <button class="danger" onclick={emergency} title={t("home.emergency_hint")}
      >■ {t("home.emergency")}</button
    >
  {/if}
</div>

<!-- Проблемы диагностики -->
{#if problems.length}
  <div class="note warn problems">
    <b>{t("home.problems")}</b>
    <ul>
      {#each problems as p (p.id)}<li>{p.message}</li>{/each}
    </ul>
    <a href={href("diagnostics")}>{t("home.open_diagnostics")}</a>
  </div>
{/if}

<!-- Включённые события -->
<div class="card">
  <div class="row">
    <h2>{t("home.events")}</h2>
    <span class="spacer"></span>
    {#if runningTotal > 0}
      <button
        onclick={async () => {
          await api.stopAll();
          toast(t("home.stopped"));
        }}>■ {t("home.stop_all")}</button
      >
    {/if}
  </div>
  {#if events.length === 0}
    <p class="muted">
      {t("home.no_events")} <a href={href("projects")}>{t("home.go_projects")}</a>
    </p>
  {:else}
    <ul class="events">
      {#each events as e (e.project + "/" + e.event)}
        {@const n = live.running[`${e.project}/${e.event}`] ?? 0}
        <li class:active={n > 0}>
          <span class="dot" aria-hidden="true">{n > 0 ? "●" : "○"}</span>
          <a href={href("editor", e.project)}>{e.name || e.event}</a>
          <span class="muted">— {projectName(e.project)}</span>
          {#if e.toggled}<span class="tag">{t("home.toggled_on")}</span>{/if}
          {#if n > 0}<span class="tag run">{t("editor.running")}</span>{/if}
        </li>
      {/each}
    </ul>
  {/if}
</div>

{#if status}
  <p class="muted small">{t("home.version", { version: status.version })}</p>
{/if}
<!-- Лицензия и поддержка автора (ADR-0043) -->
<p class="muted small">
  {t("home.license")}
  <a
    href="https://github.com/khameleonium/mKey/blob/main/COMMERCIAL.md"
    target="_blank"
    rel="noopener">{t("home.license_more")}</a
  >
</p>
<p class="small">
  <a href="https://boosty.to/khameleonium" target="_blank" rel="noopener">♥ {t("app.support")}</a>
  — {t("home.support_hint")}
</p>

<style>
  .state {
    display: flex;
    align-items: center;
    gap: 16px;
    justify-content: space-between;
    border-left: 6px solid var(--ok);
    margin-bottom: 16px;
  }
  .state.paused {
    border-left-color: var(--danger);
  }
  .state h2 {
    margin: 0;
  }
  .state p {
    margin: 4px 0 0;
  }
  .state button {
    font-size: 1.05rem;
    padding: 10px 18px;
  }
  .problems {
    margin-bottom: 16px;
  }
  .problems ul {
    margin: 6px 0;
  }
  .events {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  .events li {
    padding: 6px 0;
    border-bottom: 1px solid var(--surface-2);
    display: flex;
    gap: 8px;
    align-items: center;
    flex-wrap: wrap;
  }
  .dot {
    color: var(--muted);
  }
  .active .dot {
    color: var(--ok);
  }
  .tag {
    font-size: 0.8rem;
    padding: 1px 8px;
    border-radius: 10px;
    background: var(--surface-2);
  }
  .tag.run {
    background: var(--ok-soft);
    color: var(--ok);
  }
  .small {
    font-size: 0.85rem;
  }
</style>
