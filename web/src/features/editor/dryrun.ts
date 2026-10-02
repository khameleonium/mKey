// Тексты строк таймлайна «Сухого прогона» (FR-UI-6): строка DryStep от демона → понятная фраза
// на языке окна. Названия действий и тексты условий даёт окно (из реестра, функции Names).
import type { Condition, DryStep } from "../../lib/types";

/** Translate — функция перевода (t из i18n): ключ и параметры → текст. */
export type Translate = (key: string, params?: Record<string, string | number>) => string;

/** Names — тексты видов из реестра: название действия и условие с кратким содержанием. */
export interface Names {
  action: (type: string) => string;
  condition: (c: Condition) => string;
}

/** seconds — миллисекунды как секунды с двумя знаками после запятой для языка lang. */
export function seconds(ms: number, lang: string): string {
  return new Intl.NumberFormat(lang, { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(
    ms / 1000,
  );
}

/** wheelDir — направление прокрутки по знакам сдвига: вверх, вниз, влево, вправо. */
function wheelDir(s: DryStep): string {
  if (s.dx) return s.dx > 0 ? "right" : "left";
  return (s.dy ?? 0) > 0 ? "up" : "down";
}

/** describe возвращает текст строки таймлайна. */
export function describe(s: DryStep, t: Translate, names: Names, lang: string): string {
  const keys = (s.keys ?? []).join(" + ");
  const conds = (s.conditions ?? []).map((c) => names.condition(c)).join(", ");
  const count = s.count ?? 1;
  let text: string;

  // Шаги макроса — по виду; группы и заметки — по ключу с параметрами.
  switch (s.kind) {
    case "press":
      text = t("dry.step.press", { keys });
      break;
    case "release":
      text = t("dry.step.release", { keys });
      break;
    case "release_all":
      text = t("dry.step.release_all");
      break;
    case "tap":
      text = t(count > 1 ? "dry.step.tap_n" : "dry.step.tap", { keys, count });
      if (s.ms) text += " " + t("dry.step.hold", { ms: s.ms });
      break;
    case "wait":
      text = s.max_ms
        ? t("dry.step.wait_range", { min: s.ms ?? 0, max: s.max_ms })
        : t("dry.step.wait", { ms: s.ms ?? 0 });
      break;
    case "text":
      text = t("dry.step.text", { text: s.text ?? "" });
      break;
    case "move":
      text = t("dry.step.move", { dx: s.dx ?? 0, dy: s.dy ?? 0 });
      break;
    case "wheel":
      text = t("dry.step.wheel_" + wheelDir(s), { count });
      break;
    case "axis":
      text = t("dry.step.axis", { keys, value: s.value ?? 0 });
      break;
    case "touch":
      text = t("dry.step.touch", { point: s.points?.[0] ?? "" });
      if (s.ms) text += " " + t("dry.step.hold", { ms: s.ms });
      break;
    case "swipe":
      text = t("dry.step.swipe", {
        from: s.points?.[0] ?? "",
        to: s.points?.[1] ?? "",
        ms: s.ms ?? 0,
      });
      break;
    case "group":
    case "note": {
      const args: Record<string, string | number> = { ...(s.args ?? {}), count, conds };
      // Длительность повтора записи — в секундах.
      if (s.args?.ms !== undefined) args.sec = seconds(Number(s.args.ms), lang);
      text = t(s.key ?? "", args);
      break;
    }
    case "action":
      text = t("dry.action", { name: names.action(s.action ?? "") });
      break;
    default:
      text = s.kind;
  }

  // Устройство, если это не обычные клавиатура и мышь.
  if (s.device) text += " " + t("dry.device", { device: s.device });
  return text;
}
