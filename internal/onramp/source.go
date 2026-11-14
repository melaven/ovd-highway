package onramp

import (
	"context"

	"ovd/internal/domain"
)

type Source interface {
	Start(context.Context, chan<- domain.RawSignal) error
}
