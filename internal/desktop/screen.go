package desktop

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/khameleonium/mKey/internal/contracts"
)

// Размер рабочего стола (contracts.ScreenInfo) — для касаний в пикселях. Пока берётся из настроек
// (modules.desktop.screen: "1920x1080"); адаптеры окружений фазы 8 будут узнавать его сами.

// ScreenSize возвращает размер рабочего стола из настроек; не задан — contracts.ErrUnsupported
// с подсказкой.
func (m *Module) ScreenSize(context.Context) (int, int, error) {
	w, h, err := parseScreen(m.cfg.Screen)
	if err != nil {
		return 0, 0, err
	}
	if w == 0 {
		return 0, 0, fmt.Errorf("%w: screen size is unknown — set modules.desktop.screen in config.yaml (e.g. \"1920x1080\") or use percent ({Touch 50%% 80%%})", contracts.ErrUnsupported)
	}
	return w, h, nil
}

// parseScreen разбирает "1920x1080" (пусто — 0, 0).
func parseScreen(s string) (int, int, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, 0, nil
	}
	ws, hs, ok := strings.Cut(s, "x")
	w, errW := strconv.Atoi(strings.TrimSpace(ws))
	h, errH := strconv.Atoi(strings.TrimSpace(hs))
	if !ok || errW != nil || errH != nil || w < 1 || h < 1 || w > 100000 || h > 100000 {
		return 0, 0, fmt.Errorf("%q: write it as width x height, e.g. \"1920x1080\"", s)
	}
	return w, h, nil
}

// Проверка на этапе компиляции.
var _ contracts.ScreenInfo = (*Module)(nil)
