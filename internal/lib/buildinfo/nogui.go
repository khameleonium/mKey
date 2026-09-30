//go:build nogui

package buildinfo

// GUI сообщает, что в сборку входят окно программы и значок в трее: в консольной сборке — нет (ADR-0023).
const GUI = false
