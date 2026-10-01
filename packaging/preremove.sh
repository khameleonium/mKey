#!/bin/sh
# Перед удалением пакета mKey: файлы, которые mKey создавал по согласию пользователя (правила
# доступа, автозапуск, данные), удаляет команда `mkey uninstall` — пакетный менеджер о них не
# знает. Напоминаем, но ничего не удаляем сами (у каждого пользователя свои настройки).
cat <<'MSG'

Чтобы удалить и настройки mKey (правила доступа к устройствам, автозапуск), перед удалением
пакета выполните от своего имени: mkey uninstall
To also remove mKey's setup (device access rules, autostart), run as your user before removing
the package: mkey uninstall

MSG
exit 0
