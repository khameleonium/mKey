// Тесты текстов «Сухого прогона».
import { describe as group, expect, it } from "vitest";
import { describe, seconds } from "./dryrun";
import type { DryStep } from "../../lib/types";

/** t — перевод для тестов: ключ и параметры одной строкой. */
const t = (key: string, p?: Record<string, string | number>) =>
  key +
  (p
    ? " " +
      Object.entries(p)
        .map(([k, v]) => `${k}=${v}`)
        .join(" ")
    : "");

/** name — тексты видов из «реестра». */
const name = {
  action: (type: string) => `action:${type}`,
  condition: (c: { type: string; params?: Record<string, unknown> }) =>
    `condition:${c.type}(${JSON.stringify(c.params ?? {})})`,
};

/** step — строка таймлайна с общими полями. */
const step = (s: Partial<DryStep>): DryStep => ({ at_ms: 0, depth: 0, kind: "tap", ...s });

group("dry run texts", () => {
  it("describes macro steps", () => {
    // Нажатие: одно, несколько раз, с удержанием; пауза: точная и случайная.
    expect(describe(step({ keys: ["Ctrl", "C"] }), t, name, "ru")).toBe(
      "dry.step.tap keys=Ctrl + C count=1",
    );
    expect(describe(step({ keys: ["A"], count: 3 }), t, name, "ru")).toBe(
      "dry.step.tap_n keys=A count=3",
    );
    expect(describe(step({ keys: ["A"], ms: 500 }), t, name, "ru")).toBe(
      "dry.step.tap keys=A count=1 dry.step.hold ms=500",
    );
    expect(describe(step({ kind: "wait", ms: 100, max_ms: 200 }), t, name, "ru")).toBe(
      "dry.step.wait_range min=100 max=200",
    );
    // Колесо вверх и кнопка геймпада (устройство указывается).
    expect(describe(step({ kind: "wheel", dy: 1, count: 2 }), t, name, "ru")).toBe(
      "dry.step.wheel_up count=2",
    );
    expect(describe(step({ kind: "press", keys: ["South"], device: "pad2" }), t, name, "ru")).toBe(
      "dry.step.press keys=South dry.device device=pad2",
    );
  });

  it("describes groups, notes and other actions", () => {
    // Условия — названиями из реестра; длительность записи — в секундах.
    const cond = { type: "key_state", params: { key: "Shift" } };
    expect(
      describe(step({ kind: "group", key: "dry.if", conditions: [cond] }), t, name, "ru"),
    ).toBe('dry.if count=1 conds=condition:key_state({"key":"Shift"})');
    expect(
      describe(
        step({ kind: "note", key: "dry.play", args: { name: "игра", ms: "1500" } }),
        t,
        name,
        "ru",
      ),
    ).toBe("dry.play name=игра ms=1500 count=1 conds= sec=1,50");
    expect(describe(step({ kind: "action", action: "lua" }), t, name, "ru")).toBe(
      "dry.action name=action:lua",
    );
  });

  it("formats seconds for the language", () => {
    expect(seconds(1234, "ru")).toBe("1,23");
    expect(seconds(1234, "en")).toBe("1.23");
  });
});
