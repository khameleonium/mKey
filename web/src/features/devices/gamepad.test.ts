// Тесты мастера «второй геймпад»: раскладка по умолчанию и файл проекта.
import { describe, expect, it } from "vitest";
import { buildProject, defaultRows } from "./gamepad";

const buttons = ["South", "East", "LB", "Mode"];
const axes = ["LX", "LY", "RX", "RY", "LT"];

describe("gamepad wizard", () => {
  it("fills WASD, arrows and preset buttons", () => {
    const rows = defaultRows(buttons, axes);
    expect(rows.map((r) => `${r.id}=${r.key}`).join(" ")).toBe(
      "LY-=W LY+=S LX-=A LX+=D RY-=Up RY+=Down RX-=Left RX+=Right South=Space East=C LB=Q Mode=",
    );
  });

  it("builds bindings for a device, with mouse on the right stick", () => {
    const text = buildProject(
      {
        name: "Второй геймпад",
        pad: "pad2",
        template: "xbox360",
        device: "Клава",
        rows: defaultRows(buttons, axes),
        mouseRight: true,
        hide: true,
        rampMS: 100,
      },
      { header: "h", mouse: "m" },
    );
    expect(text).toContain('name: "Второй геймпад"');
    expect(text).toContain(
      '- { from: "{Клава.W}", to: "{pad2.LY}", value: -1, ramp_ms: 100, hide: true }',
    );
    expect(text).toContain('- { from: "{Клава.Space}", to: "{pad2.South}", hide: true }');
    expect(text).toContain('- { from: "{MouseX}", to: "{pad2.RX}", hide: true }');
    expect(text).not.toContain("Mode");
    expect(text).not.toContain("{Клава.Up}");
  });
});
