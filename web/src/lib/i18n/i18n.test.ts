// Тесты переводов: полнота каталогов (одинаковые ключи и плейсхолдеры в ru и en)
// и логика translate/detectLang.
import { describe, expect, it } from "vitest";
import en from "./en.json";
import ru from "./ru.json";
import { detectLang, translate, type Messages } from "./translate";

/** placeholders возвращает отсортированный список плейсхолдеров {name} в тексте. */
function placeholders(text: string): string[] {
  return (text.match(/\{[a-zA-Z_][a-zA-Z0-9_]*\}/g) ?? []).sort();
}

describe("catalog completeness", () => {
  const catalogs: Record<string, Messages> = { ru, en };

  it("has the same keys in every language", () => {
    // Сравниваем набор ключей каждого языка с английским.
    const ref = Object.keys(en).sort();
    for (const [lang, msgs] of Object.entries(catalogs)) {
      expect(Object.keys(msgs).sort(), lang).toEqual(ref);
    }
  });

  it("has non-empty texts with matching placeholders", () => {
    // Для каждого ключа текст непустой, а плейсхолдеры совпадают с английскими.
    for (const [lang, msgs] of Object.entries(catalogs)) {
      for (const [key, text] of Object.entries(en)) {
        const other = msgs[key] ?? "";
        expect(other, `${lang}:${key}`).not.toBe("");
        expect(placeholders(other), `${lang}:${key}`).toEqual(placeholders(text));
      }
    }
  });
});

describe("translate", () => {
  const cat = {
    en: { hello: "Hello, {name}!", onlyEn: "English only" },
    ru: { hello: "Привет, {name}!" } as Messages,
  };

  it("substitutes params and falls back", () => {
    // Подстановка, откат на английский и на сам ключ.
    expect(translate(cat, "ru", "hello", { name: "Вера" })).toBe("Привет, Вера!");
    expect(translate(cat, "ru", "onlyEn")).toBe("English only");
    expect(translate(cat, "ru", "missing.key")).toBe("missing.key");
  });
});

describe("detectLang", () => {
  it("picks the first supported browser language", () => {
    // Русский и английский поддерживаются, прочие — откат на английский.
    expect(detectLang(["ru-RU", "en-US"])).toBe("ru");
    expect(detectLang(["de-DE", "en-US"])).toBe("en");
    expect(detectLang(["de-DE"])).toBe("en");
    expect(detectLang([])).toBe("en");
  });
});

describe("used keys", () => {
  it("translates every key used in the interface", async () => {
    // Собираем ключи-строки из исходников интерфейса ("editor.save", "nav." + имя — префиксы).
    const { readdirSync, readFileSync, statSync } = await import("node:fs");
    const { join } = await import("node:path");
    const root = join(__dirname, "..", "..");
    const files: string[] = [];
    const walk = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const p = join(dir, name);
        if (statSync(p).isDirectory()) walk(p);
        else if (/\.(svelte|ts)$/.test(name) && !name.endsWith(".test.ts")) files.push(p);
      }
    };
    walk(root);

    // Каждый полный ключ есть в каталоге; для префикса ("nav.") есть хотя бы один ключ с ним.
    const re =
      /"((?:app|common|devices|diag|editor|home|key|nav|projects|rec|settings|setup|trigger)\.[a-z_.0-9]*)"/g;
    for (const f of files) {
      for (const m of readFileSync(f, "utf8").matchAll(re)) {
        const key = m[1]!;
        const ok = key.endsWith(".") ? Object.keys(en).some((k) => k.startsWith(key)) : key in en;
        expect(ok, `${f}: ${key}`).toBe(true);
      }
    }
  });
});
