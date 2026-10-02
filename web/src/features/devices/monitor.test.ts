// Тесты текстов монитора нажатий.
import { describe, expect, it } from "vitest";
import { clockText, describeWatch, logFileName, logText } from "./monitor";
import type { WatchEntry } from "../../lib/types";

/** t — перевод для тестов: ключ и параметры одной строкой. */
const t = (key: string, p?: Record<string, string | number>) =>
  key + (p ? " " + Object.values(p).join(",") : "");

/** entry — событие монитора с общими полями (время — местное). */
const entry = (e: Partial<WatchEntry>): WatchEntry => ({
  time: new Date(2026, 9, 2, 14, 5, 9, 7).toISOString(),
  device: "/dev/input/event3",
  device_name: "Kbd",
  kind: "key",
  name: "A",
  kernel: "KEY_A",
  code: 30,
  action: "down",
  ...e,
});

describe("monitor texts", () => {
  it("describes events", () => {
    expect(describeWatch(entry({}), t)).toBe("devices.ev_down {A}");
    expect(describeWatch(entry({ action: "up" }), t)).toBe("devices.ev_up {A}");
    expect(describeWatch(entry({ kind: "wheel", value: 1 }), t)).toBe("devices.ev_wheel +1");
    expect(describeWatch(entry({ kind: "move", value: -3, dy: 2 }), t)).toBe(
      "devices.ev_move -3,2",
    );
  });

  it("builds the log file", () => {
    // Строка на событие по порядку; без имени устройства — путь.
    const text = logText([entry({}), entry({ action: "up", device_name: "" })], t);
    expect(text).toBe(
      "14:05:09.007  devices.ev_down {A}  — Kbd  (KEY_A)\n" +
        "14:05:09.007  devices.ev_up {A}  — /dev/input/event3  (KEY_A)\n",
    );
    expect(logText([], t)).toBe("");
    expect(clockText(entry({}).time)).toBe("14:05:09.007");
    expect(logFileName(new Date(2026, 9, 2, 14, 5, 9))).toBe(
      "mkey-monitor-2026-10-02_14-05-09.txt",
    );
  });
});
