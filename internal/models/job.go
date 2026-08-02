package models

import "time"

type Job struct {
	ID                                    int64
	Type, Payload, Status, Message, Error string
	ProgressCurrent, ProgressTotal        int
	CreatedAt, UpdatedAt                  time.Time
}
