<!--
  Toggle — переключатель «вкл/выкл».
  Props: checked — состояние; label — подпись (необязательна, иначе title для подсказки);
  disabled — нельзя переключить; onchange(checked) — вызывается при переключении.
-->
<script lang="ts">
  let {
    checked,
    label = "",
    title = "",
    disabled = false,
    onchange,
  }: {
    checked: boolean;
    label?: string;
    title?: string;
    disabled?: boolean;
    onchange: (checked: boolean) => void;
  } = $props();
</script>

<label class="toggle" {title}>
  <!-- Настоящий флажок скрыт, виден нарисованный переключатель -->
  <input
    type="checkbox"
    {checked}
    {disabled}
    aria-label={label || title}
    onchange={(e) => onchange(e.currentTarget.checked)}
  />
  <span class="track"><span class="thumb"></span></span>
  {#if label}<span>{label}</span>{/if}
</label>

<style>
  .toggle {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
    user-select: none;
  }
  input {
    position: absolute;
    opacity: 0;
    width: 1px;
    height: 1px;
  }
  .track {
    width: 36px;
    height: 20px;
    border-radius: 10px;
    background: var(--border);
    position: relative;
    transition: background 0.15s;
    flex: none;
  }
  .thumb {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: #fff;
    box-shadow: var(--shadow);
    transition: left 0.15s;
  }
  input:checked + .track {
    background: var(--ok);
  }
  input:checked + .track .thumb {
    left: 18px;
  }
  input:focus-visible + .track {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
  input:disabled + .track {
    opacity: 0.5;
  }
</style>
