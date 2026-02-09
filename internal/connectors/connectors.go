package connectors

import "context"

type Connector interface {
	Name() string
	Start(ctx context.Context) error
}

type Publisher interface {
	Publish(ctx context.Context, externalID, text string) error
}
