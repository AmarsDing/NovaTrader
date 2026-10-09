package model

type ErrLevel int32

const (
	INFO ErrLevel = iota
	WARNING
	Err
)
