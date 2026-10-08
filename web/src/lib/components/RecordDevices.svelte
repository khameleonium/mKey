<!--
  RecordDevices — выбор «что записывать» (FR-REC-2): подключённые устройства по категориям
  («Клавиатуры, мыши и тачпады», «Геймпады и джойстики», «Сенсорные экраны и планшеты», «Другие»,
  «Виртуальные устройства») с галочками — «ID 2dc8:310a 8BitDo Ultimate 2C Wireless Controller».
  Свои устройства mKey — в «Виртуальных», без галочки (mKey их не читает). Ниже — классы «по
  умолчанию» для устройств без своей галочки и подключённых позже. Изменения сохраняются сразу
  (то же меняют меню значка и `mkey rec --devices`). Снять можно все галочки — о пустой записи
  предупредят при её начале.
  Props: onselected(n) — сколько подключённых устройств сейчас будет записываться (при каждом изменении).
-->
<script lang="ts">
  import { api } from "../api";
  import { t } from "../i18n/index.svelte";
  import { KINDS } from "../kinds";
  import { onTopic } from "../stream.svelte";
  import { errorText, toast } from "../toast.svelte";
  import type { RecordDevice, RecordSettings } from "../types";

  let { onselected }: { onselected?: (n: number) => void } = $props();

  /** CATEGORIES — категории в порядке показа (как в демоне). */
  const CATEGORIES = ["keyboards", "gamepads", "touch", "other", "virtual"];

  /** Устройства и настройки записи; error — ошибка загрузки. */
  let devices = $state<RecordDevice[]>([]);
  let settings = $state<RecordSettings | null>(null);
  let error = $state("");

  /** load перечитывает устройства и настройки. */
  async function load(): Promise<void> {
    try {
      const [d, s] = await Promise.all([api.recordDevices(), api.recordSettings()]);
      devices = d.devices;
      settings = s;
      error = "";
    } catch (e) {
      error = errorText(e);
    }
  }

  // Загрузка при показе и при подключении/отключении устройств.
  $effect(() => {
    void load();
    const offs = ["input.device_added", "input.device_removed"].map((topic) =>
      onTopic(topic, () => void load()),
    );
    return () => offs.forEach((off) => off());
  });

  // Сколько устройств будет записываться — наружу (для предупреждения о пустой записи).
  $effect(() => {
    onselected?.(devices.filter((d) => d.selected).length);
  });

  /** save сохраняет настройки записи и перечитывает выбор. */
  async function save(next: RecordSettings): Promise<void> {
    try {
      settings = await api.setRecordSettings(next);
      devices = (await api.recordDevices()).devices;
    } catch (e) {
      toast(errorText(e), "error");
    }
  }

  /** setDevice ставит или снимает галочку устройства (свой выбор важнее классов). */
  function setDevice(d: RecordDevice, on: boolean): void {
    if (settings) void save({ ...settings, devices: { ...(settings.devices ?? {}), [d.key]: on } });
  }

  /** setKind включает или выключает класс по умолчанию. */
  function setKind(kind: string, on: boolean): void {
    if (!settings) return;
    const kinds = on ? [...settings.kinds, kind] : settings.kinds.filter((k) => k !== kind);
    void save({ ...settings, kinds });
  }
</script>

<div class="devices">
  {#if error}<div class="note error">{error}</div>{/if}
  {#if devices.length === 0 && !error}<p class="muted">{t("rec.no_devices")}</p>{/if}

  <!-- Устройства по категориям -->
  {#each CATEGORIES as cat (cat)}
    {@const list = devices.filter((d) => d.category === cat)}
    {#if list.length}
      <fieldset>
        <legend>{t("rec.cat." + cat)}</legend>
        {#each list as d (d.path)}
          {#if d.own}
            <div class="own muted">
              {t("rec.device", { id: d.id, name: d.name })} — {t("rec.device_own")}
            </div>
          {:else}
            <label class="check"
              ><input
                type="checkbox"
                checked={d.selected}
                onchange={(e) => setDevice(d, e.currentTarget.checked)}
              />
              <span>{t("rec.device", { id: d.id, name: d.name })}</span></label
            >
          {/if}
        {/each}
      </fieldset>
    {/if}
  {/each}

  <!-- Классы по умолчанию: для устройств без своей галочки и подключённых позже -->
  {#if settings}
    <fieldset>
      <legend>{t("rec.kinds_default")}</legend>
      <p class="muted hint">{t("rec.kinds_default_hint")}</p>
      <div class="kinds">
        {#each KINDS as k (k)}
          <label class="check"
            ><input
              type="checkbox"
              checked={settings.kinds.includes(k)}
              onchange={(e) => setKind(k, e.currentTarget.checked)}
            />
            {t("devices.kind." + k)}</label
          >
        {/each}
      </div>
    </fieldset>
  {/if}
</div>

<style>
  .devices {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  fieldset {
    border: 1px solid var(--border);
    border-radius: var(--radius-s);
    margin: 0;
    padding: 6px 12px 8px;
  }
  legend {
    font-weight: 600;
    padding: 0 4px;
  }
  .check {
    display: flex;
    gap: 6px;
    align-items: center;
    padding: 2px 0;
    overflow-wrap: anywhere;
  }
  .own {
    padding: 2px 0 2px 22px;
  }
  .kinds {
    display: flex;
    flex-wrap: wrap;
    gap: 2px 14px;
  }
  .hint {
    margin: 0 0 4px;
    font-size: 0.85rem;
  }
</style>
