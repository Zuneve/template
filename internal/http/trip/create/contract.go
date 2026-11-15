//go:generate go tool mockgen -source ${GOFILE} -destination mocks_test.go -package ${GOPACKAGE}_test
package create

import (
	"context"

	createusecase "github.com/Zuneve/template/internal/usecase/trip/create"
)

type usecase interface {
	Execute(ctx context.Context, in createusecase.Input) (createusecase.Output, error)
}
