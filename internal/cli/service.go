package cli

import (
	"context"
	"errors"

	"github.com/ahaltindis/wipsync/internal/app"
	"github.com/ahaltindis/wipsync/internal/watch"
)

// service is the client, except that pulls and pushes of many folders run
// in wipsync watch when it runs for this client: its tray icon shows them,
// and the desktop is told how they went. Without it, they run here.
type service struct {
	*app.App
}

func (s service) PullAll(ctx context.Context, scope app.Scope) (app.AllResult, error) {
	return s.through(ctx, watch.PullAll, scope, s.App.PullAll)
}

func (s service) PushAll(ctx context.Context, scope app.Scope) (app.AllResult, error) {
	return s.through(ctx, watch.PushAll, scope, s.App.PushAll)
}

func (s service) through(ctx context.Context, a watch.Action, scope app.Scope,
	here func(context.Context, app.Scope) (app.AllResult, error)) (app.AllResult, error) {
	if sock, ok := watch.SocketPath(s.DB.Path); ok {
		res, err := watch.Ask(ctx, sock, a, scope)
		if !errors.Is(err, watch.ErrNoService) {
			return res, err
		}
	}
	return here(ctx, scope)
}
