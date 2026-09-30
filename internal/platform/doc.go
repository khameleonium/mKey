// Package platform — модуль системно-зависимых операций mKey (T1.8, FR-INST-7, ADR-0014).
//
// Всё, что отличается между дистрибутивами и системами инициализации, спрятано здесь,
// за точками расширения, чтобы остальной код не предполагал наличие systemd:
//
//   - detect — определение системы: init (systemd, OpenRC, runit, dinit, s6, sysvinit),
//     logind/elogind, менеджер устройств (systemd-udevd, eudev, mdev), дистрибутив и
//     пакетный менеджер, средства повышения прав, каталог времени выполнения;
//   - elevate — бэкенды повышения прав (contracts.Elevator): pkexec, sudo/doas в текущем
//     терминале, sudo/doas в новом окне терминала и «ручной» — показать команду;
//   - devaccess — способы выдачи доступа к устройствам (contracts.DeviceAccess):
//     udev-тег uaccess, группа input, mdev. Выполняются от root в `mkey privileged`.
//
// Модуль регистрирует все встроенные бэкенды в точках расширения PointElevator и
// PointDeviceAccess (туда же могут добавлять свои бэкенды плагины) и публикует
// сервис contracts.Platform, который выбирает подходящие бэкенды для текущей системы.
package platform
