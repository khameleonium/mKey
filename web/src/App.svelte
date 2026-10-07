<!--
  App — корневой компонент веб-интерфейса mKey: боковое меню разделов, плашки «нет связи»
  и «mKey приостановлен» поверх любой страницы, текущий раздел по адресу (#/…) и всплывающие
  сообщения. При первом открытии с неполной настройкой открывается мастер первого запуска.
  Props: нет.
-->
<script lang="ts">
  import { api } from "./lib/api";
  import { CREATOR } from "./lib/creator";
  import Toasts from "./lib/components/Toasts.svelte";
  import { setLang, t } from "./lib/i18n/index.svelte";
  import { setTheme } from "./lib/theme.svelte";
  import { href, navigate, route } from "./lib/router.svelte";
  import { connect, live, refreshStatus } from "./lib/stream.svelte";
  import DevicesPage from "./features/devices/DevicesPage.svelte";
  import DiagnosticsPage from "./features/diagnostics/DiagnosticsPage.svelte";
  import EditorPage from "./features/editor/EditorPage.svelte";
  import HomePage from "./features/home/HomePage.svelte";
  import PluginsPage from "./features/plugins/PluginsPage.svelte";
  import ProjectsPage from "./features/projects/ProjectsPage.svelte";
  import RecorderPage from "./features/recorder/RecorderPage.svelte";
  import ScriptsPage from "./features/scripts/ScriptsPage.svelte";
  import SettingsPage from "./features/settings/SettingsPage.svelte";
  import SetupWizard from "./features/setup/SetupWizard.svelte";

  /** Разделы бокового меню. */
  const NAV = [
    { name: "home", icon: "⌂" },
    { name: "projects", icon: "▦" },
    { name: "recordings", icon: "⏺" },
    { name: "scripts", icon: "✎" },
    { name: "devices", icon: "⌨" },
    { name: "plugins", icon: "⧉" },
    { name: "diagnostics", icon: "✚" },
    { name: "settings", icon: "⚙" },
  ];

  /** firstRun открывает мастер, если он ещё не пройден и есть серьёзные проблемы. */
  async function firstRun(): Promise<void> {
    try {
      if (localStorage.getItem("mkey.setup_done")) return;
    } catch {
      return;
    }
    try {
      const { checks } = await api.doctor();
      if (checks.some((c) => c.status === "fail") && route().name === "home") navigate("setup");
    } catch {
      // Демон недоступен — плашка «нет связи» уже объясняет, что делать.
    }
  }

  /** loadInterface берёт язык и тему из config.yaml (если они там заданы) — файл главнее браузера. */
  async function loadInterface(): Promise<void> {
    try {
      const s = await api.interfaceSettings();
      if (s.language === "ru" || s.language === "en") setLang(s.language);
      if (s.theme === "system" || s.theme === "light" || s.theme === "dark") setTheme(s.theme);
    } catch {
      // Демон недоступен — остаются язык и тема, запомненные браузером.
    }
  }

  // Поток новостей, состояние, язык и тема из настроек и проверка первого запуска — при открытии окна.
  $effect(() => {
    connect();
    void refreshStatus();
    void loadInterface();
    void firstRun();
  });

  /** Раздел для подсветки в меню (редактор относится к «Проектам»). */
  let current = $derived(route().name === "editor" ? "projects" : route().name);
</script>

<div class="app">
  <!-- Боковое меню -->
  <nav>
    <a class="logo" href={href("home")}><span class="key">m</span> mKey</a>
    {#each NAV as item (item.name)}
      <a class="nav" class:active={current === item.name} href={href(item.name)}>
        <span class="icon" aria-hidden="true">{item.icon}</span>
        {t("nav." + item.name)}
      </a>
    {/each}
    <span class="spacer"></span>
    <div class="conn" class:off={!live.connected}>
      ● {live.connected ? t("app.connected") : t("app.disconnected")}
    </div>
    <div class="creator">{t("app.creator", { creator: CREATOR })}</div>
  </nav>

  <main>
    <!-- Плашки поверх любой страницы -->
    {#if !live.connected}
      <div class="note error banner">{t("app.disconnected_text")}</div>
    {/if}
    {#if live.suspended && route().name !== "home"}
      <div class="note error banner row">
        ⏸ {t("home.paused")}
        <span class="spacer"></span>
        <button
          class="small primary"
          onclick={async () => {
            await api.resume();
            await refreshStatus();
          }}>{t("home.resume")}</button
        >
      </div>
    {/if}

    <!-- Текущий раздел -->
    {#if route().name === "projects"}
      <ProjectsPage />
    {:else if route().name === "editor"}
      {#key route().param}
        <EditorPage id={route().param} />
      {/key}
    {:else if route().name === "recordings"}
      <RecorderPage />
    {:else if route().name === "scripts"}
      <ScriptsPage />
    {:else if route().name === "devices"}
      <DevicesPage />
    {:else if route().name === "plugins"}
      <PluginsPage />
    {:else if route().name === "diagnostics"}
      <DiagnosticsPage />
    {:else if route().name === "settings"}
      <SettingsPage />
    {:else if route().name === "setup"}
      <SetupWizard />
    {:else}
      <HomePage />
    {/if}
  </main>
</div>

<Toasts />

<style>
  .app {
    display: grid;
    grid-template-columns: 200px 1fr;
    min-height: 100vh;
  }
  nav {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 14px 10px;
    background: var(--surface);
    border-right: 1px solid var(--border);
    position: sticky;
    top: 0;
    height: 100vh;
  }
  .logo {
    display: flex;
    align-items: center;
    gap: 8px;
    font-weight: 700;
    font-size: 1.2rem;
    color: var(--text);
    text-decoration: none;
    padding: 4px 8px 14px;
  }
  .key {
    display: inline-grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border-radius: 7px;
    background: #4f7cff;
    color: #fff;
    box-shadow: 0 3px 0 #2b3a55;
  }
  .nav {
    display: flex;
    gap: 10px;
    align-items: center;
    padding: 8px 10px;
    border-radius: var(--radius-s);
    color: var(--text);
    text-decoration: none;
  }
  .nav:hover {
    background: var(--surface-2);
  }
  .nav.active {
    background: var(--accent-soft);
    color: var(--accent);
    font-weight: 600;
  }
  .icon {
    width: 1.2em;
    text-align: center;
  }
  .spacer {
    flex: 1;
  }
  .conn {
    font-size: 0.82rem;
    color: var(--ok);
    padding: 6px 10px;
  }
  .conn.off {
    color: var(--danger);
  }
  .creator {
    font-size: 0.75rem;
    color: var(--muted);
    padding: 0 10px 6px;
  }
  main {
    padding: 20px 24px 60px;
    min-width: 0;
    max-width: 1400px;
  }
  .banner {
    margin-bottom: 14px;
  }
  @media (max-width: 700px) {
    .app {
      grid-template-columns: 1fr;
    }
    nav {
      position: static;
      height: auto;
      flex-direction: row;
      flex-wrap: wrap;
      border-right: none;
      border-bottom: 1px solid var(--border);
    }
    .logo {
      padding-bottom: 4px;
    }
    .spacer,
    .conn {
      display: none;
    }
  }
</style>
