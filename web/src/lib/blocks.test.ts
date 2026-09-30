// Тесты модели конструктора: проект → блоки → проект без потерь, копирование блоков.
import { describe, expect, it } from "vitest";
import { cloneBlock, newEventID, projectIn, projectOut } from "./blocks";
import type { Project, Registry } from "./types";

// Реестр с видами, у которых есть вложенные списки, — как в демоне.
const reg: Registry = {
  action: [
    {
      id: "repeat",
      name: "Повторять",
      name_key: "action.repeat",
      provider: "engine",
      params_schema: {
        oneOf: [
          {
            type: "object",
            required: ["times", "do"],
            properties: {
              times: { type: "integer" },
              do: { type: "array", "x-widget": "actions" },
            },
          },
          {
            type: "object",
            required: ["while", "do"],
            properties: {
              while: { enum: ["toggled"] },
              do: { type: "array", "x-widget": "actions" },
            },
          },
          {
            type: "object",
            required: ["conditions", "do"],
            properties: {
              conditions: { type: "array", "x-widget": "conditions" },
              do: { type: "array", "x-widget": "actions" },
            },
          },
        ],
      },
    },
    {
      id: "tap",
      name: "Нажать",
      name_key: "action.tap",
      provider: "engine",
      params_schema: { type: "string" },
    },
  ],
  condition: [
    {
      id: "any",
      name: "Хотя бы одно",
      name_key: "condition.any",
      provider: "engine",
      params_schema: {
        type: "object",
        properties: { of: { type: "array", "x-widget": "conditions" } },
      },
    },
  ],
};

// Автокликер: триггер-переключатель и вложенные действия и условия.
const project: Project = {
  id: "p",
  version: 1,
  name: "Игры",
  events: [
    {
      id: "autoclick",
      name: "Автокликер",
      trigger: { type: "hotkey", params: { keys: "{F8}", on: "toggle" } },
      conditions: [{ type: "any", params: { of: [{ type: "key_state", key: "Mouse1" }] } }],
      actions: [
        { type: "repeat", value: { while: "toggled", do: [{ tap: "Mouse0" }, { pause: 50 }] } },
        {
          type: "repeat",
          value: {
            conditions: [{ type: "toggle" }],
            do: [{ repeat: { times: 2, do: [{ tap: "A" }] } }],
          },
        },
        { type: "stop" },
      ],
    },
    { id: "two", triggers: [{ type: "manual" }, { type: "startup" }], actions: [] },
  ],
};

describe("project blocks", () => {
  it("round-trips a project through the editor model", () => {
    // Туда и обратно — тот же проект.
    const ep = projectIn(reg, project);
    expect(projectOut(reg, ep)).toEqual(project);
  });

  it("turns nested lists into blocks with uids", () => {
    // Вложенные действия и условия стали блоками; триггеры — всегда списком.
    const ep = projectIn(reg, project);
    const ev = ep.events[0]!;
    const repeatValue = ev.actions[0]!.value as {
      do: { uid: string; type: string; value: unknown }[];
    };
    expect(repeatValue.do.map((b) => [b.type, b.value])).toEqual([
      ["tap", "Mouse0"],
      ["pause", 50],
    ]);
    expect(repeatValue.do[0]!.uid).toMatch(/^b\d+$/);
    expect(ev.triggers).toHaveLength(1);
    const of = ev.conditions[0]!.params.of as { type: string; params: unknown }[];
    expect(of[0]).toMatchObject({ type: "key_state", params: { key: "Mouse1" } });
    expect(ep.events[1]!.triggers.map((t) => t.type)).toEqual(["manual", "startup"]);
  });

  it("clones blocks with fresh uids and picks free event ids", () => {
    // Копия блока — новые uid у всех вложенных блоков.
    const ep = projectIn(reg, project);
    const b = ep.events[0]!.actions[0]!;
    const c = cloneBlock(b);
    expect(c.uid).not.toBe(b.uid);
    const inner = (v: unknown) => (v as { do: { uid: string }[] }).do[0]!.uid;
    expect(inner(c.value)).not.toBe(inner(b.value));
    expect(newEventID(ep.events)).toBe("event3");
  });
});
