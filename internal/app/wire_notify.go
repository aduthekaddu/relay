package app

import (
	"context"
	"reflect"

	"github.com/aduthekaddu/relay/internal/notify"
)

// wireNotify is owned by the notify feature.
func wireNotify(ctx context.Context, a *App) error {
	svc, err := notify.New(a.D, notify.WithPresence(func() notify.Presence { return presenceOf(a) }))
	if err != nil {
		return err
	}
	a.D.Notifier = svc
	svc.Routes(a.Router)
	a.OnStart("notify", svc.Start)
	return nil
}

// presenceOf returns a.D.Presence when the live feature provides one.
//
// core.Deps gains its Presence field in the live feature's branch; looking
// it up by name keeps this file compiling on either side of that merge.
// Once merged this can become `return a.D.Presence`.
func presenceOf(a *App) notify.Presence {
	v := reflect.ValueOf(a.D).Elem().FieldByName("Presence")
	if !v.IsValid() || !v.CanInterface() {
		return nil
	}
	if v.Kind() == reflect.Interface && v.IsNil() {
		return nil
	}
	p, _ := v.Interface().(notify.Presence)
	return p
}
