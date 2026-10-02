# Платформы: системы инициализации, права и доступ к устройствам

Решения: [ADR-0004](adr/0004-uaccess-device-access.md), [ADR-0014](adr/0014-platform-backends.md). Требования: SPEC FR-INST-7.

Всё системно-зависимое живёт в модуле `internal/platform`. Остальной код не вызывает
`systemctl`, `pkexec`, `udevadm` напрямую и не предполагает наличие systemd.

## Определение системы (`internal/platform/detect`)

| Сведение | Как определяется |
|---|---|
| Init-система | `/run/systemd/system` → systemd; `/run/openrc` → OpenRC; `/run/runit` → runit; `/run/dinitctl` → dinit; `/run/s6` → s6; иначе имя PID 1 из `/proc/1/comm` (`init` → sysvinit) |
| Менеджер сеансов | `/run/systemd/seats` есть: systemd-logind (при systemd) или elogind; иначе нет |
| Менеджер устройств | `/run/udev` есть: systemd-udevd (при systemd или установленном `systemd-udevd`) или eudev; `/proc/sys/kernel/hotplug` содержит mdev → mdev; установлен `mdevd` → mdevd |
| Дистрибутив, пакетный менеджер | `/etc/os-release` (`ID`, затем `ID_LIKE`): apt, dnf, pacman, zypper, xbps, apk, emerge |
| Каталог времени выполнения | `$XDG_RUNTIME_DIR/mkey`, без него — `/tmp/mkey-<uid>` |

## Повышение прав (`internal/platform/elevate`)

Порядок предпочтения (выбирается первый доступный):

1. `pkexec` — графическое окно пароля (есть polkit и графическая сессия);
2. `sudo` / `doas` в текущем терминале (mKey запущен из терминала);
3. `sudo` / `doas` в новом окне терминала (графика без polkit); терминал ищется в `$TERMINAL`,
   затем `x-terminal-emulator`, konsole, gnome-terminal, kgx, xfce4-terminal, mate-terminal,
   alacritty, kitty, foot, wezterm, xterm. Окно может вернуть управление до ввода пароля —
   поэтому после него результат перепроверяется `mkey doctor`;
4. «ручной» — mKey показывает команду для копирования (`sudo …`, `doas …` или `su -c '…'`).

Бэкенды плагинов (точка расширения `PointElevator`) встают перед ручным вариантом.

## Выдача доступа к устройствам (`internal/platform/devaccess`)

Выполняется от root командой `mkey privileged install-rules` (удаление — `uninstall-rules`).
Подкоманды фиксированы, аргументов нет, содержимое файлов зашито в бинарник (SEC-6).
Пользователь, которому выдаются права, берётся из `PKEXEC_UID`, `SUDO_USER` или `DOAS_USER`.

| Способ | Когда | Что делает | Перелогин |
|---|---|---|---|
| `uaccess` | logind/elogind + udev | `/etc/udev/rules.d/70-mkey.rules` с `TAG+="uaccess"` для `uinput` и `event*` | не нужен |
| `group` | udev без logind | то же правило с `GROUP="input"`; `groupadd -r input` (если нет), `usermod -aG input <user>` | нужен |
| `mdev` | mdev/mdevd (Alpine) | блок mKey в начале `/etc/mdev.conf`; `addgroup -S input`, `addgroup <user> input`; `mdev -s` или `mdevd-coldplug` | нужен |

Для всех способов модуль `uinput` загружается сразу (`modprobe uinput`) и добавляется в
автозагрузку, если он не встроен в ядро (`modules.builtin`):
`/etc/modules-load.d/mkey.conf` (systemd, Void/runit, OpenRC modules-load), иначе блок mKey
в `/etc/modules` (Alpine, старые Debian).

Блоки mKey внутри общих файлов обрамлены маркерами `# >>> mKey >>>` / `# <<< mKey <<<`;
удаление убирает ровно их и возвращает файл к исходному виду.

Ограничение: способ `group` при удалении не выводит пользователя из группы input (группа могла
быть нужна и до mKey) — `mkey uninstall` подсказывает команду, если человек захочет выйти из неё сам.

## Проверка на системах без systemd

Юнит-тесты `detect` и `devaccess` описывают Mint (systemd), Void (runit + elogind + eudev),
Alpine (OpenRC + mdev), Devuan (sysvinit + elogind), Artix (OpenRC + udev). Проверка на
настоящих системах — задача T4.7.

## Установка и автозапуск (ADR-0021)

`mkey setup` устанавливает программу в `~/.local/bin/mkey`, ярлык и иконку в меню, выдаёт доступ
к устройствам (как `mkey doctor --fix`) и настраивает автозапуск первым подходящим способом:

| Способ | Когда | Что меняет |
|---|---|---|
| `xdg` | полноценный рабочий стол (KDE, GNOME, Cinnamon, Xfce, MATE, LXQt…) | `~/.config/autostart/mkey.desktop` |
| `systemd-user` | systemd и активная `graphical-session.target` (например, Hyprland через uwsm) | `~/.config/systemd/user/mkey.service` + `systemctl --user enable` |
| `sway`, `hyprland`, `river`, `labwc`, `niri` | соответствующий композитор | блок mKey в его конфиге |
| `manual` | ничего не подошло | показывает команду для ручной настройки |

Всё записанное учитывается в `~/.local/share/mkey/install-manifest.json`; `mkey uninstall`
удаляет ровно это, снимает правила доступа и удаляет данные (или сохраняет настройки и проекты).

Проверка на системах без systemd в контейнерах: `sh test/install/run.sh` (нужен docker или podman).
