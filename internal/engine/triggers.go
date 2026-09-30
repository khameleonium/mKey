package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mkey/internal/contracts"
	"mkey/internal/lib/project"
)

// minTimerMS — наименьший интервал таймера (защита от перегрузки, SEC-4).
const minTimerMS = 10

// builtinTriggers возвращает встроенные триггеры движка (T3.8).
func (m *Module) builtinTriggers() []contracts.TriggerType {
	return []contracts.TriggerType{
		// manual — только ручной запуск (GUI, CLI, API, действие run_event).
		builtinTrigger{
			meta:     meta("trigger", "manual", "system", `{"type":"object"}`),
			validate: noParams("manual"),
			arm: func(_ context.Context, _ contracts.EventRef, t project.Trigger, _ func(contracts.Fire)) (func(), error) {
				return func() {}, noParams("manual")(t)
			},
		},
		// startup — один раз при загрузке (или перезагрузке) проекта.
		builtinTrigger{
			meta:     meta("trigger", "startup", "system", `{"type":"object"}`),
			validate: noParams("startup"),
			arm: func(ctx context.Context, _ contracts.EventRef, t project.Trigger, fire func(contracts.Fire)) (func(), error) {
				if err := noParams("startup")(t); err != nil {
					return nil, err
				}
				// Срабатываем чуть позже, когда проект уже полностью взведён.
				timer := time.AfterFunc(50*time.Millisecond, func() {
					if ctx.Err() == nil {
						fire(contracts.Fire{})
					}
				})
				return func() { timer.Stop() }, nil
			},
		},
		// timer — периодически (every_ms) или однократно через время после загрузки (after_ms).
		builtinTrigger{
			meta:     meta("trigger", "timer", "system", `{"oneOf":[{"type":"object","required":["every_ms"],"properties":{"every_ms":{"type":"integer","minimum":10,"default":1000,"x-widget":"ms"}}},{"type":"object","required":["after_ms"],"properties":{"after_ms":{"type":"integer","minimum":0,"default":1000,"x-widget":"ms"}}}]}`),
			validate: func(t project.Trigger) error { _, err := timerParams(t); return err },
			arm:      m.armTimer,
		},
		// device — подключение или отключение устройства ввода.
		builtinTrigger{
			meta:     meta("trigger", "device", "system", `{"type":"object","required":["match"],"properties":{"match":{"type":"string","x-widget":"device"},"on":{"enum":["connected","disconnected"],"default":"connected"}}}`),
			validate: func(t project.Trigger) error { _, _, err := deviceParams(t); return err },
			arm:      m.armDevice,
		},
	}
}

// noParams возвращает проверку триггера без параметров.
func noParams(id string) func(project.Trigger) error {
	return func(t project.Trigger) error {
		if err := project.Decode(t.Params, &struct{}{}); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		return nil
	}
}

// timerCfg — параметры триггера timer.
type timerCfg struct {
	EveryMS *int `json:"every_ms"`
	AfterMS *int `json:"after_ms"`
}

// timerParams разбирает и проверяет параметры таймера: ровно один из every_ms и after_ms.
func timerParams(t project.Trigger) (timerCfg, error) {
	var p timerCfg
	if err := project.Decode(t.Params, &p); err != nil {
		return p, fmt.Errorf("timer: %w", err)
	}
	switch {
	case (p.EveryMS == nil) == (p.AfterMS == nil):
		return p, errors.New("timer: set exactly one of every_ms and after_ms")
	case p.AfterMS != nil && *p.AfterMS < 0:
		return p, errors.New("timer: after_ms must not be negative")
	case p.EveryMS != nil && *p.EveryMS < minTimerMS:
		return p, fmt.Errorf("timer: every_ms must be at least %d", minTimerMS)
	}
	return p, nil
}

// armTimer взводит таймер.
func (m *Module) armTimer(ctx context.Context, _ contracts.EventRef, t project.Trigger, fire func(contracts.Fire)) (func(), error) {
	p, err := timerParams(t)
	if err != nil {
		return nil, err
	}

	// Однократный таймер.
	if p.AfterMS != nil {
		timer := time.AfterFunc(time.Duration(*p.AfterMS)*time.Millisecond, func() {
			if ctx.Err() == nil {
				fire(contracts.Fire{})
			}
		})
		return func() { timer.Stop() }, nil
	}

	// Периодический таймер в своей горутине до снятия.
	tctx, cancel := context.WithCancel(ctx)
	go func() {
		tk := time.NewTicker(time.Duration(*p.EveryMS) * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-tctx.Done():
				return
			case <-tk.C:
				fire(contracts.Fire{})
			}
		}
	}()
	return cancel, nil
}

// deviceParams разбирает параметры триггера device: часть имени или "vid:pid" и тема шины.
func deviceParams(t project.Trigger) (string, string, error) {
	var p struct {
		Match string `json:"match"`
		On    string `json:"on"`
	}
	if err := project.Decode(t.Params, &p); err != nil {
		return "", "", fmt.Errorf("device: %w", err)
	}
	if strings.TrimSpace(p.Match) == "" {
		return "", "", project.Required("trigger", "device", "match")
	}
	switch p.On {
	case "", "connected":
		return p.Match, contracts.TopicInputDeviceAdded, nil
	case "disconnected":
		return p.Match, contracts.TopicInputDeviceRemoved, nil
	}
	return "", "", fmt.Errorf("device: unknown on %q", p.On)
}

// armDevice взводит триггер подключения/отключения устройства.
func (m *Module) armDevice(ctx context.Context, _ contracts.EventRef, t project.Trigger, fire func(contracts.Fire)) (func(), error) {
	// Параметры: часть имени или "vid:pid"; событие — подключение (по умолчанию) или отключение.
	matchText, topic, err := deviceParams(t)
	if err != nil {
		return nil, err
	}

	// Подписка на шину до снятия триггера.
	ch, unsub := m.bus.Subscribe(topic)
	match := strings.ToLower(matchText)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				d, isDev := e.Payload.(contracts.InputDevice)
				if isDev && (strings.Contains(strings.ToLower(d.Info.Name), match) || d.Info.ID.String() == match) {
					fire(contracts.Fire{Vars: map[string]any{"device": d.Info.Name}})
				}
			}
		}
	}()
	return unsub, nil
}
