// Подготовка e2e-тестов: демон mKey без настоящих устройств (--fake-backends) во временной папке
// (свои настройки, проекты и сокет — ничто не касается установленного mKey). Путь к программе —
// MKEY_BIN или ../mkey (make build). Токен входа передаётся тестам через MKEY_E2E_TOKEN.
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

/** PORT — порт окна проверочного демона (не совпадает с портом обычного mKey). */
export const PORT = 17499;

/** sleep ждёт ms миллисекунд. */
function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/** globalSetup запускает демон и возвращает функцию его остановки и уборки. */
export default async function globalSetup(): Promise<() => Promise<void>> {
  // Временная «домашняя» папка: настройки (английский язык, порт), без проекта-примера.
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "mkey-e2e-"));
  const dirs = { config: path.join(root, "config"), run: path.join(root, "run") };
  fs.mkdirSync(path.join(dirs.config, "mkey"), { recursive: true });
  fs.mkdirSync(dirs.run, { recursive: true, mode: 0o700 });
  fs.writeFileSync(
    path.join(dirs.config, "mkey", "config.yaml"),
    `version: 2\nlanguage: en\nmodules:\n  api:\n    port: ${PORT}\n  setup:\n    startup_check_ms: 0\n  store:\n    example: false\n`,
  );
  const env = {
    ...process.env,
    HOME: root,
    XDG_CONFIG_HOME: dirs.config,
    XDG_DATA_HOME: path.join(root, "data"),
    XDG_STATE_HOME: path.join(root, "state"),
    XDG_RUNTIME_DIR: dirs.run,
    LANG: "en_US.UTF-8",
  };

  // Демон без настоящих устройств.
  const bin = process.env.MKEY_BIN ?? path.resolve(import.meta.dirname, "../../mkey");
  const daemon = spawn(bin, ["daemon", "--fake-backends"], { env, stdio: "ignore" });

  // Ждём токен входа и ответ окна (не дольше 15 с).
  const tokenFile = path.join(dirs.run, "mkey", "token");
  let ready = false;
  for (let i = 0; i < 150 && !ready; i++) {
    await sleep(100);
    if (!fs.existsSync(tokenFile)) continue;
    ready = await fetch(`http://127.0.0.1:${PORT}/`).then(
      (r) => r.ok,
      () => false,
    );
  }
  if (!ready) {
    daemon.kill("SIGKILL");
    throw new Error(`mkey daemon did not start (${bin})`);
  }
  process.env.MKEY_E2E_TOKEN = fs.readFileSync(tokenFile, "utf8").trim();
  process.env.MKEY_E2E_ROOT = root;

  // Остановка: корректный выход демона, затем уборка временной папки.
  return async () => {
    const exited = new Promise((r) => daemon.once("exit", r));
    daemon.kill("SIGTERM");
    await Promise.race([exited, sleep(5000)]);
    fs.rmSync(root, { recursive: true, force: true });
  };
}
