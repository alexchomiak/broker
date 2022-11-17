package request

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// * Returns reference to logger
func GetLogger(ctx *fiber.Ctx) *zap.Logger {
	logger := ctx.Locals("logger").(*zap.Logger)
	if logger == nil {
		panic(errors.New("logger is nil"))
	}
	return logger
}
