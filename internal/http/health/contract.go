package health

import "context"

type pinger interface {
	Ping(ctx context.Context) error
}
