-- Пример Lua-плагина mKey: счётчик, который живёт, пока плагин включён.
-- mkey.register_action / mkey.register_condition / mkey.register_trigger описывают виды; функции
-- run, check и poll получают параметры из проекта (params) и сведения о событии (event).

local count = 0

mkey.register_action{
  id = "counter_add",
  name = { ru = "Прибавить к счётчику", en = "Add to counter" },
  description = { ru = "Прибавляет шаг и сообщает, если дошли до цели", en = "Adds a step, notifies at the goal" },
  category = "logic",
  params = {
    type = "object",
    properties = {
      step = { type = "integer", default = 1 },
      goal = { type = "integer", default = 10 },
    },
  },
  validate = function(params)
    if params.step and params.step <= 0 then
      return "шаг должен быть больше нуля"
    end
  end,
  run = function(params, event)
    count = count + (params.step or 1)
    mkey.log("counter = " .. count)
    if count == (params.goal or 10) then
      mkey.notify("Счётчик дошёл до " .. count, "mKey")
    end
  end,
}

mkey.register_action{
  id = "counter_reset",
  name = { ru = "Сбросить счётчик", en = "Reset counter" },
  category = "logic",
  run = function() count = 0 end,
}

mkey.register_condition{
  id = "counter_reached",
  name = { ru = "Счётчик дошёл до…", en = "Counter reached…" },
  params = { type = "object", properties = { value = { type = "integer", default = 10 } } },
  check = function(params) return count >= (params.value or 10) end,
}

-- Триггер: mKey вызывает poll раз в interval_ms, пока событие включено. Вернуть таблицу (или true) —
-- событие срабатывает; значения таблицы доступны действиям. state — память этого события между
-- вызовами: здесь — чтобы сработать один раз, когда счётчик дошёл до значения.
mkey.register_trigger{
  id = "counter_full",
  name = { ru = "Когда счётчик дошёл до…", en = "When the counter reaches…" },
  params = { type = "object", properties = { value = { type = "integer", default = 10 } } },
  interval_ms = 500,
  poll = function(params, state)
    local full = count >= (params.value or 10)
    if full and not state.fired then
      state.fired = true
      return { count = count }
    end
    if not full then state.fired = false end
  end,
}
