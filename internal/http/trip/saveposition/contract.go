//go:generate go tool mockgen -source ${GOFILE} -destination mocks_test.go -package ${GOPACKAGE}_test
package saveposition

import (
	"context"

	domain "github.com/Zuneve/template/internal/domain/trip"
	saveusecase "github.com/Zuneve/template/internal/usecase/trip/saveposition"
)

type usecase interface {
	Execute(ctx context.Context, in saveusecase.Input) (domain.Position, error)
}
