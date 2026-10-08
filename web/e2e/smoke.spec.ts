// Основные сценарии окна mKey (T5.10, T12.11) на демоне без настоящих устройств: главная,
// проект из шаблона и сухой прогон, интервалы нажатий, раздел «Скрипты», устройства.
import { expect, test, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

/** login открывает окно по ссылке с токеном (как `mkey gui`) — дальше вход по cookie. */
async function login(page: Page): Promise<void> {
  await page.goto(`/?t=${process.env.MKEY_E2E_TOKEN ?? ""}`);
  await expect(page.getByRole("navigation").getByText("mKey is running")).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  await login(page);
});

test("home shows the creator and the running state", async ({ page }) => {
  await expect(page.getByText("Creator: Ilya Ulyanov | khameleonium")).toBeVisible();
  await expect(page.getByRole("link", { name: "Scripts" })).toBeVisible();
});

test("project from a template and its dry run", async ({ page }) => {
  // Проект «Автокликер» из шаблона открывается в редакторе.
  await page.getByRole("navigation").getByRole("link", { name: "Projects" }).click();
  await page.getByRole("button", { name: /From a template/ }).click();
  await page.getByRole("button", { name: /Autoclicker/ }).click();
  await expect(page.getByRole("button", { name: /Dry run/ })).toBeVisible();

  // Сухой прогон: повтор «пока включено» и щелчок мыши, ничего не нажато.
  await page
    .getByRole("button", { name: /Dry run/ })
    .first()
    .click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Repeat while the event's toggle is on:")).toBeVisible();
  await expect(dialog.getByText(/Press Mouse0/)).toBeVisible();
  await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
});

test("key timing is saved to config.yaml", async ({ page }) => {
  await page.getByRole("navigation").getByRole("link", { name: "Settings" }).click();
  const hold = page.getByLabel("Key hold");
  await hold.fill("35");
  await page
    .locator(".card", { has: hold })
    .getByRole("button", { name: "Save", exact: true })
    .click();
  await expect(page.getByText("Key timing saved")).toBeVisible();

  // Значение — в файле настроек и после перезагрузки окна.
  const cfg = path.join(process.env.MKEY_E2E_ROOT ?? "", "config", "mkey", "config.yaml");
  expect(fs.readFileSync(cfg, "utf8")).toContain("key_hold_ms: 35");
  await page.reload();
  await expect(page.getByLabel("Key hold")).toHaveValue("35");
});

test("scripts: create, save with an error, check", async ({ page }) => {
  await page.getByRole("navigation").getByRole("link", { name: "Scripts" }).click();
  await page.getByRole("button", { name: /New script/ }).click();
  await page.getByRole("dialog").getByRole("textbox").fill("clicker");
  await page.getByRole("dialog").getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name: "clicker.lua" })).toBeVisible();

  // Ошибка синтаксиса: файл сохранён, ошибка показана со строкой.
  const editor = page.getByRole("textbox", { name: "clicker.lua" });
  await editor.click();
  await page.keyboard.press("Control+End");
  await page.keyboard.type("if x then\n");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByText("Saved, but the script has an error")).toBeVisible();
  await expect(page.getByText(/^Line \d+:/)).toBeVisible();
  const file = path.join(
    process.env.MKEY_E2E_ROOT ?? "",
    "config",
    "mkey",
    "scripts",
    "clicker.lua",
  );
  expect(fs.readFileSync(file, "utf8")).toContain("if x then");
});

test("devices page works without real devices", async ({ page }) => {
  await page.getByRole("navigation").getByRole("link", { name: "Devices" }).click();
  await expect(page.getByRole("heading", { name: "Watch presses" })).toBeVisible();
  await expect(page.getByRole("button", { name: /Watch/ }).last()).toBeEnabled();
});

test("recordings: what to record is saved at once", async ({ page }) => {
  await page.getByRole("navigation").getByRole("link", { name: "Recordings" }).click();
  // Без настоящих устройств — список пуст, но классы «по умолчанию» есть и сохраняются сразу.
  await page.getByText(/^What to record/).click();
  await expect(page.getByText("No devices found.")).toBeVisible();
  await page.getByText("By default", { exact: true }).click();
  const defaults = page.locator("details.cat", { hasText: "By default" });
  await defaults.getByLabel("gamepad").check();
  const cfg = path.join(process.env.MKEY_E2E_ROOT ?? "", "config", "mkey", "config.yaml");
  await expect.poll(() => fs.readFileSync(cfg, "utf8")).toMatch(/kinds: \[.*gamepad.*\]/);
  // Снять все классы можно.
  for (const k of ["keyboard", "mouse", "gamepad"]) await defaults.getByLabel(k).uncheck();
  await expect.poll(() => fs.readFileSync(cfg, "utf8")).toMatch(/kinds: \[\]/);
});

test("starting with nothing selected asks, an empty recording is reported", async ({ page }) => {
  await page.getByRole("navigation").getByRole("link", { name: "Recordings" }).click();
  await page.getByRole("button", { name: /Start recording/ }).click();
  // Ни одно устройство не выбрано — вопрос; «Всё равно записать» — запись идёт.
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("No device selected")).toBeVisible();
  await dialog.getByRole("button", { name: "Record anyway" }).click();
  await page.getByRole("button", { name: /Stop/ }).first().click();
  await expect(page.getByText(/has no actions\. Recorded: nothing selected/)).toBeVisible();
});
