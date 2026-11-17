package onramp

import (
	"context"

	"ovd/internal/domain"
)

// Connector is the common contract for lead sources.
type Connector interface {
	Start(ctx context.Context, signals chan<- domain.RawSignal) error
	Name() string
}
