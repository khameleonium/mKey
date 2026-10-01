// Тесты comboText: сочетания понятными словами.
import { describe, expect, it } from "vitest";
import { comboText } from "./combo";

/** t — простой переводчик для проверки. */
const t = (key: string, args?: Record<string, string | number>): string =>
  ({
    "key.left": `левый ${String(args?.key)}`,
    "key.right": `правый ${String(args?.key)}`,
    "key.space": "Пробел",
  })[key] ?? key;

describe("comboText", () => {
  it("names sides of modifiers and Space", () => {
    expect(comboText("^{LCtrl}^{RAlt}{Space}", t)).toBe("левый Ctrl + правый Alt + Пробел");
  });
  it("keeps other keys as they are", () => {
    expect(comboText("^{Ctrl}^{Alt}{R}", t)).toBe("Ctrl + Alt + R");
    expect(comboText("{F9}", t)).toBe("F9");
    expect(comboText("", t)).toBe("");
  });
});
