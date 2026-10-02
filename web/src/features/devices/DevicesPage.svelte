<!--
  DevicesPage — устройства ввода (FR-UI-1.5): список клавиатур, мышей, геймпадов и т.п.
  и монитор нажатий (FR-DEV-8): после «Следить за нажатиями» mKey непрерывно показывает журнал —
  когда, на каком устройстве и какая кнопка нажата (с именем для макросов), пока не нажать «Стоп»
  или не удержать Esc 2 секунды. Можно следить за одним устройством; журнал сохраняется в файл
  только кнопкой «Сохранить в файл» (файл создаёт браузер, mKey нажатия не сохраняет).
  Список устройств обновляется при подключении и отключении.
  Props: нет.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { t } from "../../lib/i18n/index.svelte";
  import { onTopic } from "../../lib/stream.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";
  import type {
    InputDevice,
    VirtualDeviceInfo,
    VirtualTemplateInfo,
    WatchEntry,
  } from "../../lib/types";
  import GamepadWizard from "./GamepadWizard.svelte";
  import { clockText, describeWatch, logFileName, logText } from "./monitor";
  import DeviceDetails from "../inspector/DeviceDetails.svelte";
  import RenameDialog from "../inspector/RenameDialog.svelte";

  /** Данные страницы. */
  let devices = $state<InputDevice[]>([]);
  /** opened — устройства, у которых раскрыто «Подробнее» (подробности загружаются только для них). */
  let opened = $state<Record<string, boolean>>({});
  let denied = $state<string[]>([]);
  /** autoIds — авто-ID устройств по пути (UnKey…); mode — режим раздачи авто-ID. */
  let autoIds = $state<Record<string, string>>({});
  let mode = $state("");
  /** virtuals — виртуальные устройства включённых проектов (null — модуль недоступен). */
  let virtuals = $state<VirtualDeviceInfo[] | null>(null);
  /** vtemplates — состав шаблонов (для мастера); wizard — открыт мастер «второй геймпад». */
  let vtemplates = $state<VirtualTemplateInfo[]>([]);
  let wizard = $state(false);

  /** watching — монитор включён; moves — показывать перемещения мыши; only — путь устройства,
   *  за которым следить ("" — все); log — записи на экране (новые сверху); saved — все записи
   *  наблюдения по порядку для «Сохранить в файл» (не больше MAX_SAVED). */
  let watching = $state(false);
  let moves = $state(false);
  let only = $state("");
  let log = $state<(WatchEntry & { id: number })[]>([]);
  let saved: WatchEntry[] = [];
  let savedCount = $state(0);
  /** source — поток событий; escTimer — таймер «Esc удерживается 2 секунды»; nextId — номера записей. */
  let source: EventSource | null = null;
  let escTimer: ReturnType<typeof setTimeout> | null = null;
  let nextId = 0;

  /** Наибольшее число записей в журнале на экране (старые убираются). */
  const MAX_LOG = 300;
  /** MAX_SAVED — наибольшее число записей для файла (старые убираются). */
  const MAX_SAVED = 20000;
  /** ESC_HOLD_MS — сколько удерживать Esc, чтобы остановить монитор. */
  const ESC_HOLD_MS = 2000;

  /** startWatch включает монитор нажатий. */
  function startWatch(): void {
    stopWatch();
    log = [];
    saved = [];
    savedCount = 0;
    watching = true;
    const q = [moves ? "moves=1" : "", only ? "device=" + encodeURIComponent(only) : ""];
    const query = q.filter(Boolean).join("&");
    source = new EventSource("/api/v1/input/watch" + (query ? "?" + query : ""));
    source.addEventListener("input", (e: MessageEvent<string>) => {
      const entry = JSON.parse(e.data) as WatchEntry;
      // Удержание Esc 2 секунды — остановка (отпускание раньше отменяет таймер).
      if (entry.kind === "key" && entry.kernel === "KEY_ESC") {
        if (entry.action === "down" && !escTimer) escTimer = setTimeout(stopWatch, ESC_HOLD_MS);
        if (entry.action === "up" && escTimer) {
          clearTimeout(escTimer);
          escTimer = null;
        }
      }
      nextId += 1;
      log = [{ ...entry, id: nextId }, ...log].slice(0, MAX_LOG);
      saved.push(entry);
      if (saved.length > MAX_SAVED) saved.shift();
      savedCount = saved.length;
    });
    source.onerror = () => {
      if (watching) toast(t("devices.watch_lost"), "error");
      stopWatch();
    };
  }

  /** stopWatch выключает монитор. */
  function stopWatch(): void {
    source?.close();
    source = null;
    watching = false;
    if (escTimer) clearTimeout(escTimer);
    escTimer = null;
  }

  // Уход со страницы выключает монитор.
  $effect(() => () => stopWatch());

  /** saveLog сохраняет журнал наблюдения в текстовый файл (скачивание браузером). */
  function saveLog(): void {
    const blob = new Blob([logText(saved, t)], { type: "text/plain;charset=utf-8" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = logFileName(new Date());
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  }

  /** load перечитывает устройства. */
  async function load(): Promise<void> {
    try {
      const r = await api.devices();
      devices = r.devices;
      denied = r.status?.denied ?? [];
      autoIds = r.auto_ids ?? {};
      const v = await api.virtualDevices().catch(() => null);
      virtuals = v?.devices ?? null;
      vtemplates = v?.template_info ?? [];
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** renaming — переименование кнопки из журнала монитора («Узнать кнопку», FR-DEV-4). */
  let renaming = $state<{ device: string; control: string; title: string; current: string } | null>(
    null,
  );

  /** renameFromLog открывает переименование кнопки, нажатой в журнале: номер и текущее имя —
   * из подробностей её устройства. */
  async function renameFromLog(e: WatchEntry): Promise<void> {
    try {
      const d = (await api.inspectDevice(e.device)).device;
      const list =
        e.kind === "axis" ? (d.axes ?? []) : e.kind === "wheel" ? (d.rel ?? []) : (d.keys ?? []);
      const c = list.find((x) => x.code === e.code);
      if (!c?.number) return;
      renaming = {
        device: d.auto_id ?? d.info.path,
        control: c.number,
        title: `{${e.name}}`,
        current: c.custom_name ?? "",
      };
    } catch (err) {
      toast(errorText(err), "error");
    }
  }

  /** loadMode читает режим авто-ID (без инспектора — выбор не показывается). */
  async function loadMode(): Promise<void> {
    try {
      mode = (await api.deviceSettings()).auto_ids;
    } catch {
      mode = "";
    }
  }

  /** saveMode меняет режим авто-ID: подходящие устройства сразу получают имена. */
  async function saveMode(next: string): Promise<void> {
    try {
      mode = (await api.setDeviceSettings(next)).auto_ids;
      toast(t("devices.auto_saved"));
      await load();
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  // Загрузка при открытии и при подключении/отключении устройств и раздаче авто-ID.
  $effect(() => {
    void load();
    void loadMode();
    const topics = [
      "input.device_added",
      "input.device_removed",
      "input.access_changed",
      "inspector.auto_ids_changed",
      "store.projects_changed",
    ];
    const offs = topics.map((topic) => onTopic(topic, () => void load()));
    return () => offs.forEach((off) => off());
  });
</script>

<h1>{t("devices.title")}</h1>

<!-- Монитор нажатий -->
<div class="card identify">
  <div class="grow">
    <h2>{t("devices.watch")}</h2>
    <p class="muted">{t("devices.watch_hint")}</p>
  </div>
  {#if watching}
    <button class="danger" onclick={stopWatch}>■ {t("devices.watch_stop")}</button>
  {:else}
    <label class="row"
      >{t("devices.watch_device")}
      <select bind:value={only}>
        <option value="">{t("devices.watch_all")}</option>
        {#each devices as d (d.info.path)}
          <option value={d.info.path}
            >{d.info.name} ({d.info.path.replace("/dev/input/", "")})</option
          >
        {/each}
      </select></label
    >
    <label class="row"
      ><input type="checkbox" bind:checked={moves} /> {t("devices.watch_moves")}</label
    >
    <button class="primary" onclick={startWatch}>● {t("devices.watch_start")}</button>
  {/if}
  {#if savedCount > 0}
    <button onclick={saveLog} title={t("devices.watch_save_hint")}
      >💾 {t("devices.watch_save", { n: savedCount })}</button
    >
  {/if}
  {#if watching || log.length}
    <div class="log">
      {#if watching && log.length === 0}<div class="muted">{t("devices.watch_waiting")}</div>{/if}
      {#each log as e (e.id)}
        <div class="line" class:up={e.action === "up"}>
          <span class="time">{clockText(e.time)}</span>
          {#if e.labeled}
            <button
              class="what link"
              title={t("devices.watch_rename_hint")}
              onclick={() => void renameFromLog(e)}>{describeWatch(e, t)} ✎</button
            >
          {:else}
            <span class="what">{describeWatch(e, t)}</span>
          {/if}
          <span class="dev">{e.device_name || e.device}</span>
          <span class="kernel muted">{e.kernel}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>

<!-- Автоматические имена для кнопок без стандартного имени (FR-DEV-2) -->
{#if mode}
  <div class="card auto-ids">
    <div class="grow">
      <h2>{t("devices.auto_title")}</h2>
      <p class="muted">{t("devices.auto_hint")}</p>
    </div>
    <label class="field">
      {t("devices.auto_mode")}
      <select value={mode} onchange={(e) => void saveMode(e.currentTarget.value)}>
        {#each ["smart", "all", "unusual"] as m (m)}
          <option value={m}>{t("devices.auto_mode." + m)}</option>
        {/each}
      </select>
    </label>
  </div>
{/if}

<!-- Виртуальные устройства проектов (FR-VD-1) -->
{#if virtuals !== null}
  <div class="card virtuals">
    <div class="vhead">
      <h2>{t("devices.virtual_title")}</h2>
      {#if vtemplates.length}
        <button class="primary" onclick={() => (wizard = true)}>🎮 {t("gpw.open_wizard")}</button>
      {/if}
    </div>
    {#if virtuals.length === 0}
      <p class="muted">{t("devices.virtual_none")}</p>
      <pre class="snippet">virtual_devices:
  - name: pad2
    template: xbox360</pre>
      <p class="muted">{t("devices.virtual_hint")}</p>
    {:else}
      {#each virtuals as v (v.project + "/" + v.name)}
        <div class="vrow">
          <code>{v.name}</code>
          <span>{t("vdev.template." + v.template)}</span>
          <span class="muted">{t("devices.virtual_project", { project: v.project })}</span>
          {#if v.error}
            <span class="error-text">⚠ {v.error}</span>
          {:else}
            <span class="muted">{v.system_name}{v.node ? " · " + v.node : ""}</span>
          {/if}
        </div>
      {/each}
      <p class="muted">{t("devices.virtual_hint")}</p>
    {/if}
  </div>
{/if}

{#if wizard}
  <GamepadWizard {devices} {autoIds} templates={vtemplates} onclose={() => (wizard = false)} />
{/if}

{#if denied.length}
  <div class="note warn">{t("devices.denied", { n: denied.length })}</div>
{/if}

<!-- Список устройств -->
<div class="list">
  {#each devices as d (d.info.path)}
    <div class="card device" class:wide={opened[d.info.path]}>
      <b>{d.info.name}</b>
      <span class="kinds">
        {#each d.kinds as k (k)}<span class="tag">{t("devices.kind." + k)}</span>{/each}
        {#if autoIds[d.info.path]}<span class="tag auto" title={t("devices.auto_tag_hint")}
            >{autoIds[d.info.path]}</span
          >{/if}
      </span>
      <details ontoggle={(e) => (opened[d.info.path] = e.currentTarget.open)}>
        <summary class="muted">{t("common.more")}</summary>
        {#if opened[d.info.path]}
          <DeviceDetails path={d.info.path} />
        {/if}
      </details>
    </div>
  {/each}
  {#if devices.length === 0}
    <div class="note">{t("devices.none")}</div>
  {/if}
</div>

{#if renaming}
  <RenameDialog
    device={renaming.device}
    control={renaming.control}
    title={renaming.title}
    current={renaming.current}
    onclose={() => (renaming = null)}
    ondone={() => (renaming = null)}
  />
{/if}

<style>
  .identify {
    display: flex;
    flex-wrap: wrap;
    gap: 12px 20px;
    align-items: center;
    margin-bottom: 16px;
  }
  .identify h2,
  .identify p {
    margin: 0;
  }
  /* Длинные названия устройств не раздвигают карточку. */
  .identify select {
    max-width: 18em;
  }
  .grow {
    flex: 1;
  }
  .log {
    flex-basis: 100%;
    max-height: 420px;
    overflow: auto;
    font-family: var(--mono);
    font-size: 0.85rem;
    background: var(--surface-2);
    border-radius: var(--radius-s);
    padding: 6px 10px;
  }
  .line {
    display: grid;
    grid-template-columns: 9em minmax(12em, 1fr) minmax(10em, 1.5fr) 12em;
    gap: 10px;
    padding: 2px 0;
    border-bottom: 1px solid var(--border);
  }
  .line.up .what {
    color: var(--muted);
  }
  .what {
    font-weight: 600;
  }
  /* Кнопка с авто-именем в журнале — ссылка на переименование. */
  .what.link {
    background: none;
    border: none;
    padding: 0;
    text-align: left;
    color: var(--accent);
    cursor: pointer;
    font: inherit;
    font-weight: 600;
  }
  .dev,
  .kernel {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .list {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: 10px;
    margin-top: 12px;
  }
  .device {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 12px;
    min-width: 0;
  }
  /* Раскрытые подробности занимают всю ширину: в них много кнопок и таблица осей. */
  .device.wide {
    grid-column: 1 / -1;
  }
  .kinds {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
  }
  .tag {
    font-size: 0.8rem;
    padding: 1px 8px;
    border-radius: 10px;
    background: var(--accent-soft);
  }
  /* Авто-ID устройства — моноширинным, как в макросах. */
  .tag.auto {
    font-family: var(--mono);
    background: var(--surface-2);
  }
  .auto-ids {
    display: flex;
    flex-wrap: wrap;
    gap: 12px 20px;
    align-items: center;
    margin-bottom: 16px;
  }
  .auto-ids h2,
  .auto-ids p {
    margin: 0;
  }
  summary {
    cursor: pointer;
    font-size: 0.85rem;
  }
  .virtuals {
    margin-bottom: 16px;
  }
  .vhead {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 10px;
  }
  .virtuals h2 {
    margin-top: 0;
  }
  .vrow {
    display: grid;
    grid-template-columns: minmax(6em, auto) minmax(10em, auto) minmax(10em, auto) 1fr;
    gap: 12px;
    padding: 4px 0;
  }
  .snippet {
    font-family: var(--mono);
    background: var(--surface-2);
    padding: 6px 10px;
    border-radius: var(--radius-s);
  }
  .error-text {
    color: var(--danger);
  }
</style>
