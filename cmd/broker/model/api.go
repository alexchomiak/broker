package model

import (
	"time"
)

type HealthCheckResponse struct {
	TimeStamp time.Time `json:"timestamp"`
	Status    string    `json:"status"`
}

type ErrorResponse struct {
	TimeStamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
}
