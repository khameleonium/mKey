<!--
  SetupWizard — мастер первого запуска в окне (FR-INST-2, дополняет `mkey setup`):
  1) что будет сделано, 2) доступ к устройствам (кнопка «Выдать доступ» — система спросит
  пароль), 3) проверка: нажатие клавиши и печать цифр, 4) что делать дальше.
  Установку программы и автозапуск выполняет `mkey setup`; мастер проверяет готовность.
  Props: нет.
-->
<script lang="ts">
  import { api, ApiError } from "../../lib/api";
  import { t } from "../../lib/i18n/index.svelte";
  import { href, navigate } from "../../lib/router.svelte";
  import { errorText } from "../../lib/toast.svelte";
  import type { DoctorCheck } from "../../lib/types";

  /** step — текущий шаг (1–4). */
  let step = $state(1);

  // ---- Шаг 2: доступ ----
  /** access — проверки доступа к устройствам; fixing — идёт исправление; manual — команда вручную. */
  let access = $state<DoctorCheck[]>([]);
  let fixing = $state(false);
  let manual = $state("");
  let error = $state("");
  let accessOK = $derived(access.length > 0 && access.every((c) => c.status !== "fail"));
  /** needFix — есть непройденная проверка, которую можно исправить (и ошибка, и «только часть устройств»). */
  let needFix = $derived(access.some((c) => c.fix && c.status !== "ok" && c.status !== "info"));

  /** checkAccess выполняет проверки доступа к устройствам ввода и вывода. */
  async function checkAccess(): Promise<void> {
    error = "";
    try {
      access = (await api.doctor()).checks.filter(
        (c) => c.id === "uinput" || c.id === "input_devices",
      );
    } catch (e) {
      error = errorText(e);
    }
  }

  /** fix выдаёт доступ к устройствам и повторяет проверку. */
  async function fix(): Promise<void> {
    fixing = true;
    manual = "";
    try {
      const r = await api.doctorFix();
      if (r.manual_command) manual = r.manual_command;
    } catch (e) {
      error = errorText(e);
    } finally {
      fixing = false;
    }
    await checkAccess();
  }

  // ---- Шаг 3: проверка ----
  /** pressed — имя нажатой клавиши; waiting — ждём нажатия; typed — текст в поле проверки вывода. */
  let pressed = $state("");
  let waiting = $state(false);
  let typed = $state("");
  let testInput: HTMLInputElement | undefined = $state();

  /** testPress ждёт нажатия любой клавиши. */
  async function testPress(): Promise<void> {
    waiting = true;
    error = "";
    try {
      pressed = (await api.captureKey(false, 30000)).name;
    } catch (e) {
      error = e instanceof ApiError && e.status === 408 ? t("key.timeout") : errorText(e);
    } finally {
      waiting = false;
    }
  }

  /** testType ставит курсор в поле и просит mKey напечатать цифры. */
  async function testType(): Promise<void> {
    typed = "";
    error = "";
    testInput?.focus();
    try {
      await api.send('[300]{"12345"}');
    } catch (e) {
      error = errorText(e);
    }
  }

  /** finish запоминает, что мастер пройден. */
  function finish(to: string): void {
    try {
      localStorage.setItem("mkey.setup_done", "1");
    } catch {
      // Не удалось запомнить — мастер может показаться ещё раз, это не страшно.
    }
    navigate(to);
  }

  // Переход на шаг 2 запускает проверку доступа.
  $effect(() => {
    if (step === 2) void checkAccess();
  });
</script>

<h1>{t("setup.title")}</h1>
<ol class="steps">
  {#each [1, 2, 3, 4] as n (n)}
    <li class:active={step === n} class:done={step > n}>{t("setup.step." + n)}</li>
  {/each}
</ol>

<div class="card">
  {#if step === 1}
    <h2>{t("setup.welcome")}</h2>
    <p>{t("setup.welcome_text")}</p>
    <ul>
      <li>{t("setup.welcome_1")}</li>
      <li>{t("setup.welcome_2")}</li>
      <li>{t("setup.welcome_3")}</li>
    </ul>
    <p class="muted">{t("setup.welcome_safety")}</p>
    <div class="row">
      <span class="spacer"></span><button class="primary" onclick={() => (step = 2)}
        >{t("common.next")}</button
      >
    </div>
  {:else if step === 2}
    <h2>{t("setup.access")}</h2>
    <p>{t("setup.access_text")}</p>
    <ul class="checks">
      {#each access as c, i (i)}
        <li class={c.status}>
          {c.status === "ok" ? "✔" : c.status === "fail" ? "✖" : "⚠"}
          {c.message}
        </li>
      {/each}
    </ul>
    {#if needFix}
      <button class="primary" disabled={fixing} onclick={fix}>🔧 {t("setup.grant")}</button>
      <p class="muted">{t("setup.grant_hint")}</p>
    {/if}
    {#if manual}<div class="note warn">
        {t("diag.manual")}
        <pre>{manual}</pre>
      </div>{/if}
    <div class="row">
      <button onclick={() => (step = 1)}>{t("common.back")}</button>
      <span class="spacer"></span>
      <button onclick={checkAccess}>↻ {t("diag.recheck")}</button>
      <button class="primary" disabled={!accessOK} onclick={() => (step = 3)}
        >{t("common.next")}</button
      >
    </div>
  {:else if step === 3}
    <h2>{t("setup.test")}</h2>
    <p>{t("setup.test_press")}</p>
    <div class="row">
      <button disabled={waiting} onclick={testPress}
        >{waiting ? t("key.waiting") : t("setup.test_press_button")}</button
      >
      {#if pressed}<span class="good">✔ {t("setup.test_pressed", { key: pressed })}</span>{/if}
    </div>
    <p>{t("setup.test_type")}</p>
    <div class="row">
      <button onclick={testType}>{t("setup.test_type_button")}</button>
      <input bind:this={testInput} bind:value={typed} placeholder="…" />
      {#if typed.includes("12345")}<span class="good">✔ {t("setup.test_typed")}</span>{/if}
    </div>
    <div class="row">
      <button onclick={() => (step = 2)}>{t("common.back")}</button>
      <span class="spacer"></span>
      <button class="primary" onclick={() => (step = 4)}>{t("common.next")}</button>
    </div>
  {:else}
    <h2>{t("setup.done")}</h2>
    <p>{t("setup.done_text")}</p>
    <div class="row">
      <button class="primary" onclick={() => finish("projects")}>{t("setup.go_projects")}</button>
      <button onclick={() => finish("home")}>{t("setup.go_home")}</button>
    </div>
    <p class="muted">{t("setup.autostart_hint")}</p>
  {/if}
  {#if error}<div class="note error">{error}</div>{/if}
</div>
<p class="muted"><a href={href("home")} onclick={() => finish("home")}>{t("setup.skip")}</a></p>

<style>
  .steps {
    display: flex;
    gap: 8px;
    list-style: none;
    padding: 0;
    margin: 0 0 14px;
    flex-wrap: wrap;
  }
  .steps li {
    padding: 4px 12px;
    border-radius: 14px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 0.88rem;
  }
  .steps li.active {
    background: var(--accent);
    color: var(--accent-text);
  }
  .steps li.done {
    background: var(--ok-soft);
    color: var(--ok);
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: 10px;
    max-width: 720px;
  }
  .card h2,
  .card p {
    margin: 0;
  }
  .checks {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  .checks .fail {
    color: var(--danger);
  }
  .checks .ok {
    color: var(--ok);
  }
  .checks .warn {
    color: var(--warn);
  }
  .good {
    color: var(--ok);
    font-weight: 600;
  }
</style>
