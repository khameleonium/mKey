// Группировка видов блоков по категориям палитры в постоянном порядке (FR-UI-3).
import type { RegistryEntry } from "../../lib/types";

/** Порядок известных категорий; остальные (от плагинов) идут следом по алфавиту. */
const ORDER = ["keyboard", "mouse", "time", "logic", "system", "script"];

/** Group — категория палитры и её виды блоков. */
export interface Group {
  id: string;
  name: string;
  items: RegistryEntry[];
}

/** groupByCategory раскладывает виды блоков по категориям. */
export function groupByCategory(entries: RegistryEntry[]): Group[] {
  // Собираем категории в порядке первого появления.
  const map = new Map<string, Group>();
  for (const e of entries) {
    const id = e.category ?? "other";
    let g = map.get(id);
    if (!g) {
      g = { id, name: e.category_name ?? id, items: [] };
      map.set(id, g);
    }
    g.items.push(e);
  }

  // Сортируем: известные категории — в заданном порядке, прочие — по названию.
  const rank = (id: string) => {
    const i = ORDER.indexOf(id);
    return i < 0 ? ORDER.length : i;
  };
  return [...map.values()].sort((a, b) => rank(a.id) - rank(b.id) || a.name.localeCompare(b.name));
}
