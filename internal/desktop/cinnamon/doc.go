// Package cinnamon — адаптер окружения Cinnamon для модуля desktop: раскладки клавиатуры.
//
// Cinnamon сам управляет раскладками: его оконный менеджер хранит выбранную группу XKB и
// возвращает её, если раскладку сменила другая программа (проверено на Cinnamon 6.6: чужая смена
// группы откатывается за ~50 мс). Поэтому раскладки читаются и переключаются через D-Bus самого
// Cinnamon: сервис org.Cinnamon, объект /org/Cinnamon, методы GetInputSources() →
// a(ssisssssssib) и ActivateInputSourceIndex(i) (js/ui/cinnamonDBus.js в исходниках Cinnamon).
//
// Связь с другими модулями: адаптер создаёт модуль desktop при запуске (нужен D-Bus сессии) и
// регистрирует в точке расширения layout_source (contracts.LayoutSource) раньше источника X11.
package cinnamon
