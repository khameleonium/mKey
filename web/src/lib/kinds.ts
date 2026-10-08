// Классы устройств ввода в порядке показа (как evdev.AllKinds в Go): что записывать — в «Настройках»,
// на странице «Записи» и в меню значка. Названия — i18n-ключи devices.kind.<класс>.
export const KINDS = [
  "keyboard",
  "mouse",
  "touchpad",
  "touchscreen",
  "tablet",
  "gamepad",
  "joystick",
  "other",
] as const;
