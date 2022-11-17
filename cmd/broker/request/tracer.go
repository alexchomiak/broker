package request

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/openzipkin/zipkin-go"
)

func GetTracer(ctx *fiber.Ctx) *zipkin.Tracer {
	tracer := ctx.Locals("tracer").(*zipkin.Tracer)
	if tracer == nil {
		panic(errors.New("tracer is nil"))
	}
	return tracer
}
