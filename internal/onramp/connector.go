package onramp

import (
	"context"

	"ovd/internal/domain"
)

type Connector interface {
	Start(ctx context.Context, signals chan<- domain.RawSignal) error
	Name() string
}
