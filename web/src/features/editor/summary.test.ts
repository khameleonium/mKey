// Тесты краткого описания параметров блока.
import { describe, expect, it } from "vitest";
import { summarize } from "./summary";
import type { Schema } from "../../lib/types";

describe("summarize", () => {
  it("lists values with labels", () => {
    // Горячая клавиша: клавиши, подпись режима и включённый флажок.
    const s: Schema = {
      type: "object",
      properties: {
        keys: { type: "string" },
        on: { enum: ["press", "toggle"], "x-enum-labels": ["при нажатии", "переключатель"] },
        consume: { type: "boolean", title: "Не передавать" },
        hold_ms: { type: "integer", "x-widget": "ms" },
      },
    };
    expect(summarize(s, { keys: "{F8}", on: "toggle", consume: true }, "мс")).toBe(
      "{F8}, переключатель, не передавать",
    );

    // Таймер: вариант и миллисекунды.
    const timer: Schema = {
      oneOf: [
        {
          type: "object",
          required: ["every_ms"],
          properties: { every_ms: { type: "integer", "x-widget": "ms" } },
        },
        {
          type: "object",
          required: ["after_ms"],
          properties: { after_ms: { type: "integer", "x-widget": "ms" } },
        },
      ],
    };
    expect(summarize(timer, { after_ms: 500 }, "ms")).toBe("500 ms");
    expect(summarize(undefined, 1, "ms")).toBe("");
  });
});
