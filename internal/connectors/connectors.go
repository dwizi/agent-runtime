package connectors

import "context"

type Connector interface {
	Name() string
	Start(ctx context.Context) error
}
