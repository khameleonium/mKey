#!/bin/sh
# После установки пакета mKey (deb/rpm/apk/archlinux): ничего не меняем в системе молча —
# доступ к устройствам ввода и автозапуск настраивает мастер с объяснением и согласием
# (SPEC FR-INST-2, T10.2). Только подсказываем, что делать дальше.
cat <<'MSG'

mKey установлен. Запустите его из меню приложений или командой: mkey
Первый запуск откроет мастер: он объяснит и (с вашего согласия) настроит доступ к клавиатуре
и мыши и автозапуск.

mKey is installed. Start it from the application menu or run: mkey
The first run opens a wizard that explains and (with your consent) sets up access to the
keyboard and mouse and autostart.

MSG
exit 0
