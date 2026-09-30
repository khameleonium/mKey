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
			meta: meta("trigger", "manual", "system", `{"type":"object"}`),
			arm: func(_ context.Context, _ contracts.EventRef, t project.Trigger, _ func(contracts.Fire)) (func(), error) {
				if err := project.Decode(t.Params, &struct{}{}); err != nil {
					return nil, fmt.Errorf("manual: %w", err)
				}
				return func() {}, nil
			},
		},
		// startup — один раз при загрузке (или перезагрузке) проекта.
		builtinTrigger{
			meta: meta("trigger", "startup", "system", `{"type":"object"}`),
			arm: func(ctx context.Context, _ contracts.EventRef, t project.Trigger, fire func(contracts.Fire)) (func(), error) {
				if err := project.Decode(t.Params, &struct{}{}); err != nil {
					return nil, fmt.Errorf("startup: %w", err)
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
			meta: meta("trigger", "timer", "system", `{"type":"object","properties":{"every_ms":{"type":"integer","minimum":10},"after_ms":{"type":"integer","minimum":0}}}`),
			arm:  m.armTimer,
		},
		// device — подключение или отключение устройства ввода.
		builtinTrigger{
			meta: meta("trigger", "device", "system", `{"type":"object","required":["match"],"properties":{"match":{"type":"string"},"on":{"enum":["connected","disconnected"]}}}`),
			arm:  m.armDevice,
		},
	}
}

// armTimer взводит таймер.
func (m *Module) armTimer(ctx context.Context, _ contracts.EventRef, t project.Trigger, fire func(contracts.Fire)) (func(), error) {
	// Параметры: ровно один из every_ms и after_ms.
	var p struct {
		EveryMS *int `json:"every_ms"`
		AfterMS *int `json:"after_ms"`
	}
	if err := project.Decode(t.Params, &p); err != nil {
		return nil, fmt.Errorf("timer: %w", err)
	}
	if (p.EveryMS == nil) == (p.AfterMS == nil) {
		return nil, errors.New("timer: set exactly one of every_ms and after_ms")
	}

	// Однократный таймер.
	if p.AfterMS != nil {
		if *p.AfterMS < 0 {
			return nil, errors.New("timer: after_ms must not be negative")
		}
		timer := time.AfterFunc(time.Duration(*p.AfterMS)*time.Millisecond, func() {
			if ctx.Err() == nil {
				fire(contracts.Fire{})
			}
		})
		return func() { timer.Stop() }, nil
	}

	// Периодический таймер в своей горутине до снятия.
	if *p.EveryMS < minTimerMS {
		return nil, fmt.Errorf("timer: every_ms must be at least %d", minTimerMS)
	}
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

// armDevice взводит триггер подключения/отключения устройства.
func (m *Module) armDevice(ctx context.Context, _ contracts.EventRef, t project.Trigger, fire func(contracts.Fire)) (func(), error) {
	// Параметры: часть имени или "vid:pid"; событие — подключение (по умолчанию) или отключение.
	var p struct {
		Match string `json:"match"`
		On    string `json:"on"`
	}
	if err := project.Decode(t.Params, &p); err != nil {
		return nil, fmt.Errorf("device: %w", err)
	}
	if strings.TrimSpace(p.Match) == "" {
		return nil, errors.New("device: match is required")
	}
	topic := contracts.TopicInputDeviceAdded
	switch p.On {
	case "", "connected":
	case "disconnected":
		topic = contracts.TopicInputDeviceRemoved
	default:
		return nil, fmt.Errorf("device: unknown on %q", p.On)
	}

	// Подписка на шину до снятия триггера.
	ch, unsub := m.bus.Subscribe(topic)
	match := strings.ToLower(p.Match)
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
