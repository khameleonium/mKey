// Тесты работы со схемами: значения по умолчанию, выбор и смена варианта, поля формы.
import { describe, expect, it } from "vitest";
import { defaultValue, enumLabel, fields, switchVariant, variantIndex } from "./schema";
import type { Schema } from "./types";

// Схема действия «Повторять» — как в реестре демона.
const repeat: Schema = {
  oneOf: [
    {
      type: "object",
      required: ["times", "do"],
      properties: {
        times: { type: "integer", default: 3 },
        do: { type: "array", "x-widget": "actions" },
      },
    },
    {
      type: "object",
      required: ["while", "do"],
      properties: {
        while: { enum: ["toggled", "held"], default: "toggled" },
        do: { type: "array", "x-widget": "actions" },
      },
    },
  ],
};

describe("defaultValue", () => {
  it("builds values from schemas", () => {
    // Варианты, списки, объекты с обязательными полями и значения по умолчанию.
    expect(defaultValue(repeat)).toEqual({ times: 3, do: [] });
    expect(defaultValue({ enum: ["Left", "Right"] })).toBe("Left");
    expect(defaultValue({ type: "integer", minimum: 10 })).toBe(10);
    expect(
      defaultValue({
        type: "object",
        required: ["key"],
        properties: { key: { type: "string" }, x: { type: "boolean" } },
      }),
    ).toEqual({ key: "" });
  });
});

describe("variants", () => {
  it("detects and switches variants keeping nested blocks", () => {
    // Значение с while — второй вариант; переключение на первый сохраняет «Делать».
    const v = { while: "toggled", do: [{ uid: "b1", type: "tap", value: "A" }] };
    expect(variantIndex(repeat, v)).toBe(1);
    expect(
      variantIndex(
        { oneOf: [{ type: "integer" }, { type: "object", required: ["min_ms"] }] },
        { min_ms: 1 },
      ),
    ).toBe(1);
    expect(switchVariant(repeat, v, 0)).toEqual({ times: 3, do: v.do });
  });
});

describe("fields", () => {
  it("lists fields with titles and labels", () => {
    // Подписи из схемы, «Подробнее» для дополнительных полей, подписи значений списка.
    const s: Schema = {
      type: "object",
      required: ["keys"],
      properties: {
        keys: { type: "string", title: "Клавиши" },
        on: { enum: ["press", "toggle"], "x-enum-labels": ["при нажатии", "переключатель"] },
        hold_ms: { type: "integer", "x-advanced": true },
      },
    };
    const f = fields(s);
    expect(f.map((x) => [x.name, x.title, x.required, x.advanced])).toEqual([
      ["keys", "Клавиши", true, false],
      ["on", "on", false, false],
      ["hold_ms", "hold_ms", false, true],
    ]);
    expect(enumLabel(s.properties!.on!, "toggle")).toBe("переключатель");
    expect(enumLabel(s.properties!.on!, "other")).toBe("other");
  });
});
