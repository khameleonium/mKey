<!--
  App — корневой компонент веб-интерфейса mKey.
  Пока показывает заголовок, статус каркаса и переключатель языка;
  разделы (главная, проекты, конструктор…) появятся в фазе 5.
  Props: нет.
-->
<script lang="ts">
  import { lang, setLang, t } from "./lib/i18n/index.svelte";
  import { LANGS, type Lang } from "./lib/i18n/translate";

  /** onLangChange применяет язык, выбранный в выпадающем списке. */
  function onLangChange(event: Event): void {
    setLang((event.currentTarget as HTMLSelectElement).value as Lang);
  }
</script>

<main>
  <!-- Заголовок приложения -->
  <header>
    <h1>{t("app.title")}</h1>
    <p>{t("app.tagline")}</p>
  </header>

  <!-- Статус каркаса -->
  <p>{t("app.status.skeleton")}</p>

  <!-- Переключатель языка -->
  <label>
    {t("app.lang.switch")}
    <select value={lang()} onchange={onLangChange}>
      {#each LANGS as l (l)}
        <option value={l}>{l}</option>
      {/each}
    </select>
  </label>
</main>

<style>
  main {
    font-family: system-ui, sans-serif;
    max-width: 48rem;
    margin: 2rem auto;
    padding: 0 1rem;
  }
</style>
