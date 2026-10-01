<!--
  RecorderPage — раздел «Записи» (FR-REC-2, FR-REC-4, FR-REC-6, T6.5): начать и закончить запись
  ввода, список сохранённых записей, воспроизведение (с отсчётом, скоростью и повторами),
  превращение записи в блоки конструктора («Сделать событие») и удаление.
  То же умеют команды `mkey rec` и `mkey play`. Props: нет.
-->
<script lang="ts">
  import { api, ApiError } from "../../lib/api";
  import { comboText } from "../../lib/combo";
  import Modal from "../../lib/components/Modal.svelte";
  import PlaceHint from "../../lib/components/PlaceHint.svelte";
  import { navigate } from "../../lib/router.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { onTopic } from "../../lib/stream.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type { RecordingInfo } from "../../lib/types";

  /** Данные раздела: записи и идущая запись. */
  let list = $state<RecordingInfo[]>([]);
  let current = $state<RecordingInfo | null>(null);
  /** name — имя новой записи; speed и repeat — параметры воспроизведения. */
  let name = $state("");
  let speed = $state(1);
  let repeat = $state(1);
  /** playing — какая запись воспроизводится; countdown — секунд до начала (0 — не идёт). */
  let playing = $state("");
  let countdown = $state(0);
  /** hotkey — сочетание «начать/закончить запись» ("" — выключено) для подсказки. */
  let hotkey = $state("");
  /** abort — отмена идущего воспроизведения (закрывает запрос — демон останавливает повтор). */
  let abort: AbortController | null = null;

  /** load перечитывает записи. */
  async function load(): Promise<void> {
    try {
      const r = await api.recordings();
      list = r.recordings;
      current = r.current ?? null;
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  // Загрузка при открытии и при начале/конце записи (в том числе сочетанием в другой программе).
  $effect(() => {
    void load();
    api
      .hotkeys()
      .then((h) => (hotkey = h.record ?? ""))
      .catch(() => (hotkey = ""));
    const offs = ["recorder.started", "recorder.stopped"].map((topic) =>
      onTopic(topic, () => void load()),
    );
    return () => offs.forEach((off) => off());
  });

  /** start начинает запись. */
  async function start(): Promise<void> {
    try {
      current = await api.startRecording(name.trim());
      name = "";
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** stop заканчивает запись. */
  async function stop(): Promise<void> {
    try {
      const info = await api.stopRecording();
      toast(t("rec.saved", { name: info.name, seconds: (info.duration_ms / 1000).toFixed(1) }));
    } catch (e) {
      toast(errorText(e), "error");
    }
    await load();
  }

  /** play воспроизводит запись после отсчёта 3 секунды (чтобы переключиться в нужное окно). */
  async function play(r: RecordingInfo): Promise<void> {
    playing = r.name;
    abort = new AbortController();
    const signal = abort.signal;
    try {
      // Отсчёт.
      for (countdown = 3; countdown > 0; countdown--) {
        await new Promise((res) => setTimeout(res, 1000));
        if (signal.aborted) return;
      }
      // Воспроизведение до конца; «Остановить» закрывает запрос — демон прерывает только его.
      await api.play(r.name, speed, repeat, signal);
      toast(t("rec.play_done"));
    } catch (e) {
      if (!(e instanceof ApiError && e.code === "api.stopped")) toast(errorText(e), "error");
    } finally {
      playing = "";
      countdown = 0;
      abort = null;
    }
  }

  /** stopPlay прерывает отсчёт или воспроизведение. */
  function stopPlay(): void {
    abort?.abort();
  }

  /** converting — запись, которую превращаем в блоки (открыто окно); simplify — упростить движения мыши. */
  let converting = $state<RecordingInfo | null>(null);
  let simplify = $state(true);
  let convertBusy = $state(false);

  /** openConvert открывает окно превращения; упрощение каждый раз включено по умолчанию. */
  function openConvert(r: RecordingInfo): void {
    simplify = true;
    converting = r;
  }

  /** convert превращает запись в новый проект и открывает его в редакторе. */
  async function convert(): Promise<void> {
    if (!converting) return;
    convertBusy = true;
    try {
      const { project } = await api.convertRecording(converting.name, simplify);
      converting = null;
      toast(t("rec.converted"));
      navigate("editor", project);
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      convertBusy = false;
    }
  }

  /** remove удаляет запись после подтверждения. */
  async function remove(r: RecordingInfo): Promise<void> {
    if (!confirm(t("rec.delete_confirm", { name: r.name }))) return;
    try {
      await api.deleteRecording(r.name);
    } catch (e) {
      toast(errorText(e), "error");
    }
    await load();
  }
</script>

<h1>{t("rec.title")}</h1>

<!-- Запись -->
<div class="card rec" class:on={current !== null}>
  {#if current}
    <div>
      <h2>● {t("rec.recording", { name: current.name })}</h2>
      <p class="muted">
        {t("rec.recording_hint", { hotkey: comboText(current.stop_hotkey || "—", t) })}
      </p>
    </div>
    <button class="danger" onclick={stop}>■ {t("rec.stop")}</button>
  {:else}
    <div class="grow">
      <h2>{t("rec.new")}</h2>
      <p class="muted">
        {hotkey ? t("rec.new_hint", { hotkey: comboText(hotkey, t) }) : t("rec.new_hint_nohotkey")}
      </p>
      <div class="row">
        <input bind:value={name} placeholder={t("rec.name_placeholder")} />
        <button class="primary" onclick={start}>⏺ {t("rec.start")}</button>
      </div>
    </div>
  {/if}
</div>

<!-- Воспроизведение: параметры и идущий повтор -->
<div class="row params">
  <label
    >{t("rec.speed")}
    <input type="number" min="0.1" max="10" step="0.1" bind:value={speed} /></label
  >
  <label>{t("rec.repeat")} <input type="number" min="1" step="1" bind:value={repeat} /></label>
  {#if playing}
    <span class="playing">
      ▶ {countdown > 0
        ? t("rec.countdown", { name: playing, n: countdown })
        : t("rec.playing", { name: playing })}
    </span>
    <button onclick={stopPlay}>■ {t("rec.stop_play")}</button>
  {/if}
</div>

<!-- Список записей -->
{#if list.length === 0}
  <div class="note">{t("rec.empty")}</div>
{/if}
<div class="list">
  {#each list as r (r.name)}
    <div class="card item">
      <div class="info">
        <b>{r.name}</b>
        {#if r.problem}
          <!-- Файл не читается: что и в какой строке поправить (совет — только для ошибки в строке). -->
          <span class="problem"
            >⚠ {t(r.problem.line ? "rec.problem" : "rec.problem_file", {
              problem: r.problem.message ?? "",
            })}</span
          >
        {:else}
          <span class="muted">
            {t("rec.info", { seconds: (r.duration_ms / 1000).toFixed(1), events: r.events })} ·
            {new Date(r.created).toLocaleString()}
          </span>
        {/if}
      </div>
      <span class="spacer"></span>
      <button
        class="primary"
        disabled={playing !== "" || current !== null || !!r.problem}
        onclick={() => play(r)}
      >
        ▶ {t("rec.play")}
      </button>
      <button title={t("rec.convert_hint")} disabled={!!r.problem} onclick={() => openConvert(r)}
        >⧉ {t("rec.convert")}</button
      >
      <button class="ghost" title={t("common.delete")} onclick={() => remove(r)}>✕</button>
    </div>
  {/each}
</div>
<PlaceHint id="recordings" />
<p class="muted small">{t("rec.cli_hint")}</p>

{#if converting}
  <Modal
    title={t("rec.convert_title", { name: converting.name })}
    onclose={() => (converting = null)}
  >
    <p>{t("rec.convert_text")}</p>
    <label class="row"
      ><input type="checkbox" bind:checked={simplify} /> {t("rec.convert_simplify")}</label
    >
    <p class="muted">{t(simplify ? "rec.convert_simplify_on" : "rec.convert_simplify_off")}</p>
    {#snippet footer()}
      <button onclick={() => (converting = null)}>{t("common.cancel")}</button>
      <button class="primary" disabled={convertBusy} onclick={convert}>{t("rec.convert_go")}</button
      >
    {/snippet}
  </Modal>
{/if}

<style>
  .rec {
    display: flex;
    gap: 16px;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 14px;
  }
  .rec.on {
    border-left: 6px solid var(--danger);
  }
  .rec h2,
  .rec p {
    margin: 0 0 6px;
  }
  .grow {
    flex: 1;
  }
  .params {
    margin-bottom: 12px;
  }
  .params label {
    display: inline-flex;
    gap: 6px;
    align-items: center;
  }
  .playing {
    color: var(--ok);
    font-weight: 600;
  }
  .list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .item {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 14px;
  }
  .info {
    display: flex;
    flex-direction: column;
  }
  .small {
    margin-top: 14px;
    font-size: 0.85rem;
  }
  .problem {
    color: var(--danger);
  }
</style>
