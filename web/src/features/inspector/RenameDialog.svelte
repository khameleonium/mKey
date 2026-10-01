<!--
  RenameDialog — окно переименования устройства или его кнопки (FR-DEV-3): поле нового имени,
  «Убрать имя», понятная ошибка от mKey (правила имени, занято…). Старые имена работают всегда.
  Props: device — устройство (авто-ID или путь); control — номер кнопки ("001") или "" для
  устройства; title — что переименовываем (для заголовка); current — текущее имя ("" — нет);
  onclose() — закрыть без изменений; ondone() — имя сохранено.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import Modal from "../../lib/components/Modal.svelte";
  import { t } from "../../lib/i18n/index.svelte";
  import { errorText, toast } from "../../lib/toast.svelte";

  let {
    device,
    control,
    title,
    current,
    onclose,
    ondone,
  }: {
    device: string;
    control: string;
    title: string;
    current: string;
    onclose: () => void;
    ondone: () => void;
  } = $props();

  /** name — вводимое имя (сначала — текущее; окно создаётся заново для каждой кнопки,
   * поэтому начальное значение берётся один раз); error — ошибка сохранения. */
  // svelte-ignore state_referenced_locally
  let name = $state(current);
  let error = $state("");

  /** save сохраняет имя ("" — убрать имя). */
  async function save(value: string): Promise<void> {
    try {
      await api.renameDevice(device, control, value.trim());
      toast(t("inspector.renamed"));
      ondone();
    } catch (e) {
      error = errorText(e);
    }
  }
</script>

<Modal title={t("inspector.rename_title", { what: title })} {onclose}>
  <p class="muted">
    {t(control ? "inspector.rename_button_hint" : "inspector.rename_device_hint")}
  </p>
  <label class="field">
    {t("inspector.new_name")}
    <!-- svelte-ignore a11y_autofocus -->
    <input
      bind:value={name}
      autofocus
      onkeydown={(e) => e.key === "Enter" && name.trim() && void save(name)}
    />
  </label>
  {#if error}<div class="note error">{error}</div>{/if}
  {#snippet footer()}
    {#if current}
      <button class="ghost" onclick={() => void save("")}>{t("inspector.clear_name")}</button>
    {/if}
    <button onclick={onclose}>{t("common.cancel")}</button>
    <button class="primary" disabled={!name.trim()} onclick={() => void save(name)}
      >{t("common.save")}</button
    >
  {/snippet}
</Modal>
