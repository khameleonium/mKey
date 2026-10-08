// Тесты готовых раскладок виртуальных устройств.
import { describe, expect, it } from "vitest";
import {
  buildProject,
  freeName,
  hidesByDefault,
  presetRows,
  rowBindings,
  rowsFromBindings,
  schemesFor,
  sourceDevice,
} from "./presets";

/** xbox — состав шаблона Xbox 360 (как отдаёт API). */
const xbox = {
  id: "xbox360",
  buttons: ["South", "East", "West", "North", "LB", "RB", "Start", "DPadUp"],
  axes: ["LX", "LY", "RX", "RY", "LT", "RT"],
};

describe("virtual device presets", () => {
  it("picks free names", () => {
    expect(freeName("wheel", [])).toBe("wheel");
    expect(freeName("wheel", ["Wheel", "wheel_2"])).toBe("wheel_3");
    expect(freeName("custom", [])).toBe("dev");
  });

  it("offers schemes per kind", () => {
    expect(schemesFor("wheel")).toEqual(["keyboard_mouse", "keyboard", "gamepad"]);
    expect(schemesFor("keyboard")).toEqual(["none"]);
    expect(hidesByDefault("xbox360", "keyboard")).toBe(true);
    expect(hidesByDefault("wheel", "keyboard_mouse")).toBe(false);
    expect(hidesByDefault("flightstick", "gamepad")).toBe(true);
  });

  it("builds a gamepad from the keyboard", () => {
    const rows = presetRows("xbox360", "keyboard", xbox, 120);
    expect(rows.find((r) => r.id === "LY-")).toMatchObject({
      from: "W",
      opts: { value: -1, ramp_ms: 120 },
    });
    expect(rows.find((r) => r.to === "South")?.from).toBe("Space");
    // С мышью правый стик — мышью, стрелок нет.
    const mouse = presetRows("xbox360", "keyboard_mouse", xbox);
    expect(mouse.some((r) => r.from === "Up")).toBe(false);
    expect(mouse.find((r) => r.to === "RX")).toMatchObject({ from: "MouseX", analog: true });
  });

  it("binds a wheel to the mouse and keys", () => {
    const rows = presetRows("wheel", "keyboard_mouse", undefined);
    const b = rowBindings(rows, "wheel", "Клава", true);
    // Мышь — без устройства и не прячется; клавиши — с устройством и прячутся.
    expect(b[0]).toEqual({ from: "{MouseX}", to: "{wheel.Wheel}", steer: true });
    expect(b.find((x) => x.to === "{wheel.Gas}")).toEqual({
      from: "{Клава.W}",
      to: "{wheel.Gas}",
      value: 1,
      ramp_ms: 250,
      hide: true,
    });
  });

  it("binds a flight stick throttle as a lever", () => {
    const b = rowBindings(
      presetRows("flightstick", "keyboard_mouse", undefined),
      "stick",
      "",
      false,
    );
    expect(b.filter((x) => x.to === "{stick.Throttle}")).toEqual([
      { from: "{W}", to: "{stick.Throttle}", value: 1, ramp_ms: 2000, latch: true },
      { from: "{S}", to: "{stick.Throttle}", value: 0, ramp_ms: 2000, latch: true },
      { from: "{X}", to: "{stick.Throttle}", value: 0, latch: true },
    ]);
    expect(b.find((x) => x.to === "{stick.Button12}")?.from).toBe("{9}");
  });

  it("writes a disabled project and reads the layout back", () => {
    const rows = presetRows("wheel", "gamepad", undefined);
    const text = buildProject(
      { project: "Руль", name: "wheel", kind: "wheel", device: "Pad", hide: true, rows },
      "строка 1\nстрока 2",
    );
    expect(text).toContain("# строка 2\nversion: 1\n");
    expect(text).toContain('name: "Руль"\nenabled: false\n');
    expect(text).toContain(
      '  - {"from":"{Pad.LX}","to":"{wheel.Wheel}","curve":1.5,"deadzone":0.05,"hide":true}',
    );

    // Обратно: строки и устройство-источник.
    const bindings = rowBindings(rows, "wheel", "Pad", true);
    const back = rowsFromBindings(bindings, "wheel");
    expect(back.length).toBe(rows.length);
    expect(back[0]).toMatchObject({ to: "Wheel", from: "LX", analog: true, opts: { curve: 1.5 } });
    expect(back.find((r) => r.to === "Cross")).toMatchObject({ from: "South" });
    expect(sourceDevice(bindings, "wheel")).toBe("Pad");
  });
});
