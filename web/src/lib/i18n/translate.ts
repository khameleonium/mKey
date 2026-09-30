// Чистая логика переводов без реактивности: поиск текста по ключу и подстановка параметров.
// Реактивная обёртка для компонентов — в index.svelte.ts.

/** Каталог переводов одного языка: ключ → текст. */
export type Messages = Record<string, string>;

/** Поддерживаемые языки интерфейса. */
export const LANGS = ["ru", "en"] as const;

/** Код языка интерфейса. */
export type Lang = (typeof LANGS)[number];

/** Язык, на который откатывается перевод при отсутствии текста. */
export const FALLBACK_LANG: Lang = "en";

/**
 * translate возвращает перевод ключа key на язык lang с подставленными параметрами {name}.
 * Порядок поиска: язык lang → язык по умолчанию → сам ключ.
 */
export function translate(
  catalog: Record<Lang, Messages>,
  lang: Lang,
  key: string,
  params: Record<string, string | number> = {},
): string {
  // Ищем текст в текущем языке, затем в языке по умолчанию.
  const text = catalog[lang][key] ?? catalog[FALLBACK_LANG][key];
  if (text === undefined) {
    return key;
  }

  // Подставляем параметры вида {name}; неизвестные плейсхолдеры оставляем как есть.
  return text.replace(/\{([a-zA-Z_][a-zA-Z0-9_]*)\}/g, (match, name: string) =>
    name in params ? String(params[name]) : match,
  );
}

/**
 * detectLang выбирает язык интерфейса по списку языков браузера:
 * русский, если он первый из поддерживаемых, иначе — язык по умолчанию.
 */
export function detectLang(languages: readonly string[]): Lang {
  // Берём первый язык браузера, который мы поддерживаем.
  for (const l of languages) {
    const code = l.toLowerCase().slice(0, 2);
    if ((LANGS as readonly string[]).includes(code)) {
      return code as Lang;
    }
  }
  return FALLBACK_LANG;
}
