package moq

import (
	"github.com/mengelbart/moqtransport"
	"github.com/mengelbart/qlog"
)

type Transport struct {
}

func New() (*Transport, error) {
	session := moqtransport.Session{
		InitialMaxRequestID: 0,
		Handler:             nil,
		Qlogger:             &qlog.Logger{},
	}
}
