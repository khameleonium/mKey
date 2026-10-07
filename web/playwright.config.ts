// Конфигурация e2e-тестов окна (Playwright, T5.10, T12.11, ADR-0035): перед тестами
// e2e/global-setup.ts запускает собранный ./mkey в режиме без настоящих устройств
// (`mkey daemon --fake-backends`) во временной папке; тесты открывают его окно.
// Локально можно взять установленный Chrome: MKEY_E2E_CHANNEL=chrome npm run e2e.
import { defineConfig } from "@playwright/test";
import { PORT } from "./e2e/global-setup";

export default defineConfig({
  testDir: "e2e",
  globalSetup: "./e2e/global-setup.ts",
  // Один демон на все тесты — тесты идут по очереди.
  workers: 1,
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    channel: process.env.MKEY_E2E_CHANNEL,
    locale: "en-US",
    trace: "retain-on-failure",
  },
});
