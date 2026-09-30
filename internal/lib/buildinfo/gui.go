//go:build !nogui

package buildinfo

// GUI сообщает, что в сборку входят окно программы (веб-интерфейс) и значок в трее.
// Консольная сборка (`go build -tags nogui`, `make build-cli` → mkey-cli) — без них (ADR-0023).
const GUI = true
