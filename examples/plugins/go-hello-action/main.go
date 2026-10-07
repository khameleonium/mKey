// Command go-hello-action — пример плагина mKey на Go (docs/plugins.md): действие «Поздороваться»
// печатает приветствие, условие «Будний день» и триггер «Каждые N секунд».
//
// Сборка: go build -o go-hello-action . ; установка: mkey plugin install <эта папка>.
package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/khameleonium/mKey/pkg/pluginsdk"
)

func main() {
	if err := newPlugin().Run(); err != nil {
		log.Fatal(err)
	}
}

// newPlugin описывает виды плагина (отдельно от main — чтобы проверить его тестом).
func newPlugin() *pluginsdk.Plugin {
	p := pluginsdk.New()

	// Действие: напечатать приветствие (нужно разрешение output.send).
	p.Action(pluginsdk.Type{
		ID:          "hello",
		Name:        pluginsdk.Text{"ru": "Поздороваться", "en": "Say hello"},
		Description: pluginsdk.Text{"ru": "Напечатать «Привет, <имя>!»", "en": "Type “Hello, <name>!”"},
		Category:    "text",
		Params:      []byte(`{"type":"object","properties":{"name":{"type":"string","default":"мир"}}}`),
	}, func(ctx context.Context, c *pluginsdk.Call) error {
		var a struct{ Name string }
		if err := c.Decode(&a); err != nil {
			return err
		}
		if a.Name == "" {
			a.Name = "мир"
		}
		greeting := "Hello, " + a.Name + "!"
		if c.Host != nil && c.Event.Project != "" {
			_ = c.Host.Log(ctx, "info", "greeting "+a.Name)
		}
		return c.Host.Send(ctx, `{"`+greeting+`"}`)
	})
	p.Validate("hello", func(c *pluginsdk.Call) error {
		var a struct{ Name string }
		if err := c.Decode(&a); err != nil {
			return err
		}
		if len([]rune(a.Name)) > 50 {
			return errors.New("name is too long (50 letters at most)")
		}
		return nil
	})

	// Условие: сегодня будний день.
	p.Condition(pluginsdk.Type{ID: "weekday", Name: pluginsdk.Text{"ru": "Будний день", "en": "Weekday"}, Category: "system"},
		func(context.Context, *pluginsdk.Call) (bool, error) {
			d := time.Now().Weekday()
			return d != time.Saturday && d != time.Sunday, nil
		})

	// Триггер: каждые N секунд.
	p.Trigger(pluginsdk.Type{
		ID:     "every_seconds",
		Name:   pluginsdk.Text{"ru": "Каждые N секунд", "en": "Every N seconds"},
		Params: []byte(`{"type":"object","properties":{"seconds":{"type":"integer","minimum":1,"default":10}}}`),
	}, func(ctx context.Context, c *pluginsdk.Call, fire func(map[string]any)) error {
		var a struct{ Seconds int }
		if err := c.Decode(&a); err != nil {
			return err
		}
		if a.Seconds < 1 {
			a.Seconds = 10
		}
		t := time.NewTicker(time.Duration(a.Seconds) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				fire(nil)
			}
		}
	})
	return p
}
